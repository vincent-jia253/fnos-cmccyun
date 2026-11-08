package api

// 扫码登录（139 云盘 / 中国移动云盘）。
//
// 原理：网页版 yun.139.com 的「扫码登录」是三步握手——
//
//	1. 向公钥接口要一把 RSA 公钥；
//	2. 生成 sid / deviceID，把固定 32 字节的 AUTH_KEY 用 RSA(PKCS#1 v1.5) 加密成
//	   skey，再把 sid/deviceID/cType 拼成 https://yun.139.com/w/#/qrcLogin?sID=…&dID=…
//	   这个 URL 就是二维码内容，手机 App 扫它就会弹确认；
//	3. 浏览器拿着 skey 轮询 thirdlogin：请求体是「JSON 明文 → AES-256-CBC 信封」，
//	   mcloud-sign 签的是**明文**；手机点了确认后，服务端返回 data（hex 串），
//	   用 DATA_KEY 做 AES-128-ECB 解密，里面就是 account + token。
//
// 拿到的 Authorization 和我们手工粘贴的那串完全等价：base64("pc:"+账号+":"+token)，
// 所以两种登录方式在 SetAuthorization 之后走的是同一条路，续期、挂载都不用改。
//
// 这里只依赖标准库的 crypto/*，不联网取公钥缓存，不落盘任何中间态。

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 两个上游地址做成变量，测试里可以指到 httptest。
var (
	qrPublicKeyURL = "https://yun.139.com/orchestration/auth-rebuild/key/v1.0/getRsaPublicKey"
	qrLoginURL     = "https://user-njs.yun.139.com/user/thirdlogin"
)

var qrHTTPClient = &http.Client{Timeout: 25 * time.Second}

const (
	// qrAuthKey 是网页端写死的对称密钥：既用来加密 skey（RSA 的明文），
	// 也用 AES-256-CBC 包住登录请求体、解开登录响应信封。
	qrAuthKey = "UqEZkrjCKfa02pP6jntzFmkzOz86zHUC"
	// qrDataKey 用来解 thirdlogin 里 data 字段（hex → AES-128-ECB）。
	qrDataKey = "qPqDw263XgFgL3u8"

	qrPageURL    = "https://yun.139.com/w/#/qrcLogin"
	qrVersion    = "7.18.1"
	qrChannelSrc = "10000034"
	qrClientCode = "10701"

	qrUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// QrLifetime 是二维码的服务端有效期；上游也会用 200059542 告诉我们过期。
	QrLifetime = 300 * time.Second
)

// QrStatus 是轮询到的登录状态。
type QrStatus string

const (
	QrWaiting   QrStatus = "waiting"   // 还没人扫
	QrScanned   QrStatus = "scanned"   // 扫了，等手机点确认
	QrConfirmed QrStatus = "confirmed" // 确认了，Authorization 已就绪
	QrExpired   QrStatus = "expired"   // 超时（300 秒）
	QrCancelled QrStatus = "cancelled" // 手机上取消了
	QrFailed    QrStatus = "failed"    // 上游报的其他错
)

// QRStart 是一次扫码会话的起点。
type QRStart struct {
	// Handle 是不透明凭据，调用方存起来、轮询时原样带回；里面含 skey，
	// 不要直接发给浏览器。
	Handle string
	// Content 就是二维码要编码的内容。
	Content string
	// ExpiresIn 建议展示给用户的剩余秒数。
	ExpiresIn time.Duration
}

// QRResult 是一次轮询的结果。
type QRResult struct {
	// Code 是上游原始返回码，排查"扫一下就失效"这类问题全靠它。
	Code          string
	Status        QrStatus
	Account       string
	Authorization string // base64("pc:account:token")，仅 confirmed 时有值
	Message       string
}

type qrHandle struct {
	Sid      string `json:"sid"`
	DeviceID string `json:"deviceID"`
	Skey     string `json:"skey"`
}

// StartQrLogin 取公钥、造 sid/deviceID、算出 skey，返回二维码内容和 handle。
func StartQrLogin(ctx context.Context) (*QRStart, error) {
	pub, err := qrFetchPublicKey(ctx)
	if err != nil {
		return nil, err
	}
	sid := qrRandString(16)
	deviceID := qrRandHex(16)
	skey, err := qrEncryptRSA(qrAuthKey, pub)
	if err != nil {
		return nil, fmt.Errorf("加密登录密钥失败: %w", err)
	}
	raw, err := json.Marshal(qrHandle{Sid: sid, DeviceID: deviceID, Skey: skey})
	if err != nil {
		return nil, err
	}
	handle := base64.StdEncoding.EncodeToString(raw)
	// 第一次轮询就是"登记"：网页版也是生成二维码后立刻查一次，上游据此建一条
	// 待扫码的登录。扫码前不登记，手机扫到就是个不存在的会话（提示配额已失效）。
	// 这里失败不影响出图（网络抖动时前端还会继续轮询），所以只忽略错误。
	_, _ = PollQrLogin(ctx, handle)
	return &QRStart{
		Handle:    handle,
		Content:   qrPageURL + "?sID=" + sid + "&dID=" + deviceID + "&cType=9",
		ExpiresIn: QrLifetime,
	}, nil
}

