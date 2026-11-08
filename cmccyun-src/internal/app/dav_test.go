package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// WebDAV 总览：每台账号一行，路径/用户名/密码/只读各自独立，总开关是全局的。
func TestDAVAccountsViewPerAccount(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.DAVEnabled = true
	s.cfg.Accounts = []config.AccountConfig{
		{ID: config.DefaultAccountID, Name: "主号", Account: "****9621",
			MountPoint: filepath.Join(root, "mnt1"), DAVUser: "cmccyun", DAVPassword: "pw-one"},
		{ID: "acc0123456789ab", Name: "副号", Account: "****7261", ReadOnly: true,
			MountPoint: filepath.Join(root, "mnt2"), DAVUser: "second", DAVPassword: "pw-two"},
	}
	s.cfg.DefaultAccount = config.DefaultAccountID
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}

	rows := s.DAVAccountsView()
	if len(rows) != 2 {
		t.Fatalf("应逐台返回，得到 %d 行", len(rows))
	}
	byID := map[string]map[string]any{}
	for _, r := range rows {
		byID[r["id"].(string)] = r
	}
	main, second := byID[config.DefaultAccountID], byID["acc0123456789ab"]
	if main == nil || second == nil {
		t.Fatalf("缺少账号行：%v", byID)
	}
	// 默认账号沿用老地址 /dav/，其余是 /dav/<id>/
	if main["path"] != "/dav/" {
		t.Errorf("默认账号路径应为 /dav/，得到 %v", main["path"])
	}
	if second["path"] != "/dav/acc0123456789ab/" {
		t.Errorf("第二账号路径应为 /dav/acc0123456789ab/，得到 %v", second["path"])
	}
	if main["user"] != "cmccyun" || second["user"] != "second" {
		t.Errorf("用户名应各自独立：%v / %v", main["user"], second["user"])
	}
	if main["password_set"] != true || second["password_set"] != true {
		t.Error("两台都设了密码，password_set 应为 true")
	}
	if second["read_only"] != true || main["read_only"] != false {
		t.Errorf("只读是分账号的：main=%v second=%v", main["read_only"], second["read_only"])
	}
	if main["enabled"] != true || second["enabled"] != true {
		t.Error("总开关打开时两台都应显示已启用")
	}
	if u, _ := second["url"].(string); !strings.Contains(u, "/dav/acc0123456789ab/") {
		t.Errorf("URL 应带账号路径，得到 %q", u)
	}
	// 关掉总开关：两台一起失效（这是全局的）
	s.mu.Lock()
	s.cfg.DAVEnabled = false
	s.mu.Unlock()
	for _, r := range s.DAVAccountsView() {
		if r["enabled"] != false {
			t.Errorf("关掉总开关后应全部 enabled=false：%v", r)
		}
	}
}

func TestDAVAccountsEndpoint(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.Accounts = append(s.cfg.Accounts, config.AccountConfig{
		ID: "acc0123456789ab", MountPoint: filepath.Join(root, "mnt2"), DAVUser: "u2", DAVPassword: "p2",
	})
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dav/accounts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	var body struct {
		OK       bool             `json:"ok"`
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || len(body.Accounts) != 2 {
		t.Fatalf("应返回两台，得到 ok=%v n=%d", body.OK, len(body.Accounts))
	}
}

// 界面：标题要标明当前账号，并且有「全部账号的 WebDAV」总览表。
func TestDavPageShowsAccountScope(t *testing.T) {
	for _, want := range []string{
		`id="davWho"`,
		`id="davAllWrap"`,
		`id="davAllBody"`,
		`/api/dav/accounts`,
		`data-dav-act="switch"`,
		"总开关是全局的",
		"按账号各一套",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("WebDAV 页缺 %q", want)
		}
	}
}

