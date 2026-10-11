package app

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/vfs"
)

// 挂载位置管理：让用户在管理台里挑任意位置（例如自己「我的文件」下的目录），
// 而不是固定挂在应用自己的共享目录里。
//
// 为什么允许挂在任意位置：飞牛把用户可见的空间放在 /volN/<uid>（个人空间）等
// 目录下，应用共享目录（/volN/@appshare/<app>）在「文件」里通常不好找。用户
// 想把云盘挂到自己的个人空间里是合理需求，本文件负责把「选哪、能不能挂、
// 挂了会不会遮住东西」这些判断集中起来，避免误操作。

// volumeRootRe 匹配飞牛存储卷根：/vol1、/vol2 … /vol99（含两位数的 /vol02）。
var volumeRootRe = func(name string) bool {
	if !strings.HasPrefix(name, "vol") || len(name) < 4 || len(name) > 5 {
		return false
	}
	for _, r := range name[3:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// systemNames 是飞牛的保留目录名：位置选择器里默认折叠，避免用户误挂。
var systemNames = map[string]bool{
	"@appcenter": true, "@appconf": true, "@appdata": true, "@apphome": true,
	"@appmeta": true, "@appshare": true, "@apptemp": true,
	"lost+found": true, "thumb": true, "vm": true, "appcenter-downloads": true,
}

// VolumeRoots 返回存在且允许选择的存储卷根（/vol1、/vol2…）。
// CMCCYUN_EXTRA_ROOTS（冒号分隔）可以追加自定义根，方便非标准布局与测试。
//
// 只认「真的挂上了文件系统的卷」：飞牛会在根目录预建 /vol00、/vol01、/vol02 这类
// 占位目录（里面甚至有空壳的个人空间目录），但它们其实是系统盘上的普通目录——
// 挂进去既不会出现在「文件」里，还会把系统盘塞满，所以不给选。
func VolumeRoots() []string {
	var out []string
	ents, err := os.ReadDir("/")
	if err == nil {
		for _, e := range ents {
			if e.IsDir() && volumeRootRe(e.Name()) {
				p := "/" + e.Name()
				if !vfs.IsMountPoint(p) {
					continue
				}
				out = append(out, p)
			}
		}
	}
	for _, p := range strings.Split(os.Getenv("CMCCYUN_EXTRA_ROOTS"), ":") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, filepath.Clean(p))
		}
	}
	sort.Strings(out)
	return dedupPaths(out)
}

func dedupPaths(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = filepath.Clean(v)
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// IsVolumeRoot 判断路径是否正好是某个存储卷根。
func IsVolumeRoot(p string) bool {
	return volumeRootRe(strings.TrimPrefix(filepath.Clean(p), "/"))
}

// volumeOf 返回路径所在的存储卷根（/vol4/1000/移动云盘 → /vol4）；不在任何卷下时 ""。
func volumeOf(p string) string {
	p = normMountPath(p)
	if p == "" {
		return ""
	}
	for _, root := range VolumeRoots() {
		if underRoot(p, root) {
			return root
		}
	}
	return ""
}

func underRoot(p string, root string) bool {
	root = filepath.Clean(root)
	if p == root {
		return true
	}
	return strings.HasPrefix(p+"/", root+"/")
}

func underAnyRoot(p string) bool {
	for _, r := range VolumeRoots() {
		if underRoot(p, r) {
			return true
		}
	}
	return false
}

// mountRecord 是 /proc/self/mounts 里的一行。
type mountRecord struct {
	target string
	fstype string
}

// mountTable 读取当前挂载表（解析 \040 之类的转义）。
func mountTable() []mountRecord {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return nil
	}
	var out []mountRecord
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		out = append(out, mountRecord{target: unescapeMountPath(f[1]), fstype: f[2]})
	}
	return out
}

