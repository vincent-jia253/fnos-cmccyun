package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/dav"
)

// WebDAV 入口（路线 B）。飞牛 fnOS 的「远程挂载」支持协议里的 WebDAV，所以
// 我们自己当服务端，用户把它挂成远程文件夹就行：
//
//   文件 → 远程挂载 → 挂载远程文件夹 → WebDAV
//   地址 http://<NAS-IP>:19898/dav/   账号 cmccyun   密码（控制台里复制）
//
// 这条路径不碰飞牛的任何闭源二进制（升级不会被覆盖），和 FUSE 挂载并存，
// 用户二选一或都开都行。注意同一个云盘目录不要两边同时写，缓存各自独立。

// davProxy 让 webdav.Handler 手里的 FileSystem 能跟着 client 重建（换账号、
// 重新登录后旧 FS 里的缓存要作废）。
type davProxy struct {
	s  *Service
	id string
}

func (p davProxy) fs() *dav.FS {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.davFSLocked(p.id)
}

func (p davProxy) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	f := p.fs()
	if f == nil {
		return os.ErrPermission
	}
	return f.Mkdir(ctx, name, perm)
}

func (p davProxy) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	f := p.fs()
	if f == nil {
		return nil, os.ErrPermission
	}
	return f.OpenFile(ctx, name, flag, perm)
}

func (p davProxy) RemoveAll(ctx context.Context, name string) error {
	f := p.fs()
	if f == nil {
		return os.ErrPermission
	}
	return f.RemoveAll(ctx, name)
}

func (p davProxy) Rename(ctx context.Context, oldName, newName string) error {
	f := p.fs()
	if f == nil {
		return os.ErrPermission
	}
	return f.Rename(ctx, oldName, newName)
}

func (p davProxy) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	f := p.fs()
	if f == nil {
		return nil, os.ErrPermission
	}
	return f.Stat(ctx, name)
}

// davFSLocked builds (or reuses) the WebDAV file system of one account.
// Caller holds s.mu.
func (s *Service) davFSLocked(id string) *dav.FS {
	a, ac, err := s.acctLocked(id)
	if err != nil {
		return nil
	}
	if a.davFS != nil {
		return a.davFS
	}
	c, err := s.clientLocked(a, ac)
	if err != nil {
		return nil
	}
	partMB := s.cfg.UploadPartMB
	if partMB <= 0 {
		partMB = 100
	}
	a.davFS = dav.New(c, dav.Options{
		StageDir: s.cfg.StageDir,
		PartSize: int64(partMB) << 20,
		ReadOnly: ac.ReadOnly,
		Logf:     s.logf,
	})
	return a.davFS
}

// davPathFor 是一台账号的 WebDAV 挂载路径：默认账号同时占据 /dav/（老地址不变）。
// davPathFor 返回这台账号的 WebDAV 路径。
//
// 新账号一律是 /dav/<8 位随机>/（添加账号时自动生成，见 newDAVPath）；
// 老配置里没有 dav_path 的账号沿用老规则：默认账号 /dav/，其余 /dav/<账号ID>/，
// 这样已经配好的客户端不会因为升级而失效。
func davPathFor(ac *config.AccountConfig) string {
	if ac == nil {
		return "/dav/"
	}
	if p := strings.TrimSpace(ac.DAVPath); p != "" {
		return "/dav/" + p + "/"
	}
	if ac.ID == "" || ac.ID == config.DefaultAccountID {
		return "/dav/"
	}
	return "/dav/" + ac.ID + "/"
}

// davPathForID 是给只有账号 ID 的调用方用的糖。
func (s *Service) davPathForID(id string) string {
	cfg := s.Config()
	if ac, ok := cfg.AccountByID(id); ok {
		return davPathFor(ac)
	}
	return davPathFor(nil)
}

