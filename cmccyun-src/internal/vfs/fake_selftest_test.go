package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFakeList checks the debug backend lists a directory the way the cloud
// does: files and sub-directories, with the directory flag set.
func TestFakeList(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"docs", "media"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "media", "b.bin"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	fb, err := NewFakeBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fb.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 entries, got %d (%+v)", len(files), files)
	}
	byName := map[string]bool{}
	for _, f := range files {
		if f.ID == "" {
			t.Fatalf("entry %q has no id", f.Name)
		}
		byName[f.Name] = f.IsDir
	}
	for _, d := range []string{"docs", "media"} {
		if !byName[d] {
			t.Fatalf("%q should be listed as a directory: %+v", d, byName)
		}
	}
	if byName["readme.txt"] {
		t.Fatalf("readme.txt should not be a directory: %+v", byName)
	}
}