func unescapeMountPath(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// isOurFS 判断某个挂载是不是本应用挂的：vfs.Mount 用的 fsname 是 cmccyun，
// 内核会把它显示成 fuse.cmccyun（或 cmccyun 直连）。
func isOurFS(fstype string) bool {
	return fstype == "fuse.cmccyun" || fstype == "cmccyun"
}

// mountOf 返回覆盖 path 的挂载点（最长匹配）以及它是否正好就是 path。
func mountOf(path string) (mountRecord, bool) {
	path = filepath.Clean(path)
	var best mountRecord
	found := false
	for _, m := range mountTable() {
		if underRoot(path, m.target) {
			if !found || len(m.target) > len(best.target) {
				best, found = m, true
			}
		}
	}
	return best, found
}

func isNetworkOrFuseFS(fstype string) bool {
	if strings.HasPrefix(fstype, "fuse") {
		return true
	}
	switch fstype {
	case "nfs", "nfs4", "cifs", "smb3", "smbfs", "sshfs", "9p", "ceph", "glusterfs", "davfs", "webdav":
		return true
	}
	return false
}

// DirEntry 是位置选择器中的一行。
type DirEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Owner    string `json:"owner"`
	OwnerUID uint32 `json:"owner_uid"`
	OwnerGID uint32 `json:"owner_gid"`
	Mode     string `json:"mode"`
	IsMount  bool   `json:"is_mount"`
	Empty    bool   `json:"empty"`
	System   bool   `json:"system"`
	IsVolume bool   `json:"is_volume"`
}

