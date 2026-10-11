// Package vfs mounts a cloud-drive Backend as a local file system.
package vfs

import (
	"context"
	"io"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// Backend is everything the file system needs from a cloud drive.
// *api.Client implements it against 中国移动云盘.
type Backend interface {
	List(ctx context.Context, dirID string) ([]api.File, error)
	Download(ctx context.Context, fileID string, offset, length int64) (io.ReadCloser, error)
	UploadFile(ctx context.Context, parentID, name, path string, opt api.UploadOptions) (string, error)
	// UploadReader pushes size bytes read from src. It lets the file system
	// upload content that is assembled on the fly (cloud ranges + local edits)
	// instead of a file that exists on local disk.
	UploadReader(ctx context.Context, parentID, name string, src io.ReaderAt, size int64, opt api.UploadOptions) (string, error)
	Mkdir(ctx context.Context, parentID, name string) (string, error)
	Rename(ctx context.Context, fileID, name string) error
	Move(ctx context.Context, ids []string, toParent string) error
	Trash(ctx context.Context, ids []string) error
	Account() string
}

// Owner describes the uid/gid and permissions reported for every entry.
type Owner struct {
	UID      uint32
	GID      uint32
	FilePerm uint32
	DirPerm  uint32
}

// Options configures the mounted file system.
type Options struct {
	Backend     Backend
	Owner       Owner
	StageDir    string
	DirCacheTTL time.Duration
	ReadOnly    bool
	// Capacity is reported to statfs; the service exposes no quota endpoint on
	// the personal-cloud API, so this is a configured value.
	Capacity uint64
	// Logf receives human readable diagnostics.
	Logf func(format string, args ...any)
}

func (o *Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

func (o *Options) ttl() time.Duration {
	if o.DirCacheTTL <= 0 {
		return 60 * time.Second
	}
	return o.DirCacheTTL
}
