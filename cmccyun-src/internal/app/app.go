// Package app wires the 139 client, the FUSE file system and the web console
// together into one long running service.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/vfs"
)

// Version is stamped by the build.
var Version = "dev"

// Service owns the runtime state.
//
// 锁纪律（务必遵守，v1.2.1 踩过一次：Update→validateMountPath→curMountPoint
// 自己锁死自己，表现是控制台「挂载」永远转圈、所有接口一起卡住）：
//
//   - 名字带 Locked 后缀的方法：调用者必须已经持有 s.mu，且只能调用同样带
//     Locked 后缀的方法。
//   - 不带后缀的方法：自己负责取锁，绝不能在持锁路径里调用。
//
// 换言之：持锁的代码只许碰 *Locked，不持锁的代码只许碰不带后缀的。
type Service struct {
	mu       sync.Mutex
	cfgPath  string
	cfg      *config.Config
	client   *api.Client
	server   *fuse.Server
	mounting bool
	lastErr  string
	// mountedAt 是「实际挂着的那里」。挂载点可以随时改，配置里的是目标，
	// 这个才是现状；卸载和自救清理都以它为准。
	mountedAt string
	logs      *Ring
	logger    *log.Logger
}

// New loads the configuration and prepares logging.
func New(cfgPath string) (*Service, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	applyEnv(cfg)
	if cfg.StageDir == "" {
		cfg.StageDir = filepath.Join(filepath.Dir(cfgPath), "staging")
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(filepath.Dir(cfgPath), "cmccyun.log")
	}

	ring := NewRing(500)
	var writers []interface{ Write([]byte) (int, error) }
	writers = append(writers, os.Stdout)
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err == nil {
		if f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			writers = append(writers, f)
		}
	}
	ring.Attach(writers)

	s := &Service{
		cfgPath: cfgPath,
		cfg:     cfg,
		logs:    ring,
		logger:  log.New(ring, "", log.LstdFlags),
	}
	if err := os.MkdirAll(cfg.StageDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	s.mountedAt = s.loadMountedAt()
	return s, nil
}

// mountStatePath 记录实际挂载点的文件，放在配置目录里（随应用一起被卸载清理）。
func (s *Service) mountStatePath() string {
	return filepath.Join(filepath.Dir(s.cfgPath), "mounted_at")
}

func (s *Service) loadMountedAt() string {
	b, err := os.ReadFile(s.mountStatePath())
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if v == "" || !filepath.IsAbs(v) {
		return ""
	}
	return v
}

func (s *Service) saveMountedAt(p string) {
	s.mountedAt = p
	if p == "" {
		_ = os.Remove(s.mountStatePath())
		return
	}
	if err := os.WriteFile(s.mountStatePath(), []byte(p+"\n"), 0o644); err != nil {
		s.logf("记录挂载点失败: %v", err)
	}
}

// strayMountLocked 返回「上一次运行留下的、且已经不是当前配置目标」的挂载点。
// 只在它确实还是一个 cmccyun FUSE 挂载时才返回，避免误伤别的应用。
// 只读 s.mountedAt，因此要求调用者已持有 s.mu（见 Service 的锁纪律）。
func (s *Service) strayMountLocked(target string) string {
	p := s.mountedAt
	if p == "" {
		return ""
	}
	if strings.TrimSuffix(p, "/") == strings.TrimSuffix(target, "/") {
		return ""
	}
	if !vfs.IsMountPoint(p) {
		return ""
	}
	for _, r := range mountTable() {
		if strings.TrimSuffix(r.target, "/") == strings.TrimSuffix(p, "/") && isOurFS(r.fstype) {
			return p
		}
	}
	return ""
}

