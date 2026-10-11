package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// Node is one entry of the mounted tree. It doubles as the FUSE inode
// implementation and as the local bookkeeping for not-yet-uploaded files.
type Node struct {
	fs.Inode
	cfg *Options

	mu       sync.Mutex
	fileID   string // remote object id; empty while the file is local-only
	parentID string
	name     string
	isDir    bool
	size     int64
	mtime    time.Time
	ctime    time.Time

	staged    string // staging file holding local data
	dirty     bool   // staging file differs from the cloud
	uploading bool

	// Overlay write model (borrowed from CloudDrive2): when editing an object
	// that already exists in the cloud, staged holds only the written ranges
	// and baseID keeps streaming the rest. extents says which ranges are local.
	baseID   string // cloud object the local edits are based on ("" = none)
	baseSize int64  // size of that cloud object
	extents  extents

	// directory cache
	dirLoaded  bool
	dirAt      time.Time
	dirEntries map[string]api.File
	dirOrder   []string
	// stagedChildren holds entries created locally and not yet uploaded.
	stagedChildren map[string]*Node
}

var _ fs.NodeGetattrer = (*Node)(nil)

func newDirNode(cfg *Options, fileID, parentID, name string, mtime time.Time) *Node {
	return &Node{
		cfg: cfg, fileID: fileID, parentID: parentID, name: name,
		isDir: true, mtime: mtime, ctime: mtime,
		dirEntries: map[string]api.File{}, stagedChildren: map[string]*Node{},
	}
}

func newFileNode(cfg *Options, f api.File, parentID string) *Node {
	return &Node{
		cfg: cfg, fileID: f.ID, parentID: parentID, name: f.Name,
		size: f.Size, mtime: parseTime(f.Modified), ctime: parseTime(f.Created),
	}
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Now()
}

func (n *Node) mode() uint32 {
	if n.isDir {
		return syscall.S_IFDIR | n.cfg.Owner.DirPerm
	}
	return syscall.S_IFREG | n.cfg.Owner.FilePerm
}

func (n *Node) fillAttr(a *fuse.Attr) {
	a.Mode = n.mode()
	a.Size = uint64(n.size)
	a.Blocks = (uint64(n.size) + 511) / 512
	a.Blksize = 1 << 20
	a.Owner = fuse.Owner{Uid: n.cfg.Owner.UID, Gid: n.cfg.Owner.GID}
	if n.isDir {
		a.Nlink = 2
	} else {
		a.Nlink = 1
	}
	mt := n.mtime
	if mt.IsZero() {
		mt = time.Now()
	}
	a.SetTimes(&mt, &mt, &mt)
}

// Getattr implements fs.NodeGetattrer.
func (n *Node) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fillAttr(&out.Attr)
	return 0
}

// ------------------------------------------------------------ directory layer

// list returns the directory content, using the cache unless refresh is set.
func (n *Node) list(ctx context.Context, refresh bool) (map[string]api.File, []string, syscall.Errno) {
	n.mu.Lock()
	if !n.isDir {
		n.mu.Unlock()
		return nil, nil, syscall.ENOTDIR
	}
	if n.dirLoaded && !refresh && time.Since(n.dirAt) < n.cfg.ttl() {
		entries, order := n.dirEntries, n.dirOrder
		n.mu.Unlock()
		return entries, order, 0
	}
	id := n.fileID
	n.mu.Unlock()

	files, err := n.cfg.Backend.List(ctx, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, syscall.ENOENT
		}
		n.cfg.logf("列目录失败 (%s): %v", n.displayPath(), err)
		return nil, nil, syscall.EIO
	}

	entries := make(map[string]api.File, len(files))
	order := make([]string, 0, len(files))
	for _, f := range files {
		if f.Name == "" {
			continue
		}
		entries[f.Name] = f
		order = append(order, f.Name)
	}

	n.mu.Lock()
	n.dirEntries, n.dirOrder, n.dirLoaded, n.dirAt = entries, order, true, time.Now()
	staged := make([]string, 0, len(n.stagedChildren))
	for name := range n.stagedChildren {
		if _, exists := entries[name]; !exists {
			staged = append(staged, name)
		}
	}
	n.mu.Unlock()

	order = append(order, staged...)
	return entries, order, 0
}

func (n *Node) invalidate() {
	n.mu.Lock()
	n.dirLoaded = false
	n.mu.Unlock()
}

