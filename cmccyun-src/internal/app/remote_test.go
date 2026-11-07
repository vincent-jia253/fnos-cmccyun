package app

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 控制台「云盘浏览」里的写操作（新建/重命名/回收站/下载/上传）必须真的挂上，
// 且参数不合法时要在碰网络之前就报错——不然点一下界面就得等超时。
func TestRemoteRoutesRegisteredAndValidateFirst(t *testing.T) {
	s, _ := newMountSvc(t, "")
	h := s.Handler()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		code   int
		want   string
	}{
		{"mkdir 只接受 POST", http.MethodGet, "/api/remote/mkdir", "", 400, "请用 POST"},
		{"mkdir 名字不能空", http.MethodPost, "/api/remote/mkdir", `{"parent":"","name":""}`, 400, "文件夹名不能为空"},
		{"rename 参数要全", http.MethodPost, "/api/remote/rename", `{"id":"","name":""}`, 400, "缺少文件 ID 或新名字"},
		{"trash 至少一个", http.MethodPost, "/api/remote/trash", `{"ids":[]}`, 400, "没有指定要删除的条目"},
		{"download 需要 id", http.MethodGet, "/api/remote/download", "", 400, "缺少文件 ID"},
		{"upload 只接受 POST", http.MethodGet, "/api/remote/upload", "", 400, "请用 POST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.code {
				t.Fatalf("HTTP %d，期望 %d；body=%s", rr.Code, tc.code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Errorf("响应里没有 %q：%s", tc.want, rr.Body.String())
			}
		})
	}

	// 上传不带文件时也要直接报错（不能等到挂进云盘才失败）
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("note", "没有 file 字段")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/remote/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "没有收到文件") {
		t.Fatalf("没有 file 字段时应报「没有收到文件」，实际 HTTP %d：%s", rr.Code, rr.Body.String())
	}
}

// 页面上的写操作按钮必须都有对应接口，别只在 JS 里写了入口。
func TestBrowseTabExposesFileOperations(t *testing.T) {
	for _, want := range []string{
		`/api/remote/mkdir`, `api/remote/rename`, `/api/remote/trash`,
		`/api/remote/download`, `/api/remote/upload`,
		`id="btnRemoteMkdir"`, `id="btnRemoteUpload"`, `id="f_upload"`,
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("云盘浏览页缺少 %q", want)
		}
	}
}

// STRM 映射的云端目录改成点选以后，路径解析是核心：
// 「/a/b」拆段、根目录、多余斜杠都要稳。
func TestSplitCloudPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"/", nil},
		{"   ", nil},
		{"/01影视", []string{"01影视"}},
		{"/01影视/01求片/电影/华语电影", []string{"01影视", "01求片", "电影", "华语电影"}},
		{"01影视/电影/", []string{"01影视", "电影"}},
		{"//a//b//", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := splitCloudPath(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%q → %v，期望 %v", c.in, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%q 第 %d 段：%q ≠ %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// 根目录不需要访问云端就能解析（前端打开选择器时默认落在这里）。
func TestResolveCloudPathRoot(t *testing.T) {
	s, _ := newMountSvc(t, "")
	id, chain, missing, err := s.resolveCloudPath(context.Background(), "", "/")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" || len(chain) != 0 || missing != "" {
		t.Fatalf("根目录应解析成空 id / 空链，得到 id=%q chain=%v missing=%q", id, chain, missing)
	}
}

// /api/remote/resolve 对根目录要给 found=true（不依赖云端 client）。
func TestRemoteResolveEndpointRoot(t *testing.T) {
	s, _ := newMountSvc(t, "")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remote/resolve?path=/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d：%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["found"] != true {
		t.Fatalf("根目录应 found=true，得到 %v", body)
	}
}

// 界面上必须给出目录选择器，映射的云端目录不再是纯手输。
func TestStrmCloudPickerUIPresent(t *testing.T) {
	for _, want := range []string{
		`id="cloudPickModal"`,
		`id="cloudPickList"`,
		`id="cloudPickPath"`,
		`id="cloudPickOK"`,
		`id="cloudPickUp"`,
		`/api/remote/resolve`,
		`/api/remote/list`,
		`function openCloudPicker`,
		`data-act="export"`, // 确认没把账号页那套改坏
		"/01影视",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("STRM 页缺 %q", want)
		}
	}
	if !strings.Contains(indexHTML, `readonly placeholder="点「选云端…」`) {
		t.Error("云端目录输入框应该是只读的（点选而不是手输）")
	}
}