// applyEnv lets the fnOS package (or any operator) steer a few settings through
// the environment without editing config.json. Values in the config file win,
// except for CMCCYUN_MOUNT which only fills an empty mount point.
func applyEnv(cfg *config.Config) {
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_MOUNT")); v != "" && cfg.MountPoint == "" {
		cfg.MountPoint = v
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_STAGE")); v != "" {
		cfg.StageDir = v
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_LOG")); v != "" {
		cfg.LogFile = v
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WebPort = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_LISTEN")); v != "" {
		cfg.WebListen = v
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_AUTH")); v != "" && cfg.Authorization == "" {
		cfg.Authorization = v
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_READONLY")); v != "" {
		cfg.ReadOnly = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "ro")
	}
}

// Logf implements the vfs logging hook.
func (s *Service) Logf(format string, args ...any) { s.logger.Printf(format, args...) }

// Ring exposes the log buffer to the web console.
func (s *Service) Ring() *Ring { return s.logs }

// Config returns a copy-safe snapshot.
func (s *Service) Config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.cfg
}

// MaskPtr returns the JSON-safe view of the configuration.
func (s *Service) MaskPtr() map[string]any {
	c := s.Config()
	return c.Mask()
}

// ConfigPath returns the on-disk config location.
func (s *Service) ConfigPath() string { return s.cfgPath }

// Status is what the console polls.
type Status struct {
	Version    string        `json:"version"`
	Account    string        `json:"account"`
	HasToken   bool          `json:"has_token"`
	Mounted    bool          `json:"mounted"`
	MountPoint string        `json:"mount_point"`
	ReadOnly   bool          `json:"read_only"`
	LastError  string        `json:"last_error"`
	Auth       api.AuthState `json:"auth"`
	StageBytes int64         `json:"stage_bytes"`
	StageFiles int           `json:"stage_files"`
	// StrayMount：上一次运行留下的、还挂着的旧位置（配置已指向别处）。
	// 控制台据此提示用户清理，不然云盘会同时在两个地方挂着。
	StrayMount string `json:"stray_mount,omitempty"`
	// DeadMount：配置里的挂载点上有残留挂载但已经失去响应（ENOTCONN），
	// 实际不可用，因此按「未挂载」上报，让控制台提示用户重新挂载。
	DeadMount bool `json:"dead_mount,omitempty"`
}

// Status snapshots the service state.
func (s *Service) Status() Status {
	s.mu.Lock()
	mounted := s.server != nil
	mp := s.cfg.MountPoint
	stage := s.cfg.StageDir
	if mounted && mp != "" && !vfs.IsMountPoint(mp) {
		mounted = false
	}
	acct := ""
	hasToken := s.cfg.Authorization != ""
	if s.client != nil {
		acct = s.client.Account()
	}
	if acct == "" {
		acct = s.cfg.Account
	}
	lastErr := s.lastErr
	st := Status{
		Version:    Version,
		Account:    acct,
		HasToken:   hasToken,
		Mounted:    mounted,
		MountPoint: mp,
		ReadOnly:   s.cfg.ReadOnly,
		LastError:  lastErr,
	}
	if s.client != nil {
		st.Auth = s.client.AuthState()
	} else {
		st.Auth = api.AuthState{Account: acct, HasToken: hasToken, Configured: hasToken}
	}
	st.StrayMount = s.strayMountLocked(mp)
	s.mu.Unlock()

	// 死挂载的探测会访问挂载点本身（getattr），所以放在放锁之后：
	// 持锁做文件系统 I/O 正是当初控制台卡死的根源。
	if st.Mounted && vfs.Dead(st.MountPoint) {
		st.DeadMount = true
		st.Mounted = false
	}

	st.StageBytes, st.StageFiles = stageUsage(stage)
	return st
}

