package vfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeadReportsFalseForNormalDirs：普通目录、不存在的路径都不是「死挂载」。
// 正向用例（真的挂一个 FUSE 再让对端消失）没法在单测里造，用真机验证：
// 强杀守护进程后启动，日志里会出现「失去响应的旧挂载（ENOTCONN），先清理再重新挂载」。
func TestDeadReportsFalseForNormalDirs(t *testing.T) {
	dir := t.TempDir()
	if Dead(dir) {
		t.Fatalf("普通目录 %s 被判成死挂载", dir)
	}
	if Dead("") {
		t.Fatal("空路径被判成死挂载")
	}
	if Dead(filepath.Join(dir, "不存在")) {
		t.Fatal("不存在的路径被判成死挂载")
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Dead(filepath.Join(dir, "sub")) {
		t.Fatal("子目录被判成死挂载")
	}
}
