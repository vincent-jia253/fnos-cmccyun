package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// 控制台密码：默认 admin；改完哈希落盘、旧会话立刻失效。
func TestConsolePasswordHashAndVerify(t *testing.T) {
	hash, err := hashConsolePassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Fatalf("哈希格式不对：%q", hash)
	}
	if strings.Contains(hash, "s3cret") {
		t.Fatal("哈希里不能出现明文")
	}
	if !verifyConsolePassword(hash, "s3cret") {
		t.Error("正确密码没通过")
	}
	if verifyConsolePassword(hash, "s3cret2") {
		t.Error("错误密码不该通过")
	}
	// 没设过密码时只认默认 admin
	if !verifyConsolePassword("", "admin") {
		t.Error("默认密码 admin 应可用")
	}
	if verifyConsolePassword("", "admin123") {
		t.Error("非默认密码不该通过")
	}
	if verifyConsolePassword("garbage", "admin") {
		t.Error("坏哈希不能当成默认密码放行")
	}
	// 两次哈希不同（盐不同），但都能验过
	hash2, _ := hashConsolePassword("s3cret")
	if hash == hash2 {
		t.Error("盐应该是随机的")
	}
	if !verifyConsolePassword(hash2, "s3cret") {
		t.Error("第二份哈希验不过")
	}
}

func TestConsoleSessionToken(t *testing.T) {
	now := time.Now()
	tok := consoleToken("admin", "", now)
	if !consoleTokenValid(tok, "admin", "", now.Add(time.Hour)) {
		t.Fatal("有效期内应通过")
	}
	if consoleTokenValid(tok, "admin", "", now.Add(consoleSessionTTL+time.Minute)) {
		t.Error("过期后应拒绝")
	}
	if consoleTokenValid(tok, "admin", "pbkdf2-sha256$1$aa$bb", now) {
		t.Error("改密码（哈希变了）后旧 token 必须失效")
	}
	if consoleTokenValid(tok+"x", "admin", "", now) {
		t.Error("被篡改的 token 必须拒绝")
	}
	if consoleTokenValid("no-dot", "admin", "", now) {
		t.Error("格式不对的 token 必须拒绝")
	}
}

// loginReq 走 JSON 形态：表单形态是给浏览器用的（成功/失败都 302），
// JSON 形态才能直接断言 200 / 401。
func loginReq(t *testing.T, s *Service, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"user": user, "password": pass})
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// loginFormReq 走浏览器表单形态，用来验证重定向行为。
func loginFormReq(t *testing.T, s *Service, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"user": {user}, "password": {pass}}
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func withCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == consoleCookieName {
			return c
		}
	}
	return nil
}