// stageUsage reports how much local scratch space the write path currently
// holds. With the overlay write model this is the written ranges only, not the
// whole file, so the number stays small for small edits.
func stageUsage(dir string) (int64, int) {
	if dir == "" {
		return 0, 0
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	var total int64
	n := 0
	for _, e := range ents {
		if len(ents) > 5000 {
			break
		}
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		total += info.Size()
		n++
	}
	return total, n
}

// Update applies a settings patch coming from the web console.
func (s *Service) Update(patch map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, ok := patch["authorization"].(string); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			v = strings.TrimPrefix(v, "Basic ")
			v = strings.TrimSpace(v)
			c, err := api.New(v)
			if err != nil {
				return err
			}
			s.cfg.Authorization = v
			if acct := c.Account(); acct != "" {
				s.cfg.Account = acct
			}
			if s.client != nil {
				s.client.SetAuthorization(v)
				s.client.SetClientType(s.cfg.ClientType)
			}
		}
	}
	if v, ok := patch["mount_point"].(string); ok {
		v = strings.TrimSpace(v)
		// 本方法整段持锁（见方法开头的 Lock），所以只能用 ...Locked 版本。
		if err := s.validateMountPathLocked(v); err != nil {
			return err
		}
		s.cfg.MountPoint = v
	}
	if v, ok := patch["allow_non_empty"].(bool); ok {
		s.cfg.AllowNonEmpty = v
	}
	if v, ok := patch["read_only"].(bool); ok {
		s.cfg.ReadOnly = v
	}
	if v, ok := patch["auto_mount"].(bool); ok {
		s.cfg.AutoMount = v
	}
	if v, ok := num(patch["uid"]); ok {
		s.cfg.UID = uint32(v)
	}
	if v, ok := num(patch["gid"]); ok {
		s.cfg.GID = uint32(v)
	}
	if v, ok := num(patch["dir_cache_ttl"]); ok && v > 0 {
		s.cfg.DirCacheTTL = int(v)
	}
	if v, ok := num(patch["upload_part_mb"]); ok && v > 0 {
		if v > 5120 {
			v = 5120
		}
		s.cfg.UploadPartMB = int(v)
	}
	if v, ok := num(patch["capacity_gb"]); ok && v > 0 {
		s.cfg.CapacityGB = int(v)
	}
	if v, ok := patch["file_perm"].(string); ok && v != "" {
		s.cfg.FilePerm = v
	}
	if v, ok := patch["dir_perm"].(string); ok && v != "" {
		s.cfg.DirPerm = v
	}
	if v, ok := patch["client_type"].(string); ok && v != "" {
		s.cfg.ClientType = v
	}
	if v, ok := patch["stage_dir"].(string); ok && v != "" {
		s.cfg.StageDir = v
	}
	if v, ok := patch["log_file"].(string); ok && v != "" {
		s.cfg.LogFile = v
	}
	if v, ok := num(patch["web_port"]); ok && v > 0 && int(v) != s.cfg.WebPort {
		return fmt.Errorf("修改 Web 端口需要重启应用后生效（当前 %d）", s.cfg.WebPort)
	}
	return config.Save(s.cfgPath, s.cfg)
}

func num(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int:
		return int64(t), true
	case int64:
		return t, true
	}
	return 0, false
}

