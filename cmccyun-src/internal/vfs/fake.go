package vfs

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// FakeBackend serves a local directory over the Backend interface. It exists so
// the mount stack can be exercised end to end without cloud credentials:
//
//	CMCCYUN_DEBUG_FAKE=/some/dir cmccyun --foreground
//
// It is never used in production and the daemon refuses to start with it while
// a real credential is configured.
type FakeBackend struct {
	root string
	mu   sync.Mutex
	ids  map[string]string // path -> id
	rev  map[string]string // id -> path
	seq  int
}

// NewFakeBackend returns a Backend backed by a local directory.
func NewFakeBackend(root string) (*FakeBackend, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	f := &FakeBackend{root: root, ids: map[string]string{}, rev: map[string]string{}}
	f.register("/", "/")
	return f, nil
}

// Account implements Backend.
func (f *FakeBackend) Account() string { return "debug" }

func (f *FakeBackend) register(rel, id string) {
	f.ids[rel] = id
	f.rev[id] = rel
}

func (f *FakeBackend) idFor(rel string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel = path.Clean("/" + strings.TrimPrefix(rel, "/"))
	if id, ok := f.ids[rel]; ok {
		return id
	}
	f.seq++
	id := fmt.Sprintf("fake-%d-%s", f.seq, shortHash(rel))
	f.register(rel, id)
	return id
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:6])
}

func (f *FakeBackend) pathOf(id string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, ok := f.rev[id]
	if !ok {
		return "", false
	}
	return filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(rel, "/"))), true
}

// List implements Backend.
func (f *FakeBackend) List(_ context.Context, dirID string) ([]api.File, error) {
	dir, ok := f.pathOf(dirID)
	if !ok {
		return nil, os.ErrNotExist
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]api.File, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		relDir, _ := f.relOf(dirID)
		rel := path.Join(relDir, e.Name())
		out = append(out, api.File{
			ID:       f.idFor(rel),
			Name:     e.Name(),
			Size:     info.Size(),
			IsDir:    e.IsDir(),
			Created:  info.ModTime().Format(time.RFC3339),
			Modified: info.ModTime().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (f *FakeBackend) relOf(dir string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, ok := f.rev[dir]
	return rel, ok
}

// Download implements Backend.
func (f *FakeBackend) Download(_ context.Context, fileID string, offset, length int64) (io.ReadCloser, error) {
	p, ok := f.pathOf(fileID)
	if !ok {
		return nil, os.ErrNotExist
	}
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		fh.Close()
		return nil, err
	}
	if length > 0 {
		return io.NopCloser(io.LimitReader(fh, length)), nil
	}
	return fh, nil
}

// UploadFile implements Backend.
func (f *FakeBackend) UploadFile(_ context.Context, parentID, name, localPath string, opt api.UploadOptions) (string, error) {
	src, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return "", err
	}
	return f.UploadReader(context.Background(), parentID, name, src, st.Size(), opt)
}

// UploadReader implements Backend. Like the real object store it stages the new
// object next to the old one and swaps it in at the end, so reading the old
// object while the new one is being assembled still works.
func (f *FakeBackend) UploadReader(_ context.Context, parentID, name string, src io.ReaderAt, size int64, _ api.UploadOptions) (string, error) {
	parent, ok := f.pathOf(parentID)
	if !ok {
		return "", os.ErrNotExist
	}
	dst := filepath.Join(parent, name)
	tmp, err := os.CreateTemp(parent, ".upload-*")
	if err != nil {
		return "", err
	}
	if size > 0 {
		if _, err := io.Copy(tmp, io.NewSectionReader(src, 0, size)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", err
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	rel, _ := f.relOf(parentID)
	f.forget(path.Join(rel, name))
	return name, nil
}

// forget drops a remembered id so a rewritten path is re-resolved.
func (f *FakeBackend) forget(rel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel = path.Clean("/" + strings.TrimPrefix(rel, "/"))
	if id, ok := f.ids[rel]; ok {
		delete(f.ids, rel)
		delete(f.rev, id)
	}
}

// Mkdir implements Backend.
func (f *FakeBackend) Mkdir(_ context.Context, parentID, name string) (string, error) {
	parent, ok := f.pathOf(parentID)
	if !ok {
		return "", os.ErrNotExist
	}
	if err := os.Mkdir(filepath.Join(parent, name), 0o755); err != nil {
		return "", err
	}
	return name, nil
}

// Rename implements Backend.
func (f *FakeBackend) Rename(_ context.Context, fileID, name string) error {
	p, ok := f.pathOf(fileID)
	if !ok {
		return os.ErrNotExist
	}
	return os.Rename(p, filepath.Join(filepath.Dir(p), name))
}

// Move implements Backend.
func (f *FakeBackend) Move(_ context.Context, ids []string, toParent string) error {
	dst, ok := f.pathOf(toParent)
	if !ok {
		return os.ErrNotExist
	}
	for _, id := range ids {
		src, ok := f.pathOf(id)
		if !ok {
			return os.ErrNotExist
		}
		if err := os.Rename(src, filepath.Join(dst, filepath.Base(src))); err != nil {
			return err
		}
	}
	return nil
}

// Trash implements Backend.
func (f *FakeBackend) Trash(_ context.Context, ids []string) error {
	for _, id := range ids {
		p, ok := f.pathOf(id)
		if !ok {
			continue
		}
		os.RemoveAll(p)
	}
	return nil
}
