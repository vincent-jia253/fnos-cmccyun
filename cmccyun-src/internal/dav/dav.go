// Package dav exposes the 移动云盘 as a WebDAV file system.
//
// 为什么要有这一层：飞牛 fnOS 的「远程挂载」对用户开放的协议里就有 WebDAV
// （mountmgr 用 rclone 当后端）。我们自己做一个 WebDAV 服务端，用户就能在
// 「远程挂载 → 挂载远程文件夹 → WebDAV」里填上本机地址，把云盘挂成 rclone
// 卷；这条路不碰飞牛的任何闭源二进制，升级不会被覆盖。
//
// 设计取舍：
//   - 路径解析靠 list 逐级下钻（139 的 API 只有 fileId，没有"路径→id"接口），
//     所以目录列表有短 TTL 缓存，否则 Emby/影视刮削一个库就是几千次请求。
//   - 读：Download(id, offset, -1) 真流（Range），Seek 只挪偏移、下一次 Read
//     才重开流；这样 http.ServeContent 的 Seek(0,End) 探测不花网络。
//   - 写：先落到本地 stage 临时文件，Close 时整文件分片上传。代价是改大文件
//     会整文件重传（FUSE 那条路有 overlay 局部写优化，WebDAV 没有），README
//     里写明了这个取舍。
//   - 删除进云端回收站（可还原），不是真删。
package dav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// Backend is the slice of api.Client this package needs. Keeping it an
// interface makes the WebDAV layer testable without a live cloud account.
type Backend interface {
	List(ctx context.Context, parentID string) ([]api.File, error)
	Mkdir(ctx context.Context, parentID, name string) (string, error)
	Rename(ctx context.Context, fileID, name string) error
	Move(ctx context.Context, ids []string, toParent string) error
	Trash(ctx context.Context, ids []string) error
	Download(ctx context.Context, fileID string, offset, length int64) (io.ReadCloser, error)
	DownloadURL(ctx context.Context, fileID string) (string, error)
	UploadReader(ctx context.Context, parentID, name string, src io.ReaderAt, size int64, opt api.UploadOptions) (string, error)
}

// Options tunes one FS instance.
type Options struct {
	// StageDir 是上传缓冲目录（和 FUSE 的暂存目录同一个）。
	StageDir string
	// PartSize 是分片上传的片大小。
	PartSize int64
	// ReadOnly 打开后所有写方法返回权限错误（WebDAV 客户端会看到 403）。
	ReadOnly bool
	// CacheTTL 是目录列表缓存时长，0 表示默认 5 秒。
	CacheTTL time.Duration
	Logf     func(format string, args ...any)
}

// Entry is the resolved identity of one remote object.
type Entry struct {
	ID      string
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
	// ParentID / Base 指向它在云端的父目录，改名/删除时要用。
	ParentID string
	Base     string
}

// FS implements webdav.FileSystem on top of the 139 personal cloud.
type FS struct {
	be       Backend
	opt      Options
	now      func() time.Time
	mu       sync.Mutex
	cache    map[string]cacheEntry
	cacheAge time.Duration
}

type cacheEntry struct {
	at    time.Time
	files []api.File
}

// defaultCacheTTL 是目录列表缓存。太短等于没有缓存（刮削会打爆 API），
// 太长会让 WebDAV 侧看不到刚写进去的文件；5 秒是两者之间的折中，而且所有
// 写操作都会主动失效父目录缓存，所以自己写完立刻能读到。
const defaultCacheTTL = 5 * time.Second

// New builds an FS.
func New(be Backend, opt Options) *FS {
	if opt.CacheTTL <= 0 {
		opt.CacheTTL = defaultCacheTTL
	}
	if opt.PartSize <= 0 {
		opt.PartSize = 100 << 20
	}
	f := &FS{be: be, opt: opt, now: time.Now, cache: map[string]cacheEntry{}}
	return f
}

func (f *FS) logf(format string, args ...any) {
	if f.opt.Logf != nil {
		f.opt.Logf(format, args...)
	}
}

// Invalidate drops the cached listing of one folder ("" = root).
func (f *FS) Invalidate(dirID string) {
	f.mu.Lock()
	delete(f.cache, dirID)
	f.mu.Unlock()
}

func (f *FS) list(ctx context.Context, id string) ([]api.File, error) {
	f.mu.Lock()
	if e, ok := f.cache[id]; ok && f.now().Sub(e.at) < f.opt.CacheTTL {
		files := e.files
		f.mu.Unlock()
		return files, nil
	}
	f.mu.Unlock()

	files, err := f.be.List(ctx, id)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.cache[id] = cacheEntry{at: f.now(), files: files}
	f.mu.Unlock()
	return files, nil
}

var errNotDir = errors.New("not a directory")

