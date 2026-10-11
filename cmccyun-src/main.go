// Command cmccyun mounts 中国移动云盘 (China Mobile Cloud Drive, a.k.a. 139 云盘)
// as a local file system and serves a small management console.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	userpkg "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/app"
	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/vfs"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("c", defaultConfigPath(), "配置文件路径")
	fs.Parse(os.Args[2:])

	switch cmd {
	case "run":
		os.Exit(run(*cfgPath))
	case "mount":
		os.Exit(mountAndWait(*cfgPath))
	case "unmount":
		os.Exit(withService(*cfgPath, func(s *app.Service) error { return s.Unmount() }))
	case "path":
		os.Exit(withService(*cfgPath, func(s *app.Service) error {
			fmt.Println(s.Config().MountPoint)
			return nil
		}))
	case "status":
		os.Exit(withService(*cfgPath, func(s *app.Service) error {
			st := s.Status()
			fmt.Printf("版本: %s\n账号: %s\n授权: %v\n挂载: %v\n挂载点: %s\n错误: %s\n",
				st.Version, st.Account, st.HasToken, st.Mounted, st.MountPoint, st.LastError)
			return nil
		}))
	case "token":
		os.Exit(withService(*cfgPath, func(s *app.Service) error {
			v := fs.Arg(0)
			if v == "" {
				return fmt.Errorf("用法: cmccyun token <Authorization>")
			}
			return s.Update(map[string]any{"authorization": v})
		}))
	case "version":
		fmt.Println(app.Version)
		return
	case "fnos-init":
		os.Exit(fnosInit(*cfgPath))
	case "prepare":
		// Root-side preparation used by the fnOS lifecycle script: make sure
		// every directory the daemon needs exists and is owned by the user the
		// daemon will run as.
		user := ""
		if len(fs.Args()) > 0 {
			user = fs.Args()[0]
		}
		os.Exit(prepare(*cfgPath, user))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `移动云盘挂载 %s

用法:
  cmccyun run      [-c 配置]  启动服务（挂载 + 管理界面）
  cmccyun mount    [-c 配置]  仅执行挂载
  cmccyun unmount  [-c 配置]  卸载
  cmccyun status   [-c 配置]  查看状态
  cmccyun path     [-c 配置]  输出挂载点路径
  cmccyun token    [-c 配置] <Authorization>  写入授权并保存
  cmccyun fnos-init [-c 配置] 由 fnOS 安装/设置向导调用，套用 wizard_* 环境变量
  cmccyun prepare  [-c 配置] [用户名]  准备目录并按需切换属主（root 调用）
  cmccyun version

若 -c 未指定，则使用 $CMCCYUN_ETC/config.json，其次 /etc/cmccyun/config.json。
`, app.Version)
}

// fnosInit translates the environment variables that the fnOS installer and
// settings page export (TRIM_*, wizard_*) into config.json. It is idempotent:
// values the user left empty keep whatever is already configured.
func fnosInit(cfgPath string) int {
	s, err := app.New(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return 1
	}

	patch := map[string]any{}
	varRoot := strings.TrimSpace(os.Getenv("TRIM_PKGVAR"))

	if varRoot != "" {
		patch["stage_dir"] = filepath.Join(varRoot, "staging")
		patch["log_file"] = filepath.Join(varRoot, "cmccyun.log")
	}

	// Mount point: an explicit absolute path wins, otherwise mount inside the
	// shared folder that config/resource created for this app. An empty mount
	// point is only filled on the first run, so upgrading or opening the
	// settings page never clobbers a custom path.
	sub := sanitizeName(os.Getenv("wizard_mount_subdir"))
	explicit := strings.TrimSpace(os.Getenv("wizard_mount_point"))
	share := firstShare()
	switch {
	case explicit != "":
		patch["mount_point"] = explicit
	case sub != "" && share != "":
		patch["mount_point"] = filepath.Join(share, sub)
	case s.Config().MountPoint == "" && share != "":
		patch["mount_point"] = filepath.Join(share, "移动云盘")
	case s.Config().MountPoint == "" && varRoot != "":
		patch["mount_point"] = filepath.Join(varRoot, "mnt")
	}

	if v := strings.TrimSpace(os.Getenv("wizard_authorization")); v != "" {
		patch["authorization"] = v
	}
	if v := strings.TrimSpace(os.Getenv("wizard_readonly")); v != "" {
		patch["read_only"] = v == "ro" || v == "readonly" || v == "true" || v == "1"
	}
	if v := strings.TrimSpace(os.Getenv("TRIM_SERVICE_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			patch["web_port"] = float64(n)
		}
	}

	if err := s.Update(patch); err != nil {
		fmt.Fprintln(os.Stderr, "写入配置失败:", err)
		if lf := os.Getenv("TRIM_TEMP_LOGFILE"); lf != "" {
			fmt.Fprintf(os.Stderr, "请检查 %s 是否可写\n", cfgPath)
		}
		return 1
	}

	cfg := s.Config()
	fmt.Printf("配置已保存到 %s\n", s.ConfigPath())
	fmt.Printf("挂载点: %s（只读: %v）\n", cfg.MountPoint, cfg.ReadOnly)
	fmt.Printf("暂存目录: %s\n", cfg.StageDir)

	if cfg.MountPoint != "" {
		if err := ensureMountDir(cfg.MountPoint); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

// ensureMountDir creates the mount point directory, but never the shared folder
// it lives in: those are managed by fnOS (ACLs and all) and an app that creates
// one by hand would leave it without the right permissions.
//
// 新建的目录属主跟随父目录：挂载点在用户自己的个人空间（/volN/<uid>）里时，
// 这条目录在挂载失效时会露出来，属主是用户本人才符合直觉。
func ensureMountDir(mp string) error {
	parent := filepath.Dir(strings.TrimSuffix(mp, "/"))
	pinfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("上级目录 %s 不存在：请在应用设置或管理界面里修改挂载路径", parent)
	}
	if _, err := os.Lstat(mp); err == nil {
		return nil // 已存在，不动它的属主
	}
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return fmt.Errorf("无法创建挂载点 %s: %w", mp, err)
	}
	if os.Geteuid() == 0 {
		if st, ok := pinfo.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(mp, int(st.Uid), int(st.Gid))
		}
	}
	return nil
}