// newDAVPath 生成 8 位随机路径段：只用小写字母 + 数字（URL 里不用转义），
// 并去掉 0/o/1/l/i 这些容易看错的字符。
func newDAVPath() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]byte, 8)
	limit := big.NewInt(int64(len(alphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			return strings.ToLower(base64.RawURLEncoding.EncodeToString(b))[:8]
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out)
}

// uniqueDAVPathLocked 挑一个没被任何账号（含账号 ID）占用的随机路径段。
func (s *Service) uniqueDAVPathLocked() string {
	taken := map[string]bool{}
	for i := range s.cfg.Accounts {
		ac := &s.cfg.Accounts[i]
		taken[strings.ToLower(ac.ID)] = true
		if p := strings.ToLower(strings.TrimSpace(ac.DAVPath)); p != "" {
			taken[p] = true
		}
		if p := strings.ToLower(strings.TrimSpace(ac.DAVPathPrev)); p != "" {
			taken[p] = true
		}
	}
	for n := 0; n < 64; n++ {
		cand := newDAVPath()
		if !taken[cand] {
			return cand
		}
	}
	return newDAVPath()
}

// davConf 是处理一个 WebDAV 请求需要的全部配置快照。
type davConf struct {
	id               string
	user, pass       string
	redirect, ro, ok bool
}

// davReady reports whether the WebDAV endpoint of one account should answer.
func (s *Service) davReady(id string) davConf {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ac, err := s.acctLocked(id)
	if err != nil {
		return davConf{}
	}
	if !s.cfg.DAVEnabled || ac.DAVPassword == "" {
		return davConf{}
	}
	if a.client == nil && ac.Authorization == "" {
		return davConf{}
	}
	return davConf{
		id:       ac.ID,
		user:     ac.DAVUser,
		pass:     ac.DAVPassword,
		redirect: ac.DAVRedirectOn(),
		ro:       ac.ReadOnly,
		ok:       true,
	}
}

// davUnsafe 是「会改云端内容」的方法集合：只读模式下控制台在这一层就拦掉，
// 而不是等 webdav 库把 os.ErrPermission 翻成 404/405（PUT 会被翻成 404，
// 客户端会误以为是路径写错，排查起来很费劲）。
func davUnsafe(method string) bool {
	switch method {
	case "PUT", "DELETE", "MKCOL", "MOVE", "COPY", "PROPPATCH":
		return true
	}
	return false
}

// davRouteID 从 /dav/<id>/... 里认出账号。认不出来（或者就是默认账号）时返回 ""，
// 交给默认账号处理——这样 /dav/01影视/x.mkv 这种老地址永远有效。
//
// 账号是运行时可新增的，路由不能在启动时钉死，所以 /dav/ 用一个分派器。
func (s *Service) davRouteID(p string) string {
	rest := strings.TrimPrefix(p, "/dav")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return ""
	}
	seg := rest
	if i := strings.Index(seg, "/"); i >= 0 {
		seg = seg[:i]
	}
	if seg == "" || seg == config.DefaultAccountID {
		return ""
	}
	snap := s.Config()
	// ① 随机路径（新账号的正式地址）+ 上一版路径（换过路径的老地址）
	for i := range snap.Accounts {
		ac := &snap.Accounts[i]
		if p := ac.DAVPath; p != "" && strings.EqualFold(p, seg) {
			return ac.ID
		}
		if p := ac.DAVPathPrev; p != "" && strings.EqualFold(p, seg) {
			return ac.ID
		}
	}
	// ② 账号 ID（老地址，保留兼容）
	if _, ok := snap.AccountByID(seg); ok {
		return seg
	}
	return ""
}

// davDispatch 按 URL 第一段分派到对应账号（见 davRouteID）。
func (s *Service) davDispatch() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.davHandler(s.davRouteID(r.URL.Path)).ServeHTTP(w, r)
	})
}

