package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// 授权串的形状是 base64("pc:<账号>:<令牌>")，测试里造一条假的就够：
// 我们要验的是 WebDAV 这层的鉴权/开关，不是云端。
func fakeAuth() string {
	return base64.StdEncoding.EncodeToString([]byte("pc:13800000000:test-token"))
}

func davSvc(t *testing.T) *Service {
	t.Helper()
	s, _ := newMountSvc(t, "")
	acct0(s.cfg).Authorization = fakeAuth()
	return s
}

func davPost(t *testing.T, s *Service, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// 默认关闭：WebDAV 是新增的网络入口，没显式打开之前谁都不许进来。
func TestDAVOffByDefault(t *testing.T) {
	s := davSvc(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/dav/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未启用时应 503，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "未启用") {
		t.Errorf("应提示去控制台打开开关，实际：%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dav/info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/dav/info 应 200，实际 %d", rec.Code)
	}
	var resp struct {
		DAV struct {
			Enabled  bool   `json:"enabled"`
			Password string `json:"password"`
			User     string `json:"user"`
		} `json:"dav"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.DAV.Enabled {
		t.Error("默认不该是启用的")
	}
	if resp.DAV.User != config.DefaultDAVUser {
		t.Errorf("默认用户名应是 %q，实际 %q", config.DefaultDAVUser, resp.DAV.User)
	}
	if resp.DAV.Password != "" {
		t.Error("没打开开关前不该有密码")
	}
}

// 打开开关必须同时生成一个像样的随机密码，并且写进配置文件（重启后仍可用）。
func TestDAVEnableGeneratesPassword(t *testing.T) {
	s := davSvc(t)
	rec := davPost(t, s, "/api/dav/config", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("打开开关失败：%d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		DAV struct {
			Enabled  bool     `json:"enabled"`
			Password string   `json:"password"`
			URLs     []string `json:"urls"`
		} `json:"dav"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.DAV.Enabled {
		t.Fatal("开关没生效")
	}
	if len(resp.DAV.Password) != 16 {
		t.Errorf("密码应是 16 位随机串，实际 %q（%d 位）", resp.DAV.Password, len(resp.DAV.Password))
	}
	if len(resp.DAV.URLs) == 0 || !strings.HasSuffix(resp.DAV.URLs[0], "/dav/") {
		t.Errorf("要给出可粘贴的地址（以 /dav/ 结尾），实际 %v", resp.DAV.URLs)
	}

	// 落盘：重新 Load 一次还在。
	cfg2, err := config.Load(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg2.DAVEnabled || acctView(*cfg2).DAVPassword != resp.DAV.Password {
		t.Error("开关/密码没有写进配置文件，重启就丢了")
	}

	// 关掉再开，密码不能换（用户已经填进飞牛了，换掉等于断掉）。
	pw := resp.DAV.Password
	_ = davPost(t, s, "/api/dav/config", `{"enabled":false}`)
	_ = davPost(t, s, "/api/dav/config", `{"enabled":true}`)
	if got := acctView(s.Config()).DAVPassword; got != pw {
		t.Errorf("重新打开开关不该换密码：原 %q 现 %q", pw, got)
	}

	// 显式重置才换。
	before := acctView(s.Config()).DAVPassword
	_ = davPost(t, s, "/api/dav/config", `{"regenerate":true}`)
	after := acctView(s.Config()).DAVPassword
	if after == before || len(after) != 16 {
		t.Errorf("regenerate 应换一个新的 16 位密码，原 %q 现 %q", before, after)
	}
}

// BasicAuth 是这一层唯一的门：没带、带错都要 401 并给 WWW-Authenticate，
// 带对了才允许继续（这里没有真云端，所以只验「不是 401」）。
func TestDAVAuthGate(t *testing.T) {
	s := davSvc(t)
	if rec := davPost(t, s, "/api/dav/config", `{"enabled":true,"password":"test-pass-123"}`); rec.Code != http.StatusOK {
		t.Fatalf("设置密码失败：%s", rec.Body.String())
	}
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/dav/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("没带凭据应 401，实际 %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("401 必须带 WWW-Authenticate，否则客户端不会弹登录框：%q", rec.Header().Get("WWW-Authenticate"))
	}

	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.SetBasicAuth("cmccyun", "错的密码")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("密码错误应 401，实际 %d", rec.Code)
	}

	req = httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.SetBasicAuth("cmccyun", "test-pass-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("正确凭据被拒了")
	}
}

// 只读模式下写方法必须当场 403（不能等 webdav 库把 ErrPermission 翻成 404）。
func TestDAVReadOnlyBlocksWrites(t *testing.T) {
	s := davSvc(t)
	_ = davPost(t, s, "/api/dav/config", `{"enabled":true,"password":"test-pass-123"}`)
	acct0(s.cfg).ReadOnly = true
	davReset(s)

	for _, m := range []string{"PUT", "DELETE", "MKCOL", "MOVE", "COPY"} {
		req := httptest.NewRequest(m, "/dav/01影视/x.mkv", strings.NewReader("x"))
		req.SetBasicAuth("cmccyun", "test-pass-123")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("只读模式 %s 应 403，实际 %d：%s", m, rec.Code, rec.Body.String())
		}
	}

	// 读方法不拦（这里会走到 davProxy，没有真云端所以 500 之类都行，
	// 唯独不能是 403）。
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	req.SetBasicAuth("cmccyun", "test-pass-123")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Error("只读不该拦住 PROPFIND")
	}
}

// /api/dav/config 只收 POST；GET 给 400 提示，不能悄悄改配置。
func TestDAVConfigNeedsPOST(t *testing.T) {
	s := davSvc(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dav/config", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET 应 400，实际 %d", rec.Code)
	}
}

// 控制台得有这个页签：飞牛用户是照着页面上的地址/账号/密码去填的。
func TestDAVTabInConsole(t *testing.T) {
	for _, want := range []string{
		`data-tab="dav"`,
		`id="tab-dav"`,
		`id="davEnabled"`,
		`id="davUser"`,
		`id="davPass"`,
		`id="davURL"`,
		`id="btnDavCopy"`,
		`id="btnDavReset"`,
		"远程挂载",
		"WebDAV",
		"挂载远程文件夹",
		"/api/dav/info",
		"/api/dav/config",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("界面缺少 %q（远程挂载页是路线 B 的入口）", want)
		}
	}
	if strings.Contains(indexHTML, "cdn.jsdelivr") || strings.Contains(indexHTML, "unpkg.com") {
		t.Error("控制台不许引外网资源")
	}
}
