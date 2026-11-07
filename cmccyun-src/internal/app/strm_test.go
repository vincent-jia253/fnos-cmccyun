package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// 页面上得有这个页签，而且四件事都要能找到：开关、映射、立即扫描、接影视的教程。
func TestStrmTabInConsole(t *testing.T) {
	for _, want := range []string{
		`data-tab="strm"`,
		`id="tab-strm"`,
		`id="strmEnabled"`,
		`id="strmPairs"`,
		`id="btnStrmScan"`,
		"/api/strm/scan",
		"/api/strm/status",
		"影视 STRM",
		"怎么接影视 / Emby",
		"不在云盘挂载点里", // 输出目录的硬约束必须写在页面上
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("界面缺少 %q", want)
		}
	}
	// 目录选择器要能被 STRM 映射复用（回调式），否则用户只能手打本地路径
	if !strings.Contains(indexHTML, "openPicker(function (path)") {
		t.Error("映射行没有复用目录选择器")
	}
}

// 输出目录塞进挂载点＝把 .strm 写到云端去，必须拦下来。
func TestStrmRejectsOutputInsideMount(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-strmtest"))
	s.cfg.MountPoint = filepath.Join(root, "mnt")
	if err := osMkdirAll(s.cfg.MountPoint); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/strm/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"pairs":[{"cloud":"/01影视","local":"` + s.cfg.MountPoint + `/movie"}]}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "挂载点") {
		t.Fatalf("输出目录在挂载点里应被拒绝，实际 %d：%s", rec.Code, rec.Body.String())
	}

	rec = post(`{"pairs":[{"cloud":"/01影视","local":"相对路径/strm"}]}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "绝对路径") {
		t.Fatalf("相对路径应被拒绝，实际 %d：%s", rec.Code, rec.Body.String())
	}

	// 正常的一条：保存成功，并且 url 模式会自动生成口令
	out := filepath.Join(root, "strmout")
	rec = post(`{"enabled":true,"mode":"url","interval_min":7,"pairs":[{"cloud":"/01影视","local":"` + out + `"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法配置应保存成功，实际 %d：%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"token_set":true`) {
		t.Error("url 模式应自动生成访问口令")
	}
	cfg := s.Config()
	if len(cfg.StrmPairs) != 1 || cfg.StrmPairs[0].Local != out {
		t.Fatalf("映射没存进配置：%+v", cfg.StrmPairs)
	}
	if cfg.StrmIntervalMin != 7 || cfg.StrmMode != "url" || !cfg.StrmEnabled {
		t.Fatalf("其它字段没存对：%+v", cfg)
	}
	// 落盘的配置也要真的带上（重启后还在）
	reloaded, err := loadCfg(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.StrmPairs) != 1 || reloaded.StrmPairs[0].Local != out {
		t.Error("配置没有写进磁盘")
	}
}

// 没启用时点「立即扫描」要立刻给出人话错误，而不是去连云端。
func TestStrmScanNeedsEnable(t *testing.T) {
	s, _ := newMountSvc(t, "")
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/strm/scan", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("扫描失败应回 200 + ok:false，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "未启用") {
		t.Errorf("错误提示应说明未启用：%s", rec.Body.String())
	}

	// GET 只收 POST
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/strm/config", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET /api/strm/config 应 400，实际 %d", rec.Code)
	}
}

// 播放直链：口令不对 403；口令对但没 id 400。
func TestStrmPlayGuards(t *testing.T) {
	s, _ := newMountSvc(t, "")
	s.cfg.StrmToken = "tk-abc"
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/strm/play?id=f1&k=wrong", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("口令错应 403，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/strm/play?k=tk-abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("缺 id 应 400，实际 %d", rec.Code)
	}

	// 没设口令时不拦（局域网自用场景），但缺 id 仍然是 400
	s.cfg.StrmToken = ""
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/strm/play", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("无口令但缺 id 应 400，实际 %d", rec.Code)
	}
}

// 扩展名白名单的解析：中英文逗号、分号、空格都要认。
func TestSplitExts(t *testing.T) {
	got := splitExts("mp4，mkv; avi  ts、iso")
	want := []string{"mp4", "mkv", "avi", "ts", "iso"}
	if len(got) != len(want) {
		t.Fatalf("解析结果 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("解析结果 %v，期望 %v", got, want)
		}
	}
	if splitExts("   ") != nil {
		t.Error("空白应返回 nil（表示用内置列表）")
	}
}

// 模式判定：挂载在线 → path，否则 → url；显式配置优先。
func TestStrmModeSelection(t *testing.T) {
	s, _ := newMountSvc(t, "")
	cfg := s.Config()
	cfg.MountPoint = filepath.Join(t.TempDir(), "不存在的挂载点")
	if got := strmMode(cfg); got != "url" {
		t.Errorf("没挂载时应退回 url，实际 %s", got)
	}
	cfg.StrmMode = "path"
	if got := strmMode(cfg); got != "path" {
		t.Errorf("显式 path 应被尊重，实际 %s", got)
	}
}

// 小工具：测试里建目录 / 读回配置。放这里是因为只有这个文件用得到。
func osMkdirAll(p string) error { return os.MkdirAll(p, 0o755) }

func loadCfg(p string) (*config.Config, error) { return config.Load(p) }

// TestStrmMountPrefix 回归：path 模式的 strm 内容必须带上「映射的云端目录」这一段。
// 真机第一轮就踩到了——只拼挂载点，生成 /vol4/1000/移动云盘/学爸 (2023)/…，
// 而文件实际在 /vol4/1000/移动云盘/01影视/01求片/电影/华语电影/学爸 (2023)/…，
// 结果 31 个 strm 全部指向不存在的路径（ls 报 No such file、dd 读 0 字节）。
func TestStrmMountPrefix(t *testing.T) {
	cases := []struct{ mount, cloud, want string }{
		{"/vol4/1000/移动云盘", "/01影视/01求片/电影/华语电影", "/vol4/1000/移动云盘/01影视/01求片/电影/华语电影"},
		{"/vol4/1000/移动云盘/", "01影视", "/vol4/1000/移动云盘/01影视"},
		{"/mnt/cloud", "/", "/mnt/cloud"},
		{"/mnt/cloud", "", "/mnt/cloud"},
		{"", "/01影视", ""},
	}
	for _, c := range cases {
		if got := strmMountPrefix(c.mount, c.cloud); got != c.want {
			t.Fatalf("strmMountPrefix(%q,%q)=%q，期望 %q", c.mount, c.cloud, got, c.want)
		}
	}
}