// splitPath normalises a WebDAV path into (parent, base). Root yields ("", "").
func splitPath(name string) (parent, base string) {
	p := path.Clean("/" + strings.TrimPrefix(name, "/"))
	if p == "/" {
		return "/", ""
	}
	return path.Dir(p), path.Base(p)
}

// walk resolves the folder at dirPath and returns its fileId.
func (f *FS) walk(ctx context.Context, dirPath string) (string, error) {
	p := path.Clean("/" + strings.TrimPrefix(dirPath, "/"))
	if p == "/" {
		return api.RootID, nil
	}
	cur := api.RootID
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		files, err := f.list(ctx, cur)
		if err != nil {
			return "", err
		}
		found := false
		for _, fl := range files {
			if fl.Name == part {
				if !fl.IsDir {
					return "", errNotDir
				}
				cur = fl.ID
				found = true
				break
			}
		}
		if !found {
			return "", os.ErrNotExist
		}
	}
	return cur, nil
}

// resolve maps a WebDAV path to one remote entry.
func (f *FS) resolve(ctx context.Context, name string) (Entry, error) {
	parentPath, base := splitPath(name)
	if base == "" {
		return Entry{ID: api.RootID, Name: "/", IsDir: true, ModTime: f.now()}, nil
	}
	parentID, err := f.walk(ctx, parentPath)
	if err != nil {
		return Entry{}, err
	}
	files, err := f.list(ctx, parentID)
	if err != nil {
		return Entry{}, err
	}
	for _, fl := range files {
		if fl.Name == base {
			return Entry{
				ID: fl.ID, Name: fl.Name, Size: fl.Size, IsDir: fl.IsDir,
				ModTime: parseTime(fl.Modified), ParentID: parentID, Base: fl.Name,
			}, nil
		}
	}
	return Entry{}, os.ErrNotExist
}

// Resolve is the exported form used by the HTTP layer (e.g. to answer a GET
// with a redirect to the CDN instead of proxying the bytes).
func (f *FS) Resolve(ctx context.Context, name string) (Entry, error) { return f.resolve(ctx, name) }

// DirectURL asks the cloud for a fresh signed link to a file.
func (f *FS) DirectURL(ctx context.Context, id string) (string, error) {
	return f.be.DownloadURL(ctx, id)
}

// --- webdav.FileSystem -----------------------------------------------------

// Mkdir creates one folder. WebDAV maps os.ErrExist to 405 and
// os.ErrNotExist to 409, which is exactly the RFC behaviour clients expect.
func (f *FS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	if f.opt.ReadOnly {
		return os.ErrPermission
	}
	parentPath, base := splitPath(name)
	if base == "" {
		return os.ErrExist
	}
	parentID, err := f.walk(ctx, parentPath)
	if err != nil {
		return mapErr(err)
	}
	if _, err := f.resolve(ctx, name); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}
	if _, err := f.be.Mkdir(ctx, parentID, base); err != nil {
		f.logf("WebDAV：新建目录 %q 失败：%v", name, err)
		return err
	}
	f.Invalidate(parentID)
	f.logf("WebDAV：已新建目录 %s", name)
	return nil
}

// OpenFile serves both directions: read handles (dir listing / ranged file
// reads) and write handles (buffered upload finalised on Close).
func (f *FS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		if f.opt.ReadOnly {
			return nil, os.ErrPermission
		}
		parentPath, base := splitPath(name)
		if base == "" {
			return nil, os.ErrPermission
		}
		parentID, err := f.walk(ctx, parentPath)
		if err != nil {
			return nil, mapErr(err)
		}
		tmp, err := os.CreateTemp(f.opt.StageDir, "dav-*")
		if err != nil {
			return nil, err
		}
		return &uploadFile{
			fs: f, ctx: ctx, parentID: parentID, name: base,
			tmp: tmp, tmpPath: tmp.Name(),
		}, nil
	}

	e, err := f.resolve(ctx, name)
	if err != nil {
		return nil, mapErr(err)
	}
	if e.IsDir {
		return &dirFile{fs: f, ctx: ctx, entry: e}, nil
	}
	return &readFile{fs: f, ctx: ctx, entry: e}, nil
}

// RemoveAll sends the object (recursively, for folders) to the cloud recycle
// bin. 139 deletes a folder as a whole, so one call covers a whole subtree.
func (f *FS) RemoveAll(ctx context.Context, name string) error {
	if f.opt.ReadOnly {
		return os.ErrPermission
	}
	e, err := f.resolve(ctx, name)
	if err != nil {
		return mapErr(err)
	}
	if e.Base == "" {
		return os.ErrPermission
	}
	if err := f.be.Trash(ctx, []string{e.ID}); err != nil {
		f.logf("WebDAV：删除 %q 失败：%v", name, err)
		return err
	}
	f.Invalidate(e.ParentID)
	f.logf("WebDAV：已把 %s 移到云端回收站", name)
	return nil
}