func (n *Node) displayPath() string {
	root := n.Root()
	if root == nil {
		return "/" + n.name
	}
	return n.Path(root)
}

// Lookup implements fs.NodeLookuper.
func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return nil, syscall.ENOENT
	}
	child, errno := n.child(ctx, name)
	if errno != 0 {
		return nil, errno
	}
	child.mu.Lock()
	child.fillAttr(&out.Attr)
	st := fs.StableAttr{Mode: child.mode()}
	child.mu.Unlock()
	return n.NewInode(ctx, child, st), 0
}

func (n *Node) child(ctx context.Context, name string) (*Node, syscall.Errno) {
	n.mu.Lock()
	if sc := n.stagedChildren[name]; sc != nil {
		n.mu.Unlock()
		return sc, 0
	}
	n.mu.Unlock()

	entries, _, errno := n.list(ctx, false)
	if errno != 0 {
		return nil, errno
	}
	f, ok := entries[name]
	if !ok {
		return nil, syscall.ENOENT
	}
	n.mu.Lock()
	parentID := n.fileID
	n.mu.Unlock()

	if f.IsDir {
		return newDirNode(n.cfg, f.ID, parentID, f.Name, parseTime(f.Modified)), 0
	}
	return newFileNode(n.cfg, f, parentID), 0
}

// Readdir implements fs.NodeReaddirer.
func (n *Node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	_, order, errno := n.list(ctx, false)
	if errno != 0 {
		return nil, errno
	}
	n.mu.Lock()
	entries := n.dirEntries
	staged := make(map[string]*Node, len(n.stagedChildren))
	for k, v := range n.stagedChildren {
		staged[k] = v
	}
	n.mu.Unlock()

	dirs := make([]fuse.DirEntry, 0, len(order)+2)
	for _, name := range order {
		if sc := staged[name]; sc != nil {
			dirs = append(dirs, fuse.DirEntry{Name: name, Mode: syscall.S_IFDIR & 0})
			continue
		}
		f, ok := entries[name]
		if !ok {
			continue
		}
		mode := uint32(syscall.S_IFREG)
		if f.IsDir {
			mode = syscall.S_IFDIR
		}
		dirs = append(dirs, fuse.DirEntry{Name: name, Mode: mode})
	}
	for name := range staged {
		dirs = append(dirs, fuse.DirEntry{Name: name, Mode: syscall.S_IFREG})
	}
	return fs.NewListDirStream(dirs), 0
}

// ------------------------------------------------------------- mutating nodes

// Mkdir implements fs.NodeMkdirer.
func (n *Node) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if n.cfg.ReadOnly {
		return nil, syscall.EROFS
	}
	n.mu.Lock()
	parentID := n.fileID
	n.mu.Unlock()
	stored, err := n.cfg.Backend.Mkdir(ctx, parentID, name)
	if err != nil {
		n.cfg.logf("新建目录失败 (%s): %v", name, err)
		return nil, syscall.EIO
	}
	n.invalidate()
	entries, _, errno := n.list(ctx, true)
	if errno != 0 {
		return nil, errno
	}
	f, ok := entries[stored]
	if !ok {
		f = api.File{Name: stored, IsDir: true, ID: stored}
	}
	child := newDirNode(n.cfg, f.ID, parentID, stored, time.Now())
	child.mu.Lock()
	child.fillAttr(&out.Attr)
	st := fs.StableAttr{Mode: child.mode()}
	child.mu.Unlock()
	return n.NewInode(ctx, child, st), 0
}

// Create implements fs.NodeCreater: a new file starts life in the staging dir.
func (n *Node) Create(ctx context.Context, name string, _ uint32, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if n.cfg.ReadOnly {
		return nil, nil, 0, syscall.EROFS
	}
	if existing, errno := n.child(ctx, name); errno == 0 && existing != nil {
		existing.mu.Lock()
		isDir := existing.isDir
		existing.mu.Unlock()
		if isDir {
			return nil, nil, 0, syscall.EISDIR
		}
	}

	n.mu.Lock()
	parentID := n.fileID
	n.mu.Unlock()

	stage, err := os.CreateTemp(n.cfg.StageDir, "cmccyun-*")
	if err != nil {
		n.cfg.logf("创建暂存文件失败: %v", err)
		return nil, nil, 0, syscall.EIO
	}

	child := &Node{
		cfg: n.cfg, parentID: parentID, name: name,
		staged: stage.Name(), dirty: true, mtime: time.Now(), ctime: time.Now(),
	}
	n.mu.Lock()
	n.stagedChildren[name] = child
	n.mu.Unlock()

	fh := &fileHandle{node: child, stage: stage, writable: true, dirty: true}

	child.mu.Lock()
	child.fillAttr(&out.Attr)
	st := fs.StableAttr{Mode: child.mode()}
	child.mu.Unlock()
	return n.NewInode(ctx, child, st), fh, 0, 0
}

