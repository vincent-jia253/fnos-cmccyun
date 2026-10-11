package vfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// allocatedBytes reports how much disk a (possibly sparse) file really uses.
func allocatedBytes(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

// ---------------------------------------------------------------- extents

func TestExtentsAddMergesAndSplits(t *testing.T) {
	var e extents
	e = e.add(extent{off: 100, length: 10})
	e = e.add(extent{off: 110, length: 5}) // adjacent, same backing -> merge
	if len(e) != 1 || e[0].off != 100 || e[0].length != 15 {
		t.Fatalf("adjacent ranges did not merge: %+v", e)
	}
	e = e.add(extent{off: 120, length: 5}) // gap stays separate
	if len(e) != 2 {
		t.Fatalf("gap was merged: %+v", e)
	}
	// A zero range overlapping a data range takes precedence in the overlap.
	e = extents{{off: 0, length: 100}}
	e = e.add(extent{off: 50, length: 10, zero: true})
	if len(e) != 3 || !e[1].zero || e[1].off != 50 || e[1].length != 10 {
		t.Fatalf("zero override wrong: %+v", e)
	}
	// Shrinking drops anything past the new size.
	e = extents{{off: 0, length: 100}, {off: 200, length: 100}}
	e = e.trim(150)
	if len(e) != 1 || e[0].length != 100 {
		t.Fatalf("trim wrong: %+v", e)
	}
	if got := (extents{{off: 40, length: 10}}).nextStart(0, 1000); got != 40 {
		t.Fatalf("nextStart=%d want 40", got)
	}
	if got := (extents{{off: 40, length: 10}}).nextStart(45, 1000); got != 1000 {
		t.Fatalf("nextStart inside range=%d want 1000", got)
	}
}

// ---------------------------------------------------------------- overlay

func newStub(t *testing.T) (*FakeBackend, string) {
	t.Helper()
	root := t.TempDir()
	fb, err := NewFakeBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	return fb, root
}

func cfgFor(fb *FakeBackend, stage string) *Options {
	return &Options{
		Backend:     fb,
		StageDir:    stage,
		Capacity:    1 << 30,
		DirCacheTTL: time.Minute,
	}
}

// TestOverlayEditUploadsWithoutFullDownload is the regression test for the
// CloudDrive2-style write model: patching a file that lives in the cloud must
// not copy the whole object to local disk, yet the uploaded result must be the
// full, correct file.
func TestOverlayEditUploadsWithoutFullDownload(t *testing.T) {
	ctx := context.Background()
	fb, root := newStub(t)
	stage := t.TempDir()

	// 4 MiB object in the "cloud", with a recognisable pattern.
	size := 4 << 20
	base := make([]byte, size)
	for i := 0; i < size; i += 4 {
		binary.BigEndian.PutUint32(base[i:], uint32(i/4))
	}
	basePath := filepath.Join(root, "movie.bin")
	if err := os.WriteFile(basePath, base, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := fb.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	var entry api.File
	for _, f := range files {
		if f.Name == "movie.bin" {
			entry = f
		}
	}
	if entry.ID == "" {
		t.Fatal("seed file not listed")
	}

	cfg := cfgFor(fb, stage)
	n := newFileNode(cfg, entry, "/")

	// Open read/write and patch two small ranges.
	fh, _, errno := n.Open(ctx, uint32(os.O_RDWR))
	if errno != 0 {
		t.Fatalf("open: errno=%d", errno)
	}
	h, ok := fh.(*fileHandle)
	if !ok {
		t.Fatal("unexpected handle type")
	}
	if h.overlay == nil {
		t.Fatal("expected an overlay handle for a cloud-backed file")
	}

	patches := []struct {
		off  int64
		data []byte
	}{
		{off: 1000, data: []byte("HELLO-CMCCYUN")},
		{off: 3 << 20, data: []byte("TAIL-PATCH-0123456789")},
	}
	for _, p := range patches {
		if _, errno := h.Write(ctx, p.data, p.off); errno != 0 {
			t.Fatalf("write at %d: errno=%d", p.off, errno)
		}
	}

	// Reading back through the same handle must see the patched bytes AND the
	// untouched cloud bytes.
	if _, errno := h.Read(ctx, make([]byte, 16), 900); errno != 0 {
		t.Fatalf("read: errno=%d", errno)
	}
	buf := make([]byte, 64)
	res, errno := h.Read(ctx, buf, 990)
	if errno != 0 {
		t.Fatalf("read patch: errno=%d", errno)
	}
	got, _ := res.Bytes(buf)
	want := base[990:1000]
	if !bytes.Equal(got[:10], want) {
		t.Fatalf("read before the patch returned %q want %q", got[:10], want)
	}
	patchAt := 10
	if !bytes.Equal(got[patchAt:patchAt+len(patches[0].data)], patches[0].data) {
		t.Fatalf("read of the patch returned %q want %q",
			got[patchAt:patchAt+len(patches[0].data)], patches[0].data)
	}

	// The staging file must hold the written ranges only. It is sparse, so
	// what matters is the allocated size, not the logical length.
	stagedInfo, err := os.Stat(n.staged)
	if err != nil {
		t.Fatalf("stat staging: %v", err)
	}
	alloc := allocatedBytes(stagedInfo)
	if alloc > 256<<10 {
		t.Fatalf("staging allocated %d bytes; the file should not be materialised", alloc)
	}
	t.Logf("staging logical=%d allocated=%d", stagedInfo.Size(), alloc)

	// Flushing uploads the stitched file.
	if errno := h.Flush(ctx); errno != 0 {
		t.Fatalf("flush/upload: errno=%d", errno)
	}

	expect := append([]byte(nil), base...)
	for _, p := range patches {
		copy(expect[p.off:], p.data)
	}
	after, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(expect) {
		t.Fatalf("uploaded size %d want %d", len(after), len(expect))
	}
	if !bytes.Equal(after, expect) {
		for i := range after {
			if after[i] != expect[i] {
				t.Fatalf("uploaded content differs at offset %d: got %02x want %02x", i, after[i], expect[i])
			}
		}
	}
	if _, err := os.Stat(n.staged); !os.IsNotExist(err) {
		t.Fatalf("staging file should be removed after upload: %v", err)
	}
}

// TestOverlayTruncateShrinkAndGrow checks the size semantics of the overlay.
func TestOverlayTruncateShrinkAndGrow(t *testing.T) {
	ctx := context.Background()
	fb, root := newStub(t)
	base := bytes.Repeat([]byte("0123456789abcdef"), 4096) // 64 KiB
	if err := os.WriteFile(filepath.Join(root, "doc.bin"), base, 0o644); err != nil {
		t.Fatal(err)
	}
	files, _ := fb.List(ctx, "/")
	var entry api.File
	for _, f := range files {
		if f.Name == "doc.bin" {
			entry = f
		}
	}
	n := newFileNode(cfgFor(fb, t.TempDir()), entry, "/")

	if errno := n.truncate(ctx, 1000); errno != 0 {
		t.Fatalf("shrink errno=%d", errno)
	}
	if n.size != 1000 {
		t.Fatalf("size after shrink=%d", n.size)
	}
	if errno := n.truncate(ctx, 5000); errno != 0 {
		t.Fatalf("grow errno=%d", errno)
	}
	ov := n.overlayReader(ctx, mustOpen(t, n.staged))
	defer ov.remote.close()

	got := make([]byte, 5000)
	if _, err := io.ReadFull(io.NewSectionReader(ov, 0, 5000), got); err != nil {
		t.Fatalf("read composed: %v", err)
	}
	if !bytes.Equal(got[:1000], base[:1000]) {
		t.Fatal("shrunk head should still come from the cloud")
	}
	if !bytes.Equal(got[1000:5000], make([]byte, 4000)) {
		t.Fatal("grown tail should read as zeroes")
	}

	// A partial write inside the grown tail must win over the zeroes.
	if errno := n.truncate(ctx, 5000); errno != 0 {
		t.Fatalf("retruncate errno=%d", errno)
	}
	n.addExtent(2000, 5)
	f := mustOpen(t, n.staged)
	if _, err := f.WriteAt([]byte("PATCH"), 2000); err != nil {
		t.Fatal(err)
	}
	ov2 := n.overlayReader(ctx, f)
	got2 := make([]byte, 5000)
	if _, err := io.ReadFull(io.NewSectionReader(ov2, 0, 5000), got2); err != nil {
		t.Fatalf("read composed 2: %v", err)
	}
	if !bytes.Equal(got2[2000:2005], []byte("PATCH")) {
		t.Fatalf("patch not visible: %q", got2[2000:2005])
	}
	if !bytes.Equal(got2[3000:5000], make([]byte, 2000)) {
		t.Fatal("remaining grown tail should read as zeroes")
	}
	f.Close()
	ov2.remote.close()
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
