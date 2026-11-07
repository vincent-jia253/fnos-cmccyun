package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 控制台访问密码（v1.8.0）。
//
// 背景：控制台监听 0.0.0.0:19898，飞牛的网页上一敲 http://<NAS>:19898/ 就能进，
// 进去就能改挂载点、看授权状态、导出配置——局域网里任何一台设备都能干这事。
// 所以控制台整体挂在登录门后面：默认用户名/密码都是 admin，进页面后能改。
//
// 不受登录门保护的只有三处，各自有自己的凭据：
//   /dav/          WebDAV（每个账号自己的 dav_user / dav_password）
//   /api/strm/play STRM 直链（播放器带的 k 口令，常数时间比较）
//   /login /api/login /api/logout     登录口本身
//
// 密码用 PBKDF2-HMAC-SHA256 存哈希（不用明文），会话是 HMAC 签名的无状态 cookie：
// 进程重启不用重登，改密码则立刻让所有旧 cookie 失效。

const (
	consoleCookieName    = "cmcc_auth"
	consoleSessionTTL    = 7 * 24 * time.Hour
	consoleDefaultUser   = "admin"
	consoleDefaultPass   = "admin"
	consolePBKDF2Iter    = 120000
	consolePBKDF2KeyLen  = 32
	consoleSaltBytes     = 16
	consolePasswordLabel = "pbkdf2-sha256"
)

// ---- 密码哈希 --------------------------------------------------------------

// pbkdf2SHA256 是 RFC 8018 的 PBKDF2-HMAC-SHA256（stdlib 没有，标准库够用就自己写，
// 免得为一个小功能再拉一个依赖）。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	u := make([]byte, hashLen)
	var counter [4]byte
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		counter[0] = byte(block >> 24)
		counter[1] = byte(block >> 16)
		counter[2] = byte(block >> 8)
		counter[3] = byte(block)
		prf.Write(counter[:])
		out = prf.Sum(out)
		t := out[len(out)-hashLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return out[:keyLen]
}

// hashConsolePassword 产出 "pbkdf2-sha256$<iter>$<b64 salt>$<b64 hash>"。
func hashConsolePassword(password string) (string, error) {
	salt := make([]byte, consoleSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2SHA256([]byte(password), salt, consolePBKDF2Iter, consolePBKDF2KeyLen)
	return fmt.Sprintf("%s$%d$%s$%s", consolePasswordLabel, consolePBKDF2Iter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// verifyConsolePassword 校验密码；stored 为空表示"还没设过"，此时只认默认密码 admin。
func verifyConsolePassword(stored, password string) bool {
	if strings.TrimSpace(stored) == "" {
		return subtle.ConstantTimeCompare([]byte(password), []byte(consoleDefaultPass)) == 1
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != consolePasswordLabel {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 || iter > 5_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- 会话 cookie -----------------------------------------------------------

// consoleSecret 由「用户名 + 密码哈希」派生：改密码 = 换密钥 = 所有旧会话立刻失效，
// 不需要在磁盘上多存一份随机密钥。
func consoleSecret(user, storedPassword string) []byte {
	sum := sha256.Sum256([]byte("cmccyun-console|" + user + "|" + storedPassword))
	return sum[:]
}

func consoleToken(user, storedPassword string, now time.Time) string {
	exp := now.Add(consoleSessionTTL).Unix()
	payload := fmt.Sprintf("%d", exp)
	mac := hmac.New(sha256.New, consoleSecret(user, storedPassword))
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func consoleTokenValid(token, user, storedPassword string, now time.Time) bool {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, consoleSecret(user, storedPassword))
	mac.Write([]byte(payload))
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), want)
}

// consoleAuthExempt 是「不走登录门」的路径：这些端点各有各的凭据。
func consoleAuthExempt(p string) bool {
	switch {
	case p == "/login" || p == "/api/login" || p == "/api/logout":
		return true
	case p == "/favicon.ico":
		return true
	case strings.HasPrefix(p, "/dav/") || p == "/dav":
		return true
	case p == "/api/strm/play":
		return true
	}
	return false
}

// consoleAuthorized 判断这次请求有没有带有效的会话 cookie。
func (s *Service) consoleAuthorized(r *http.Request) bool {
	if s.consoleAuthDisabled() {
		return true
	}
	cookie, err := r.Cookie(consoleCookieName)
	if err != nil {
		return false
	}
	user, stored := s.consoleCredentials()
	return consoleTokenValid(cookie.Value, user, stored, time.Now())
}

func (s *Service) consoleAuthDisabled() bool {
	return s.Config().WebAuthDisabled
}

// consoleCredentials 返回当前生效的用户名与密码哈希（空哈希 = 默认密码 admin）。
func (s *Service) consoleCredentials() (string, string) {
	cfg := s.Config()
	user := strings.TrimSpace(cfg.WebUser)
	if user == "" {
		user = consoleDefaultUser
	}
	return user, cfg.WebPassword
}

// consoleAuth 是包住整个 mux 的登录门：页面未登录跳 /login，接口未登录回 401 JSON。
func (s *Service) consoleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if consoleAuthExempt(p) || s.consoleAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{
				"ok":    false,
				"error": "需要登录控制台：请先打开 http://<NAS-IP>:<端口>/ 输入控制台密码",
			})
			return
		}
		http.Redirect(w, r, "/login?next="+urlQueryEscape(p), http.StatusFound)
	})
}