// Unlink implements fs.NodeUnlinker.
func (n *Node) Unlink(ctx context.Context, name string) syscall.Errno {
	if n.cfg.ReadOnly {
		return syscall.EROFS
	}
	n.mu.Lock()
	sc := n.stagedChildren[name]
	if sc != nil {
		delete(n.stagedChildren, name)
	}
	n.mu.Unlock()
	if sc != nil {
		sc.mu.Lock()
		staged := sc.staged
		sc.staged, sc.dirty, sc.fileID = "", false, ""
		sc.mu.Unlock()
		if staged != "" {
			os.Remove(staged)
		}
		if errno := dropStagingSidecars(sc); errno != 0 {
			return errno
		}
		return 0
	}

	child, errno := n.child(ctx, name)
	if errno != 0 {
		return errno
	}
	child.mu.Lock()
	id, isDir := child.fileID, child.isDir
	child.mu.Unlock()
	if isDir {
		return syscall.EISDIR
	}
	if id == "" {
		return syscall.ENOENT
	}
	if err := n.cfg.Backend.Trash(ctx, []string{id}); err != nil {
		n.cfg.logf("删除失败 (%s): %v", name, err)
		return syscall.EIO
	}
	n.invalidate()
	return 0
}

// Rmdir implements fs.NodeRmdirer.
func (n *Node) Rmdir(ctx context.Context, name string) syscall.Errno {
	if n.cfg.ReadOnly {
		return syscall.EROFS
	}
	child, errno := n.child(ctx, name)
	if errno != 0 {
		return errno
	}
	child.mu.Lock()
	id, isDir, staged := child.fileID, child.isDir, child.staged
	child.mu.Unlock()
	if !isDir {
		return syscall.ENOTDIR
	}
	if staged != "" {
		return syscall.ENOTEMPTY
	}
	entries, order, errno := child.list(ctx, true)
	if errno != 0 {
		return errno
	}
	if len(order) > 0 || len(entries) > 0 {
		return syscall.ENOTEMPTY
	}
	if err := n.cfg.Backend.Trash(ctx, []string{id}); err != nil {
		n.cfg.logf("删除目录失败 (%s): %v", name, err)
		return syscall.EIO
	}
	n.invalidate()
	child.invalidate()
	return 0
}

// Rename implements fs.NodeRenamer.
func (n *Node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	if n.cfg.ReadOnly {
		return syscall.EROFS
	}
	dst, ok := newParent.(*Node)
	if !ok {
		return syscall.EXDEV
	}
	if strings.ContainsRune(newName, '/') || newName == "" {
		return syscall.EINVAL
	}

	n.mu.Lock()
	src := n.stagedChildren[name]
	n.mu.Unlock()

	// Drop an existing destination first: FUSE rename replaces it.
	if existing, errno := dst.child(ctx, newName); errno == 0 && existing != nil {
		existing.mu.Lock()
		exID, exStaged := existing.fileID, existing.staged
		existing.mu.Unlock()
		if exStaged != "" {
			dst.mu.Lock()
			delete(dst.stagedChildren, newName)
			dst.mu.Unlock()
			os.Remove(exStaged)
		} else if exID != "" {
			if err := dst.cfg.Backend.Trash(ctx, []string{exID}); err != nil {
				n.cfg.logf("重命名时覆盖目标失败 (%s): %v", newName, err)
				return syscall.EIO
			}
		}
	}

	if src != nil {
		// Local-only file: nothing to tell the cloud yet.
		dst.mu.Lock()
		parentID := dst.fileID
		dst.mu.Unlock()
		src.mu.Lock()
		src.name, src.parentID = newName, parentID
		src.mu.Unlock()
		n.mu.Lock()
		delete(n.stagedChildren, name)
		n.mu.Unlock()
		dst.mu.Lock()
		dst.stagedChildren[newName] = src
		dst.mu.Unlock()
		n.invalidate()
		dst.invalidate()
		return 0
	}

	child, errno := n.child(ctx, name)
	if errno != 0 {
		return errno
	}
	child.mu.Lock()
	srcID, srcIsDir, oldName := child.fileID, child.isDir, child.name
	child.mu.Unlock()
	if srcID == "" {
		return syscall.ENOENT
	}
	if srcIsDir && child.hasChildren(ctx) {
		// Keep the operation honest: the service cannot merge directories.
		if _, errno := dst.child(ctx, newName); errno == 0 {
			return syscall.ENOTEMPTY
		}
	}
	if err := n.cfg.Backend.Move(ctx, []string{srcID}, dstID(dst)); err != nil {
		n.cfg.logf("移动失败 (%s -> %s): %v", name, newName, err)
		return syscall.EIO
	}
	if oldName != newName {
		if err := n.cfg.Backend.Rename(ctx, srcID, newName); err != nil {
			n.cfg.logf("重命名失败 (%s -> %s): %v", name, newName, err)
			return syscall.EIO
		}
	}
	child.mu.Lock()
	child.name, child.parentID = newName, dstID(dst)
	child.mu.Unlock()
	n.invalidate()
	dst.invalidate()
	return 0
}

