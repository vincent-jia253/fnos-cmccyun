package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stagedChild mirrors what fs.Node.Create hands to the kernel: a node whose
// whole content lives in a fresh staging file, plus a writable handle on it.
// (Create itself needs a mounted inode tree, which a unit test does not have.)
func stagedChild(t *testing.T, cfg *Options, parentID, name string) *fileHandle {
	t.Helper()
	stage, err := os.CreateTemp(cfg.StageDir, "cmccyun-*")
	if err != nil {
		t.Fatal(err)
	}
	child := &Node{
		cfg: cfg, parentID: parentID, name: name,
		staged: stage.Name(), dirty: true, mtime: time.Now(), ctime: time.Now(),
	}
	return &fileHandle{node: child, stage: stage, writable: true, dirty: true}
}

func fakeDirID(t *testing.T, ctx context.Context, fb *FakeBackend, root, name string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := fb.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("%s dir not listed", name)
	return ""
}

// TestCreateSurvivesFlushBeforeFirstWrite is the regression test for a real
// kernel behaviour: opening a file with O_CREAT|O_TRUNC makes the kernel send
// FLUSH *before* the first WRITE. An early flush must not throw away the
// staging file, otherwise the bytes that arrive right after it land in an
// unlinked inode and the new file ends up empty in the cloud.
func TestCreateSurvivesFlushBeforeFirstWrite(t *testing.T) {
	ctx := context.Background()
	fb, root := newStub(t)
	parentID := fakeDirID(t, ctx, fb, root, "docs")

	cfg := cfgFor(fb, t.TempDir())
	h := stagedChild(t, cfg, parentID, "note.txt")

	// 1. the kernel's flush of the O_TRUNC open, before any data was written
	if errno := h.Flush(ctx); errno != 0 {
		t.Fatalf("pre-write flush errno=%d", errno)
	}
	if h.node.staged == "" {
		t.Fatal("pre-write flush dropped the staging file")
	}

	// 2. the actual write, then the flush that happens on close
	if _, errno := h.Write(ctx, []byte("hello cmccyun\n"), 0); errno != 0 {
		t.Fatalf("write errno=%d", errno)
	}
	if errno := h.Flush(ctx); errno != 0 {
		t.Fatalf("upload flush errno=%d", errno)
	}
	if errno := h.Release(ctx); errno != 0 {
		t.Fatalf("release errno=%d", errno)
	}

	data, err := os.ReadFile(filepath.Join(root, "docs", "note.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(data) != "hello cmccyun\n" {
		t.Fatalf("uploaded content=%q", data)
	}

	// 3. the node is rewired to the new object and is clean again
	h.node.mu.Lock()
	staged, fileID, dirty := h.node.staged, h.node.fileID, h.node.dirty
	h.node.mu.Unlock()
	if staged != "" || fileID == "" || dirty {
		t.Fatalf("node not rewired: staged=%q id=%q dirty=%v", staged, fileID, dirty)
	}
}

// TestCreateEmptyFileDoesNotPushObject documents the other half of the rule:
// flushing a still-empty new file is a no-op, and the staging file survives so
// a later write can still be captured.
func TestCreateEmptyFileDoesNotPushObject(t *testing.T) {
	ctx := context.Background()
	fb, root := newStub(t)
	parentID := fakeDirID(t, ctx, fb, root, "docs")

	cfg := cfgFor(fb, t.TempDir())
	h := stagedChild(t, cfg, parentID, "empty.txt")

	if errno := h.Flush(ctx); errno != 0 {
		t.Fatalf("flush errno=%d", errno)
	}
	if errno := h.Release(ctx); errno != 0 {
		t.Fatalf("release errno=%d", errno)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "empty.txt")); err == nil {
		t.Fatal("an empty new file should not be pushed to the cloud")
	}

	// A write after the empty flush must still make it to the cloud.
	h2 := stagedChild(t, cfg, parentID, "empty2.txt")
	if errno := h2.Flush(ctx); errno != 0 {
		t.Fatalf("flush errno=%d", errno)
	}
	if _, errno := h2.Write(ctx, []byte("x"), 0); errno != 0 {
		t.Fatalf("write errno=%d", errno)
	}
	if errno := h2.Flush(ctx); errno != 0 {
		t.Fatalf("upload flush errno=%d", errno)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "empty2.txt")); err != nil {
		t.Fatalf("second file not uploaded: %v", err)
	}
}