// qrPollPayload 是网页版扫码页 queryQrcLoginResult() 真正发的那一条：
//
//	{account:"", dycPwd:<sID>, type:5, authLogin:true}
//
// 经 AES-256-CBC 包成一串 base64 直接当请求体（不是 {"encryptMsg":…} 信封）。
//
// 老实现发的是飞信那套 {dycpwd,secinfo,cpid,clienttype,pintype:21,…}，上游回
// 200059541「未找到设备的二维码信息」——那根本不是"等待扫码"，而是"这个设备根本
// 没有二维码会话"。结果就是：控制台一直显示"等手机扫码"，手机扫到的却是个不存在
// 的会话，于是立刻提示"二维码已失效"。
type qrPollPayload struct {
	Account   string `json:"account"`
	DycPwd    string `json:"dycPwd"`
	Type      int    `json:"type"`
	AuthLogin bool   `json:"authLogin"`
}

func qrPollBody(sid string) ([]byte, error) {
	plain, err := json.Marshal(qrPollPayload{Account: "", DycPwd: sid, Type: 5, AuthLogin: true})
	if err != nil {
		return nil, err
	}
	enc, err := qrEncryptCBC(plain)
	if err != nil {
		return nil, err
	}
	return []byte(enc), nil
}

// qrStatusFromCode 把上游返回码翻成界面状态。
//
// 关键一条：200059541 的原话是「未找到设备的二维码信息」、9999 是「登录失败」——
// 这两个都不是错误，而是"还没人扫/手机还没确认"。网页版对它们一律继续每 2 秒轮询，
// 我们照做；真正表示完蛋的是 542（过期）、549（取消）和 543/545/546/547。
func qrStatusFromCode(code, message string) *QRResult {
	out := &QRResult{Code: code}
	switch code {
	case "200059548":
		out.Status, out.Message = QrScanned, "已扫码，请在手机上点「确认登录」"
	case "200059542":
		out.Status, out.Message = QrExpired, "二维码已过期，请重新生成"
	case "200059549":
		out.Status, out.Message = QrCancelled, "手机上取消了登录"
	case "200059543", "200059545", "200059546", "200059547":
		out.Status = QrFailed
		out.Message = "扫码登录失败（code " + code + "），请重新生成二维码"
	default:
		out.Status = QrWaiting
		out.Message = "等待手机扫码"
		if code != "" && code != "200059541" && code != "9999" {
			// 不认识的码也先当"继续等"，但把码带上，方便对日志。
			out.Message = fmt.Sprintf("等待手机扫码（code %s）", code)
		}
		if strings.TrimSpace(message) != "" && code == "" {
			out.Message = strings.TrimSpace(message)
		}
	}
	return out
}

// PollQrLogin 带着 handle 问一次上游：扫了没 / 能不能给令牌。
func PollQrLogin(ctx context.Context, handle string) (*QRResult, error) {
	var h qrHandle
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(handle))
	if err == nil {
		err = json.Unmarshal(raw, &h)
	}
	if err != nil || h.Sid == "" || h.Skey == "" {
		return nil, errors.New("扫码会话无效，请重新生成二维码")
	}

	plain, err := json.Marshal(qrPollPayload{Account: "", DycPwd: h.Sid, Type: 5, AuthLogin: true})
	if err != nil {
		return nil, err
	}
	body, err := qrEncryptCBC(plain)
	if err != nil {
		return nil, err
	}

	hdr := qrBaseHeaders()
	hdr["Caller"] = "web"
	hdr["CMS-DEVICE"] = "default"
	hdr["mcloud-route"] = "001"
	hdr["x-DeviceInfo"] = qrDeviceInfo(h.DeviceID)
	hdr["x-yun-client-info"] = qrClientInfo(h.DeviceID)
	hdr["hcy-cool-flag"] = "1"
	hdr["x-yun-api-version"] = "v1"
	hdr["x-yun-app-channel"] = qrChannelSrc
	hdr["x-yun-channel-source"] = qrChannelSrc
	hdr["x-yun-module-type"] = "100"
	hdr["x-yun-svc-type"] = "1"
	hdr["x-SvcType"] = "1"
	hdr["mcloud-skey"] = h.Skey
	ts, rnd := qrSignParts()
	// 注意：签的是明文 payload，不是信封。
	hdr["mcloud-sign"] = ts + "," + rnd + "," + calSign(string(plain), ts, rnd)

	obj, err := qrPostJSON(ctx, qrLoginURL, hdr, []byte(body))
	if err != nil {
		return nil, err
	}

	code := asString(obj["code"])
	ok := code == "0000"
	if v, has := obj["success"]; has {
		if b, isBool := v.(bool); isBool && b {
			ok = true
		}
	}
	if !ok {
		msg := asString(obj["message"])
		if msg == "" {
			msg = asString(obj["msg"])
		}
		return qrStatusFromCode(code, msg), nil
	}

	acc, tok, err := qrDecodeData(obj["data"])
	if err != nil {
		return nil, err
	}
	auth := base64.StdEncoding.EncodeToString([]byte("pc:" + acc + ":" + tok))
	switch {
	case strings.HasPrefix(tok, "pc:"), strings.HasPrefix(tok, "Basic "):
		// 上游换了口子，直接给了一整串授权
		auth = base64.StdEncoding.EncodeToString([]byte(strings.TrimPrefix(tok, "Basic ")))
	}
	return &QRResult{Status: QrConfirmed, Account: acc, Authorization: auth, Message: "登录成功"}, nil
}