func dstID(n *Node) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.fileID
}

func (n *Node) hasChildren(ctx context.Context) bool {
	entries, order, errno := n.list(ctx, false)
	if errno != 0 {
		return true
	}
	return len(order) > 0 || len(entries) > 0
}

// Setattr implements fs.NodeSetattrer (truncate and timestamp updates).
func (n *Node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if sz, ok := in.GetSize(); ok {
		if n.cfg.ReadOnly {
			return syscall.EROFS
		}
		if errno := n.truncate(ctx, int64(sz)); errno != 0 {
			return errno
		}
	}
	if mtime, ok := in.GetMTime(); ok {
		n.mu.Lock()
		n.mtime = mtime
		n.mu.Unlock()
	}
	n.mu.Lock()
	n.fillAttr(&out.Attr)
	n.mu.Unlock()
	return 0
}

func (n *Node) truncate(ctx context.Context, size int64) syscall.Errno {
	n.mu.Lock()
	staged := n.staged
	isDir := n.isDir
	base := n.baseID
	n.mu.Unlock()
	if isDir {
		return syscall.EISDIR
	}
	if size < 0 {
		return syscall.EINVAL
	}

	if staged == "" {
		if errno := n.beginEdit(ctx); errno != 0 {
			return errno
		}
		n.mu.Lock()
		staged, base = n.staged, n.baseID
		n.mu.Unlock()
	}

	if base == "" {
		// A brand new (or fully local) file: the staging file is the file.
		if err := os.Truncate(staged, size); err != nil {
			n.cfg.logf("截断文件失败: %v", err)
			return syscall.EIO
		}
	} else {
		n.mu.Lock()
		switch {
		case size < n.size:
			n.extents = n.extents.trim(size)
		case size > n.size:
			n.extents = n.extents.add(extent{off: n.size, length: size - n.size, zero: true})
		}
		n.mu.Unlock()
	}

	n.mu.Lock()
	n.size = size
	n.mtime = time.Now()
	n.dirty = true
	n.mu.Unlock()
	return 0
}

// beginEdit prepares a node for local modification. Editing a file that already
// lives in the cloud does NOT download it: the staging file starts empty and
// the untouched ranges keep streaming from the cloud (CloudDrive2's model).
func (n *Node) beginEdit(_ context.Context) syscall.Errno {
	n.mu.Lock()
	if n.staged != "" {
		n.mu.Unlock()
		return 0
	}
	fileID, size := n.fileID, n.size
	n.mu.Unlock()

	f, err := os.CreateTemp(n.cfg.StageDir, "cmccyun-*")
	if err != nil {
		n.cfg.logf("创建暂存文件失败: %v", err)
		return syscall.EIO
	}
	name := f.Name()
	f.Close()

	n.mu.Lock()
	switch {
	case n.staged != "":
		// Someone else got here first.
		n.mu.Unlock()
		os.Remove(name)
		return 0
	case fileID != "":
		n.staged = name
		n.baseID = fileID
		n.baseSize = size
	default:
		n.staged = name
	}
	n.mu.Unlock()
	return 0
}

// addExtent records that [off, off+length) now lives in the staging file.
// It only matters in overlay mode (a file based on a cloud object); a file that
// was created locally keeps its whole content in the staging file.
func (n *Node) addExtent(off, length int64) {
	if length <= 0 {
		return
	}
	n.mu.Lock()
	if n.baseID != "" {
		n.extents = n.extents.add(extent{off: off, length: length})
	}
	n.mu.Unlock()
}