// 登录门：接口 401、页面跳登录页；登录后放行；/dav/ 与 STRM 直链不受影响。
func TestConsoleAuthGate(t *testing.T) {
	s, _ := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.WebAuthDisabled = false // newMountSvc 默认关掉登录门，这里显式打开
	s.mu.Unlock()

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api/status 应 401，得到 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/login") {
		t.Fatalf("未登录访问首页应跳 /login，得到 %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "控制台") {
		t.Fatalf("登录页打不开：%d", rec.Code)
	}

	// 密码错
	if rec = loginReq(t, s, "admin", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("错误密码应 401，得到 %d", rec.Code)
	}

	rec = loginReq(t, s, "admin", "admin")
	cookie := withCookie(rec)
	if rec.Code != http.StatusOK || cookie == nil {
		t.Fatalf("默认密码 admin 应能登录：%d cookie=%v", rec.Code, cookie)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("带 cookie 访问 /api/status 应 200，得到 %d", rec.Code)
	}
	var status map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	cfg, _ := status["config"].(map[string]any)
	if cfg["web_password_set"] != false {
		t.Error("默认密码状态下 web_password_set 应为 false")
	}

	// 浏览器表单形态：成功/失败都是 302（失败带 bad=1），并且都下发/清除 cookie
	if rec = loginFormReq(t, s, "admin", "admin"); rec.Code != http.StatusFound || withCookie(rec) == nil {
		t.Errorf("表单登录应 302 并下发 cookie，得到 %d", rec.Code)
	}
	if rec = loginFormReq(t, s, "admin", "nope"); rec.Code != http.StatusFound ||
		!strings.Contains(rec.Header().Get("Location"), "bad=1") {
		t.Errorf("表单密码错应跳 /login?bad=1，得到 %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// WebDAV 与 STRM 直链不走这道门（各有各的凭据）
	for _, p := range []string{"/dav/", "/api/strm/play?id=x&k=y"} {
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "需要登录控制台") {
			t.Errorf("%s 不该被控制台登录门拦", p)
		}
	}
}

// 改密码：旧密码错要拒；改完旧 cookie 失效、新密码可登录、登录名也能改。
func TestConsolePasswordChange(t *testing.T) {
	s, _ := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.WebAuthDisabled = false
	s.mu.Unlock()
	oldCookie := withCookie(loginReq(t, s, "admin", "admin"))
	if oldCookie == nil {
		t.Fatal("登录失败，拿不到 cookie")
	}

	change := func(body map[string]string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/console-password", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(oldCookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	if rec := change(map[string]string{"current": "nope", "password": "s3cret", "confirm": "s3cret"}); rec.Code == http.StatusOK {
		t.Fatal("当前密码不对时必须拒绝")
	}
	if rec := change(map[string]string{"current": "admin", "password": "s3cret", "confirm": "s3cre"}); rec.Code == http.StatusOK {
		t.Fatal("两次输入不一致必须拒绝")
	}
	if rec := change(map[string]string{"current": "admin", "password": "abc", "confirm": "abc"}); rec.Code == http.StatusOK {
		t.Fatal("太短的密码必须拒绝")
	}
	rec := change(map[string]string{"current": "admin", "password": "s3cret", "confirm": "s3cret", "user": "vincent"})
	if rec.Code != http.StatusOK {
		t.Fatalf("改密码应成功，得到 %d %s", rec.Code, rec.Body.String())
	}

	// 旧 cookie 失效
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(oldCookie)
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("改密码后旧会话应失效，得到 %d", rec2.Code)
	}

	// 旧密码不能登、新密码能登、登录名换成了 vincent
	if rec = loginReq(t, s, "admin", "admin"); rec.Code != http.StatusUnauthorized {
		t.Errorf("旧密码应失效，得到 %d", rec.Code)
	}
	if rec = loginReq(t, s, "vincent", "s3cret"); rec.Code != http.StatusOK {
		t.Errorf("新登录名 + 新密码应能登录，得到 %d", rec.Code)
	}

	// 哈希落盘，且 config.json 里没有明文
	loaded, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loaded.WebPassword, "pbkdf2-sha256$") {
		t.Errorf("配置里应存哈希，得到 %q", loaded.WebPassword)
	}
	if strings.Contains(loaded.WebPassword, "s3cret") {
		t.Error("配置里不能出现明文密码")
	}
	if loaded.WebUser != "vincent" {
		t.Errorf("登录名没保存：%q", loaded.WebUser)
	}
}

// web_auth_disabled=true 时整道门让开（测试/纯内网直连用）。
func TestConsoleAuthCanBeDisabled(t *testing.T) {
	s, _ := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.WebAuthDisabled = true
	s.mu.Unlock()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭登录门后 /api/status 应 200，得到 %d", rec.Code)
	}
}

// 界面必须给出入口：改密码、退出、默认密码提示，且说明 /dav/ 不受影响。
func TestConsoleAuthUIPresent(t *testing.T) {
	for _, want := range []string{
		`id="btnWebPass"`,
		`id="btnLogout"`,
		`id="webPassCur"`,
		`id="webPassNew"`,
		`id="webPassDefault"`,
		`/api/console-password`,
		`/api/logout`,
		`web_password_set`,
		"默认 <b>admin / admin</b>",
		"不受这个密码影响",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("控制台缺 %q", want)
		}
	}
	// 登录页本身要有用户名/密码字段和默认提示
	for _, want := range []string{`name="user"`, `name="password"`, "默认 <code>admin / admin</code>"} {
		if !strings.Contains(loginPageHTML, want) {
			t.Errorf("登录页缺 %q", want)
		}
	}
}
