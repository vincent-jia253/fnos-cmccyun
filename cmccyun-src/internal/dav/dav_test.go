package dav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// --- 一个内存版「云盘」，只实现 Backend 需要的那几个方法 ---------------------

type fnode struct {
	id     string
	name   string
	parent string
	dir    bool
	data   []byte
	mod    time.Time
}

type fake struct {
	mu    sync.Mutex
	nodes map[string]*fnode
	seq   int
	// calls 记录调用次数，用来验证缓存是否真的拦住了重复 List。
	lists int
}

func newFake() *fake {
	f := &fake{nodes: map[string]*fnode{}}
	f.nodes["/"] = &fnode{id: "/", dir: true, mod: time.Now()}
	return f
}

func (f *fake) add(parent, name string, dir bool, data []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("id%d", f.seq)
	f.nodes[id] = &fnode{id: id, name: name, parent: parent, dir: dir, data: data, mod: time.Now()}
	return id
}

func (f *fake) path(id string) string {
	if id == "/" {
		return "/"
	}
	n, ok := f.nodes[id]
	if !ok {
		return ""
	}
	return strings.TrimSuffix(f.path(n.parent), "/") + "/" + n.name
}

func (f *fake) List(ctx context.Context, id string) ([]api.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if _, ok := f.nodes[id]; !ok {
		return nil, fmt.Errorf("no such folder %q", id)
	}
	var out []api.File
	for _, n := range f.nodes {
		if n.parent != id {
			continue
		}
		out = append(out, api.File{
			ID: n.id, Name: n.name, IsDir: n.dir, Size: int64(len(n.data)),
			Modified: n.mod.Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fake) Mkdir(ctx context.Context, parentID, name string) (string, error) {
	return f.add(parentID, name, true, nil), nil
}

func (f *fake) Rename(ctx context.Context, id, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[id]
	if !ok {
		return os.ErrNotExist
	}
	n.name = name
	return nil
}

func (f *fake) Move(ctx context.Context, ids []string, toParent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if n, ok := f.nodes[id]; ok {
			n.parent = toParent
		}
	}
	return nil
}

func (f *fake) Trash(ctx context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kill func(string)
	kill = func(id string) {
		for _, n := range f.nodes {
			if n.parent == id {
				kill(n.id)
			}
		}
		delete(f.nodes, id)
	}
	for _, id := range ids {
		kill(id)
	}
	return nil
}

func (f *fake) Download(ctx context.Context, id string, offset, length int64) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	if offset > int64(len(n.data)) {
		offset = int64(len(n.data))
	}
	end := int64(len(n.data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(n.data[offset:end])), nil
}

func (f *fake) DownloadURL(ctx context.Context, id string) (string, error) {
	return "https://cdn.example/" + id, nil
}

func (f *fake) UploadReader(ctx context.Context, parentID, name string, src io.ReaderAt, size int64, opt api.UploadOptions) (string, error) {
	buf := make([]byte, size)
	if size > 0 {
		if _, err := src.ReadAt(buf, 0); err != nil && err != io.EOF {
			return "", err
		}
	}
	return f.add(parentID, name, false, buf), nil
}

// --- 测试脚手架 -----------------------------------------------------------

// newServer returns an httptest server in front of the real webdav.Handler, i.e.
// the same code path the console uses, minus BasicAuth (covered in the app
// package).
func newServer(t *testing.T, opt Options) (*fake, *httptest.Server, *FS) {
	t.Helper()
	be := newFake()
	be.add("/", "01影视", true, nil)
	be.add("/", "空目录", true, nil)
	fs := New(be, opt)
	h := &webdav.Handler{Prefix: "/dav", FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.StripPrefix("", h))
	t.Cleanup(srv.Close)
	return be, srv, fs
}

func do(t *testing.T, method, url string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s 失败: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// --- 用例 -----------------------------------------------------------------

// TestPutGetRoundTrip 覆盖「写到云盘再读回来」这条主链路：
// MKCOL 建目录 → PUT 上传 → PROPFIND 看大小 → GET 读全量。
func TestPutGetRoundTrip(t *testing.T) {
	be, srv, _ := newServer(t, Options{StageDir: t.TempDir()})

	if resp, body := do(t, "MKCOL", srv.URL+"/dav/01影视/新目录", nil, nil); resp.StatusCode != 201 {
		t.Fatalf("MKCOL 应返回 201，实际 %d：%s", resp.StatusCode, body)
	}
	payload := []byte("hello 移动云盘 webdav")
	if resp, body := do(t, "PUT", srv.URL+"/dav/01影视/新目录/a.txt", payload, nil); resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("PUT 应返回 201/204，实际 %d：%s", resp.StatusCode, body)
	}
	be.mu.Lock()
	found := false
	for _, n := range be.nodes {
		if n.name == "a.txt" && !n.dir {
			found = true
			if string(n.data) != string(payload) {
				t.Fatalf("云端内容不对：%q", n.data)
			}
		}
	}
	be.mu.Unlock()
	if !found {
		t.Fatal("PUT 之后云端没有 a.txt")
	}

	resp, body := do(t, "GET", srv.URL+"/dav/01影视/新目录/a.txt", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET 应返回 200，实际 %d", resp.StatusCode)
	}
	if string(body) != string(payload) {
		t.Fatalf("读回内容不一致：%q", body)
	}
}

// TestRangeGet 是播放器拖动进度条的基础：Range 请求必须只回一段，且带
// 206 + Content-Range。
func TestRangeGet(t *testing.T) {
	be, srv, _ := newServer(t, Options{StageDir: t.TempDir()})
	full := bytes.Repeat([]byte("0123456789"), 100) // 1000 字节
	be.add("/", "大文件.bin", false, full)

	resp, body := do(t, "GET", srv.URL+"/dav/大文件.bin", nil, map[string]string{"Range": "bytes=100-199"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 请求应返回 206，实际 %d", resp.StatusCode)
	}
	if len(body) != 100 || !bytes.Equal(body, full[100:200]) {
		t.Fatalf("Range 内容不对：%d 字节 %q", len(body), body)
	}
	if cr := resp.Header.Get("Content-Range"); !strings.HasPrefix(cr, "bytes 100-199/1000") {
		t.Fatalf("Content-Range 不对：%q", cr)
	}
}

// TestPropfindAndSeekBackwards 检查目录列举与「往后拖再往前拖」（Seek 回退时
// 必须重开远端流，否则会读到错位数据）。
func TestPropfindAndSeekBackwards(t *testing.T) {
	be, srv, fs := newServer(t, Options{StageDir: t.TempDir()})
	data := bytes.Repeat([]byte("abcdefghij"), 10)
	id := be.add("/", "seek.bin", false, data)

	ctx := context.Background()
	f, err := fs.OpenFile(ctx, "/seek.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil || string(buf) != "abcde" {
		t.Fatalf("首读 = %q, %v", buf, err)
	}
	if _, err := f.Seek(90, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if _, err := f.Read(buf); err != nil || string(buf) != "abcde" {
		t.Fatalf("Seek 后读 = %q, %v", buf, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek back: %v", err)
	}
	if _, err := f.Read(buf); err != nil || string(buf) != "abcde" {
		t.Fatalf("回退后读 = %q, %v", buf, err)
	}
	_ = id

	resp, body := do(t, "PROPFIND", srv.URL+"/dav/", []byte(`<?xml version="1.0"?><propfind xmlns="DAV:"><allprop/></propfind>`),
		map[string]string{"Depth": "1"})
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND 应返回 207，实际 %d：%s", resp.StatusCode, body)
	}
	for _, want := range []string{"01影视", "空目录", "seek.bin"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("PROPFIND 少了 %q：%s", want, body)
		}
	}
}

// TestMoveAndDelete：MOVE 要能同时处理改名和换目录，DELETE 走回收站语义。
func TestMoveAndDelete(t *testing.T) {
	be, srv, _ := newServer(t, Options{StageDir: t.TempDir()})
	be.add(be.idOf("01影视"), "老名字.mkv", false, []byte("x"))

	resp, body := do(t, "MOVE", srv.URL+"/dav/01影视/老名字.mkv", nil, map[string]string{
		"Destination": srv.URL + "/dav/01影视/新名字.mkv",
	})
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("MOVE 应返回 201/204，实际 %d：%s", resp.StatusCode, body)
	}
	be.mu.Lock()
	names := map[string]bool{}
	for _, n := range be.nodes {
		names[n.name] = true
	}
	be.mu.Unlock()
	if !names["新名字.mkv"] || names["老名字.mkv"] {
		t.Fatalf("改名没生效：%v", names)
	}

	if resp, body := do(t, "DELETE", srv.URL+"/dav/01影视/新名字.mkv", nil, nil); resp.StatusCode != 204 {
		t.Fatalf("DELETE 应返回 204，实际 %d：%s", resp.StatusCode, body)
	}
	be.mu.Lock()
	defer be.mu.Unlock()
	for _, n := range be.nodes {
		if n.name == "新名字.mkv" {
			t.Fatal("删除后云端还在")
		}
	}
}

// TestReadOnlyRejects 只读模式：写操作必须 403，读操作照常。
func TestReadOnlyRejects(t *testing.T) {
	be, srv, _ := newServer(t, Options{StageDir: t.TempDir(), ReadOnly: true})
	be.add("/", "只能读.txt", false, []byte("ok"))

	if resp, _ := do(t, "PUT", srv.URL+"/dav/新文件.txt", []byte("x"), nil); resp.StatusCode < 400 {
		t.Fatalf("只读模式 PUT 应被拒，实际 %d", resp.StatusCode)
	}
	if resp, _ := do(t, "MKCOL", srv.URL+"/dav/新目录", nil, nil); resp.StatusCode < 400 {
		t.Fatalf("只读模式 MKCOL 应被拒，实际 %d", resp.StatusCode)
	}
	if resp, _ := do(t, "DELETE", srv.URL+"/dav/只能读.txt", nil, nil); resp.StatusCode < 400 {
		t.Fatalf("只读模式 DELETE 应被拒，实际 %d", resp.StatusCode)
	}
	be.mu.Lock()
	for _, n := range be.nodes {
		if n.name == "新文件.txt" || n.name == "新目录" {
			be.mu.Unlock()
			t.Fatal("只读模式下竟然写进了云端")
		}
	}
	be.mu.Unlock()
	if resp, body := do(t, "GET", srv.URL+"/dav/只能读.txt", nil, nil); resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("只读模式读失败：%d %q", resp.StatusCode, body)
	}
}

// TestDirCacheAvoidsRepeatedList：缓存命中时不该重复打 139（刮削一个库几千次
// 请求会把接口打爆）。
func TestDirCacheAvoidsRepeatedList(t *testing.T) {
	be := newFake()
	for i := 0; i < 20; i++ {
		be.add("/", fmt.Sprintf("f%02d", i), false, []byte("x"))
	}
	fs := New(be, Options{StageDir: t.TempDir(), CacheTTL: time.Hour})
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := fs.Stat(ctx, "/f00"); err != nil {
			t.Fatalf("Stat: %v", err)
		}
	}
	be.mu.Lock()
	n := be.lists
	be.mu.Unlock()
	if n != 1 {
		t.Fatalf("20 次 Stat 应该只 List 一次，实际 %d 次", n)
	}
}

// TestResolveRoot 根目录的 Stat / Readdir 要能正常工作（rclone 挂载第一步就是
// 探根）。
func TestResolveRoot(t *testing.T) {
	_, _, fs := newServer(t, Options{StageDir: t.TempDir()})
	ctx := context.Background()
	fi, err := fs.Stat(ctx, "/")
	if err != nil || !fi.IsDir() {
		t.Fatalf("根目录 Stat: %v %v", fi, err)
	}
	if _, err := fs.OpenFile(ctx, "/nope/", os.O_RDONLY, 0); err == nil {
		t.Fatal("不存在的目录应该报错")
	}
}

// idOf 按名字找测试节点的 id（只用于用例搭建）。
func (f *fake) idOf(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.nodes {
		if n.name == name {
			return n.id
		}
	}
	return ""
}
