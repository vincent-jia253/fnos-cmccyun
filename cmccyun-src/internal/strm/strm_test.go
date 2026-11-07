package strm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// fakeCloud 是一棵内存里的目录树，够测「扫描 + diff」了。
type fakeCloud struct {
	dirs  map[string][]api.File
	calls int
}

func newFake() *fakeCloud { return &fakeCloud{dirs: map[string][]api.File{}} }

func (f *fakeCloud) add(dir, id, name string, isDir bool, size int64) {
	mod := "2026-01-02T03:04:05+08:00"
	f.dirs[dir] = append(f.dirs[dir], api.File{ID: id, Name: name, IsDir: isDir, Size: size, Modified: mod})
}

func (f *fakeCloud) list(id string) []api.File { return f.dirs[id] }

func (f *fakeCloud) drop(dir, id string) {
	out := f.dirs[dir][:0]
	for _, x := range f.dirs[dir] {
		if x.ID != id {
			out = append(out, x)
		}
	}
	f.dirs[dir] = out
}

func (f *fakeCloud) rename(dir, id, name string) {
	for i := range f.dirs[dir] {
		if f.dirs[dir][i].ID == id {
			f.dirs[dir][i].Name = name
		}
	}
}

func (f *fakeCloud) List(ctx context.Context, parentID string) ([]api.File, error) {
	f.calls++
	return f.dirs[parentID], nil
}

// 一棵两层树：01影视/{电影/, 剧集/}，里面混着视频和非视频。
func seedTree(f *fakeCloud) {
	f.add("/", "d1", "01影视", true, 0)
	f.add("/", "d9", "02资源", true, 0)
	f.add("d1", "d2", "电影", true, 0)
	f.add("d1", "d3", "剧集", true, 0)
	f.add("d2", "f1", "沙丘2.mkv", false, 8<<30)
	f.add("d2", "f2", "海报.jpg", false, 1<<20)
	f.add("d3", "f3", "S01E01.MP4", false, 1<<30)
	f.add("d3", "f4", "note.txt", false, 100)
}

func opts(t *testing.T, f *fakeCloud, out, mode string) Options {
	t.Helper()
	return Options{
		Backend:     f,
		CloudRootID: "d1",
		OutputDir:   out,
		Mode:        mode,
		MountPrefix: "/vol4/1000/移动云盘",
		URLBase:     "http://192.168.0.200:19898/api/strm/play",
		Token:       "tk123",
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到 %s：%v", p, err)
	}
	return string(b)
}