// 新建账号要拿到不重名的 WebDAV 用户名（默认值以前一律 cmccyun，两台必撞）。
func TestNewAccountsGetUniqueDAVUser(t *testing.T) {
	s, root := newMountSvc(t, "")
	first, err := s.AddAccount("一号", filepath.Join(root, "m1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddAccount("二号", filepath.Join(root, "m2"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	a1, _ := cfg.AccountByID(first)
	a2, _ := cfg.AccountByID(second)
	if a1.DAVUser == "" || a2.DAVUser == "" {
		t.Fatal("新账号应带默认 WebDAV 用户名")
	}
	if strings.EqualFold(a1.DAVUser, a2.DAVUser) {
		t.Fatalf("两台账号的 WebDAV 用户名不能相同：%q / %q", a1.DAVUser, a2.DAVUser)
	}
	// 默认账号本来就占着 cmccyun，新账号应顺延到 cmccyun2 / cmccyun3
	if a1.DAVUser != "cmccyun2" || a2.DAVUser != "cmccyun3" {
		t.Errorf("期望 cmccyun2 / cmccyun3，得到 %q / %q", a1.DAVUser, a2.DAVUser)
	}
}

// 总览要标出「用户名重复」的那几台，界面据此提示改名。
func TestDAVAccountsViewFlagsDuplicateUser(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.Accounts = []config.AccountConfig{
		{ID: config.DefaultAccountID, MountPoint: filepath.Join(root, "m1"), DAVUser: "same", DAVPassword: "p1"},
		{ID: "acc0123456789ab", MountPoint: filepath.Join(root, "m2"), DAVUser: "same", DAVPassword: "p2"},
	}
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.DAVAccountsView() {
		if r["user_duplicated"] != true {
			t.Errorf("两台同名，user_duplicated 应为 true：%v", r)
		}
	}
	// 改掉一台之后就不再是同名
	s.mu.Lock()
	s.cfg.Accounts[1].DAVUser = "other"
	snap2 := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap2); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.DAVAccountsView() {
		if r["user_duplicated"] != false {
			t.Errorf("改名后不该再标重名：%v", r)
		}
	}
}

// WebDAV 账号密码是「完全随机」的：账号 8 位、密码 16 位，且不含易混淆字符。
func TestRandomDAVCredentials(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		u := newDAVUser()
		if len(u) != 8 {
			t.Fatalf("用户名长度应为 8，得到 %d（%q）", len(u), u)
		}
		if strings.ContainsAny(u, "0O1lI") {
			t.Fatalf("用户名不该含易混淆字符：%q", u)
		}
		seen[u] = true
	}
	if len(seen) < 190 {
		t.Errorf("200 次生成只出现 %d 个不同值，随机性可疑", len(seen))
	}
	pw := newDAVPassword()
	if len(pw) != 16 {
		t.Fatalf("密码长度应为 16，得到 %d（%q）", len(pw), pw)
	}
	if strings.ContainsAny(pw, "0O1lI") {
		t.Errorf("密码不该含易混淆字符：%q", pw)
	}
	if newDAVPassword() == pw {
		t.Error("两次生成的密码不该相同")
	}
}