// Rename supports both a pure rename (same folder) and a move (another folder),
// because WebDAV clients (Overwrite) use MOVE for both.
func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	if f.opt.ReadOnly {
		return os.ErrPermission
	}
	e, err := f.resolve(ctx, oldName)
	if err != nil {
		return mapErr(err)
	}
	if e.Base == "" {
		return os.ErrPermission
	}
	newParentPath, newBase := splitPath(newName)
	if newBase == "" {
		return os.ErrPermission
	}
	newParentID, err := f.walk(ctx, newParentPath)
	if err != nil {
		return mapErr(err)
	}
	// WebDAV 的 Rename 语义是「目标已存在就覆盖」。
	if tgt, terr := f.resolve(ctx, newName); terr == nil && tgt.ID != e.ID {
		if err := f.be.Trash(ctx, []string{tgt.ID}); err != nil {
			return err
		}
		f.Invalidate(tgt.ParentID)
	}
	if newParentID != e.ParentID {
		if err := f.be.Move(ctx, []string{e.ID}, newParentID); err != nil {
			f.logf("WebDAV：移动 %q 失败：%v", oldName, err)
			return err
		}
	}
	if newBase != e.Base {
		if err := f.be.Rename(ctx, e.ID, newBase); err != nil {
			f.logf("WebDAV：重命名 %q 失败：%v", oldName, err)
			return err
		}
	}
	f.Invalidate(e.ParentID)
	f.Invalidate(newParentID)
	f.logf("WebDAV：%s → %s", oldName, newName)
	return nil
}

// Stat reports one object.
func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	e, err := f.resolve(ctx, name)
	if err != nil {
		return nil, mapErr(err)
	}
	return infoOf(e), nil
}

// mapErr keeps the sentinels that golang.org/x/net/webdav understands intact;
// os.IsNotExist / os.IsPermission do not unwrap generic errors.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errNotDir):
		return os.ErrInvalid
	case errors.Is(err, os.ErrNotExist), errors.Is(err, os.ErrExist), errors.Is(err, os.ErrPermission):
		return err
	}
	return err
}

// --- fileInfo --------------------------------------------------------------

type fileInfo struct {
	name  string
	size  int64
	dir   bool
	mtime time.Time
}

func infoOf(e Entry) fileInfo {
	name := e.Base
	if name == "" {
		name = "/"
	}
	return fileInfo{name: name, size: e.Size, dir: e.IsDir, mtime: e.ModTime}
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) ModTime() time.Time {
	return i.mtime
}
func (i fileInfo) IsDir() bool { return i.dir }
func (i fileInfo) Sys() any    { return nil }
func (i fileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

func parseTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05.999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}

// --- read handles ---------------------------------------------------------

// readFile is a lazily-opened, seekable view of one remote file. Seek never
// touches the network: it only moves the offset and drops the stream, so the
// Seek(0, End) probe that http.ServeContent does is free.
type readFile struct {
	fs    *FS
	ctx   context.Context
	entry Entry
	off   int64
	rc    io.ReadCloser
	rcOff int64
	dead  bool
}

func (r *readFile) Stat() (os.FileInfo, error) { return infoOf(r.entry), nil }

func (r *readFile) Read(p []byte) (int, error) {
	if r.dead {
		return 0, os.ErrClosed
	}
	if r.entry.Size > 0 && r.off >= r.entry.Size {
		return 0, io.EOF
	}
	if r.rc == nil {
		rc, err := r.fs.be.Download(r.ctx, r.entry.ID, r.off, -1)
		if err != nil {
			return 0, err
		}
		r.rc, r.rcOff = rc, r.off
	}
	n, err := r.rc.Read(p)
	r.off += int64(n)
	if err == io.EOF {
		r.closeStream()
	} else if err != nil {
		r.closeStream()
	}
	return n, err
}

func (r *readFile) closeStream() {
	if r.rc != nil {
		_ = r.rc.Close()
		r.rc = nil
	}
}

func (r *readFile) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.off + offset
	case io.SeekEnd:
		abs = r.entry.Size + offset
	default:
		return 0, os.ErrInvalid
	}
	if abs < 0 {
		return 0, os.ErrInvalid
	}
	if abs != r.off {
		r.closeStream()
		r.off = abs
	}
	return abs, nil
}

