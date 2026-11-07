package api

import (
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// qrTestECBEncrypt 只给测试用：把 JSON 按 AES-128-ECB 加密再转 hex，
// 模拟 thirdlogin 成功响应里的 data 字段。
func qrTestECBEncrypt(t *testing.T, key, plain []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	bs := block.BlockSize()
	buf := qrPKCS7Pad(plain, bs)
	out := make([]byte, len(buf))
	for i := 0; i < len(buf); i += bs {
		block.Encrypt(out[i:i+bs], buf[i:i+bs])
	}
	return hex.EncodeToString(out)
}

func TestQrPayloadMatchesWebClient(t *testing.T) {
	sid := "abcdefghijklmnop"
	sum := sha1.Sum([]byte("fetion.com.cn:" + sid))
	want := `{"msisdn":"","random":"","dycpwd":"abcdefghijklmnop","cpid":292,"clienttype":670,` +
		`"version":"mCloud_4.3.0_536","pintype":21,"secinfo":"` + strings.ToUpper(hex.EncodeToString(sum[:])) + `",` +
		`"loginMode":"0","extInfo":{}}`
	if got := string(qrPayload(sid)); got != want {
		t.Fatalf("payload 不一致\n got: %s\nwant: %s", got, want)
	}
}

func TestQrEnvelopeRoundTrip(t *testing.T) {
	plain := []byte(`{"hello":"世界"}`)
	env, err := qrEncryptCBC(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := qrDecryptCBC(env)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("往返不一致: %s", got)
	}
	// 信封应该是 base64(16 字节 IV + 密文)
	raw, err := base64.StdEncoding.DecodeString(env)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if len(raw)%16 != 0 || len(raw) < 32 {
		t.Fatalf("信封长度不对: %d", len(raw))
	}
	if _, err := qrDecryptCBC("bm90LWEtZW52ZWxvcGU="); err == nil {
		t.Fatal("坏信封应该报错")
	}
}

func TestQrDecodeDataFromHex(t *testing.T) {
	plain := []byte(`{"account":"13474089621","token":"abc|1|RCS|1893456000000|zzz"}`)
	data := qrTestECBEncrypt(t, []byte(qrDataKey), plain)
	account, token, err := qrDecodeData(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if account != "13474089621" || token != "abc|1|RCS|1893456000000|zzz" {
		t.Fatalf("解出来的东西不对: %q / %q", account, token)
	}
	// 对象形态（上游偶尔直接给 JSON）
	acc2, tok2, err := qrDecodeData(map[string]any{"simplifyAccount": "13900000000", "authToken": "t2"})
	if err != nil || acc2 != "13900000000" || tok2 != "t2" {
		t.Fatalf("对象形态解析失败: %v %q %q", err, acc2, tok2)
	}
	if _, _, err := qrDecodeData("zzzz"); err == nil {
		t.Fatal("乱码应该报错")
	}
}

func TestQrLoginFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(der)

	var (
		gotSkey  string
		gotSid   string
		polled   int
		signOK   int
		sawPlain string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pubkey":
			if r.Header.Get("mcloud-sign") == "" {
				t.Errorf("公钥请求缺 mcloud-sign")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "0000", "data": map[string]any{"publicKey": pubB64},
			})
		case "/login":
			polled++
			gotSkey = r.Header.Get("mcloud-skey")
			if gotSkey == "" {
				t.Errorf("轮询缺 mcloud-skey")
			}
			body, rerr := io.ReadAll(r.Body)
			if rerr != nil {
				t.Fatalf("读 body: %v", rerr)
			}
			plain, err := qrDecryptCBC(string(body))
			if err != nil {
				t.Fatalf("请求体不是合法信封: %v", err)
			}
			sawPlain = string(plain)
			var payload struct {
				Dycpwd  string `json:"dycpwd"`
				Secinfo string `json:"secinfo"`
			}
			_ = json.Unmarshal(plain, &payload)
			gotSid = payload.Dycpwd
			sum := sha1.Sum([]byte("fetion.com.cn:" + payload.Dycpwd))
			if payload.Secinfo != strings.ToUpper(hex.EncodeToString(sum[:])) {
				t.Errorf("secinfo 不对: %s", payload.Secinfo)
			}
			if strings.HasPrefix(r.Header.Get("mcloud-sign"), "") && r.Header.Get("mcloud-sign") != "" {
				signOK++
			}
			switch polled {
			case 1:
				// 明文 JSON：还没扫
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "200059541", "message": "wait"})
			case 2:
				// 信封 + 已扫码
				env, _ := qrEncryptCBC([]byte(`{"code":"200059548"}`))
				_, _ = w.Write([]byte(env))
			default:
				// 信封 + 成功，data 是 hex(AES-128-ECB)
				data := qrTestECBEncrypt(t, []byte(qrDataKey),
					[]byte(`{"account":"13474089621","token":"tok|1|RCS|1893456000000|sig"}`))
				env, _ := qrEncryptCBC([]byte(`{"success":true,"data":"` + data + `"}`))
				_, _ = w.Write([]byte(env))
			}
		default:
			t.Errorf("意外请求 %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	oldPub, oldLogin, oldClient := qrPublicKeyURL, qrLoginURL, qrHTTPClient
	qrPublicKeyURL = srv.URL + "/pubkey"
	qrLoginURL = srv.URL + "/login"
	qrHTTPClient = srv.Client()
	defer func() { qrPublicKeyURL, qrLoginURL, qrHTTPClient = oldPub, oldLogin, oldClient }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	start, err := StartQrLogin(ctx)
	if err != nil {
		t.Fatalf("StartQrLogin: %v", err)
	}
	if !strings.HasPrefix(start.Content, "https://yun.139.com/w/#/qrcLogin?sID=") ||
		!strings.Contains(start.Content, "&dID=") || !strings.HasSuffix(start.Content, "&cType=9") {
		t.Fatalf("二维码内容不对: %s", start.Content)
	}
	if start.ExpiresIn != QrLifetime {
		t.Fatalf("有效期不对: %v", start.ExpiresIn)
	}

	// skey 必须是「被公钥加过密的 AUTH_KEY」
	raw, err := base64.StdEncoding.DecodeString(start.Handle)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var h qrHandle
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("handle json: %v", err)
	}
	skeyBin, err := base64.StdEncoding.DecodeString(h.Skey)
	if err != nil {
		t.Fatalf("skey base64: %v", err)
	}
	dec, err := rsa.DecryptPKCS1v15(rand.Reader, key, skeyBin)
	if err != nil {
		t.Fatalf("skey 解不开: %v", err)
	}
	if string(dec) != qrAuthKey {
		t.Fatalf("skey 内容不对: %s", dec)
	}

	// 三次轮询：等待 → 已扫码 → 成功
	res, err := PollQrLogin(ctx, start.Handle)
	if err != nil || res.Status != QrWaiting {
		t.Fatalf("第 1 次轮询: %v %+v", err, res)
	}
	res, err = PollQrLogin(ctx, start.Handle)
	if err != nil || res.Status != QrScanned {
		t.Fatalf("第 2 次轮询: %v %+v", err, res)
	}
	res, err = PollQrLogin(ctx, start.Handle)
	if err != nil {
		t.Fatalf("第 3 次轮询: %v", err)
	}
	if res.Status != QrConfirmed {
		t.Fatalf("状态不是 confirmed: %+v", res)
	}
	if res.Account != "13474089621" {
		t.Fatalf("账号不对: %q", res.Account)
	}
	triplet, err := base64.StdEncoding.DecodeString(res.Authorization)
	if err != nil {
		t.Fatalf("Authorization 不是 base64: %v", err)
	}
	if string(triplet) != "pc:13474089621:tok|1|RCS|1893456000000|sig" {
		t.Fatalf("Authorization 内容不对: %s", triplet)
	}
	if gotSid != h.Sid {
		t.Fatalf("轮询用的 sid 不对: %q vs %q", gotSid, h.Sid)
	}
	if gotSkey != h.Skey {
		t.Fatal("轮询没有带上 skey")
	}
	if signOK == 0 {
		t.Fatal("轮询没有带 mcloud-sign")
	}
	if !strings.Contains(sawPlain, `"cpid":292`) {
		t.Fatalf("明文 payload 被改了: %s", sawPlain)
	}

	// 扫码拿到的授权必须能直接喂给现有客户端
	c, err := New(res.Authorization)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Account() != "13474089621" {
		t.Fatalf("客户端账号不对: %q", c.Account())
	}
}