// 新建账号直接带随机账号密码（不再用 cmccyun 这种可读默认值）。
func TestNewAccountHasRandomDavCredentials(t *testing.T) {
	s, root := newMountSvc(t, "")
	id, err := s.AddAccount("测试号", filepath.Join(root, "m1"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok {
		t.Fatal("新账号没找到")
	}
	if len(ac.DAVUser) != 8 {
		t.Errorf("新账号 WebDAV 用户名应为 8 位随机，得到 %q", ac.DAVUser)
	}
	if len(ac.DAVPassword) != 16 {
		t.Errorf("新账号 WebDAV 密码应为 16 位随机，得到 %q", ac.DAVPassword)
	}
	if ac.DAVUser == "cmccyun" {
		t.Error("不该再出现 cmccyun 默认用户名")
	}
}

// 手改用户名时不许重名（入口直接拒）。
func TestSetDAVRejectsDuplicateUser(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.Accounts = []config.AccountConfig{
		{ID: config.DefaultAccountID, MountPoint: filepath.Join(root, "m1"), DAVUser: "aaaa2222", DAVPassword: "p1"},
		{ID: "acc0123456789ab", MountPoint: filepath.Join(root, "m2"), DAVUser: "bbbb3333", DAVPassword: "p2"},
	}
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDAV("acc0123456789ab", map[string]any{"user": "aaaa2222"}); err == nil {
		t.Fatal("重名应该被拒")
	} else if !strings.Contains(err.Error(), "已经") {
		t.Errorf("错误信息应说明重名，得到 %v", err)
	}
	// 改成没被占用的可以
	if _, err := s.SetDAV("acc0123456789ab", map[string]any{"user": "cccc4444"}); err != nil {
		t.Fatalf("不重名应允许：%v", err)
	}
	// 留空 → 自动生成 8 位随机
	if info, err := s.SetDAV("acc0123456789ab", map[string]any{"user": ""}); err != nil {
		t.Fatal(err)
	} else if u := info["user"].(string); len(u) != 8 {
		t.Errorf("留空应生成 8 位随机用户名，得到 %q", u)
	}
}

// 单台重置：账号密码都换新。
func TestResetDAVCredentials(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.Accounts = []config.AccountConfig{{ID: config.DefaultAccountID, MountPoint: filepath.Join(root, "m1"), DAVUser: "olduser1", DAVPassword: "oldpassword1"}}
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}
	info, err := s.ResetDAVCredentials("")
	if err != nil {
		t.Fatal(err)
	}
	if info["user"] == "olduser1" || info["password"] == "oldpassword1" {
		t.Fatal("重置后账号密码都该变")
	}
	if u := info["user"].(string); len(u) != 8 {
		t.Errorf("新用户名应 8 位，得到 %q", u)
	}
	if pw := info["password"].(string); len(pw) != 16 {
		t.Errorf("新密码应 16 位，得到 %q", pw)
	}
	// 落盘了
	loaded, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if ac, ok := loaded.AccountByID(""); !ok || ac.DAVUser != info["user"] {
		t.Error("新凭据没有写进配置文件")
	}
}

// 一键修掉重名：只动重复/为空的那几台，唯一的账号不动。
func TestFixDuplicateDAVUsers(t *testing.T) {
	s, root := newMountSvc(t, "")
	s.mu.Lock()
	s.cfg.Accounts = []config.AccountConfig{
		{ID: config.DefaultAccountID, MountPoint: filepath.Join(root, "m1"), DAVUser: "sameuser", DAVPassword: "p1"},
		{ID: "acc0123456789ab", MountPoint: filepath.Join(root, "m2"), DAVUser: "sameuser", DAVPassword: "p2"},
		{ID: "acc999999999999", MountPoint: filepath.Join(root, "m3"), DAVUser: "unique11", DAVPassword: "p3"},
		{ID: "acc888888888888", MountPoint: filepath.Join(root, "m4"), DAVUser: "", DAVPassword: ""},
	}
	snap := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}

	changed, err := s.FixDuplicateDAVUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("应改 3 台（两台重名 + 一台空），实际 %d：%v", len(changed), changed)
	}
	cfg := s.Config()
	users := map[string]string{}
	for _, ac := range cfg.Accounts {
		users[ac.ID] = ac.DAVUser
		if ac.ID != "acc999999999999" && len(ac.DAVUser) != 8 {
			t.Errorf("改过的账号用户名应 8 位：%s=%q", ac.ID, ac.DAVUser)
		}
	}
	if users["acc999999999999"] != "unique11" {
		t.Error("已经唯一的账号不该被动")
	}
	if users["default"] == users["acc0123456789ab"] {
		t.Error("改完还有重名")
	}
	// 再跑一次：不该再改动任何东西（幂等）
	if again, _ := s.FixDuplicateDAVUsers(); len(again) != 0 {
		t.Errorf("第二次调用不该再改，实际改了 %d 台", len(again))
	}
}

// 界面入口：只读凭据 + 随机生成 + 一键修重名 + 行内重置。
func TestDavRandomCredsUIPresent(t *testing.T) {
	for _, want := range []string{
		`id="davUser"`,
		`id="davPass"`,
		`readonly title="点「随机生成」换一组"`,
		`id="btnDavFixDup"`,
		`data-dav-act="reset"`,
		`/api/dav/reset`,
		"8 位随机",
		"16 位随机",
		"一键修掉重名",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("WebDAV 页缺 %q", want)
		}
	}
}
