package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// 「云盘浏览」页的写操作：新建文件夹 / 重命名 / 移到回收站 / 下载 / 上传。
//
// 这些动作直接作用在云端（不是挂载点），所以和 FUSE 挂载互不影响：
// 挂载目录里的目录缓存（默认 60 秒）过期后自然看到新内容。
// 删除一律进云端回收站，可在 139 网页版 / App 里还原。

// apiClient 取一个客户端指针。锁只用来拿指针，网络请求一律在锁外做。
func (s *Service) apiClient() (*api.Client, error) {
	return s.apiClientOf("")
}

// apiClientOf 取指定账号的客户端（多账号下控制台按 ?account= 选）。
func (s *Service) apiClientOf(id string) (*api.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ac, err := s.acctLocked(id)
	if err != nil {
		return nil, err
	}
	return s.clientLocked(a, ac)
}

func writeOK(w http.ResponseWriter, kv map[string]any) {
	if kv == nil {
		kv = map[string]any{}
	}
	kv["ok"] = true
	writeJSON(w, kv)
}

// childID 在父目录里按名字找一个条目的 fileId（找不到返回空串）。
func (s *Service) childID(ctx context.Context, parentID, name string) string {
	files, err := s.remoteListOf("", ctx, parentID)
	if err != nil {
		return ""
	}
	for _, f := range files {
		if f.Name == name {
			return f.ID
		}
	}
	return ""
}

func (s *Service) registerRemoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/remote/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var body struct {
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if body.Name == "" {
			writeErr(w, "文件夹名不能为空")
			return
		}
		if body.Parent == "" {
			body.Parent = api.RootID
		}
		c, err := s.apiClientOf(accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		name, err := c.Mkdir(ctx, body.Parent, body.Name)
		if err != nil {
			s.logf("云盘：新建文件夹 %q 失败：%v", body.Name, err)
			writeErr(w, err.Error())
			return
		}
		// /file/create 只回「最终用的名字」（重名时服务端会改名），真正的 fileId
		// 要回读一次父目录；控制台后续的重命名/删除/上传都用这个 id。
		id := name
		if got := s.childID(ctx, body.Parent, name); got != "" {
			id = got
		}
		s.logf("云盘：已新建文件夹 %q（父目录 %s）", name, body.Parent)
		writeOK(w, map[string]any{"id": id, "name": name, "message": "已创建 " + name})
	})

	mux.HandleFunc("/api/remote/rename", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var body struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if body.ID == "" || body.Name == "" {
			writeErr(w, "缺少文件 ID 或新名字")
			return
		}
		c, err := s.apiClientOf(accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		if err := c.Rename(ctx, body.ID, body.Name); err != nil {
			s.logf("云盘：重命名失败：%v", err)
			writeErr(w, err.Error())
			return
		}
		s.logf("云盘：已重命名为 %q", body.Name)
		writeOK(w, map[string]any{"name": body.Name, "message": "已重命名为 " + body.Name})
	})

	mux.HandleFunc("/api/remote/trash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if len(body.IDs) == 0 {
			writeErr(w, "没有指定要删除的条目")
			return
		}
		c, err := s.apiClientOf(accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err := c.Trash(ctx, body.IDs); err != nil {
			s.logf("云盘：删除失败：%v", err)
			writeErr(w, err.Error())
			return
		}
		s.logf("云盘：已把 %d 个条目移到回收站", len(body.IDs))
		writeOK(w, map[string]any{
			"count":   len(body.IDs),
			"message": fmt.Sprintf("已移到云盘回收站（%d 项），可在 139 网页版或 App 里还原", len(body.IDs)),
		})
	})

	// 下载走 302 到云端签名链接：不占 NAS 带宽中转，浏览器直接下。
	mux.HandleFunc("/api/remote/download", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, "缺少文件 ID")
			return
		}
		c, err := s.apiClientOf(accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		u, err := c.DownloadURL(ctx, id)
		if err != nil {
			s.logf("云盘：取下载地址失败：%v", err)
			writeErr(w, err.Error())
			return
		}
		http.Redirect(w, r, u, http.StatusFound)
	})

	// 上传：浏览器 multipart → 落一个临时文件 → 分片上传到云端当前目录。
	mux.HandleFunc("/api/remote/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		parent := r.URL.Query().Get("parent")
		if parent == "" {
			parent = api.RootID
		}
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			writeErr(w, "解析上传内容失败: "+err.Error())
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, "没有收到文件: "+err.Error())
			return
		}
		defer f.Close()
		cfg := s.Config()
		tmp, err := os.CreateTemp(cfg.StageDir, "upload-*")
		if err != nil {
			writeErr(w, "创建临时文件失败: "+err.Error())
			return
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		size, err := io.Copy(tmp, f)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			writeErr(w, "写入临时文件失败: "+err.Error())
			return
		}
		c, err := s.apiClientOf(accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		// 上传大文件可能很久，别用请求上下文（它没有更短的期限，但也不该被控制台的
		// 25 秒超时打断——前端为此用了不带超时的 fetch）。
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Hour)
		defer cancel()
		partMB := cfg.UploadPartMB
		if partMB <= 0 {
			partMB = 100
		}
		s.logf("云盘：开始上传 %q（%d 字节）", hdr.Filename, size)
		name, err := c.UploadFile(ctx, parent, hdr.Filename, tmpName, api.UploadOptions{
			PartSize: int64(partMB) << 20,
		})
		if err != nil {
			s.logf("云盘：上传 %q 失败：%v", hdr.Filename, err)
			writeErr(w, err.Error())
			return
		}
		s.logf("云盘：上传完成 %q（%d 字节）", name, size)
		writeOK(w, map[string]any{
			"name":    name,
			"size":    size,
			"message": "已上传 " + name + "（" + fmt.Sprintf("%.1f MB", float64(size)/1048576) + "）",
		})
	})
}
