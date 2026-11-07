package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
type davProxy struct{ s *Service }

func (p davProxy) fs() *dav.FS {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.davFSLocked()
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

// davFSLocked builds (or reuses) the WebDAV file system. Caller holds s.mu.
func (s *Service) davFSLocked() *dav.FS {
	if s.davFS != nil {
		return s.davFS
	}
	c, err := s.clientLocked()
	if err != nil {
		return nil
	}
	partMB := s.cfg.UploadPartMB
	if partMB <= 0 {
		partMB = 100
	}
	s.davFS = dav.New(c, dav.Options{
		StageDir: s.cfg.StageDir,
		PartSize: int64(partMB) << 20,
		ReadOnly: s.cfg.ReadOnly,
		Logf:     s.logf,
	})
	return s.davFS
}

// davConf 是处理一个 WebDAV 请求需要的全部配置快照。
type davConf struct {
	user, pass       string
	redirect, ro, ok bool
}

// davReady reports whether the /dav/ endpoint should answer at all.
func (s *Service) davReady() davConf {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.DAVEnabled || s.cfg.DAVPassword == "" {
		return davConf{}
	}
	if s.client == nil && s.cfg.Authorization == "" {
		return davConf{}
	}
	return davConf{
		user:     s.cfg.DAVUser,
		pass:     s.cfg.DAVPassword,
		redirect: s.cfg.DAVRedirectOn(),
		ro:       s.cfg.ReadOnly,
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

// davHandler 是挂在 /dav/ 上的完整入口：BasicAuth → （可选）GET 302 到 CDN
// → 其余交给 webdav.Handler。
func (s *Service) davHandler() http.Handler {
	h := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: davProxy{s: s},
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
		dc := s.davReady()
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
			if f := (davProxy{s: s}).fs(); f != nil {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				e, err := f.Resolve(ctx, strings.TrimPrefix(r.URL.Path, "/dav"))
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
		if r.URL.Path == "/dav" {
			http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
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
func newDAVPassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败时退回时间戳，别让开关打不开。
		return fmt.Sprintf("cmcc%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DAVInfo is what the console page shows: connection details plus a couple of
// ready-to-paste examples.
func (s *Service) DAVInfo() map[string]any {
	cfg := s.Config()
	urls := make([]string, 0, 4)
	for _, host := range localIPv4() {
		urls = append(urls, fmt.Sprintf("http://%s:%d/dav/", host, cfg.WebPort))
	}
	if len(urls) == 0 {
		urls = append(urls, fmt.Sprintf("http://127.0.0.1:%d/dav/", cfg.WebPort))
	}
	return map[string]any{
		"enabled":      cfg.DAVEnabled,
		"user":         cfg.DAVUser,
		"password":     cfg.DAVPassword,
		"password_set": cfg.DAVPassword != "",
		"port":         cfg.WebPort,
		"urls":         urls,
		"read_only":    cfg.ReadOnly,
		"redirect":     cfg.DAVRedirectOn(),
		"rclone": fmt.Sprintf(
			"rclone mount :webdav: /mnt/cmccyun --webdav-url %s --webdav-vendor other --webdav-user %s --webdav-pass $(rclone obscure '%s')",
			urls[0], cfg.DAVUser, cfg.DAVPassword),
	}
}

// SetDAV applies a patch from the console: enable/disable, rename, reset the
// password, toggle redirect. Enabling without a password generates one.
func (s *Service) SetDAV(patch map[string]any) (map[string]any, error) {
	s.mu.Lock()
	if v, ok := patch["enabled"].(bool); ok {
		s.cfg.DAVEnabled = v
		if v && s.cfg.DAVPassword == "" {
			s.cfg.DAVPassword = newDAVPassword()
			s.logger.Printf("WebDAV：已生成访问密码（在控制台「远程挂载 (WebDAV)」里复制）")
		}
	}
	if v, ok := patch["user"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			v = config.DefaultDAVUser
		}
		s.cfg.DAVUser = v
	}
	if v, ok := patch["password"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			s.cfg.DAVPassword = newDAVPassword()
		} else {
			if len(v) < 4 {
				s.mu.Unlock()
				return nil, fmt.Errorf("密码至少 4 位")
			}
			s.cfg.DAVPassword = v
		}
	}
	if v, ok := patch["regenerate"].(bool); ok && v {
		s.cfg.DAVPassword = newDAVPassword()
	}
	if v, ok := patch["redirect"].(bool); ok {
		vv := v
		s.cfg.DAVRedirect = &vv
	}
	// 只读是 FUSE 和 WebDAV 共用的开关，改了就重建 FS，让新连接立刻生效。
	if _, ok := patch["read_only"].(bool); ok {
		s.davFS = nil
	}
	cfgPath := s.cfgPath
	cfg := *s.cfg
	s.mu.Unlock()

	if err := config.Save(cfgPath, &cfg); err != nil {
		return nil, err
	}
	return s.DAVInfo(), nil
}

// registerDAVRoutes wires the console API for this page.
func (s *Service) registerDAVRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/dav/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "dav": s.DAVInfo()})
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
		info, err := s.SetDAV(patch)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		// 开关状态变化后要让页面上的「已启用」徽标立刻对：配置已经在
		// SetDAV 里写盘，这里只需要回最新的 info。
		writeJSON(w, map[string]any{"ok": true, "dav": info, "config": s.MaskPtr()})
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
