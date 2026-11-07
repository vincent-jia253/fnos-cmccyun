package app

import (
	"context"
	"encoding/base64"
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

// accID 从请求里取账号 ID（?account=xxx；空表示默认账号）。
// 多账号下所有接口都用它选目标，单账号时永远是空串——行为与以前完全一致。
func accID(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("account"))
}

// remoteList lists a directory on the cloud drive (used by the console picker).
func (s *Service) remoteList(ctx context.Context, dirID string) ([]api.File, error) {
	return s.remoteListOf("", ctx, dirID)
}

// remoteListOf 列指定账号的云盘目录。
func (s *Service) remoteListOf(id string, ctx context.Context, dirID string) ([]api.File, error) {
	s.mu.Lock()
	a, ac, aerr := s.acctLocked(id)
	var c *api.Client
	var err error
	if aerr == nil {
		c, err = s.clientLocked(a, ac)
	} else {
		err = aerr
	}
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
		writeJSON(w, map[string]any{
			"status":   s.StatusOf(accID(r)),
			"accounts": s.AccountsView(),
			"config":   s.MaskPtrFor(accID(r)),
			"pid":      os.Getpid(),
		})
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, map[string]any{"config": s.MaskPtrFor(accID(r))})
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if err := s.UpdateOf(accID(r), patch); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": s.MaskPtrFor(accID(r))})
	})

	// /api/auth/refresh renews the pasted ticket on demand. The ticket is good
	// for about a month and only the service can renew it, so the console
	// offers a manual trigger next to the six-hourly background check.
	mux.HandleFunc("/api/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		aid := accID(r)
		if err := s.refreshAuth(ctx, aid, true); err != nil {
			writeErr(w, err.Error())
			return
		}
		st := s.StatusOf(aid).Auth
		msg := "令牌已刷新"
		if st.ExpiresDays > 0 {
			msg = fmt.Sprintf("令牌已刷新，剩余 %.1f 天", st.ExpiresDays)
		}
		writeJSON(w, map[string]any{"ok": true, "auth": st, "message": msg})
	})

	// /api/auth/qr/start 生成登录二维码。二维码内容直接进 PNG、再以 data URI
	// 回给前端，所以控制台不需要外网、不需要前端 QR 库。
	mux.HandleFunc("/api/auth/qr/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		id, png, ttl, err := s.StartQrLogin(ctx, accID(r))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"ok":         true,
			"id":         id,
			"image":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
			"expires_in": ttl,
		})
	})

	// /api/auth/qr/poll 轮询扫码状态。状态走 waiting/scanned/confirmed/expired/
	// cancelled，confirmed 时服务端已经把授权写进配置并在后台挂载。
	mux.HandleFunc("/api/auth/qr/poll", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		res, err := s.PollQrLogin(ctx, r.URL.Query().Get("id"))
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		files, err := s.remoteListOf(accID(r), ctx, api.RootID)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "account": s.StatusOf(accID(r)).Account, "entries": len(files)})
	})

	mux.HandleFunc("/api/remote/list", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		dir := r.URL.Query().Get("dir")
		files, err := s.remoteListOf(accID(r), ctx, dir)
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
		if err := s.MountOf(ctx, accID(r)); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/unmount", func(w http.ResponseWriter, r *http.Request) {
		if err := s.UnmountOf(accID(r)); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/remount", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		if err := s.RemountOf(ctx, accID(r)); err != nil {
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
		// ?account=<id> 只看这一台的日志（行里没有账号字段，按 ID/尾号/备注名/
		// 挂载点这类线索过滤）。多账号时先多捞一些，过滤完再从尾部截 n 行。
		id := accID(r)
		logs := s.Ring().Tail(n)
		filtered := false
		if id != "" {
			cfg := s.Config()
			if ac, ok := cfg.AccountByID(id); ok {
				logs = filterLogsForAccount(s.Ring().Tail(n*20), ac)
				if len(logs) > n {
					logs = logs[len(logs)-n:]
				}
				filtered = true
			}
		}
		writeJSON(w, map[string]any{"ok": true, "lines": logs,
			"filtered": filtered, "account": id})
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
		cfgNow := s.Config()
		curAcc, _ := cfgNow.AccountByID(accID(r))
		curMp := ""
		if curAcc != nil {
			curMp = curAcc.MountPoint
		}
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
		if mp := curMp; mp != "" {
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
		p, err := s.CreateMountDirOf(accID(r), body.Parent, body.Name)
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
		info, err := s.CheckMountPathOf(accID(r), body.Path, body.AllowNonEmpty)
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
		if err := s.SetMountPointOf(accID(r), ctx, body.Path, SetMountPointOptions{
			Create:        body.Create,
			AllowNonEmpty: body.AllowNonEmpty,
			FollowOwner:   body.FollowOwner,
		}); err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": s.MaskPtrFor(accID(r)),
			"status": s.StatusOf(accID(r)), "accounts": s.AccountsView()})
	})

	// 「云盘浏览」里的写操作（新建文件夹 / 重命名 / 回收站 / 下载 / 上传）
	s.registerRemoteRoutes(mux)

	// 影视 STRM：控制台接口 + 播放直链。
	s.registerStrmRoutes(mux)

	// 多账号管理：列表 / 新增 / 改名 / 删除 / 设默认。
	s.registerAccountRoutes(mux)

	// 远程挂载（WebDAV）：控制台接口 + /dav/ 本体（多账号时还有 /dav/<id>/）。
	s.registerDAVRoutes(mux)
	// /dav/<id>/ 靠分派器认账号，不再按账号注册路由：账号能在运行时新增，
	// 路由钉死了新账号就 404（要么就得重启才生效）。
	mux.Handle("/dav", s.davDispatch())
	mux.Handle("/dav/", s.davDispatch())

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