// davHandler 是挂在 /dav/ 上的完整入口：BasicAuth → （可选）GET 302 到 CDN
// → 其余交给 webdav.Handler。
func (s *Service) davHandler(id string) http.Handler {
	prefix := "/dav"
	if id != "" && id != config.DefaultAccountID {
		prefix = "/dav/" + id
	}
	h := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: davProxy{s: s, id: id},
		// 锁只在进程内有效：多个客户端并发写同一个文件时能互相排斥就够了，
		// 云盘那边没有跨进程锁可用。
		LockSystem: webdav.NewMemLS(),
		// 只有真出错才记：x/net 的 Handler 有些分支会带着 nil 回调 Logger，
		// 原样打印会刷出一堆「出错：<nil>」的假警报。
		Logger: func(r *http.Request, err error) {
			if err == nil {
				return
			}
			s.logf("WebDAV：%s %s 出错：%v", r.Method, r.URL.Path, err)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dc := s.davReady(id)
		if !dc.ok {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("WebDAV 未启用：请在移动云盘控制台「远程挂载 (WebDAV)」页打开开关\n"))
			return
		}
		if !basicAuthOK(r, dc.user, dc.pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="cmccyun", charset="UTF-8"`)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("需要用户名和密码\n"))
			return
		}

		if dc.ro && davUnsafe(r.Method) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("挂载是只读的：请先在控制台把「只读」关掉再写入\n"))
			return
		}

		// GET/HEAD 直接 302 到 139 的 CDN 签名链接：4K 播放不必绕 NAS，
		// 也顺带避开本进程单连接的吞吐上限。Range 会由客户端带给 CDN
		// （实测 CDN 返回 206，拖动进度条正常）。
		if dc.redirect && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if f := (davProxy{s: s, id: id}).fs(); f != nil {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				e, err := f.Resolve(ctx, strings.TrimPrefix(r.URL.Path, prefix))
				if err == nil && !e.IsDir {
					if u, uerr := f.DirectURL(ctx, e.ID); uerr == nil && u != "" {
						cancel()
						http.Redirect(w, r, u, http.StatusFound)
						return
					}
				}
				cancel()
			}
		}

		// /dav 后面补斜杠，省得客户端把 301 当成挂载失败。
		if r.URL.Path == prefix {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func basicAuthOK(r *http.Request, user, pass string) bool {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}
	// 常数时间比较：密码是 16 位随机串，但仍然不给按字节试探的时序差。
	ueq := subtle.ConstantTimeCompare([]byte(u), []byte(user))
	peq := subtle.ConstantTimeCompare([]byte(p), []byte(pass))
	return ueq&peq == 1
}

// newDAVPassword 生成 16 位 URL 安全随机密码（96 bit）。
// davAlphabet 去掉了 0/O/1/l/I 这些抄下来会看错的字符；55 个字符够随机了。
const davAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"

// randToken 生成 n 位随机串（crypto/rand，无模偏）。
func randToken(n int) string {
	out := make([]byte, n)
	limit := big.NewInt(int64(len(davAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			b := make([]byte, n)
			_, _ = rand.Read(b)
			return base64.RawURLEncoding.EncodeToString(b)[:n]
		}
		out[i] = davAlphabet[idx.Int64()]
	}
	return string(out)
}

// newDAVUser 是 8 位随机 WebDAV 用户名（不再用可读的 cmccyun 式默认值）。
func newDAVUser() string { return randToken(8) }

// newDAVPassword 是 16 位随机密码。
func newDAVPassword() string { return randToken(16) }

// DAVInfo is what the console page shows: connection details plus a couple of
// ready-to-paste examples.
// DAVAccountsView 是「全部账号的 WebDAV」总览：每台一行，只给界面要显示的字段。
//
// WebDAV 的开关（DAVEnabled）是全局的，但路径 `/dav/` vs `/dav/<账号ID>/`、
// 用户名、密码、只读、是否 302 到 CDN 都是**按账号**的；多账号时没有一个总表，
// 用户根本记不住第二台的地址和密码，所以单独给一份。
func (s *Service) DAVAccountsView() []map[string]any {
	cfg := s.Config()
	// 先统计用户名出现次数：多账号沿用同一个默认用户名时，界面上要标出来。
	count := map[string]int{}
	for i := range cfg.Accounts {
		if u := strings.ToLower(strings.TrimSpace(cfg.Accounts[i].DAVUser)); u != "" {
			count[u]++
		}
	}
	out := make([]map[string]any, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		ac := &cfg.Accounts[i]
		path := davPathFor(ac)
		urls := make([]string, 0, 4)
		for _, host := range localIPv4() {
			urls = append(urls, fmt.Sprintf("http://%s:%d%s", host, cfg.WebPort, path))
		}
		if len(urls) == 0 {
			urls = append(urls, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.WebPort, path))
		}
		out = append(out, map[string]any{
			"id":              ac.ID,
			"name":            ac.DisplayName(),
			"account":         ac.Account,
			"default":         ac.ID == cfg.DefaultAccount,
			"path":            path,
			"url":             urls[0],
			"user":            ac.DAVUser,
			"password_set":    ac.DAVPassword != "",
			"read_only":       ac.ReadOnly,
			"redirect":        ac.DAVRedirectOn(),
			"enabled":         cfg.DAVEnabled,
			"user_duplicated": count[strings.ToLower(strings.TrimSpace(ac.DAVUser))] > 1,
			"mounted":         s.StatusOf(ac.ID).Mounted,
		})
	}
	return out
}

func (s *Service) DAVInfo(id string) map[string]any {
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok {
		return map[string]any{"enabled": false, "urls": []string{}}
	}
	path := davPathFor(ac)
	urls := make([]string, 0, 4)
	for _, host := range localIPv4() {
		urls = append(urls, fmt.Sprintf("http://%s:%d%s", host, cfg.WebPort, path))
	}
	if len(urls) == 0 {
		urls = append(urls, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.WebPort, path))
	}
	return map[string]any{
		"enabled":      cfg.DAVEnabled,
		"account":      ac.ID,
		"path":         path,
		"user":         ac.DAVUser,
		"password":     ac.DAVPassword,
		"password_set": ac.DAVPassword != "",
		"port":         cfg.WebPort,
		"urls":         urls,
		"read_only":    ac.ReadOnly,
		"redirect":     ac.DAVRedirectOn(),
		"rclone": fmt.Sprintf(
			"rclone mount :webdav: /mnt/cmccyun --webdav-url %s --webdav-vendor other --webdav-user %s --webdav-pass $(rclone obscure '%s')",
			urls[0], ac.DAVUser, ac.DAVPassword),
	}
}

// SetDAV applies a patch from the console: enable/disable, rename, reset the
// password, toggle redirect. Enabling without a password generates one.

// davUserTakenLocked 判断某个 WebDAV 用户名是否已被「别的」账号占用（忽略大小写）。
func (s *Service) davUserTakenLocked(user, exceptID string) bool {
	user = strings.ToLower(strings.TrimSpace(user))
	if user == "" {
		return false
	}
	for i := range s.cfg.Accounts {
		o := &s.cfg.Accounts[i]
		if o.ID == exceptID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(o.DAVUser)) == user {
			return true
		}
	}
	return false
}

// ResetDAVCredentials 给指定账号重新生成「8 位随机用户名 + 16 位随机密码」。
func (s *Service) ResetDAVCredentials(id string) (map[string]any, error) {
	s.mu.Lock()
	ac, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("账号不存在：%s", id)
	}
	ac.DAVUser = s.uniqueDAVUserLocked(newDAVUser())
	ac.DAVPassword = newDAVPassword()
	name, user := ac.DisplayName(), ac.DAVUser
	snapshot := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return nil, err
	}
	s.logf("WebDAV：账号「%s」换了一组随机账号密码（用户名 %s）", name, user)
	return s.DAVInfo(id), nil
}

// ResetDAVPath 给指定账号换一个 8 位随机 WebDAV 路径（老地址仍然可用，不会立刻断）。
func (s *Service) ResetDAVPath(id string) (map[string]any, error) {
	s.mu.Lock()
	ac, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("账号不存在：%s", id)
	}
	if old := strings.TrimSpace(ac.DAVPath); old != "" {
		ac.DAVPathPrev = old
	}
	ac.DAVPath = s.uniqueDAVPathLocked()
	name, path := ac.DisplayName(), davPathFor(ac)
	snapshot := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return nil, err
	}
	s.logf("WebDAV：账号「%s」的地址改为 %s（旧地址仍可用）", name, path)
	return s.DAVInfo(id), nil
}

// FixDuplicateDAVUsers 把「用户名重复或为空」的账号一次性换成随机用户名。
//
// 历史上新建账号的默认用户名都是 cmccyun，多账号下必然撞车；这就是界面上
// 「一键修掉重名」按钮的实现。已经唯一的账号一律不动（不打扰用户已配好的客户端）。
func (s *Service) FixDuplicateDAVUsers() ([]map[string]any, error) {
	s.mu.Lock()
	count := map[string]int{}
	for i := range s.cfg.Accounts {
		u := strings.ToLower(strings.TrimSpace(s.cfg.Accounts[i].DAVUser))
		if u != "" {
			count[u]++
		}
	}
	changed := make([]map[string]any, 0, len(s.cfg.Accounts))
	for i := range s.cfg.Accounts {
		ac := &s.cfg.Accounts[i]
		u := strings.ToLower(strings.TrimSpace(ac.DAVUser))
		if u != "" && count[u] == 1 {
			continue
		}
		ac.DAVUser = s.uniqueDAVUserLocked(newDAVUser())
		if ac.DAVPassword == "" {
			ac.DAVPassword = newDAVPassword()
		}
		changed = append(changed, map[string]any{
			"id": ac.ID, "name": ac.DisplayName(), "user": ac.DAVUser,
		})
	}
	snapshot := *s.cfg
	s.mu.Unlock()
	if len(changed) == 0 {
		return changed, nil
	}
	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return nil, err
	}
	for _, c := range changed {
		s.logf("WebDAV：账号「%v」改为随机用户名 %v", c["name"], c["user"])
	}
	return changed, nil
}

func (s *Service) SetDAV(id string, patch map[string]any) (map[string]any, error) {
	s.mu.Lock()
	a, ac, err := s.acctLocked(id)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if v, ok := patch["enabled"].(bool); ok {
		s.cfg.DAVEnabled = v
		// 打开开关时，把所有还没设过密码的账号补一条（多账号下每台都有自己的凭据）。
		if v {
			for i := range s.cfg.Accounts {
				o := &s.cfg.Accounts[i]
				if o.DAVUser == "" {
					o.DAVUser = config.DefaultDAVUser
				}
				if o.DAVPassword == "" && o.Authorization != "" {
					o.DAVPassword = newDAVPassword()
				}
			}
			if ac.DAVPassword != "" {
				s.logger.Printf("WebDAV：已开启（账号 %s，用户 %s）", ac.DisplayName(), ac.DAVUser)
			}
		}
	}
	if v, ok := patch["user"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			v = s.uniqueDAVUserLocked(newDAVUser())
		} else if s.davUserTakenLocked(v, ac.ID) {
			// 重名会让用户分不清 /dav/ 与 /dav/<id>/ 该用哪个号，入口就拒掉。
			s.mu.Unlock()
			return nil, fmt.Errorf("WebDAV 用户名 %q 已经被另一台账号用了，换一个或点「随机生成」", v)
		}
		ac.DAVUser = v
	}
	if v, ok := patch["new_path"].(bool); ok && v {
		if old := strings.TrimSpace(ac.DAVPath); old != "" {
			ac.DAVPathPrev = old
		}
		ac.DAVPath = s.uniqueDAVPathLocked()
	}
	if v, ok := patch["password"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			ac.DAVPassword = newDAVPassword()
		} else {
			if len(v) < 4 {
				s.mu.Unlock()
				return nil, fmt.Errorf("密码至少 4 位")
			}
			ac.DAVPassword = v
		}
	}
	if v, ok := patch["regenerate"].(bool); ok && v {
		ac.DAVPassword = newDAVPassword()
	}
	if v, ok := patch["redirect"].(bool); ok {
		vv := v
		ac.DAVRedirect = &vv
	}
	// 只读是 FUSE 和 WebDAV 共用的开关，改了就重建 FS，让新连接立刻生效。
	if _, ok := patch["read_only"].(bool); ok {
		a.davFS = nil
	}
	cfgPath := s.cfgPath
	cfg := *s.cfg
	s.mu.Unlock()

	if err := config.Save(cfgPath, &cfg); err != nil {
		return nil, err
	}
	return s.DAVInfo(id), nil
}

// registerDAVRoutes wires the console API for this page.
func (s *Service) registerDAVRoutes(mux *http.ServeMux) {
	// POST /api/dav/reset {"id":"<账号>"} → 该账号换一组随机账号密码
	// POST /api/dav/reset {"fix_duplicates":true} → 一键把重名/空用户名的账号换成随机
	mux.HandleFunc("/api/dav/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var body struct {
			ID            string `json:"id"`
			FixDuplicates bool   `json:"fix_duplicates"`
			NewPath       bool   `json:"new_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		if body.FixDuplicates {
			changed, err := s.FixDuplicateDAVUsers()
			if err != nil {
				writeErr(w, err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true, "changed": changed, "accounts": s.DAVAccountsView()})
			return
		}
		id := strings.TrimSpace(body.ID)
		if id == "" {
			id = accID(r)
		}
		if body.NewPath {
			info, err := s.ResetDAVPath(id)
			if err != nil {
				writeErr(w, err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true, "dav": info, "accounts": s.DAVAccountsView()})
			return
		}
		info, err := s.ResetDAVCredentials(id)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "dav": info, "accounts": s.DAVAccountsView()})
	})

	mux.HandleFunc("/api/dav/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "accounts": s.DAVAccountsView(),
			"enabled": s.Config().DAVEnabled})
	})

	mux.HandleFunc("/api/dav/info", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("account")
		writeJSON(w, map[string]any{"ok": true, "dav": s.DAVInfo(id)})
	})

	mux.HandleFunc("/api/dav/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		id := r.URL.Query().Get("account")
		if v, ok := patch["account"].(string); ok && v != "" {
			id = v
		}
		info, err := s.SetDAV(id, patch)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		// 开关状态变化后要让页面上的「已启用」徽标立刻对：配置已经在
		// SetDAV 里写盘，这里只需要回最新的 info。
		writeJSON(w, map[string]any{"ok": true, "dav": info, "config": s.MaskPtrFor(accID(r))})
	})
}