// 首次扫描：只挑白名单里的视频，按云端目录层级镜像出 .strm。
func TestFirstRunMirrorsTree(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()

	st, res, err := Run(context.Background(), opts(t, f, out, "path"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || res.Added != 2 || res.Updated != 0 {
		t.Fatalf("统计不对：%+v", res)
	}
	got := readFile(t, filepath.Join(out, "电影", "沙丘2.mkv.strm"))
	want := "/vol4/1000/移动云盘/电影/沙丘2.mkv\n"
	if got != want {
		t.Errorf("strm 内容 = %q，期望 %q", got, want)
	}
	// 大小写不敏感
	if _, err := os.Stat(filepath.Join(out, "剧集", "S01E01.MP4.strm")); err != nil {
		t.Errorf("大写扩展名没被识别：%v", err)
	}
	// 非视频不能生成
	if _, err := os.Stat(filepath.Join(out, "电影", "海报.jpg.strm")); err == nil {
		t.Error("jpg 不该生成 strm")
	}
	// 另一个云端目录（不在本次映射内）不该被扫到
	if len(st.Items) != 2 {
		t.Errorf("快照条目 = %d，期望 2", len(st.Items))
	}
}

// 第二轮：内容没变就一个字节都不动（mtime 也不能变，否则媒体库会整库重扫）。
func TestSecondRunIsNoop(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	st, _, err := Run(context.Background(), opts(t, f, out, "path"), nil)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(out, "电影", "沙丘2.mkv.strm")
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	st2, res, err := Run(context.Background(), opts(t, f, out, "path"), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 0 || res.Skipped != 2 {
		t.Fatalf("第二轮应该什么都没干：%+v", res)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("内容没变却改了 mtime，媒体库会整库重扫")
	}
	if len(st2.Items) != 2 {
		t.Errorf("快照条目 = %d，期望 2", len(st2.Items))
	}
}

// 改名/挪目录：fileId 不变但 Rel 变了，旧路径的 .strm 必须消失，否则媒体库里
// 会同时出现新旧两条（旧的点开 404）。
func TestRenameCleansOldPath(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	st, _, err := Run(context.Background(), opts(t, f, out, "path"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.rename("d2", "f1", "沙丘2-导演剪辑版.mkv")
	_, res, err := Run(context.Background(), opts(t, f, out, "path"), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Fatalf("期望 1 个更新，实际 %+v", res)
	}
	if _, err := os.Stat(filepath.Join(out, "电影", "沙丘2.mkv.strm")); !os.IsNotExist(err) {
		t.Error("旧名字的 strm 没被清掉")
	}
	if _, err := os.Stat(filepath.Join(out, "电影", "沙丘2-导演剪辑版.mkv.strm")); err != nil {
		t.Errorf("新名字的 strm 没生成：%v", err)
	}
}

// 云端删掉：本地 .strm 一起删，空目录也顺手收掉。
func TestDeleteMissing(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	o := opts(t, f, out, "path")
	o.DeleteMissing = true
	st, _, err := Run(context.Background(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.drop("d2", "f1")
	f.drop("d1", "d2") // 电影目录也删了
	_, res, err := Run(context.Background(), o, st)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("期望删掉 1 个，实际 %+v", res)
	}
	if _, err := os.Stat(filepath.Join(out, "电影")); !os.IsNotExist(err) {
		t.Error("空目录应该被收掉")
	}

	// 关掉 DeleteMissing 时：文件留着（用户可能手动放了自己的东西）
	f2 := newFake()
	seedTree(f2)
	out2 := t.TempDir()
	o2 := opts(t, f2, out2, "path")
	st2, _, err := Run(context.Background(), o2, nil)
	if err != nil {
		t.Fatal(err)
	}
	f2.drop("d2", "f1")
	if _, res2, err := Run(context.Background(), o2, st2); err != nil || res2.Removed != 0 {
		t.Fatalf("DeleteMissing 关着却删了：%+v %v", res2, err)
	}
	if _, err := os.Stat(filepath.Join(out2, "电影", "沙丘2.mkv.strm")); err != nil {
		t.Errorf("DeleteMissing 关着时不该删文件：%v", err)
	}
}

// url 模式：内容是指向本应用的直链，带 fileId 和访问口令。
func TestURLMode(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	_, _, err := Run(context.Background(), opts(t, f, out, "url"), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(readFile(t, filepath.Join(out, "电影", "沙丘2.mkv.strm")))
	want := "http://192.168.0.200:19898/api/strm/play?id=f1&k=tk123"
	if got != want {
		t.Errorf("url 内容 = %q，期望 %q", got, want)
	}
}

// 扩展名白名单可覆盖；MaxEntries 到顶要标记 truncated 而不是静默截断。
func TestExtFilterAndTruncate(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	o := opts(t, f, out, "path")
	o.Exts = []string{"mkv"}
	_, res, err := Run(context.Background(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Errorf("只留 mkv 时应只有 1 个，实际 %d", res.Total)
	}

	out2 := t.TempDir()
	o2 := opts(t, f, out2, "path")
	o2.MaxEntries = 1
	_, res2, err := Run(context.Background(), o2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Truncated || res2.Total != 1 {
		t.Errorf("MaxEntries=1 应该截断：%+v", res2)
	}
}

// 状态文件要能落盘再读回来（调度器靠它做增量）。
func TestStateRoundTrip(t *testing.T) {
	f := newFake()
	seedTree(f)
	out := t.TempDir()
	st, _, err := Run(context.Background(), opts(t, f, out, "path"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "strm-state", "x.json")
	if err := SaveState(p, st); err != nil {
		t.Fatal(err)
	}
	back := LoadState(p)
	if back == nil || len(back.Items) != 2 {
		t.Fatalf("快照读回失败：%+v", back)
	}
	if back.Items["f1"].Rel != "电影/沙丘2.mkv" {
		t.Errorf("rel 不对：%q", back.Items["f1"].Rel)
	}
	if LoadState(filepath.Join(t.TempDir(), "不存在.json")) != nil {
		t.Error("读不到的快照应该返回 nil")
	}
}

// 参数校验：输出目录必须绝对路径；path / url 模式各自缺参数要报错。
func TestOptionsValidation(t *testing.T) {
	f := newFake()
	cases := []Options{
		{Backend: f, OutputDir: "相对/路径", Mode: "path", MountPrefix: "/mnt"},
		{Backend: f, OutputDir: t.TempDir(), Mode: "path"},
		{Backend: f, OutputDir: t.TempDir(), Mode: "url"},
	}
	for i, o := range cases {
		if _, _, err := Run(context.Background(), o, nil); err == nil {
			t.Errorf("第 %d 个非法参数没有报错", i)
		}
	}
}