// overlayReader composes the node's local edits with the untouched cloud bytes.
// staged must be an open handle on n.staged (the caller keeps it open). The
// returned overlay keeps a live view of the node, so ranges written later
// through the same handle are read back from the staging file.
func (n *Node) overlayReader(_ context.Context, staged *os.File) *overlay {
	n.mu.Lock()
	ov := &overlay{
		node:     n,
		size:     n.size,
		baseSize: n.baseSize,
		staged:   staged,
		ext:      append(extents(nil), n.extents...),
	}
	base := n.baseID
	n.mu.Unlock()
	if base != "" {
		ov.remote = &remoteStream{be: n.cfg.Backend, fileID: base}
	}
	return ov
}

// Statfs implements fs.NodeStatfser.
func (n *Node) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	const bsize = 4096
	total := n.cfg.Capacity
	if total == 0 {
		total = 2 << 40
	}
	out.Bsize = bsize
	out.Frsize = bsize
	out.Blocks = total / bsize
	out.Bfree = out.Blocks / 2
	out.Bavail = out.Bfree
	out.Files = 1 << 32
	out.Ffree = 1 << 31
	out.NameLen = 255
	return 0
}

func dropStagingSidecars(*Node) syscall.Errno { return 0 }

// ------------------------------------------------------------- file handles

// fileHandle serves one open file. Reads come from the staging file when local
// data exists, otherwise straight from the cloud with HTTP range requests.
type fileHandle struct {
	node     *Node
	stage    *os.File
	writable bool
	dirty    bool

	// overlay is set when the handle serves a file whose untouched bytes are
	// still in the cloud; nil means the staging file (or the cloud stream) is
	// the whole content.
	overlay *overlay

	mu     sync.Mutex
	stream *remoteStream
	closed bool
}

var (
	_ fs.FileReader    = (*fileHandle)(nil)
	_ fs.FileWriter    = (*fileHandle)(nil)
	_ fs.FileFlusher   = (*fileHandle)(nil)
	_ fs.FileReleaser  = (*fileHandle)(nil)
	_ fs.FileFsyncer   = (*fileHandle)(nil)
	_ fs.FileGetattrer = (*fileHandle)(nil)
)

// Open implements fs.NodeOpener.
func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	n.mu.Lock()
	isDir, staged, fileID, size, base := n.isDir, n.staged, n.fileID, n.size, n.baseID
	n.mu.Unlock()
	if isDir {
		return nil, 0, syscall.EISDIR
	}

	accMode := flags & syscall.O_ACCMODE
	writable := accMode == syscall.O_WRONLY || accMode == syscall.O_RDWR
	if writable && n.cfg.ReadOnly {
		return nil, 0, syscall.EROFS
	}

	fh := &fileHandle{node: n, writable: writable}

	if writable {
		if staged == "" {
			if errno := n.beginEdit(ctx); errno != 0 {
				return nil, 0, errno
			}
			n.mu.Lock()
			staged, base = n.staged, n.baseID
			n.mu.Unlock()
		}
		f, err := os.OpenFile(staged, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			n.cfg.logf("打开暂存文件失败: %v", err)
			return nil, 0, syscall.EIO
		}
		fh.stage = f
		if flags&syscall.O_TRUNC != 0 && size > 0 {
			if base == "" {
				if err := f.Truncate(0); err != nil {
					f.Close()
					return nil, 0, syscall.EIO
				}
			} else {
				n.mu.Lock()
				n.extents = nil
				n.mu.Unlock()
			}
			n.mu.Lock()
			n.size = 0
			n.mu.Unlock()
			fh.dirty = true
			n.mu.Lock()
			n.dirty = true
			n.mu.Unlock()
		}
		if base != "" {
			fh.overlay = n.overlayReader(ctx, f)
		}
		return fh, fuse.FOPEN_KEEP_CACHE, 0
	}

	if staged != "" {
		f, err := os.Open(staged)
		if err != nil {
			return nil, 0, syscall.EIO
		}
		fh.stage = f
		if base != "" {
			fh.overlay = n.overlayReader(ctx, f)
		}
		return fh, fuse.FOPEN_KEEP_CACHE, 0
	}
	if fileID == "" {
		return nil, 0, syscall.ENOENT
	}
	fh.stream = &remoteStream{be: n.cfg.Backend, fileID: fileID}
	return fh, fuse.FOPEN_KEEP_CACHE, 0
}