// localIPv4 lists the host's LAN IPv4 addresses, best candidate first.
//
// 只认真实网卡：docker0 / br-* / veth* / virbr* 这些是容器和虚拟网络的地址，
// 把 19 条 172.x 列在页面上只会让用户挑错（飞牛机器上装了 Docker 就一屏都是）。
func localIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	type cand struct {
		ip    string
		score int
	}
	var cands []cand
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := ifc.Name
		for _, bad := range []string{"docker", "br-", "veth", "virbr", "tun", "tap", "zt", "wg", "tailscale", "kube"} {
			if strings.HasPrefix(name, bad) {
				name = ""
				break
			}
		}
		if name == "" {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.To4()
			// 只留内网地址：飞牛「远程挂载」填公网 IP 会绕出去，没意义。
			if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
				continue
			}
			// 家宽/办公网最常见的是 192.168，其次是 10/8；172.16/12 往往是
			// Docker 默认网段，排最后。
			score := 3
			switch {
			case ip[0] == 192 && ip[1] == 168:
				score = 0
			case ip[0] == 10:
				score = 1
			case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
				score = 2
			}
			cands = append(cands, cand{ip: ip.String(), score: score})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score < cands[j].score
		}
		return cands[i].ip < cands[j].ip
	})
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.ip)
	}
	return out
}
