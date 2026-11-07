package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// 多账号的界面必须自洽：能选账号、能看到每个账号的挂载状态、能新增/删除。
// 这几个 id 是前端 JS 直接 $() 取的，少一个页面就白屏一半。
func TestAccountsTabInConsole(t *testing.T) {
	for _, want := range []string{
		`data-tab="accounts"`,
		`id="tab-accounts"`,
		`id="acctSel"`,
		`id="acctBody"`,
		`id="acctCount"`,
		`id="btnAcctAdd"`,
		`id="newAcctMount"`,
		"/api/accounts",
		"互不包含", // 挂载点互不包含这条硬约束必须写在页面上
		"未授权",  // 新账号还没授权时的提示
		"切换账号", // 顶栏下拉的 hover 说明
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("界面缺少 %q", want)
		}
	}
	// 所有 /api 请求都要能被加上 ?account=，否则页签切换后还在读默认账号
	if !strings.Contains(indexHTML, "function withAcct(") {
		t.Error("缺少 withAcct()：请求不会带当前账号")
	}
	if !strings.Contains(indexHTML, "fetch(withAcct(path), req)") {
		t.Error("api() 没有走 withAcct()")
	}
}

func acctReq(t *testing.T, s *Service, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// 新增 → 切过去 → 改它的挂载点 → 删除，整条链路走 HTTP 接口，
// 顺便确认默认账号一个字节都没被动过。
func TestAccountsHTTPLifecycle(t *testing.T) {
	s, root := newMountSvc(t, "")
	defMount := filepath.Join(root, "mnt1")
	acct0(s.cfg).MountPoint = defMount
	snap := s.Config()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}
	newMount := filepath.Join(root, "mnt2")

	code, out := acctReq(t, s, http.MethodPost, "/api/accounts",
		`{"action":"add","name":"家庭号","mount_point":"`+newMount+`"}`)
	if code != http.StatusOK {
		t.Fatalf("新增账号失败：HTTP %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" || id == config.DefaultAccountID {
		t.Fatalf("新账号 ID 不合法：%q", id)
	}

	// 列表里必须有两条，并且各自带着自己的挂载点
	code, out = acctReq(t, s, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK {
		t.Fatalf("读列表失败：HTTP %d", code)
	}
	list, _ := out["accounts"].([]any)
	if len(list) != 2 {
		t.Fatalf("账号数应为 2，实际 %d", len(list))
	}

	// ?account=<id> 要能把 status/config 都指向这台
	code, out = acctReq(t, s, http.MethodGet, "/api/status?account="+id, "")
	if code != http.StatusOK {
		t.Fatalf("读状态失败：HTTP %d", code)
	}
	st, _ := out["status"].(map[string]any)
	if st["id"] != id {
		t.Errorf("status.id = %v，想要 %s", st["id"], id)
	}
	cfgMap, _ := out["config"].(map[string]any)
	if cfgMap["mount_point"] != newMount {
		t.Errorf("config.mount_point = %v，想要 %s", cfgMap["mount_point"], newMount)
	}
	if cfgMap["dav_path"] != "/dav/"+id+"/" {
		t.Errorf("非默认账号的 WebDAV 路径应为 /dav/%s/，实际 %v", id, cfgMap["dav_path"])
	}

	// 默认账号的挂载点不能被这次新增/读取动过
	if got := acctView(s.Config()).MountPoint; got != defMount {
		t.Errorf("默认账号挂载点被改成了 %q", got)
	}

	// 挂载点互相包含必须被拒（两台云盘不能挂在同一棵子树里）
	code, out = acctReq(t, s, http.MethodPost, "/api/accounts",
		`{"action":"add","name":"坏的","mount_point":"`+defMount+`/sub"}`)
	if code == http.StatusOK {
		t.Fatalf("互相包含的挂载点居然被接受了：%v", out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "包含") {
		t.Errorf("错误信息没说清原因：%q", msg)
	}

	// 改这一台的挂载点，另一台不受影响
	code, out = acctReq(t, s, http.MethodPost, "/api/accounts",
		`{"action":"update","id":"`+id+`","name":"家庭号","mount_point":"`+filepath.Join(root, "mnt3")+`"}`)
	if code != http.StatusOK {
		t.Fatalf("改挂载点失败：HTTP %d %v", code, out)
	}
	snap = s.Config()
	if ac, ok := snap.AccountByID(id); !ok || ac.MountPoint != filepath.Join(root, "mnt3") {
		t.Errorf("新账号挂载点没改到：%+v", ac)
	}
	if got := acctView(s.Config()).MountPoint; got != defMount {
		t.Errorf("默认账号挂载点被连带改了：%q", got)
	}

	// 删除后只剩一台
	code, out = acctReq(t, s, http.MethodPost, "/api/accounts", `{"action":"delete","id":"`+id+`"}`)
	if code != http.StatusOK {
		t.Fatalf("删除账号失败：HTTP %d %v", code, out)
	}
	if n := len(s.Config().Accounts); n != 1 {
		t.Errorf("删完应剩 1 个账号，实际 %d", n)
	}
}

// 默认账号的字段要「浮」到 config 顶层，前端才不用为多账号改一堆读法；
// 密码类字段仍然只能以 *_set 的形式出现。
func TestMaskLiftsAccountAndHidesSecrets(t *testing.T) {
	s, root := newMountSvc(t, "")
	acct0(s.cfg).MountPoint = filepath.Join(root, "mnt1")
	s.mu.Lock()
	ac := acct0(s.cfg)
	ac.Authorization = "cGM6MTM0NzQwODk2MjE6c2VjcmV0"
	ac.DAVPassword = "super-secret"
	ac.StrmToken = "tok-123456"
	s.mu.Unlock()

	m := s.MaskPtr()
	if m["mount_point"] != acctView(s.Config()).MountPoint {
		t.Errorf("顶层没有浮出默认账号的挂载点：%v", m["mount_point"])
	}
	if m["account_count"] != 1 {
		t.Errorf("account_count = %v", m["account_count"])
	}
	blob, _ := json.Marshal(m)
	for _, secret := range []string{"super-secret", "tok-123456", "c2VjcmV0"} {
		if strings.Contains(string(blob), secret) {
			t.Errorf("配置视图泄漏了密文 %q", secret)
		}
	}
	if m["dav_password_set"] != true {
		t.Errorf("dav_password_set 应为 true，实际 %v", m["dav_password_set"])
	}

	// 指定账号时浮出的是那一台
	if _, err := s.AddAccount("第二台", filepath.Join(root, "mnt2")); err != nil {
		t.Fatal(err)
	}
	var secondID string
	for _, a := range s.Config().Accounts {
		if a.ID != config.DefaultAccountID {
			secondID = a.ID
		}
	}
	m2 := s.MaskPtrFor(secondID)
	if m2["account_id"] != secondID {
		t.Errorf("MaskPtrFor 没指向目标账号：%v", m2["account_id"])
	}
	if m2["dav_password_set"] != false {
		t.Errorf("第二台不该有 WebDAV 密码，实际 %v", m2["dav_password_set"])
	}
}

// 新增账号的挂载位置必须过和「挂载设置」同一套规则：不能挂到存储卷外面
// （/etc 这种），否则第一次挂载就会把系统目录盖住。
func TestAddAccountRejectsOutsideVolume(t *testing.T) {
	s, _ := newMountSvc(t, "")
	for _, bad := range []string{"/etc/evil", "/", "/tmp/cmccyun-not-a-volume/x"} {
		if _, err := s.AddAccount("坏位置", bad); err == nil {
			t.Errorf("挂载位置 %q 居然被接受了", bad)
		} else if !strings.Contains(err.Error(), "存储卷") && !strings.Contains(err.Error(), "根目录") {
			t.Errorf("挂载位置 %q 的错误信息没说清原因：%v", bad, err)
		}
	}
	if n := len(s.Config().Accounts); n != 1 {
		t.Errorf("失败的新增不该留下账号，现在有 %d 个", n)
	}
	// 空位置也要拦住：账号建好却没有落点，用户会以为挂上了
	if _, err := s.AddAccount("没位置", ""); err == nil {
		t.Error("空挂载位置居然被接受了")
	}
}

// WebDAV 的 /dav/<id>/ 必须在账号新增后立刻可用：路由是按启动时的账号表
// 注册死的，新账号就会 404，所以 /dav/ 走的是运行时分派。
func TestDAVDispatchPerAccount(t *testing.T) {
	s, root := newMountSvc(t, "")
	if _, err := s.SetDAV("", map[string]any{"enabled": true}); err != nil {
		t.Fatalf("启用 WebDAV 失败：%v", err)
	}
	id, err := s.AddAccount("第二台", filepath.Join(root, "mnt2"))
	if err != nil {
		t.Fatal(err)
	}

	if got := s.davRouteID("/dav/" + id + "/01影视"); got != id {
		t.Errorf("/dav/%s/ 没分派到该账号，得到 %q", id, got)
	}
	// 老地址不能被账号 ID 吃掉：/dav/01影视/x.mkv 还是默认账号里的目录
	for _, p := range []string{"/dav/", "/dav/01影视/x.mkv", "/dav", "/dav/" + config.DefaultAccountID + "/x"} {
		if got := s.davRouteID(p); got != "" {
			t.Errorf("%s 应归默认账号，却分派给 %q", p, got)
		}
	}

	h := s.Handler()
	for _, p := range []string{"/dav/", "/dav/" + id + "/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("PROPFIND", p, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s 返回 404：路由没认到账号", p)
		}
	}

	// 每台账号的 WebDAV 地址各自独立
	if got := s.DAVInfo(id)["path"]; got != "/dav/"+id+"/" {
		t.Errorf("第二台的 WebDAV 路径 = %v", got)
	}
	if got := s.DAVInfo("")["path"]; got != "/dav/" {
		t.Errorf("默认账号的 WebDAV 路径 = %v", got)
	}
}

// 两台云盘的 STRM 输出目录隔着账号互相踩：写进对方挂载点 = 往人家云盘上传；
// 两台共用一个本地目录 = 双方的「删除缺失」互相清文件。这些都得挡住。
func TestStrmRejectsCrossAccountOutputDir(t *testing.T) {
	s, root := newMountSvc(t, "")
	defMount := filepath.Join(root, "mnt1")
	acct0(s.cfg).MountPoint = defMount
	snap := s.Config()
	if err := config.Save(s.cfgPath, &snap); err != nil {
		t.Fatal(err)
	}

	media := filepath.Join(root, "media")
	mnt2 := filepath.Join(media, "mnt2")
	code, out := acctReq(t, s, http.MethodPost, "/api/accounts",
		`{"action":"add","name":"二号","mount_point":"`+mnt2+`"}`)
	if code != http.StatusOK {
		t.Fatalf("新增账号失败：HTTP %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatal("没拿到新账号 ID")
	}
	pairs := func(dirs ...string) map[string]any {
		raw := make([]any, 0, len(dirs))
		for _, d := range dirs {
			raw = append(raw, map[string]any{"cloud": "01影视", "local": d})
		}
		return map[string]any{"pairs": raw}
	}

	// 1) 第二台的输出目录落进默认账号的挂载点
	if _, err := s.SetStrm(id, pairs(filepath.Join(defMount, "strm"))); err == nil ||
		!strings.Contains(err.Error(), "挂载点") {
		t.Fatalf("允许把输出目录放进另一台的挂载点：%v", err)
	}

	// 2) 输出目录里面有另一台的挂载点（扫进去会把人家的片也写成 .strm）
	if _, err := s.SetStrm("", pairs(media)); err == nil ||
		!strings.Contains(err.Error(), "里面挂着账号") {
		t.Fatalf("允许输出目录套住另一台的挂载点：%v", err)
	}

	shared := filepath.Join(root, "strm-out")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStrm(id, pairs(shared)); err != nil {
		t.Fatalf("正常设置失败：%v", err)
	}

	// 3) 默认账号也来抢同一个输出目录
	if _, err := s.SetStrm("", pairs(shared)); err == nil || !strings.Contains(err.Error(), "占用") {
		t.Fatalf("两台云盘共用了同一个输出目录：%v", err)
	}
	// 4) 同一台账号自己填两遍
	if _, err := s.SetStrm(id, pairs(shared, shared)); err == nil || !strings.Contains(err.Error(), "两次") {
		t.Fatalf("同一个输出目录填了两次也过：%v", err)
	}

	// 5) 把挂载点搬到别人 STRM 输出目录上
	err := s.SetMountPointOf("", context.Background(), shared, SetMountPointOptions{AllowNonEmpty: true})
	if err == nil || !strings.Contains(err.Error(), "STRM 输出目录") {
		t.Fatalf("允许把云盘挂到别人的 STRM 输出目录上：%v", err)
	}
}