// MountPointInfo 描述一个候选挂载位置。
type MountPointInfo struct {
	Path       string `json:"path"`
	Parent     string `json:"parent"`
	Exists     bool   `json:"exists"`
	Empty      bool   `json:"empty"`
	Entries    int    `json:"entries"`
	IsMount    bool   `json:"is_mount"`
	MountType  string `json:"mount_type,omitempty"`
	Owner      string `json:"owner,omitempty"`
	SuggestUID uint32 `json:"suggest_uid"`
	SuggestGID uint32 `json:"suggest_gid"`
	Mode       string `json:"mode,omitempty"`
	SystemDir  bool   `json:"system_dir"`
	// Masked 表示这个位置在「当前挂载点」里面：它的真身被挂载遮住了，
	// 现在看到的是云盘的内容。挂载时会先卸下当前挂载点再看真实目录。
	Masked   bool     `json:"masked,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func normMountPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = "/" + p
	}
	return filepath.Clean(p)
}

func ownerOf(path string) (uint32, uint32, os.FileMode, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, false
	}
	uid, gid := uint32(0), uint32(0)
	if sys, ok := statOwner(st); ok {
		uid, gid = sys[0], sys[1]
	}
	return uid, gid, st.Mode().Perm(), true
}

// primaryGID 返回某个 uid 的主组（/etc/passwd 里的 gid）。
// fnOS 个人空间里的目录基本都是 user:Users，而 /volN/<uid> 本身属主是
// <用户>:root —— 所以挂载点属主按「用户的主组」来，才和系统里其它目录一致。
func primaryGID(uid uint32) (uint32, bool) {
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return 0, false
	}
	g, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(g), true
}

// suggestOwner 把「父目录的属主」修正成推荐给挂载点用的 uid/gid。
func suggestOwner(uid, gid uint32) (uint32, uint32) {
	if uid == 0 {
		return uid, gid
	}
	if pg, ok := primaryGID(uid); ok {
		return uid, pg
	}
	return uid, gid
}

func ownerName(uid uint32) string {
	if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil && u.Username != "" {
		return u.Username
	}
	return strconv.FormatUint(uint64(uid), 10)
}

// dirCount 统计目录里的条目数（只读目录本身，不递归）。
func dirCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	return len(names), nil
}

// ensureMountDir 建挂载目录：属主跟随父目录（这样挂在用户自己的个人空间里时，
// 目录在「文件」里看起来就是用户自己的），权限 0755。
func ensureMountDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	parent := filepath.Dir(path)
	if uid, gid, _, ok := ownerOf(parent); ok && uid != 0 {
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			return fmt.Errorf("设置目录属主失败: %w", err)
		}
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("设置目录权限失败: %w", err)
	}
	return nil
}

// validateMountPath 是「与运行状态无关」的语法级校验：绝对路径、在存储卷下、
// 不在卷根、不与运行目录重叠。
//
// 注意：目标落在「当前挂载点」里面的情况**不算语法错误**——那是可以做的，
// 只是要先把当前挂载点卸下来才看得到真实目录（见 checkMountPath / SetMountPoint）。
// 只有 Update()（直接改配置、不换挂载）才需要禁止它，因此单独用 allowInside=false 走一遍。
//
// 不带后缀的版本自己取锁；Update 那条路径已经持锁，必须用 ...Locked 版本。
func (s *Service) validateMountPath(p string) error {
	return s.validateMountPathOpt(p, false)
}

func (s *Service) validateMountPathLocked(p string) error {
	return s.validateMountPathOptLocked(p, false)
}

// validateMountPathOpt: allowInside=true 时允许目标位于当前挂载点内部。
func (s *Service) validateMountPathOpt(p string, allowInside bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateMountPathOptLocked(p, allowInside)
}

// validateMountPathOptLocked 要求调用者已持有 s.mu（只读 s.cfg 与 s.mountedAt）。
func (s *Service) validateMountPathOptLocked(p string, allowInside bool) error {
	if strings.TrimSpace(p) == "" {
		return nil // 允许清空
	}
	if !filepath.IsAbs(strings.TrimSpace(p)) {
		return fmt.Errorf("挂载位置要填绝对路径，例如 /vol4/1000/移动云盘（当前：%s）", strings.TrimSpace(p))
	}
	n := normMountPath(p)
	if n == "/" {
		return fmt.Errorf("不能挂在根目录")
	}
	if IsVolumeRoot(n) {
		return fmt.Errorf("不能直接挂在存储卷根目录（%s）：那样会遮住卷里已有的目录，请在其下选一个目录", n)
	}
	if !underAnyRoot(n) {
		roots := strings.Join(VolumeRoots(), "、")
		if roots == "" {
			roots = "/vol1 … /vol99"
		}
		return fmt.Errorf("只能选择飞牛存储卷下的位置（%s），当前：%s", roots, n)
	}
	// 判断依据是「实际挂着的那里」，不是配置里写的目标位置。
	// 这里读的是 mountedAt/cfg，所以只允许用 ...Locked 版本（本函数要求持锁）。
	if cur := s.curMountPointLocked(); cur != "" && !allowInside && n != cur && underRoot(n, cur) {
		return fmt.Errorf("不能直接把配置改成当前挂载点（%s）内部——那样旧挂载会变成看不见的僵尸。要用「挂到这里」按钮切换，它会先卸下旧位置再挂", cur)
	}
	return nil
}

// CheckMountPath 校验一个候选挂载位置。它只读，不会创建或挂载任何东西。
func (s *Service) CheckMountPath(path string, allowNonEmpty bool) (*MountPointInfo, error) {
	return s.checkMountPath(s.Config(), path, allowNonEmpty)
}

func (s *Service) checkMountPath(cfg config.Config, path string, allowNonEmpty bool) (*MountPointInfo, error) {
	p := normMountPath(path)
	if p == "" {
		if err := s.validateMountPathOpt(p, false); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("请先选择挂载位置")
	}
	// 目标在「当前挂载点」里面？它的真身被挂载遮着，现在看到的是云盘内容，
	// 所以既不能 stat 也不能判定是否非空——挂载时先卸下再看（见 SetMountPoint）。
	curMount := s.curMountPoint()
	masked := curMount != "" && p != curMount && underRoot(p, curMount)
	if err := s.validateMountPathOpt(p, masked); err != nil {
		return nil, err
	}
	info := &MountPointInfo{Path: p, Parent: filepath.Dir(p), SystemDir: false, Masked: masked}
	if masked {
		info.IsMount = true // 父级是我们的挂载
		info.MountType = "fuse.cmccyun"
		info.Empty = true
		info.Exists = false // 真身未知：卸下来之后才知道在不在
		info.Warnings = append(info.Warnings,
			fmt.Sprintf("这个位置在当前的挂载点 %s 里面（现在看到的是云盘内容）：挂到这里时会先卸下 %s，再看/建真实目录，然后挂上", curMount, curMount))
		if uid, gid, _, ok := ownerOf(filepath.Dir(curMount)); ok {
			info.Owner = ownerName(uid)
			info.SuggestUID, info.SuggestGID = suggestOwner(uid, gid)
		}
		return info, nil
	}

	// 就是想挂回当前位置：直接告知已挂载，后面按重新挂载处理。
	cur := filepath.Clean(cfg.MountPoint)
	if cfg.MountPoint != "" && p == cur {
		info.Exists = true
		info.IsMount = vfs.IsMountPoint(p)
		info.MountType = "fuse.cmccyun"
		if uid, gid, mode, ok := ownerOf(p); ok {
			info.Owner = ownerName(uid)
			info.SuggestUID, info.SuggestGID = suggestOwner(uid, gid)
			info.Mode = fmt.Sprintf("%04o", mode)
		}
		// 挂着的时候数的是「透过挂载看到的内容」，没挂就是目录本身的内容；
		// 两种情况都要给出真实数字，否则前端会把 0 当成「非空」。
		if n, err := dirCount(p); err == nil {
			info.Entries, info.Empty = n, n == 0
		}
		return info, nil
	}

	// 拒绝应用自己的配置/暂存目录：既不能挂在里面，也不能挂在它们的上层
	// （挂上层同样会把运行数据遮住）。
	for _, reserved := range []string{cfg.StageDir, filepath.Dir(s.cfgPath), cfg.LogFile} {
		if reserved == "" {
			continue
		}
		r := filepath.Clean(reserved)
		if underRoot(p, r) || underRoot(r, p) {
			return nil, fmt.Errorf("%s 与应用运行目录（%s）重叠，不能作为挂载位置", p, r)
		}
	}

	// 已经有别的挂载占着这个目录？
	if m, ok := mountOf(p); ok && filepath.Clean(m.target) == p {
		info.IsMount = true
		info.MountType = m.fstype
		if m.fstype != "fuse.cmccyun" {
			return nil, fmt.Errorf("%s 已经是一个挂载点（%s），请换一个目录", p, m.fstype)
		}
	}

	// 父目录不能落在别的 FUSE/网络挂载里（不支持嵌套挂载）。
	if m, ok := mountOf(filepath.Dir(p)); ok && isNetworkOrFuseFS(m.fstype) && m.fstype != "fuse.cmccyun" {
		return nil, fmt.Errorf("上级目录 %s 是挂载点（%s），不支持在它里面再挂载一层", m.target, m.fstype)
	}

	if uid, gid, mode, ok := ownerOf(p); ok {
		info.Exists = true
		info.Owner = ownerName(uid)
		info.SuggestUID, info.SuggestGID = suggestOwner(uid, gid)
		info.Mode = fmt.Sprintf("%04o", mode)
		info.SystemDir = isSystemPath(p)
		if n, err := dirCount(p); err == nil {
			info.Entries = n
			info.Empty = n == 0
		}
		if !info.IsMount && !info.Empty && !allowNonEmpty {
			names := sampleNames(p, 3)
			return nil, fmt.Errorf("目标目录 %s 里已经有 %d 项（%s…）：挂载后这些内容会被遮住看不到。确认要挂在这里，请勾选「我确认遮住原有内容」", p, info.Entries, names)
		}
	} else if _, err := os.Stat(filepath.Dir(p)); err != nil {
		return nil, fmt.Errorf("上级目录不存在：%s", filepath.Dir(p))
	}

	if info.SystemDir {
		info.Warnings = append(info.Warnings, "这是应用/系统目录（@…），在「文件」里通常不方便找到")
	}
	if !info.Exists {
		// 属主按父目录推断，方便前端预填 UID/GID。
		if uid, gid, _, ok := ownerOf(info.Parent); ok {
			info.Owner = ownerName(uid)
			info.SuggestUID, info.SuggestGID = suggestOwner(uid, gid)
		}
	}
	return info, nil
}

func isNumericName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isSystemPath(p string) bool {
	for _, part := range strings.Split(filepath.Clean(p), "/") {
		if systemNames[part] || strings.HasPrefix(part, "@") {
			return true
		}
	}
	return false
}

func sampleNames(dir string, max int) string {
	f, err := os.Open(dir)
	if err != nil {
		return ""
	}
	defer f.Close()
	names, err := f.Readdirnames(max)
	if err != nil && len(names) == 0 {
		return ""
	}
	if len(names) > max {
		names = names[:max]
	}
	return strings.Join(names, "、")
}

// BrowseDirs 列出 path 下的子目录，供位置选择器逐级浏览。
// 返回当前路径、上级路径与条目列表；path 为空或 "/" 时列出所有存储卷。
func (s *Service) BrowseDirs(path string) (string, string, []DirEntry, error) {
	cur := normMountPath(path)
	if cur == "" || cur == "/" {
		var out []DirEntry
		for _, root := range VolumeRoots() {
			e := DirEntry{Name: filepath.Base(root), Path: root, IsVolume: true}
			if uid, gid, mode, ok := ownerOf(root); ok {
				e.Owner, e.OwnerUID, e.OwnerGID, e.Mode = ownerName(uid), uid, gid, fmt.Sprintf("%04o", mode)
			}
			if m, ok := mountOf(root); ok && filepath.Clean(m.target) == root {
				e.IsMount = true
			}
			out = append(out, e)
		}
		return "/", "", out, nil
	}
	if !underAnyRoot(cur) {
		return "", "", nil, fmt.Errorf("只能浏览飞牛存储卷下的位置（%s）", strings.Join(VolumeRoots(), "、"))
	}
	ents, err := os.ReadDir(cur)
	if err != nil {
		return "", "", nil, fmt.Errorf("无法读取 %s：%w", cur, err)
	}
	out := make([]DirEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		p := filepath.Join(cur, name)
		isDir := e.IsDir()
		if !isDir {
			// 允许跟进指向目录的软链接（飞牛里常见）。
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		d := DirEntry{Name: name, Path: p, System: systemNames[name] || strings.HasPrefix(name, "@") || strings.HasPrefix(name, ".")}
		if uid, gid, mode, ok := ownerOf(p); ok {
			d.Owner, d.OwnerUID, d.OwnerGID, d.Mode = ownerName(uid), uid, gid, fmt.Sprintf("%04o", mode)
		}
		if m, ok := mountOf(p); ok && filepath.Clean(m.target) == p {
			d.IsMount = true
		}
		if n, err := dirCount(p); err == nil {
			d.Empty = n == 0
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := filepath.Dir(cur)
	if !underAnyRoot(parent) {
		parent = "/"
	}
	return cur, parent, out, nil
}

// CreateMountDir 在 parent 下新建一个目录（位置选择器里的「新建文件夹」）。
func (s *Service) CreateMountDir(parent, name string) (string, error) {
	p := normMountPath(parent)
	if !underAnyRoot(p) {
		return "", fmt.Errorf("只能建在飞牛存储卷下（%s）", strings.Join(VolumeRoots(), "、"))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("请输入文件夹名")
	}
	// 父目录在当前挂载点里面（含挂载点本身）：这里的写入会落到云盘上
	// （只读挂载则直接失败），也会被挂载遮住看不见。真实目录只有卸下后才动得了，
	// 所以让「挂到这里」去处理（它会先在卸载窗口里建好目录）。
	if cur := s.curMountPoint(); cur != "" && underRoot(p, cur) {
		return "", fmt.Errorf("%s 在当前挂载点（%s）里面：在这里新建会建到云盘上或被遮住。别用新建，直接点「挂到这里」，它会先卸下原位置、再建好真实目录并挂上", p, cur)
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("文件夹名不能包含斜杠")
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
		return "", fmt.Errorf("文件夹名不能以 . 或 @ 开头")
	}
	target := filepath.Join(p, name)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("已存在同名目录：%s", target)
	}
	if err := ensureMountDir(target); err != nil {
		return "", err
	}
	return target, nil
}

// SetMountPointOptions 控制切换挂载位置的行为。
type SetMountPointOptions struct {
	Create        bool    `json:"create"`         // 目录不存在则创建
	AllowNonEmpty bool    `json:"allow_nonempty"` // 允许目标目录里已有内容（会遮住）
	FollowOwner   bool    `json:"follow_owner"`   // 把文件属主设成目标目录的属主
	UID           *uint32 `json:"uid,omitempty"`
	GID           *uint32 `json:"gid,omitempty"`
}

// SetMountPoint 把云盘换挂到 path：先校验、必要时建目录，然后落配置并重新挂载；
// 挂载失败会把配置（和新目录属主）回滚到原样，并尽力挂回旧位置。
func (s *Service) SetMountPoint(ctx context.Context, path string, opt SetMountPointOptions) error {
	info, err := s.CheckMountPath(path, opt.AllowNonEmpty)
	if err != nil {
		return err
	}
	// 目标在当前挂载点里面（典型场景：云盘挂在了个人空间根 /vol4/1000，
	// 想挪进它下面的「移动云盘」子目录）。此时真身被遮住，必须先卸下来，
	// 才能看到 / 创建真实目录——不然目录会被建到云盘里，或者因只读直接失败。
	unmounted := false
	if info.Masked {
		old := s.curMountPoint()
		s.logf("目标 %s 在当前挂载点 %s 里面，先卸下再核对真实目录", info.Path, old)
		if err := s.Unmount(); err != nil {
			return fmt.Errorf("要先卸下当前挂载点 %s 才能挂到它里面，卸载失败：%w", old, err)
		}
		unmounted = true
		var cerr error
		info, cerr = s.CheckMountPath(path, opt.AllowNonEmpty)
		if cerr != nil {
			if rerr := s.Remount(ctx); rerr != nil {
				return fmt.Errorf("目标位置不可用（%v），且原位置 %s 没能挂回去：%v", cerr, old, rerr)
			}
			return fmt.Errorf("目标位置不可用：%w（原位置 %s 已挂回）", cerr, old)
		}
	}
	if !info.Exists {
		if !opt.Create {
			if unmounted {
				if rerr := s.Remount(ctx); rerr != nil {
					return fmt.Errorf("目录不存在：%s（可勾选「目录不存在时自动创建」），且原位置没能挂回去：%v", info.Path, rerr)
				}
			}
			return fmt.Errorf("目录不存在：%s（可勾选「目录不存在时自动创建」）", info.Path)
		}
		if err := ensureMountDir(info.Path); err != nil {
			if unmounted {
				if rerr := s.Remount(ctx); rerr != nil {
					s.logf("创建目录失败后，原位置没能挂回：%v", rerr)
				}
			}
			return err
		}
		s.logf("已创建挂载目录 %s（属主 %s）", info.Path, info.Owner)
	}

	// 先把旧位置卸干净，再改配置：卸载要按「原来的挂载点」进行，所以必须发生在
	// 改 s.cfg / 落盘之前，否则旧挂载会被漏掉（切换挂载点最经典的坑）。
	if err := s.Unmount(); err != nil {
		return fmt.Errorf("原位置卸载失败，请先关掉正在使用它的程序再试：%w", err)
	}

	s.mu.Lock()
	before := *s.cfg
	s.cfg.MountPoint = info.Path
	s.cfg.AllowNonEmpty = opt.AllowNonEmpty
	if opt.FollowOwner {
		s.cfg.UID, s.cfg.GID = info.SuggestUID, info.SuggestGID
	}
	if opt.UID != nil {
		s.cfg.UID = *opt.UID
	}
	if opt.GID != nil {
		s.cfg.GID = *opt.GID
	}
	err = config.Save(s.cfgPath, s.cfg)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	if err := s.Remount(ctx); err != nil {
		// 回滚配置并尝试挂回原位置，避免留下「配置指向新位置但实际没挂上」的状态。
		s.mu.Lock()
		s.cfg.MountPoint, s.cfg.AllowNonEmpty = before.MountPoint, before.AllowNonEmpty
		s.cfg.UID, s.cfg.GID = before.UID, before.GID
		_ = config.Save(s.cfgPath, s.cfg)
		old := before.MountPoint
		s.mu.Unlock()
		if old == "" {
			return fmt.Errorf("挂载到 %s 失败（已回滚配置）：%w", info.Path, err)
		}
		if uerr := s.Unmount(); uerr != nil {
			s.logf("回滚前卸载 %s 失败: %v", info.Path, uerr)
		}
		if rerr := s.Remount(ctx); rerr != nil {
			return fmt.Errorf("挂载到 %s 失败，且旧位置 %s 也没能挂回：%v / %v", info.Path, old, err, rerr)
		}
		return fmt.Errorf("挂载到 %s 失败，已挂回原位置 %s：%v", info.Path, old, err)
	}
	s.logf("挂载位置已切换到 %s", info.Path)
	return nil
}