// firstShare returns the first path declared through config/resource
// (data-share), as exported by fnOS in TRIM_DATA_SHARE_PATHS.
func firstShare() string {
	for _, p := range strings.Split(os.Getenv("TRIM_DATA_SHARE_PATHS"), ":") {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}
	return ""
}

// sanitizeName keeps a wizard-provided directory name to a single safe path
// element, so the mount point can never escape the shared folder.
func sanitizeName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "." || v == ".." || strings.ContainsAny(v, `/\`) {
		return ""
	}
	return v
}

// prepare is the root-side half of the fnOS start sequence: it materialises the
// directories the daemon needs and hands them to the user the daemon runs as.
func prepare(cfgPath, user string) int {
	s, err := app.New(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return 1
	}
	// Persist any value that was supplied through the environment.
	if err := s.Update(map[string]any{}); err != nil {
		fmt.Fprintln(os.Stderr, "写入配置失败:", err)
		return 1
	}
	cfg := s.Config()
	dirs := []string{cfg.StageDir}
	if cfg.LogFile != "" {
		dirs = append(dirs, filepath.Dir(cfg.LogFile))
	}
	if cfg.MountPoint != "" {
		if err := ensureMountDir(cfg.MountPoint); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		dirs = append(dirs, cfg.MountPoint)
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "创建目录 %s 失败: %v\n", d, err)
			return 1
		}
	}
	if user == "" {
		return 0
	}
	u, err := userpkg.Lookup(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "找不到用户 %s: %v\n", user, err)
		return 1
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	targets := append([]string{cfgPath}, dirs...)
	if cfg.LogFile != "" {
		targets = append(targets, cfg.LogFile)
	}
	for _, t := range targets {
		if t == "" {
			continue
		}
		if err := walkChown(t, uid, gid); err != nil {
			fmt.Fprintf(os.Stderr, "设置 %s 属主失败: %v\n", t, err)
			return 1
		}
	}
	fmt.Printf("准备完成：挂载点 %s，运行用户 %s(%d:%d)\n", cfg.MountPoint, user, uid, gid)
	return 0
}

func walkChown(path string, uid, gid int) error {
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != path {
			// Keep it shallow: descend only one level so a cloud drive mounted
			// underneath is never touched.
			return filepath.SkipDir
		}
		return os.Lchown(p, uid, gid)
	})
}

func defaultConfigPath() string {
	if v := os.Getenv("CMCCYUN_CONFIG"); v != "" {
		return v
	}
	if etc := os.Getenv("CMCCYUN_ETC"); etc != "" {
		return etc + "/config.json"
	}
	if _, err := os.Stat("/etc/cmccyun"); err == nil {
		return "/etc/cmccyun/config.json"
	}
	return "./cmccyun-data/config.json"
}

func withService(cfgPath string, fn func(*app.Service) error) int {
	s, err := app.New(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return 1
	}
	if err := fn(s); err != nil {
		fmt.Fprintln(os.Stderr, "失败:", err)
		return 1
	}
	return 0
}

// mountAndWait mounts the file system and keeps the process (and therefore the
// mount) alive until a signal arrives.
func mountAndWait(cfgPath string) int {
	s, err := app.New(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	if err := s.Mount(ctx); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "失败:", err)
		return 1
	}
	cancel()

	st := s.Status()
	fmt.Printf("已挂载 %s（账号 %s）\n", st.MountPoint, st.Account)

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	s.Logf("收到退出信号，正在卸载")
	if err := s.Unmount(); err != nil {
		fmt.Fprintln(os.Stderr, "卸载失败:", err)
		return 1
	}
	return 0
}

func run(cfgPath string) int {
	s, err := app.New(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		return 1
	}

	// Publish our pid so the fnOS lifecycle script can stop us precisely, even
	// when the process was started through runuser.
	pidPath := os.Getenv("CMCCYUN_PIDFILE")
	if pidPath == "" {
		pidPath = filepath.Join(filepath.Dir(cfgPath), "cmccyun.pid")
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err == nil {
		defer os.Remove(pidPath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Fan out the console first so the user can fix credentials in the browser
	// even when the very first mount fails.
	httpSrv, _, err := s.Listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		sh, cancelSh := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelSh()
		_ = httpSrv.Shutdown(sh)
	}()

	cfg := s.Config()
	// Keep a long-running mount alive: the pasted ticket expires in roughly 30
	// days and the service can renew it silently while it is still valid.
	go s.AuthLoop(ctx)
	if cfg.AutoMount {
		mctx, mcancel := context.WithTimeout(ctx, 120*time.Second)
		if err := s.Mount(mctx); err != nil {
			s.Logf("自动挂载失败: %v", err)
			fmt.Fprintln(os.Stderr, "自动挂载失败（可在管理界面修正后重试）:", err)
		}
		mcancel()
	} else {
		s.Logf("配置为不自动挂载，等待管理界面操作")
	}

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	sigReceived := <-sig
	s.Logf("收到信号 %s，正在退出", sigReceived)

	if err := s.Unmount(); err != nil {
		s.Logf("卸载失败: %v", err)
	}
	return 0
}

var _ = config.Default
var _ = vfs.IsMountPoint
