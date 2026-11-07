package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

// splitCloudPath 把 /01影视/01求片/电影/华语电影 拆成路径段。
//
// STRM 映射里的"云端目录"是路径写法，但云盘接口只认 fileId，所以选目录时得把
// 每一段依次列出来比对（见 resolveCloudPath）。支持首尾多余斜杠、连续斜杠，
// 也支持只有一个 "/"（= 云盘根）。
func splitCloudPath(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return nil
	}
	out := make([]string, 0, 6)
	for _, seg := range strings.Split(p, "/") {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// cloudSeg 是路径解析出来的一节（目录名 + fileId），目录选择器靠它画面包屑、
// 也靠它一级级往回走（云盘接口只认 fileId，没有 parentId 可用）。
type cloudSeg struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// resolveCloudPath 顺着路径一段段列目录，返回最后一段的 fileId 与整条链。
// 找不到时 missing 是断掉的那一段名字，chain 是断掉之前已经解析成功的部分。
func (s *Service) resolveCloudPath(ctx context.Context, id, path string) (string, []cloudSeg, string, error) {
	cur := ""
	chain := make([]cloudSeg, 0, 6)
	for _, seg := range splitCloudPath(path) {
		files, err := s.remoteListOf(id, ctx, cur)
		if err != nil {
			return "", chain, seg, err
		}
		next := ""
		for _, f := range files {
			if !f.IsDir {
				continue
			}
			if f.Name == seg {
				next = f.ID
				break
			}
			if next == "" && strings.EqualFold(f.Name, seg) {
				next = f.ID // 大小写不同也认，云盘里确实有这种情况
			}
		}
		if next == "" {
			return "", chain, seg, nil
		}
		cur = next
		chain = append(chain, cloudSeg{Name: seg, ID: next})
	}
	return cur, chain, "", nil
}

func (s *Service) registerRemoteRoutes(mux *http.ServeMux) {
	// 把「路径写法」解析成 fileId：STRM 映射的目录选择器用它从当前值出发，
	// 也用来校验手输的路径到底存不存在。
	mux.HandleFunc("/api/remote/resolve", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		path := r.URL.Query().Get("path")
		fileID, chain, missing, err := s.resolveCloudPath(ctx, accID(r), path)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		// 注意：解析不到不算接口错误（ok 恒为 true），用 found 表达结果——
		// 前端目录选择器要靠 chain 停到「最后一个存在的目录」上。
		writeJSON(w, map[string]any{
			"ok": true, "found": missing == "", "path": path, "id": fileID, "chain": chain,
			"missing": missing, "segments": len(splitCloudPath(path)),
		})
	})

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
