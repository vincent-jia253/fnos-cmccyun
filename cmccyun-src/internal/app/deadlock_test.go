package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// mustReturn 在 watch 时间内等 fn 返回；超时说明有人把锁锁死了（自锁死不会自己恢复，
// 唯一的表征就是「永远不返回」，所以只能这样测）。
func mustReturn(t *testing.T, name string, watch time.Duration, fn func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case <-done:
	case <-time.After(watch):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("%s 在 %s 内没有返回（疑似自己把自己锁死）\n%s", name, watch, buf[:n])
	}
}

// TestConsoleEntryPointsDoNotDeadlock 是锁纪律的看门狗。
//
// 背景：v1.2.1 上线的版本里，Service.Update()（整段持有 s.mu）调用
// validateMountPath → validateMountPathOpt → curMountPoint()，而 curMountPoint
// 自己又去取同一把 s.mu（Go 的 Mutex 不可重入）→ 自锁死。表征：
//   - 管理台点「挂载」永远停在「正在挂载…」（因为按钮先 POST /api/config 保存设置）；
//   - 之后每个接口（/api/status、/api/fs/list…）一起卡住，页面像死了一样；
//   - 守护进程 0% CPU、挂载本身还活着（FUSE 在另一个 goroutine 里正常服务）。
//
// 每个控制台入口都必须能在有限时间内返回。新加入口时把它加进这张表。
func TestConsoleEntryPointsDoNotDeadlock(t *testing.T) {
	cases := []struct {
		name string
		run  func(s *Service, root string) error
	}{
		{"Status", func(s *Service, root string) error { _ = s.Status(); return nil }},
		{"Config+MaskPtr", func(s *Service, root string) error { _ = s.Config(); _ = s.MaskPtr(); return nil }},
		{"curMountPoint", func(s *Service, root string) error { _ = s.curMountPoint(); return nil }},
		// 出问题的那一条：带 mount_point 的配置保存（保存设置 / 点「挂载」都会走）。
		{"Update(mount_point)", func(s *Service, root string) error {
			return s.Update(map[string]any{"mount_point": filepath.Join(root, "1000", "移动云盘")})
		}},
		// 挂载点与配置一致（最常用的一次点击：什么都不改，直接点「挂载」）。
		{"Update(mount_point) 与配置相同", func(s *Service, root string) error {
			return s.Update(map[string]any{"mount_point": s.cfg.MountPoint})
		}},
		{"Update(其它字段)", func(s *Service, root string) error {
			return s.Update(map[string]any{"read_only": true, "uid": 1000, "gid": 1001, "auto_mount": true})
		}},
		{"CheckMountPath", func(s *Service, root string) error {
			_, err := s.CheckMountPath(filepath.Join(root, "1000", "移动云盘"), false)
			return err
		}},
		{"CreateMountDir", func(s *Service, root string) error {
			_, err := s.CreateMountDir(filepath.Join(root, "1000"), "新建目录")
			return err
		}},
		{"BrowseDirs", func(s *Service, root string) error { _, _, _, err := s.BrowseDirs(root); return err }},
		{"Unmount(没挂载)", func(s *Service, root string) error { return s.Unmount() }},
		// 没授权时必须快速报错，而不是把锁攥着等网络。
		{"Mount(未授权)", func(s *Service, root string) error { return s.Mount(context.Background()) }},
		{"Remount(未授权)", func(s *Service, root string) error { return s.Remount(context.Background()) }},
		{"refreshAuth(未授权)", func(s *Service, root string) error { return s.refreshAuth(context.Background(), true) }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// 配置直接改内存字段（不经 Update），免得准备阶段就卡住、把「失败」变成「挂死」。
			// 临时目录就是允许的卷根，所以 <root>/1000/移动云盘 能通过语法校验、
			// 一路走到会取锁的那几行。
			s, root := newMountSvc(t, "")
			s.cfg.MountPoint = filepath.Join(root, "1000", "移动云盘")
			mustReturn(t, tc.name, 5*time.Second, func() error { return tc.run(s, root) })
		})
	}
}