// qrFetchPublicKey 取 base64(DER) 形式的 RSA 公钥。
func qrFetchPublicKey(ctx context.Context) (string, error) {
	body := []byte(`{"clientCode":"10701","type":1}`)
	hdr := qrBaseHeaders()
	hdr["mcloud-version"] = "7.14.0"
	hdr["x-DeviceInfo"] = "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||"
	hdr["x-SvcType"] = "1"
	ts, rnd := qrSignParts()
	hdr["mcloud-sign"] = ts + "," + rnd + "," + calSign(string(body), ts, rnd)

	obj, err := qrPostJSON(ctx, qrPublicKeyURL, hdr, body)
	if err != nil {
		return "", err
	}
	key, found := qrFindString(obj, "publicKey")
	if !found || key == "" {
		return "", errors.New("公钥接口没有返回 publicKey")
	}
	return key, nil
}

// qrPayload 是 thirdlogin 的明文请求体。字段顺序不能动——mcloud-sign 是对
// 这串字符做签名再比对的。

func qrBaseHeaders() map[string]string {
	return map[string]string{
		"Accept":                 "application/json;charset=UTF-8",
		"Content-Type":           "application/json;charset=UTF-8",
		"User-Agent":             qrUA,
		"mcloud-channel":         "1000101",
		"mcloud-client":          qrClientCode,
		"mcloud-version":         qrVersion,
		"Origin":                 "https://yun.139.com",
		"Referer":                "https://yun.139.com",
		"x-huawei-channelSrc":    qrChannelSrc,
		"x-inner-ntwk":           "2",
		"x-m4c-caller":           "PC",
		"x-m4c-src":              "10002",
		"Inner-Hcy-Router-Https": "1",
	}
}

func qrDeviceInfo(deviceID string) string {
	return "||9|" + qrVersion + "|edge||" + deviceID + "||macos 10.15.7||zh-CN|||"
}

func qrClientInfo(deviceID string) string {
	return qrDeviceInfo(deviceID) + "ZWRnZQ==||"
}

// qrPostJSON 发一个 POST，返回解析后的 JSON 对象；响应被 AES 信封包住时自动解开。
func qrPostJSON(ctx context.Context, url string, hdr map[string]string, body []byte) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := qrHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", qrHostOf(url), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 %s 响应失败: %w", qrHostOf(url), err)
	}
	obj, err := qrParseObject(raw)
	if err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("%s 返回 HTTP %d: %s", qrHostOf(url), resp.StatusCode, qrClip(string(raw), 160))
		}
		return nil, err
	}
	return obj, nil
}