func TestQrLoginStatusCodes(t *testing.T) {
	cases := map[string]QrStatus{
		"200059541": QrWaiting,
		"200059548": QrScanned,
		"200059542": QrExpired,
		"200059549": QrCancelled,
		"999999999": QrFailed,
	}
	for code, want := range cases {
		code := code
		want := want
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": "x"})
		}))
		oldPub, oldLogin, oldClient := qrPublicKeyURL, qrLoginURL, qrHTTPClient
		qrPublicKeyURL = srv.URL
		qrLoginURL = srv.URL
		qrHTTPClient = srv.Client()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		handle := base64.StdEncoding.EncodeToString([]byte(`{"sid":"s","deviceID":"d","skey":"k"}`))
		res, err := PollQrLogin(ctx, handle)
		cancel()
		srv.Close()
		qrPublicKeyURL, qrLoginURL, qrHTTPClient = oldPub, oldLogin, oldClient

		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if res.Status != want {
			t.Fatalf("%s: 期望 %s，实际 %s", code, want, res.Status)
		}
	}
}

func TestQrLoginBadHandle(t *testing.T) {
	ctx := context.Background()
	if _, err := PollQrLogin(ctx, "not-a-handle"); err == nil {
		t.Fatal("坏 handle 应该报错")
	}
	if _, err := PollQrLogin(ctx, ""); err == nil {
		t.Fatal("空 handle 应该报错")
	}
}