// Read implements fs.FileReader.
func (h *fileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if h.overlay != nil {
		n, err := h.overlay.readAt(ctx, dest, off)
		if err != nil && err != io.EOF {
			h.node.cfg.logf("读取失败 (%s @%d): %v", h.node.displayPath(), off, err)
			return nil, syscall.EIO
		}
		return fuse.ReadResultData(dest[:n]), 0
	}
	if h.stage != nil {
		n, err := h.stage.ReadAt(dest, off)
		if err != nil && err != io.EOF {
			return nil, syscall.EIO
		}
		return fuse.ReadResultData(dest[:n]), 0
	}
	if h.stream == nil {
		return nil, syscall.EBADF
	}
	n, err := h.stream.readAt(ctx, dest, off)
	if err != nil && err != io.EOF {
		h.node.cfg.logf("读取失败 (%s @%d): %v", h.node.displayPath(), off, err)
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(dest[:n]), 0
}

// Write implements fs.FileWriter.
func (h *fileHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if !h.writable {
		return 0, syscall.EBADF
	}
	if h.stage == nil {
		return 0, syscall.EIO
	}
	n, err := h.stage.WriteAt(data, off)
	if n > 0 {
		h.node.addExtent(off, int64(n))
	}
	if err != nil {
		return uint32(n), syscall.EIO
	}
	h.dirty = true
	h.node.mu.Lock()
	if end := off + int64(n); end > h.node.size {
		h.node.size = end
	}
	h.node.dirty = true
	h.node.mtime = time.Now()
	h.node.mu.Unlock()
	return uint32(n), 0
}

// Getattr implements fs.FileGetattrer.
func (h *fileHandle) Getattr(_ context.Context, out *fuse.AttrOut) syscall.Errno {
	h.node.mu.Lock()
	defer h.node.mu.Unlock()
	h.node.fillAttr(&out.Attr)
	return 0
}

// Fsync implements fs.FileFsyncer: the cloud has no fsync, so this is where we
// actually push the data.
func (h *fileHandle) Fsync(ctx context.Context, _ uint32) syscall.Errno { return flush(ctx, h) }

// Flush implements fs.FileFlusher.
func (h *fileHandle) Flush(ctx context.Context) syscall.Errno { return flush(ctx, h) }

func flush(ctx context.Context, h *fileHandle) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.writable || (!h.dirty && !h.node.isDirty()) {
		return 0
	}
	if h.stage != nil {
		h.stage.Sync()
	}
	if errno := uploadStaged(ctx, h.node); errno != 0 {
		return errno
	}
	h.dirty = false
	return 0
}

func (n *Node) isDirty() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dirty
}

// Release implements fs.FileReleaser.
func (h *fileHandle) Release(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0
	}
	h.closed = true
	h.mu.Unlock()

	var errno syscall.Errno
	if h.writable && (h.dirty || h.node.isDirty()) {
		errno = uploadStaged(ctx, h.node)
	}
	if h.stage != nil {
		h.stage.Close()
	}
	if h.overlay != nil && h.overlay.remote != nil {
		h.overlay.remote.close()
	}
	if h.stream != nil {
		h.stream.close()
	}
	return errno
}