// clientLocked builds (or reuses) the API client.
func (s *Service) clientLocked() (*api.Client, error) {
	if s.client != nil {
		return s.client, nil
	}
	if s.cfg.Authorization == "" {
		return nil, api.ErrNoAuth
	}
	c, err := s.newClient(s.cfg.Authorization)
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

// setLastErr records an error for the console.
func (s *Service) setLastErr(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

// connect returns a client whose credential is valid and whose regional host is
// known. The round trips (token check, route lookup, possibly a forced renewal)
// run with NO service lock held, exactly like remoteList: a slow or unreachable
// cloud must not freeze /api/status or the file system behind it.
func (s *Service) connect(ctx context.Context) (*api.Client, error) {
	s.mu.Lock()
	c, err := s.clientLocked()
	s.mu.Unlock()
	if err != nil {
		s.setLastErr(err.Error())
		return nil, err
	}
	if err := c.Init(ctx); err != nil {
		s.setLastErr("连接移动云盘失败: " + err.Error())
		return nil, fmt.Errorf("连接移动云盘失败: %w", err)
	}
	return c, nil
}

// newClient builds a client with the service's callbacks and the optional
// endpoint overrides (CMCCYUN_ROUTE_BASE / CMCCYUN_REFRESH_URL are handy behind
// a proxy, and they let the test harness point the real daemon at a stub).
func (s *Service) newClient(authorization string) (*api.Client, error) {
	c, err := api.New(authorization)
	if err != nil {
		return nil, err
	}
	c.SetClientType(s.cfg.ClientType)
	// The callback fires from inside a client call, which may already hold
	// s.mu (Mount → Init). Run it in its own goroutine: a callback into the
	// service must never take a lock its caller may hold, or the first forced
	// renewal deadlocks the daemon.
	c.SetRefreshFunc(func(auth string) { go s.persistAuthorization(auth) })
	c.SetRecoverFunc(func(msg string) { s.logf("[鉴权恢复] %s", msg) })
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_ROUTE_BASE")); v != "" {
		c.SetRouteBase(v)
	}
	if v := strings.TrimSpace(os.Getenv("CMCCYUN_REFRESH_URL")); v != "" {
		c.SetRefreshURL(v)
	}
	return c, nil
}

// persistAuthorization writes a token that the client refreshed back to the
// config file, so a restart does not resurrect the expired credential.
func (s *Service) persistAuthorization(auth string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if auth == "" || auth == s.cfg.Authorization {
		return
	}
	s.cfg.Authorization = auth
	if s.client != nil {
		s.cfg.Account = s.client.Account()
	}
	if err := config.Save(s.cfgPath, s.cfg); err != nil {
		s.lastErr = "保存刷新后的授权失败: " + err.Error()
		return
	}
	s.logger.Printf("授权令牌已刷新并写入配置（新有效期见控制台）")
}

// AuthLoop keeps a long-running mount's credential alive: the ticket the user
// pastes is good for roughly 30 days, and only the app can renew it after that.
// Borrowed from the OpenList 139 driver, which refreshes on demand and falls
// back to a full login when the refresh is rejected.
func (s *Service) AuthLoop(ctx context.Context) {
	tick := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	for {
		_ = s.refreshAuth(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// refreshAuth performs one credential check and records the outcome.
// refreshAuth renews the credential. force skips the "still fresh" shortcut so
// the console button always talks to the service; the background loop passes
// false and lets EnsureFresh decide (it only renews inside the 15-day window).
func (s *Service) refreshAuth(ctx context.Context, force bool) error {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c == nil {
		return api.ErrNoAuth
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var err error
	if force {
		err = c.RefreshToken(cctx)
	} else {
		err = c.EnsureFresh(cctx)
	}
	if err != nil {
		s.logger.Printf("授权刷新失败: %v", err)
		s.mu.Lock()
		s.lastErr = err.Error()
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	if s.lastErr != "" && strings.Contains(s.lastErr, "令牌") {
		s.lastErr = ""
	}
	s.mu.Unlock()
	return nil
}

// Mount attaches the cloud drive at the configured mount point.
func (s *Service) Mount(ctx context.Context) error {
	// Talk to the cloud before taking the lock (skipped when something is
	// already mounted, or in debug mode which uses a local directory).
	var c *api.Client
	if os.Getenv("CMCCYUN_DEBUG_FAKE") == "" {
		s.mu.Lock()
		// 注意 vfs.Dead：上一轮被强杀会留下一个读不了的残留挂载（ENOTCONN），
		// 那种情况必须重新连接并挂载，不能当成「已经挂好了」跳过。
		busy := s.server != nil ||
			(s.cfg.MountPoint != "" && vfs.IsMountPoint(s.cfg.MountPoint) && !vfs.Dead(s.cfg.MountPoint))
		s.mu.Unlock()
		if !busy {
			var err error
			if c, err = s.connect(ctx); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mountLocked(ctx, c)
}

// mountLocked performs the local part of attaching the file system. c is a
// connected client (nil only in debug mode, where no cloud is involved).
func (s *Service) mountLocked(ctx context.Context, c *api.Client) error {
	if s.server != nil {
		return nil
	}
	mp := s.cfg.MountPoint
	if mp == "" {
		return fmt.Errorf("尚未设置挂载目录")
	}
	if !filepath.IsAbs(mp) {
		return fmt.Errorf("挂载目录必须是绝对路径: %s", mp)
	}
	if vfs.IsMountPoint(mp) {
		if vfs.Dead(mp) {
			// 上一轮进程被强杀/掉电留下的残留：读不了，但仍在 /proc/mounts 里。
			// 直接认成「已挂载」会让用户在「文件」里看到一个空壳，所以先清掉。
			s.logf("检测到 %s 上是失去响应的旧挂载（ENOTCONN），先清理再重新挂载", mp)
			if err := vfs.Unmount(mp); err != nil {
				s.logf("清理残留挂载失败: %v", err)
			} else {
				s.saveMountedAt("")
			}
		}
		if vfs.IsMountPoint(mp) && !vfs.Dead(mp) {
			s.lastErr = ""
			s.saveMountedAt(mp)
			s.logf("检测到 %s 已存在挂载，跳过", mp)
			return nil
		}
	}
	// 挂到有内容的目录上会把原有文件「遮住」（数据还在，只是看不见），
	// 除非用户在选择位置时明确确认过，否则拒绝，避免误操作。
	if s.cfg.MountPoint != "" {
		if n, err := dirCount(mp); err == nil && n > 0 && !s.cfg.AllowNonEmpty {
			s.lastErr = fmt.Sprintf("挂载目录非空：%s", mp)
			return fmt.Errorf("挂载目录 %s 里已有 %d 项内容，挂载会遮住它们。若确定要挂在这里，请在管理台重新选择位置并勾选确认", mp, n)
		}
	}
	// 上一次运行在别处留下的挂载（挂载点改过、或进程被强杀）：先卸掉再挂新的，
	// 否则云盘会同时在两个位置挂着，配置里那个是新的、另一个成了僵尸。
	if stray := s.strayMountLocked(mp); stray != "" {
		s.logf("清理旧的挂载点 %s（当前挂载位置已改为 %s）", stray, mp)
		if err := vfs.Unmount(stray); err != nil {
			s.logf("卸载旧挂载点 %s 失败: %v（继续挂到 %s）", stray, err, mp)
		} else {
			s.saveMountedAt("")
		}
	}

	if err := ensureMountDir(mp); err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("创建挂载目录失败: %w", err)
	}

	// Self-test mode: the file system is exercised against a local directory
	// instead of the cloud, which lets the mount layer be validated without a
	// real account.
	if debug := os.Getenv("CMCCYUN_DEBUG_FAKE"); debug != "" {
		fb, ferr := vfs.NewFakeBackend(debug)
		if ferr != nil {
			s.lastErr = ferr.Error()
			return fmt.Errorf("调试伪后端初始化失败: %w", ferr)
		}
		opts := &vfs.Options{
			Backend:     fb,
			StageDir:    s.cfg.StageDir,
			DirCacheTTL: time.Duration(s.cfg.DirCacheTTL) * time.Second,
			ReadOnly:    s.cfg.ReadOnly,
			Capacity:    uint64(s.cfg.CapacityGB) << 30,
			Logf:        s.Logf,
			Owner: vfs.Owner{
				UID:      s.cfg.UID,
				GID:      s.cfg.GID,
				FilePerm: config.ParseMode(s.cfg.FilePerm, 0o644),
				DirPerm:  config.ParseMode(s.cfg.DirPerm, 0o755),
			},
		}
		srv, err := vfs.Mount(mp, opts)
		if err != nil {
			s.lastErr = err.Error()
			return fmt.Errorf("挂载失败: %w", err)
		}
		s.server = srv
		s.lastErr = ""
		s.saveMountedAt(mp)
		s.logf("调试模式：已把本地目录 %s 挂载到 %s", debug, mp)
		return nil
	}

	if c == nil {
		// Mount/Remount always connect() first; reaching this means a bug.
		s.lastErr = "内部错误：未连接云盘即尝试挂载"
		return fmt.Errorf("%s", s.lastErr)
	}
	s.cfg.Account = c.Account()
	_ = config.Save(s.cfgPath, s.cfg)

	opts := &vfs.Options{
		Backend:     c,
		StageDir:    s.cfg.StageDir,
		DirCacheTTL: time.Duration(s.cfg.DirCacheTTL) * time.Second,
		ReadOnly:    s.cfg.ReadOnly,
		Capacity:    uint64(s.cfg.CapacityGB) << 30,
		Logf:        s.Logf,
		Owner: vfs.Owner{
			UID:      s.cfg.UID,
			GID:      s.cfg.GID,
			FilePerm: config.ParseMode(s.cfg.FilePerm, 0o644),
			DirPerm:  config.ParseMode(s.cfg.DirPerm, 0o755),
		},
	}

	srv, err := vfs.Mount(mp, opts)
	if err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("挂载失败: %w", err)
	}
	s.server = srv
	s.lastErr = ""
	s.saveMountedAt(mp)
	s.logf("已挂载 %s（账号 %s，%s）", mp, c.Account(), modeLabel(s.cfg.ReadOnly))
	return nil
}

func modeLabel(ro bool) string {
	if ro {
		return "只读"
	}
	return "读写"
}

// Unmount detaches the file system.
func (s *Service) Unmount() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unmountLocked()
}

// curMountPoint 返回「现在实际挂着的那里」（可能为空）。调用者不要持有 mu。
func (s *Service) curMountPoint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.curMountPointLocked()
}

// curMountPointLocked: 优先运行时记录的 mountedAt（配置里的位置只是目标，
// 切换过程中两者会短暂不一致），其次看配置位置是否真的是挂载点。
func (s *Service) curMountPointLocked() string {
	if s.mountedAt != "" {
		return s.mountedAt
	}
	mp := filepath.Clean(s.cfg.MountPoint)
	if mp != "" && mp != "." && vfs.IsMountPoint(mp) {
		return mp
	}
	return ""
}

func (s *Service) unmountLocked() error {
	srv := s.server
	s.server = nil
	// 卸载一定按「实际挂着的那里」来：切换挂载点时配置已经指向新位置，
	// 只按配置卸会卸错地方，把旧挂载漏成僵尸挂载。
	mp := s.curMountPointLocked()
	if mp != "" && vfs.IsMountPoint(mp) {
		if err := vfs.Unmount(mp); err != nil {
			s.lastErr = err.Error()
			return err
		}
		s.saveMountedAt("")
		s.logf("已卸载 %s", mp)
		return nil
	}
	if srv != nil {
		if err := srv.Unmount(); err != nil {
			s.lastErr = err.Error()
			return err
		}
		s.saveMountedAt("")
		s.logf("已卸载 %s", s.cfg.MountPoint)
	}
	return nil
}

// Remount applies the current configuration.
func (s *Service) Remount(ctx context.Context) error {
	// Phase 1 — local and fast: pick up outside changes (settings page, CLI,
	// fnOS wizard) and drop the old mount.
	s.mu.Lock()
	// Pick up changes made from the outside (settings page, CLI, fnOS wizard)
	// before tearing the old mount down.
	if c, err := config.Load(s.cfgPath); err == nil {
		applyEnv(c)
		if c.StageDir == "" {
			c.StageDir = filepath.Join(filepath.Dir(s.cfgPath), "staging")
		}
		if c.LogFile == "" {
			c.LogFile = filepath.Join(filepath.Dir(s.cfgPath), "cmccyun.log")
		}
		*s.cfg = *c
		s.logf("已重新读取配置 %s", s.cfgPath)
	}
	if err := s.unmountLocked(); err != nil {
		s.logf("卸载失败（继续尝试挂载）: %v", err)
	}
	s.client = nil
	s.mu.Unlock()

	// Phase 2 — network, lock released: reconnect with the new credential.
	var c *api.Client
	if os.Getenv("CMCCYUN_DEBUG_FAKE") == "" {
		var err error
		if c, err = s.connect(ctx); err != nil {
			return err
		}
	}

	// Phase 3 — local: attach again.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mountLocked(ctx, c)
}

func (s *Service) logf(format string, args ...any) {
	s.logger.Printf(format, args...)
}