func (r *readFile) Write([]byte) (int, error)          { return 0, os.ErrPermission }
func (r *readFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (r *readFile) Close() error {
	r.closeStream()
	r.dead = true
	return nil
}

// --- directory handle -----------------------------------------------------

type dirFile struct {
	fs    *FS
	ctx   context.Context
	entry Entry
	once  sync.Once
	infos []os.FileInfo
	err   error
	pos   int
}

func (d *dirFile) load() {
	d.once.Do(func() {
		files, err := d.fs.list(d.ctx, d.entry.ID)
		if err != nil {
			d.err = err
			return
		}
		sort.Slice(files, func(i, j int) bool {
			if files[i].IsDir != files[j].IsDir {
				return files[i].IsDir
			}
			return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
		})
		out := make([]os.FileInfo, 0, len(files))
		for _, fl := range files {
			out = append(out, fileInfo{
				name: fl.Name, size: fl.Size, dir: fl.IsDir,
				mtime: parseTime(fl.Modified),
			})
		}
		d.infos = out
	})
}

func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	d.load()
	if d.err != nil {
		return nil, d.err
	}
	if count <= 0 {
		out := d.infos[d.pos:]
		d.pos = len(d.infos)
		return out, nil
	}
	if d.pos >= len(d.infos) {
		return nil, io.EOF
	}
	end := d.pos + count
	if end > len(d.infos) {
		end = len(d.infos)
	}
	out := d.infos[d.pos:end]
	d.pos = end
	return out, nil
}

func (d *dirFile) Stat() (os.FileInfo, error) { return infoOf(d.entry), nil }
func (d *dirFile) Read([]byte) (int, error)   { return 0, os.ErrInvalid }
func (d *dirFile) Seek(int64, int) (int64, error) {
	return 0, os.ErrInvalid
}
func (d *dirFile) Write([]byte) (int, error) { return 0, os.ErrPermission }
func (d *dirFile) Close() error              { return nil }

// --- write handle --------------------------------------------------------

// uploadFile buffers a whole PUT into the stage directory and pushes it to the
// cloud on Close. WebDAV has no partial-write API we could map to 139's
// multipart uploader, so this is a full-file upload by design.
type uploadFile struct {
	fs       *FS
	ctx      context.Context
	parentID string
	name     string
	tmp      *os.File
	tmpPath  string
	off      int64
	closed   bool
}

func (u *uploadFile) Write(p []byte) (int, error) {
	if u.closed {
		return 0, os.ErrClosed
	}
	n, err := u.tmp.WriteAt(p, u.off)
	u.off += int64(n)
	return n, err
}

func (u *uploadFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		u.off = offset
	case io.SeekCurrent:
		u.off += offset
	case io.SeekEnd:
		u.off = u.size() + offset
	default:
		return 0, os.ErrInvalid
	}
	if u.off < 0 {
		u.off = 0
	}
	return u.off, nil
}

func (u *uploadFile) size() int64 {
	st, err := u.tmp.Stat()
	if err != nil {
		return u.off
	}
	return st.Size()
}

func (u *uploadFile) Stat() (os.FileInfo, error) {
	return fileInfo{name: u.name, size: u.size(), mtime: time.Now()}, nil
}

func (u *uploadFile) Read([]byte) (int, error) { return 0, os.ErrInvalid }
func (u *uploadFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

// Close finalises the upload. Any error here is what the client sees as the
// PUT result, so it must not be swallowed.
func (u *uploadFile) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	defer os.Remove(u.tmpPath)
	size := u.size()
	if err := u.tmp.Sync(); err != nil {
		_ = u.tmp.Close()
		return err
	}
	if _, err := u.tmp.Seek(0, io.SeekStart); err != nil {
		_ = u.tmp.Close()
		return err
	}
	defer u.tmp.Close()

	// WebDAV 的 PUT 可能来自请求上下文中途取消的场景（客户端断开），但文件
	// 已经完整收到，所以上传用独立的超时上下文，避免半截文件。
	ctx := u.ctx
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	u.fs.logf("WebDAV：开始上传 %s（%d 字节）", u.name, size)
	name, err := u.fs.be.UploadReader(ctx, u.parentID, u.name, u.tmp, size, api.UploadOptions{
		PartSize: u.fs.opt.PartSize,
	})
	if err != nil {
		u.fs.logf("WebDAV：上传 %s 失败：%v", u.name, err)
		return err
	}
	if name != u.name {
		u.fs.logf("WebDAV：同名冲突，云端把 %q 存成了 %q", u.name, name)
	}
	u.fs.Invalidate(u.parentID)
	u.fs.logf("WebDAV：上传完成 %s（%d 字节）", name, size)
	return nil
}

// Interface assertions: a compile error here means the webdav.Handler contract
// changed under us.
var (
	_ webdav.FileSystem = (*FS)(nil)
	_ webdav.File       = (*readFile)(nil)
	_ webdav.File       = (*dirFile)(nil)
	_ webdav.File       = (*uploadFile)(nil)
	_                   = fmt.Sprintf
)