// uploadStaged pushes the staged content to the cloud and rewires the node to
// the resulting remote object. When the node was edited in overlay mode the
// new object is assembled from the cloud ranges plus the local ranges, without
// ever materialising the whole file on local disk.
func uploadStaged(ctx context.Context, n *Node) syscall.Errno {
	n.mu.Lock()
	if n.uploading {
		n.mu.Unlock()
		return 0
	}
	staged, parentID, name := n.staged, n.parentID, n.name
	oldID, baseID, size := n.fileID, n.baseID, n.size
	if staged == "" {
		n.dirty = false
		n.mu.Unlock()
		return 0
	}
	n.uploading = true
	n.mu.Unlock()

	defer func() {
		n.mu.Lock()
		n.uploading = false
		n.mu.Unlock()
	}()

	localSize := int64(0)
	if st, err := os.Stat(staged); err == nil {
		localSize = st.Size()
	}
	if size == 0 && localSize == 0 && oldID == "" {
		// A brand new file that has not received any byte yet: nothing to push.
		// Keep the staging file — the kernel flushes an O_TRUNC open *before*
		// the first write arrives, and the data written right after that flush
		// must still land here.
		n.mu.Lock()
		n.dirty = false
		n.mu.Unlock()
		return 0
	}

	var stored string
	var err error
	if baseID != "" {
		f, openErr := os.Open(staged)
		if openErr != nil {
			n.cfg.logf("打开暂存文件失败 (%s): %v", n.displayPath(), openErr)
			return syscall.EIO
		}
		ov := n.overlayReader(ctx, f)
		ov.ctx = ctx
		ov.size = size
		stored, err = n.cfg.Backend.UploadReader(ctx, parentID, name, ov, size, api.UploadOptions{})
		f.Close()
	} else {
		stored, err = n.cfg.Backend.UploadFile(ctx, parentID, name, staged, api.UploadOptions{})
	}
	if err != nil {
		n.cfg.logf("上传失败 (%s): %v", n.displayPath(), err)
		return syscall.EIO
	}

	// Remove the object this file replaced, then make sure the new object is
	// reachable under the user-visible name.
	if oldID != "" {
		if err := n.cfg.Backend.Trash(ctx, []string{oldID}); err != nil {
			n.cfg.logf("清理旧版本失败 (%s): %v", name, err)
		}
	}

	files, err := n.cfg.Backend.List(ctx, parentID)
	if err != nil {
		n.cfg.logf("上传后刷新目录失败: %v", err)
		return syscall.EIO
	}
	var found *api.File
	for i := range files {
		if files[i].Name == stored {
			found = &files[i]
			break
		}
	}
	if found == nil {
		return syscall.EIO
	}
	if stored != name && oldID != "" {
		if err := n.cfg.Backend.Rename(ctx, found.ID, name); err != nil {
			n.cfg.logf("上传后改名失败 (%s): %v", name, err)
			return syscall.EIO
		}
	}
	api.WaitSettle()

	n.mu.Lock()
	n.fileID = found.ID
	n.size = found.Size
	n.mtime = parseTime(found.Modified)
	n.dirty = false
	n.staged = ""
	n.baseID = ""
	n.baseSize = 0
	n.extents = nil
	_, parent := n.Parent()
	n.mu.Unlock()

	os.Remove(staged)
	if parent != nil {
		if pn, ok := parent.Operations().(*Node); ok {
			pn.mu.Lock()
			delete(pn.stagedChildren, name)
			pn.mu.Unlock()
			pn.invalidate()
		}
	}
	return 0
}

// remoteStream keeps one HTTP range request open and serves sequential reads
// from it, reopening only when the access pattern jumps.
type remoteStream struct {
	be     Backend
	fileID string
	window int64 // bytes fetched per range request (>= one read call)

	mu    sync.Mutex
	rc    io.ReadCloser
	pos   int64 // current read position (file offset of next byte)
	limit int64 // end of the currently open range window
}

// defaultWindow is the size of a single range request. Sequential readers
// (media playback, cp, tar) keep one request open and stream through it;
// the connection is reopened only when the reader seeks.
const defaultWindow = 8 << 20

func (r *remoteStream) readAt(ctx context.Context, dest []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for total < len(dest) {
		cur := off + int64(total)
		// (Re)open a range request when there is none, when the reader seeks
		// (backwards or past the window), or when the window is exhausted.
		if r.rc == nil || cur < r.pos || cur >= r.limit {
			r.closeLocked()
			length := r.window
			if length <= 0 {
				length = defaultWindow
			}
			if int64(len(dest)) > length {
				length = int64(len(dest))
			}
			rc, err := r.be.Download(ctx, r.fileID, cur, length)
			if err != nil {
				if total > 0 {
					return total, nil
				}
				return 0, err
			}
			r.rc, r.pos, r.limit = rc, cur, cur+length
		}
		// Skip forward inside the window (readahead holes) before consuming.
		if cur > r.pos {
			if _, err := io.CopyN(io.Discard, r.rc, cur-r.pos); err != nil {
				r.closeLocked()
				break
			}
			r.pos = cur
		}
		n, err := r.rc.Read(dest[total:])
		r.pos += int64(n)
		total += n
		if err != nil {
			r.closeLocked()
			break // EOF or error: stop, a fresh window would reach the same end
		}
		if n == 0 { // defensive: no progress
			r.closeLocked()
			break
		}
	}
	if total == 0 {
		return 0, io.EOF
	}
	return total, nil
}