func urlQueryEscape(v string) string {
	var b strings.Builder
	for _, c := range []byte(v) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---- 登录 / 退出 / 改密码 ---------------------------------------------------

const loginPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>移动云盘 · 控制台登录</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;background:#f4f6f8;color:#1c2229}
@media (prefers-color-scheme:dark){body{background:#14181c;color:#e6edf3}}
.card{width:340px;max-width:calc(100vw - 32px);background:#fff;border-radius:14px;padding:26px 24px;
  box-shadow:0 8px 30px rgba(0,0,0,.09)}
@media (prefers-color-scheme:dark){.card{background:#1d2329;box-shadow:none;border:1px solid #2b333b}}
h1{font-size:17px;margin:0 0 4px}
p.sub{margin:0 0 18px;color:#6b7784;font-size:13px}
label{display:block;font-size:13px;color:#6b7784;margin:12px 0 6px}
input{width:100%;box-sizing:border-box;padding:10px 12px;border:1px solid #d3dae1;border-radius:9px;
  font-size:14px;background:transparent;color:inherit}
input:focus{outline:2px solid #2f81f7;border-color:transparent}
button{width:100%;margin-top:18px;padding:11px;border:0;border-radius:9px;background:#2f81f7;color:#fff;
  font-size:15px;cursor:pointer}
button:disabled{opacity:.6;cursor:default}
.msg{margin-top:12px;font-size:13px;min-height:18px;color:#e5534b}
.foot{margin-top:16px;font-size:12px;color:#8b97a3}
</style></head><body>
<form class="card" method="post" action="/api/login">
  <h1>移动云盘 · 控制台</h1>
  <p class="sub">需要密码才能打开。默认 <code>admin / admin</code>，登录后可改。</p>
  <label>用户名</label><input name="user" value="admin" autocomplete="username" autofocus>
  <label>密码</label><input name="password" type="password" autocomplete="current-password">
  <input type="hidden" name="next" value="{{NEXT}}">
  <button type="submit">登录</button>
  <div class="msg">{{ERROR}}</div>
  <div class="foot">仅本机与可信内网使用；忘了密码可改 config.json 里删掉 web_password 后重启（回到默认 admin）。</div>
</form></body></html>`

// handleLoginPage 渲染登录页。
func (s *Service) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.consoleAuthorized(r) && !s.consoleAuthDisabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	errText := ""
	if r.URL.Query().Get("bad") == "1" {
		errText = "用户名或密码不对"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	page := strings.ReplaceAll(loginPageHTML, "{{NEXT}}", html.EscapeString(next))
	page = strings.ReplaceAll(page, "{{ERROR}}", html.EscapeString(errText))
	_, _ = w.Write([]byte(page))
}

// handleLogin 校验用户名密码并下发会话 cookie。同时接受表单和 JSON。
func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	user, password, next := "", "", "/"
	ctype := r.Header.Get("Content-Type")
	if strings.HasPrefix(ctype, "application/json") {
		var body struct {
			User     string `json:"user"`
			Password string `json:"password"`
			Next     string `json:"next"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		user, password, next = body.User, body.Password, body.Next
	} else {
		_ = r.ParseForm()
		user, password, next = r.PostFormValue("user"), r.PostFormValue("password"), r.PostFormValue("next")
	}
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}

	wantUser, stored := s.consoleCredentials()
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(user)), []byte(wantUser)) == 1
	passOK := verifyConsolePassword(stored, password)
	if !userOK || !passOK {
		s.logf("控制台登录失败（用户 %q，来自 %s）", strings.TrimSpace(user), r.RemoteAddr)
		if strings.HasPrefix(ctype, "application/json") {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "用户名或密码不对"})
			return
		}
		http.Redirect(w, r, "/login?bad=1&next="+urlQueryEscape(next), http.StatusFound)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     consoleCookieName,
		Value:    consoleToken(wantUser, stored, time.Now()),
		Path:     "/",
		MaxAge:   int(consoleSessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.logf("控制台登录成功（用户 %s，来自 %s）", wantUser, r.RemoteAddr)
	if strings.HasPrefix(ctype, "application/json") {
		writeJSON(w, map[string]any{"ok": true, "user": wantUser})
		return
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// handleLogout 清掉会话 cookie。
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: consoleCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
	})
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") ||
		strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// handleConsolePassword 改控制台密码：必须带旧密码（会话被偷也不能直接改掉）。
func (s *Service) handleConsolePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, "请用 POST")
		return
	}
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
		User     string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, "请求格式错误: "+err.Error())
		return
	}
	_, stored := s.consoleCredentials()
	if !verifyConsolePassword(stored, body.Current) {
		writeErr(w, "当前密码不对")
		return
	}
	if body.Password != body.Confirm {
		writeErr(w, "两次输入的新密码不一致")
		return
	}
	if len(body.Password) < 4 {
		writeErr(w, "新密码至少 4 位")
		return
	}
	hash, err := hashConsolePassword(body.Password)
	if err != nil {
		writeErr(w, "生成密码哈希失败: "+err.Error())
		return
	}
	if err := s.UpdateConsoleAuth(body.User, hash); err != nil {
		writeErr(w, err.Error())
		return
	}
	// 密码一变，旧 cookie 全部作废：这里顺手给当前会话换一张新票。
	user, newStored := s.consoleCredentials()
	http.SetCookie(w, &http.Cookie{
		Name: consoleCookieName, Value: consoleToken(user, newStored, time.Now()),
		Path: "/", MaxAge: int(consoleSessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, map[string]any{"ok": true, "message": "控制台密码已更新（其它设备上的登录已失效）"})
}
