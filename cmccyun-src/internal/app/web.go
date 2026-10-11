package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// remoteList lists a directory on the cloud drive (used by the console picker).
func (s *Service) remoteList(ctx context.Context, dirID string) ([]api.File, error) {
	s.mu.Lock()
	c, err := s.clientLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := c.Init(ctx); err != nil {
		return nil, err
	}
	if dirID == "" {
		dirID = api.RootID
	}
	files, err := c.List(ctx, dirID)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return files, nil
}

// Handler builds the HTTP mux for the console.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 界面是内嵌的单文件页，升级后必须立刻生效（否则浏览器会用旧页面，
		// 看起来像"改了没生效"）。
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(indexHTML))
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		st := s.Status()
		writeJSON(w, map[string]any{
			"status": st,
			"config": s.MaskPtr(),
			"pid":    os.Getpid(),
		})
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, map[string]any{"config": s.MaskPtr()})
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if err := s.Update(patch); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": s.MaskPtr()})
	})

	// /api/auth/refresh renews the pasted ticket on demand. The ticket is good
	// for about a month and only the service can renew it, so the console
	// offers a manual trigger next to the six-hourly background check.
	mux.HandleFunc("/api/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err := s.refreshAuth(ctx, true); err != nil {
			writeErr(w, err.Error())
			return
		}
		st := s.Status().Auth
		msg := "令牌已刷新"
		if st.ExpiresDays > 0 {
			msg = fmt.Sprintf("令牌已刷新，剩余 %.1f 天", st.ExpiresDays)
		}
		writeJSON(w, map[string]any{"ok": true, "auth": st, "message": msg})
	})

	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		files, err := s.remoteList(ctx, api.RootID)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "account": s.Status().Account, "entries": len(files)})
	})

	mux.HandleFunc("/api/remote/list", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		dir := r.URL.Query().Get("dir")
		files, err := s.remoteList(ctx, dir)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		type entry struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size"`
		}
		out := make([]entry, 0, len(files))
		for _, f := range files {
			out = append(out, entry{ID: f.ID, Name: f.Name, IsDir: f.IsDir, Size: f.Size})
		}
		writeJSON(w, map[string]any{"ok": true, "entries": out})
	})

	mux.HandleFunc("/api/mount", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := s.Mount(ctx); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/unmount", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Unmount(); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/remount", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		if err := s.Remount(ctx); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 200
		}
		logs := s.Ring().Tail(n)
		writeJSON(w, map[string]any{"ok": true, "lines": logs})
	})

	mux.HandleFunc("/api/shares", func(w http.ResponseWriter, r *http.Request) {
		// Suggest mount points: the user-visible ones first（个人空间 /volN/<uid>），
		// then the app's own share, then whatever is configured right now.
		type link struct {
			Label string `json:"label"`
			Path  string `json:"path"`
			Hint  string `json:"hint,omitempty"`
			Vol   string `json:"-"`
		}
		var links []link
		curMp := s.Config().MountPoint
		curVol := volumeOf(curMp)
		for _, root := range VolumeRoots() {
			ents, err := os.ReadDir(root)
			if err != nil {
				continue
			}
			for _, e := range ents {
				if !e.IsDir() || !isNumericName(e.Name()) {
					continue
				}
				p := filepath.Join(root, e.Name())
				uid, _, _, ok := ownerOf(p)
				if !ok || uid < 1000 {
					continue
				}
				// 只给「真实存在的用户」做快捷入口：飞牛卷里常有已删用户的残留目录
				// （比如 /vol1/1001），点进去只会是一堆孤儿文件。
				name := ownerName(uid)
				if name == strconv.FormatUint(uint64(uid), 10) {
					continue
				}
				links = append(links, link{
					Label: fmt.Sprintf("个人空间（%s）· %s", name, root),
					Path:  p,
					Hint:  "在「文件」里就是你的个人文件夹",
					Vol:   root,
				})
			}
		}
		// 当前挂载点所在的那个卷排最前，其余按路径排序：常用的就在手边。
		sort.SliceStable(links, func(i, j int) bool {
			ai, aj := links[i].Vol != curVol, links[j].Vol != curVol
			if ai != aj {
				return !ai
			}
			return links[i].Path < links[j].Path
		})
		var out []string
		for _, dir := range []string{os.Getenv("TRIM_DATA_SHARE_PATHS"), "/vol1/@appshare", "/vol2/@appshare", "/vol3/@appshare", "/vol4/@appshare"} {
			if dir == "" {
				continue
			}
			for _, p := range strings.Split(dir, ":") {
				if p == "" {
					continue
				}
				if st, err := os.Stat(p); err == nil && st.IsDir() {
					out = append(out, p)
				}
			}
		}
		// Any existing mount point from config is always offered.
		if mp := s.Config().MountPoint; mp != "" {
			out = append(out, mp)
			links = append(links, link{Label: "当前挂载点", Path: mp, Hint: "正在使用的位置"})
		}
		writeJSON(w, map[string]any{"ok": true, "paths": dedup(out), "links": links, "roots": VolumeRoots()})
	})

	// /api/fs/list 供位置选择器逐级浏览目录（只列目录，不读文件内容）。
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		cur, parent, entries, err := s.BrowseDirs(r.URL.Query().Get("path"))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": cur, "parent": parent, "entries": entries})
	})

	// /api/fs/mkdir 在所选目录下新建文件夹。
	mux.HandleFunc("/api/fs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "需要 POST")
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
		p, err := s.CreateMountDir(body.Parent, body.Name)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": p})
	})

	// /api/mountpoint/check 校验候选位置（只读：不动配置也不挂载）。
	mux.HandleFunc("/api/mountpoint/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path          string `json:"path"`
			AllowNonEmpty bool   `json:"allow_nonempty"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		info, err := s.CheckMountPath(body.Path, body.AllowNonEmpty)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "info": info})
	})

	// /api/mountpoint/apply 真正切换挂载位置（会卸载旧位置、挂到新位置，失败回滚）。
	mux.HandleFunc("/api/mountpoint/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path          string `json:"path"`
			Create        bool   `json:"create"`
			AllowNonEmpty bool   `json:"allow_nonempty"`
			FollowOwner   bool   `json:"follow_owner"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		if err := s.SetMountPoint(ctx, body.Path, SetMountPointOptions{
			Create:        body.Create,
			AllowNonEmpty: body.AllowNonEmpty,
			FollowOwner:   body.FollowOwner,
		}); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": s.MaskPtr(), "status": s.Status()})
	})

	return mux
}

// Listen starts the console HTTP server.
func (s *Service) Listen() (*http.Server, net.Listener, error) {
	cfg := s.Config()
	addr := fmt.Sprintf("%s:%d", cfg.WebListen, cfg.WebPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		s.logf("管理界面已启动: http://%s/", addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logf("管理界面退出: %v", err)
		}
	}()
	return srv, ln, nil
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = filepath.Clean(v)
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