func (r *remoteStream) closeLocked() {
	if r.rc != nil {
		r.rc.Close()
		r.rc = nil
	}
	r.pos, r.limit = 0, 0
}

func (r *remoteStream) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

// ---------------------------------------------------------------- mount glue

// Mount attaches the file system at mountpoint and returns the running server.
func Mount(mountpoint string, cfg *Options) (*fuse.Server, error) {
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.StageDir, 0o700); err != nil {
		return nil, err
	}

	root := newDirNode(cfg, api.RootID, "", "", time.Now())
	root.name = ""

	entry := time.Second
	attr := time.Second
	neg := time.Second

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:    allowOther(),
			FsName:        "cmccyun",
			Name:          "cmccyun",
			MaxWrite:      1 << 20,
			MaxReadAhead:  1 << 19,
			DisableXAttrs: true,
			DirectMount:   os.Geteuid() == 0,
			Options:       []string{"default_permissions", "ro"},
		},
		EntryTimeout:    &entry,
		AttrTimeout:     &attr,
		NegativeTimeout: &neg,
		NullPermissions: true,
		UID:             cfg.Owner.UID,
		GID:             cfg.Owner.GID,
	}
	if !cfg.ReadOnly {
		opts.MountOptions.Options = []string{"default_permissions"}
	}

	return fs.Mount(mountpoint, root, opts)
}

// allowOther decides whether the mount may be shared with users other than the
// one performing the mount. As root it always works; as an unprivileged user
// fusermount refuses unless /etc/fuse.conf enables user_allow_other.
func allowOther() bool {
	if os.Geteuid() == 0 {
		return true
	}
	data, err := os.ReadFile("/etc/fuse.conf")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "user_allow_other") {
			return true
		}
	}
	return false
}

// Unmount detaches a previously mounted file system. It first tries the
// syscall (works as root) and falls back to the setuid fusermount helper. When
// the mount is still busy it retries lazily, which is what a service stop wants:
// the daemon is going away anyway.
func Unmount(mountpoint string) error {
	err := syscall.Unmount(mountpoint, 0)
	if err == nil {
		return nil
	}
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EINVAL {
		return fmt.Errorf("not a mount point: %s", mountpoint)
	}
	firstErr := err
	var last string
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if p, err := exec.LookPath(bin); err == nil {
			if out, err := exec.Command(p, "-u", mountpoint).CombinedOutput(); err == nil {
				return nil
			} else {
				last = fmt.Sprintf("%s -u %s: %v: %s", bin, mountpoint, err, strings.TrimSpace(string(out)))
			}
		}
	}
	if err := syscall.Unmount(mountpoint, syscall.MNT_DETACH); err == nil {
		return nil
	}
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if p, err := exec.LookPath(bin); err == nil {
			if _, err := exec.Command(p, "-uz", mountpoint).CombinedOutput(); err == nil {
				return nil
			}
		}
	}
	if last != "" {
		return fmt.Errorf("unmount %s 失败: %v (%s)", mountpoint, firstErr, last)
	}
	return fmt.Errorf("unmount %s 失败: %v", mountpoint, firstErr)
}

// IsMountPoint reports whether something is currently mounted on dir.
func IsMountPoint(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		data, err = os.ReadFile("/proc/mounts")
		if err != nil {
			return false
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// /proc/self/mountinfo: id parent major:minor root mountpoint ...
		// /proc/mounts:         device mountpoint fstype options ...
		if unescapeMount(fields[4]) == abs || unescapeMount(fields[1]) == abs {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// Dead 判断 dir 上的挂载是不是「躺在地上」了：上一轮进程被强杀（kill -9）、掉电
// 或者 OOM 之后，/proc/mounts 里还留着记录，但任何访问都直接返回 ENOTCONN
// （transport endpoint is not connected）。
//
// 这个判断很重要：只看 IsMountPoint 会把这种残留当成「已经挂好了」，于是启动时
// 跳过挂载，用户在「文件」里看到的是一个读不了的空壳。
func Dead(dir string) bool {
	if dir == "" || !IsMountPoint(dir) {
		return false
	}
	// getattr 一次就够：对端没了的话，内核立刻回 ENOTCONN，不会卡住。
	if _, err := os.Stat(dir); err != nil {
		return errors.Is(err, syscall.ENOTCONN)
	}
	return false
}

// StageFilePath is exported for diagnostics.
func StageFilePath(dir, name string) string { return filepath.Join(dir, name) }
