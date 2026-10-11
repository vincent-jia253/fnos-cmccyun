package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/vfs"
)

// newMountSvc 造一个跑在临时目录里的 Service：tmpRoot 通过 CMCCYUN_EXTRA_ROOTS
// 变成「允许的卷根」，这样位置选择逻辑能在临时目录里被测到。
func newMountSvc(t *testing.T, mountPoint string) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CMCCYUN_EXTRA_ROOTS", root)
	cfgPath := filepath.Join(root, "@appconf", "config.json")
	cfg := config.Default()
	cfg.MountPoint = mountPoint
	cfg.StageDir = filepath.Join(root, "@appdata", "staging")
	cfg.LogFile = filepath.Join(root, "@appdata", "cmccyun.log")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if mountPoint == "" {
		s.cfg.MountPoint = ""
	}
	return s, root
}

func TestValidateMountPathRules(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	s.cfg.MountPoint = filepath.Join(root, "mnt")

	cases := []struct {
		path string
		want string // "" = 应通过
	}{
		{"", ""}, // 清空挂载点
		{"vol4/1000/移动云盘", "绝对路径"}, // 相对路径
		{"/", "根目录"},
		{"/vol1", "存储卷根目录"}, // 卷根：会遮住整个卷
		{"/vol02", "存储卷根目录"},
		{"/etc/passwd", "只能选择飞牛存储卷"},
		{"/mnt/elsewhere", "只能选择飞牛存储卷"},
		{filepath.Join(root, "1000", "移动云盘"), ""},
		// 配置里写了 <root>/mnt，但并没有真的挂上去：这时在它下面选位置是允许的
		// （没有活挂载就不存在「被遮住」的问题）。活挂载的情形见下一个测试。
		{filepath.Join(root, "mnt", "inner"), ""},
	}
	for _, tc := range cases {
		err := s.validateMountPath(tc.path)
		if tc.want == "" {
			if err != nil {
				t.Errorf("validateMountPath(%q) = %v, 期望通过", tc.path, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("validateMountPath(%q) 通过了，期望被拒（%s）", tc.path, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("validateMountPath(%q) = %v, 期望包含 %q", tc.path, err, tc.want)
		}
	}
}

// TestMountPointInsideLiveMount：云盘挂在某个目录上时，它里面的路径「真身」被遮住。
// 这种位置要能用（挂载时会先卸下再建真目录），但必须带 masked 标记，并且不能
// 直接在那儿新建文件夹（否则会建到云盘上）。
func TestMountPointInsideLiveMount(t *testing.T) {
	s, root := newMountSvc(t, "")
	cur := filepath.Join(root, "1000") // 典型场景：云盘挂在了个人空间根
	inner := filepath.Join(cur, "移动云盘")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	s.cfg.MountPoint = cur

	// 没有活挂载（mountedAt 为空且 cur 不是挂载点）：允许，按普通目录处理。
	info, err := s.CheckMountPath(inner, false)
	if err != nil {
		t.Fatalf("没有活挂载时不该拒绝: %v", err)
	}
	if info.Masked {
		t.Fatal("没有活挂载却标记成 masked")
	}

	// 模拟「真的挂着」：mountedAt 就是运行时的依据。
	s.mountedAt = cur

	// 直接改配置到挂载点内部：必须拒绝（会留下僵尸挂载），并指向「挂到这里」。
	err = s.validateMountPath(inner)
	if err == nil || !strings.Contains(err.Error(), "当前挂载点") {
		t.Fatalf("validateMountPath(%q) = %v, 期望拒绝并提示当前挂载点", inner, err)
	}

	// 但 check（只读预览）必须通过并给出 masked 提示：挂载时可以卸了再做。
	info, err = s.CheckMountPath(inner, false)
	if err != nil {
		t.Fatalf("check 不该拒绝挂载点内部的位置: %v", err)
	}
	if !info.Masked {
		t.Fatal("挂载点内部的位置没被标记 masked")
	}
	if len(info.Warnings) == 0 {
		t.Fatal("masked 位置没有给出提示")
	}
	if !info.Empty || info.Exists {
		t.Fatalf("masked 位置不该谎报真实内容: exists=%v empty=%v entries=%d", info.Exists, info.Empty, info.Entries)
	}

	// 在挂载点里面新建文件夹：必须被拒（会写到云盘上或被遮住）。
	if _, err := s.CreateMountDir(cur, "移动云盘"); err == nil {
		t.Fatal("在挂载点里面新建文件夹应被拒")
	} else if !strings.Contains(err.Error(), "云盘") {
		t.Fatalf("拒绝原因没说清: %v", err)
	}
	// 挂载点之外的目录照常能建。
	if p, err := s.CreateMountDir(filepath.Join(root, "2000"), "新目录"); err != nil {
		t.Fatalf("挂载点外新建失败: %v", err)
	} else if _, err := os.Stat(p); err != nil {
		t.Fatalf("目录没建成: %v", err)
	}
}

func TestCheckMountPathRefusesNonEmptyUnlessConfirmed(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	target := filepath.Join(root, "1000", "已有内容")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(target, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.CheckMountPath(target, false); err == nil {
		t.Fatal("非空目录未被拒绝")
	} else if !strings.Contains(err.Error(), "遮住") {
		t.Fatalf("错误信息没说清原因: %v", err)
	}

	info, err := s.CheckMountPath(target, true)
	if err != nil {
		t.Fatalf("勾选确认后仍被拒: %v", err)
	}
	if info.Empty || info.Entries != 2 {
		t.Fatalf("条目统计不对: empty=%v entries=%d", info.Empty, info.Entries)
	}
}

func TestCheckMountPathEmptyAndMissing(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	owner := filepath.Join(root, "1000")
	if err := os.MkdirAll(owner, 0o755); err != nil {
		t.Fatal(err)
	}

	// 空目录：可直接用
	emptyDir := filepath.Join(owner, "空目录")
	if err := os.Mkdir(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := s.CheckMountPath(emptyDir, false)
	if err != nil {
		t.Fatalf("空目录被拒: %v", err)
	}
	if !info.Exists || !info.Empty || info.Entries != 0 {
		t.Fatalf("空目录判定不对: %+v", info)
	}
	if info.Owner != ownerName(uint32(os.Getuid())) {
		t.Fatalf("属主推断不对: %s", info.Owner)
	}

	// 不存在但父目录在：算「可创建」，属主沿用父目录
	missing := filepath.Join(owner, "移动云盘")
	info, err = s.CheckMountPath(missing, false)
	if err != nil {
		t.Fatalf("可创建目录被拒: %v", err)
	}
	if info.Exists {
		t.Fatal("不存在的目录被当成已存在")
	}
	if info.SuggestUID != uint32(os.Getuid()) {
		t.Fatalf("建议属主不对: %d", info.SuggestUID)
	}

	// 父目录都不在：明确报错
	if _, err := s.CheckMountPath(filepath.Join(owner, "a", "b"), false); err == nil {
		t.Fatal("父目录不存在时应报错")
	}
}

func TestCheckMountPathRefusesAppRuntimeDirs(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	cfg := s.Config()
	sub := filepath.Join(cfg.StageDir, "x")
	if _, err := s.CheckMountPath(sub, true); err == nil {
		t.Fatal("允许挂在暂存目录里")
	}
	if _, err := s.CheckMountPath(root, true); err == nil {
		t.Fatal("允许挂在配置目录的上层")
	}
	if _, err := s.CheckMountPath(cfg.LogFile, true); err == nil {
		t.Fatal("允许挂在日志文件上")
	}
}

func TestCreateMountDir(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	parent := filepath.Join(root, "1000")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := s.CreateMountDir(parent, "移动云盘")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(parent, "移动云盘"); got != want {
		t.Fatalf("创建路径 = %s, 期望 %s", got, want)
	}
	if st, err := os.Stat(got); err != nil || !st.IsDir() {
		t.Fatalf("目录没建出来: %v", err)
	}
	if _, err := s.CreateMountDir(parent, "移动云盘"); err == nil {
		t.Fatal("同名目录应被拒")
	}
	for _, bad := range []string{"", "a/b", ".hidden", "@appdata", ".."} {
		if _, err := s.CreateMountDir(parent, bad); err == nil {
			t.Errorf("非法名字 %q 被接受", bad)
		}
	}
	if _, err := s.CreateMountDir("/etc", "x"); err == nil {
		t.Fatal("卷外位置应被拒")
	}
}

func TestBrowseDirs(t *testing.T) {
	s, root := newMountSvc(t, filepath.Join("/tmp", "cmccyun-cfgtest"))
	owner := filepath.Join(root, "1000")
	for _, d := range []string{"1000/游戏", "1000/@appdata", "1000/空目录"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(owner, "游戏", "save.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 根：只列存储卷
	cur, _, entries, err := s.BrowseDirs("")
	if err != nil {
		t.Fatal(err)
	}
	if cur != "/" {
		t.Fatalf("根路径 = %s", cur)
	}
	found := false
	for _, e := range entries {
		if e.Path == root {
			found = true
		}
		if !e.IsVolume {
			t.Fatalf("根层出现了非卷条目: %+v", e)
		}
	}
	if !found {
		t.Fatalf("根层没有列出卷 %s", root)
	}

	// 卷内：系统目录靠后、文件不出现、空目录标记正确
	cur, parent, entries, err := s.BrowseDirs(owner)
	if err != nil {
		t.Fatal(err)
	}
	if cur != owner || parent != root {
		t.Fatalf("cur=%s parent=%s", cur, parent)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		if e.Name == "readme.txt" {
			t.Fatal("文件不该出现在目录选择器里")
		}
	}
	if len(names) != 3 || names[len(names)-1] != "@appdata" {
		t.Fatalf("系统目录应排在最后: %v", names)
	}
	if names[0] == "@appdata" || names[1] == "@appdata" {
		t.Fatalf("排序/过滤不对: %v", names)
	}
	for _, e := range entries {
		switch e.Name {
		case "空目录":
			if !e.Empty || e.System {
				t.Fatalf("空目录标记不对: %+v", e)
			}
		case "游戏":
			if e.Empty {
				t.Fatalf("非空目录被标成空: %+v", e)
			}
		case "@appdata":
			if !e.System {
				t.Fatalf("系统目录没被标记: %+v", e)
			}
		}
	}

	if _, _, _, err := s.BrowseDirs("/etc"); err == nil {
		t.Fatal("卷外浏览应被拒")
	}
}

// TestSetMountPointMovesMount 需要一个可用的 FUSE（真机/CI 设 CMCCYUN_TEST_FUSE=1）。
func TestSetMountPointMovesMount(t *testing.T) {
	if os.Getenv("CMCCYUN_TEST_FUSE") == "" {
		t.Skip("设 CMCCYUN_TEST_FUSE=1 才跑（需要 /dev/fuse）")
	}
	backend := t.TempDir()
	if err := os.WriteFile(filepath.Join(backend, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMCCYUN_DEBUG_FAKE", backend)

	s, root := newMountSvc(t, "")
	old := filepath.Join(root, "1000", "旧位置")
	newDir := filepath.Join(root, "1000", "新位置")
	if err := os.MkdirAll(filepath.Join(root, "1000"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 第一次挂到「旧位置」（走创建流程）
	if err := s.SetMountPoint(context.Background(), old, SetMountPointOptions{Create: true}); err != nil {
		t.Fatalf("首次挂载失败: %v", err)
	}
	defer func() { _ = s.Unmount() }()
	if !vfs.IsMountPoint(old) {
		t.Fatal("旧位置没挂上")
	}
	if got := s.Config().MountPoint; got != old {
		t.Fatalf("配置里的挂载点 = %s, 期望 %s", got, old)
	}

	// 换到「新位置」：旧的要卸掉，新的要挂上并落盘
	if err := s.SetMountPoint(context.Background(), newDir, SetMountPointOptions{Create: true, FollowOwner: true}); err != nil {
		t.Fatalf("切换失败: %v", err)
	}
	if vfs.IsMountPoint(old) {
		t.Fatal("旧位置没有被卸载")
	}
	if !vfs.IsMountPoint(newDir) {
		t.Fatal("新位置没有挂上")
	}
	onDisk, err := config.Load(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.MountPoint != newDir {
		t.Fatalf("磁盘上的挂载点 = %s, 期望 %s", onDisk.MountPoint, newDir)
	}
	if onDisk.UID != uint32(os.Getuid()) {
		t.Fatalf("FollowOwner 没把属主写进配置: %d", onDisk.UID)
	}
	if entries, err := os.ReadDir(newDir); err != nil || len(entries) == 0 {
		t.Fatalf("新位置内容读不到: %v", err)
	}

	// 目标非空且未确认：必须失败，并且仍然挂在原来的「新位置」
	bad := filepath.Join(root, "1000", "有东西")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMountPoint(context.Background(), bad, SetMountPointOptions{Create: true}); err == nil {
		t.Fatal("非空目录未确认也切换成功了")
	}
	if !vfs.IsMountPoint(newDir) {
		t.Fatal("失败的切换把原有挂载弄丢了")
	}
	if got := s.Config().MountPoint; got != newDir {
		t.Fatalf("失败后配置没回滚: %s", got)
	}
	if data, err := os.ReadFile(filepath.Join(bad, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("原有文件受影响: %v %q", err, data)
	}
}

// TestSetMountPointMovesOutOfLiveMount 复现用户踩的坑：先把云盘挂在了个人空间根
// 目录（/vol4/1000），真实文件夹被遮住；再让它挪进子目录 /vol4/1000/移动云盘。
// 需要一个可用的 FUSE（真机/CI 设 CMCCYUN_TEST_FUSE=1）。
func TestSetMountPointMovesOutOfLiveMount(t *testing.T) {
	if os.Getenv("CMCCYUN_TEST_FUSE") == "" {
		t.Skip("设 CMCCYUN_TEST_FUSE=1 才跑（需要 /dev/fuse）")
	}
	backend := t.TempDir()
	if err := os.WriteFile(filepath.Join(backend, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMCCYUN_DEBUG_FAKE", backend)

	s, root := newMountSvc(t, "")
	defer func() { _ = s.Unmount() }()
	space := filepath.Join(root, "1000")
	mine := filepath.Join(space, "我的文件夹")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}

	// 踩坑：挂在个人空间根上（真实文件夹被遮住）
	if err := s.SetMountPoint(context.Background(), space, SetMountPointOptions{Create: true, AllowNonEmpty: true, FollowOwner: true}); err != nil {
		t.Fatalf("挂到个人空间根失败: %v", err)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Fatal("预期真实文件夹被挂载遮住")
	}

	// 修正：挪进它下面的子目录 —— 目标被遮住，必须先卸下再建目录
	inner := filepath.Join(space, "移动云盘")
	if err := s.SetMountPoint(context.Background(), inner, SetMountPointOptions{Create: true, AllowNonEmpty: true, FollowOwner: true}); err != nil {
		t.Fatalf("从挂载点内部挪出来失败: %v", err)
	}
	if vfs.IsMountPoint(space) {
		t.Fatal("原来的挂载点没有卸载")
	}
	if !vfs.IsMountPoint(inner) {
		t.Fatal("没有挂到子目录上")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("真实文件夹没回来: %v", err)
	}
	if entries, err := os.ReadDir(inner); err != nil || len(entries) == 0 {
		t.Fatalf("子目录里看不到云盘内容: %v", err)
	}
	onDisk, err := config.Load(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.MountPoint != inner {
		t.Fatalf("磁盘上的挂载点 = %s, 期望 %s", onDisk.MountPoint, inner)
	}
}

// TestMountedAtStateFile 检查「实际挂载点」的落盘与读取：进程重启后要能靠它
// 认出上一次挂在哪里。
func TestMountedAtStateFile(t *testing.T) {
	s, root := newMountSvc(t, "")
	p := filepath.Join(root, "1000", "移动云盘")
	s.saveMountedAt(p)
	if got := s.loadMountedAt(); got != p {
		t.Fatalf("读回 %q, 期望 %q", got, p)
	}
	// 换一个实例读（模拟重启）
	s2, err := New(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if s2.mountedAt != p {
		t.Fatalf("重启后没认出上次的挂载点: %q", s2.mountedAt)
	}
	s.saveMountedAt("")
	if got := s.loadMountedAt(); got != "" {
		t.Fatalf("清空后仍读到 %q", got)
	}
	if _, err := os.Stat(s.mountStatePath()); !os.IsNotExist(err) {
		t.Fatal("状态文件没删掉")
	}
}

// TestStrayMountCleanup 覆盖「配置改到了新位置、上一次运行还挂在旧位置」的自救：
// 守护进程重新起来时要先把旧位置卸掉，不能两个位置同时挂着。
func TestStrayMountCleanup(t *testing.T) {
	if os.Getenv("CMCCYUN_TEST_FUSE") == "" {
		t.Skip("设 CMCCYUN_TEST_FUSE=1 才跑（需要 /dev/fuse）")
	}
	backend := t.TempDir()
	if err := os.WriteFile(filepath.Join(backend, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMCCYUN_DEBUG_FAKE", backend)

	s, root := newMountSvc(t, "")
	oldLoc := filepath.Join(root, "1000", "旧位置")
	newLoc := filepath.Join(root, "1000", "新位置")
	if err := os.MkdirAll(filepath.Join(root, "1000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMountPoint(context.Background(), oldLoc, SetMountPointOptions{Create: true}); err != nil {
		t.Fatalf("首次挂载失败: %v", err)
	}
	if !vfs.IsMountPoint(oldLoc) {
		t.Fatal("旧位置没挂上")
	}

	// 直接改盘上的配置（模拟手工编辑 config.json / 向导改了挂载点），
	// 但不卸载旧位置 —— 再起一个服务来接管，它必须自己把旧的清掉。
	cfg, err := config.Load(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MountPoint = newLoc
	if err := config.Save(s.ConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	s2, err := New(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	s2.mu.Lock()
	got := s2.strayMountLocked(newLoc)
	s2.mu.Unlock()
	if got != oldLoc {
		t.Fatalf("没认出旧挂载点: %q（旧位置是否还挂着: %v）", got, vfs.IsMountPoint(oldLoc))
	}
	if err := s2.Mount(context.Background()); err != nil {
		t.Fatalf("接管挂载失败: %v", err)
	}
	defer func() { _ = s2.Unmount() }()
	if vfs.IsMountPoint(oldLoc) {
		t.Fatal("旧位置没被清理，僵尸挂载")
	}
	if !vfs.IsMountPoint(newLoc) {
		t.Fatal("新位置没挂上")
	}
	if got := s2.loadMountedAt(); got != newLoc {
		t.Fatalf("mounted_at 记录的是 %q，期望 %q", got, newLoc)
	}
	// 卸载后状态文件应清掉
	if err := s2.Unmount(); err != nil {
		t.Fatal(err)
	}
	if got := s2.loadMountedAt(); got != "" {
		t.Fatalf("卸载后仍有记录: %q", got)
	}
}