// qrParseObject 先按 JSON 解析，不行就当成 AES-256-CBC 信封解开再解析。
func qrParseObject(raw []byte) (map[string]any, error) {
	if obj, err := qrDecodeObject(raw); err == nil && obj != nil {
		return obj, nil
	}
	plain, err := qrDecryptCBC(string(raw))
	if err == nil {
		if obj, err2 := qrDecodeObject(plain); err2 == nil && obj != nil {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("响应无法解析: %s", qrClip(strings.TrimSpace(string(raw)), 160))
}

func qrDecodeObject(raw []byte) (map[string]any, error) {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// qrEncryptCBC 把明文包成 base64(IV||密文)。
func qrEncryptCBC(plain []byte) (string, error) {
	block, err := aes.NewCipher([]byte(qrAuthKey))
	if err != nil {
		return "", err
	}
	iv := make([]byte, block.BlockSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	buf := qrPKCS7Pad(plain, block.BlockSize())
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(buf, buf)
	return base64.StdEncoding.EncodeToString(append(iv, buf...)), nil
}

// qrDecryptCBC 解 base64(IV||密文)。
func qrDecryptCBC(text string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(qrAuthKey))
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	if len(raw) < 2*bs || len(raw)%bs != 0 {
		return nil, errors.New("信封长度不对")
	}
	out := make([]byte, len(raw)-bs)
	cipher.NewCBCDecrypter(block, raw[:bs]).CryptBlocks(out, raw[bs:])
	return qrPKCS7Unpad(out, bs)
}

// qrDecryptECB 解 AES-128-ECB（thirdlogin 的 data 字段）。
func qrDecryptECB(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	if len(data) == 0 || len(data)%bs != 0 {
		return nil, errors.New("密文长度不是块大小的整数倍")
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += bs {
		block.Decrypt(out[i:i+bs], data[i:i+bs])
	}
	return qrPKCS7Unpad(out, bs)
}

func qrPKCS7Pad(in []byte, bs int) []byte {
	n := bs - len(in)%bs
	out := make([]byte, len(in)+n)
	copy(out, in)
	for i := len(in); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func qrPKCS7Unpad(in []byte, bs int) ([]byte, error) {
	if len(in) == 0 || len(in)%bs != 0 {
		return nil, errors.New("填充长度不对")
	}
	n := int(in[len(in)-1])
	if n == 0 || n > bs || n > len(in) {
		return nil, errors.New("填充内容不对")
	}
	for _, b := range in[len(in)-n:] {
		if int(b) != n {
			return nil, errors.New("填充内容不对")
		}
	}
	return in[:len(in)-n], nil
}

// qrEncryptRSA 用公钥做 PKCS#1 v1.5 加密，输出 base64。
func qrEncryptRSA(text, pubB64 string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil {
		return "", fmt.Errorf("公钥不是合法 base64: %w", err)
	}
	var pub *rsa.PublicKey
	if parsed, err := x509.ParsePKIXPublicKey(der); err == nil {
		p, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return "", errors.New("公钥类型不是 RSA")
		}
		pub = p
	} else if p, err := x509.ParsePKCS1PublicKey(der); err == nil {
		pub = p
	} else {
		return "", fmt.Errorf("公钥解析失败: %w", err)
	}
	out, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(text))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// qrDecodeData 解 thirdlogin 成功响应里的 data，取出账号与令牌。
func qrDecodeData(v any) (account, token string, err error) {
	if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" {
		if bin, derr := hex.DecodeString(strings.TrimSpace(s)); derr == nil {
			if plain, derr2 := qrDecryptECB([]byte(qrDataKey), bin); derr2 == nil {
				if obj, derr3 := qrDecodeObject(plain); derr3 == nil {
					return qrPickAccount(obj), qrPickToken(obj), nil
				}
			}
		}
	}
	if obj, isObj := v.(map[string]any); isObj && len(obj) > 0 {
		return qrPickAccount(obj), qrPickToken(obj), nil
	}
	return "", "", errors.New("登录响应里没有找到账号和令牌")
}

func qrPickAccount(obj map[string]any) string {
	for _, k := range []string{"account", "simplifyAccount", "userName", "username", "msisdn", "phone"} {
		if s := asString(obj[k]); s != "" {
			return s
		}
	}
	return ""
}

func qrPickToken(obj map[string]any) string {
	for _, k := range []string{"token", "authToken", "authorization", "Authorization"} {
		s := asString(obj[k])
		if s == "" {
			continue
		}
		s = strings.TrimPrefix(s, "Basic ")
		if !strings.HasPrefix(s, "pc:") {
			s = strings.TrimPrefix(s, "pc:")
			s = strings.TrimPrefix(s, ":")
		}
		return s
	}
	return ""
}

// qrFindString 在任意深度的 JSON 里找第一个非空字符串字段。
func qrFindString(v any, key string) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		if raw, has := t[key]; has {
			if s := asString(raw); s != "" {
				return s, true
			}
		}
		for _, item := range t {
			if s, found := qrFindString(item, key); found {
				return s, true
			}
		}
	case []any:
		for _, item := range t {
			if s, found := qrFindString(item, key); found {
				return s, true
			}
		}
	}
	return "", false
}

// asString 把 JSON 的任意标量摊平成字符串（UseNumber 下数字是 json.Number）。
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func qrSignParts() (string, string) {
	return strconv.FormatInt(time.Now().UnixMilli(), 10), qrRandString(8)
}

const qrAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func qrRandString(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = qrAlphabet[mrand.Intn(len(qrAlphabet))]
	}
	return string(out)
}

func qrRandHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = byte(mrand.Intn(256))
		}
	}
	return hex.EncodeToString(buf)
}

func qrHostOf(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i > 0 {
		return s[:i]
	}
	return s
}

func qrClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
