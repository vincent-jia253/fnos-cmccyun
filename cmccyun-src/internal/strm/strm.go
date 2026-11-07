// Package strm 把云盘里的影视文件映射成本地 .strm 文件。
//
// 为什么是 .strm：飞牛影视和 Emby 都支持这种「一行文本就是一个视频源」的
// 索引文件（内容是本地路径或 http 直链），播放器拿到它之后自己再去拉流。
// 这样云盘里几十 GB 的片子不用落盘，媒体库却能正常刮削、出海报、记录进度。
//
// 三条设计取舍：
//   - 增量：不重扫就重写，靠上一次的快照（按 fileId 比 Rel/Size/Mod）做 diff，
//     只有新增/改名/大小变了才落盘，定时任务每分钟跑也不心疼。
//   - 幂等：内容一样就不动文件（连 mtime 都不碰），否则媒体库会以为整库都变了，
//     每轮都重新探测一遍，硬盘和 CPU 都受不了。
//   - 可删：云端删掉的片子，本地 .strm 一并清掉（DeleteMissing），
//     不然媒体库里永远挂着一堆点开就 404 的条目。
package strm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// Backend 是云盘的最小只读接口（*api.Client 直接满足）。
type Backend interface {
	List(ctx context.Context, parentID string) ([]api.File, error)
}

// DefaultExts 是默认当作「影视文件」的扩展名。
var DefaultExts = []string{
	"mp4", "mkv", "avi", "ts", "m2ts", "mov", "wmv", "flv",
	"rmvb", "rm", "m4v", "webm", "mpg", "mpeg", "iso",
}

// Options 是一次扫描的全部参数。
type Options struct {
	Backend Backend

	// CloudRootID 是这次要扫的云端目录 id（"" 或 "/" 表示根）。
	CloudRootID string
	// OutputDir 是本地输出根目录。
	OutputDir string

	// Mode 是 strm 内容的形式：
	//   path = 本地挂载路径（依赖 FUSE 挂载在线）
	//   url  = http 直链（指向本应用的 /api/strm/play）
	Mode string
	// MountPrefix 是 path 模式下云盘挂载到本机的位置。
	MountPrefix string
	// URLBase 是 url 模式下的直链前缀（形如 http://192.168.1.2:19898/api/strm/play）。
	// 只写前缀，不要自己带 ?xxx=，参数统一由这里拼。
	URLBase string
	// AccountID 是 url 模式带的账号（a= 参数），多账号时播放才知道用哪台的授权。
	AccountID string
	// Token 是 url 模式带的访问口令（k= 参数），空表示不带。
	Token string

	// Exts 是视频扩展名白名单（不带点，小写）。空 = DefaultExts。
	Exts []string
	// DeleteMissing 表示云端没了的条目要删掉本地 .strm。
	DeleteMissing bool

	// MaxEntries 是单次扫描的条目上限（防呆：误把整个云盘根目录加进来时，
	// 别让一次扫描把接口打爆）。0 = 50000。
	MaxEntries int

	Logf func(string, ...any)
}

// Item 是快照里的一条记录（fileId 为键）。
type Item struct {
	ID   string `json:"id"`
	Rel  string `json:"rel"` // 相对云端根目录的路径，用 / 分隔
	Size int64  `json:"size"`
	Mod  string `json:"mod,omitempty"`
}

// State 是落盘的快照，用来做增量。
type State struct {
	CloudRootID string          `json:"cloud_root_id"`
	OutputDir   string          `json:"output_dir"`
	Mode        string          `json:"mode"`
	Items       map[string]Item `json:"items"`
	ScannedAt   time.Time       `json:"scanned_at"`
}

// Result 是一次扫描的结果，直接喂给控制台。
type Result struct {
	Total     int      `json:"total"`
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Removed   int      `json:"removed"`
	Skipped   int      `json:"skipped"`
	Truncated bool     `json:"truncated"`
	Errors    []string `json:"errors,omitempty"`
}

// Options.normalized 填默认值。
func (o *Options) normalized() {
	if o.CloudRootID == "" {
		o.CloudRootID = api.RootID
	}
	if o.Mode == "" {
		o.Mode = "path"
	}
	if len(o.Exts) == 0 {
		o.Exts = DefaultExts
	}
	exts := make([]string, 0, len(o.Exts))
	for _, e := range o.Exts {
		e = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(e, ".")))
		if e != "" {
			exts = append(exts, e)
		}
	}
	o.Exts = exts
	if o.MaxEntries <= 0 {
		o.MaxEntries = 50000
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// Run 扫一遍云端目录，把差异落到本地 .strm，返回新快照。
//
// prev 为 nil 表示首次运行（所有条目都算新增）。
func Run(ctx context.Context, o Options, prev *State) (*State, Result, error) {
	o.normalized()
	if o.OutputDir == "" || !filepath.IsAbs(o.OutputDir) {
		return nil, Result{}, fmt.Errorf("输出目录必须是绝对路径")
	}
	if o.Mode == "path" && o.MountPrefix == "" {
		return nil, Result{}, fmt.Errorf("path 模式需要知道挂载点")
	}
	if o.Mode == "url" && o.URLBase == "" {
		return nil, Result{}, fmt.Errorf("url 模式需要知道本机地址")
	}

	res := Result{}
	cur := map[string]Item{}
	truncated, err := walk(ctx, o, "", o.CloudRootID, cur, &res)
	if err != nil {
		return nil, res, err
	}
	res.Total = len(cur)
	res.Truncated = truncated

	// 旧快照（只信和我们同源的那份）。
	if prev != nil && prev.Items != nil {
		for id, old := range prev.Items {
			now, still := cur[id]
			if still {
				// 改名/挪目录：同一 fileId 换了 Rel，旧路径的文件要清掉。
				if old.Rel != now.Rel && strings.TrimSpace(old.Rel) != "" {
					_ = os.Remove(strmPath(o.OutputDir, old.Rel))
					pruneEmpty(filepath.Dir(strmPath(o.OutputDir, old.Rel)), o.OutputDir)
				}
				continue
			}
			// 云端没了。
			if o.DeleteMissing {
				if err := os.Remove(strmPath(o.OutputDir, old.Rel)); err == nil {
					res.Removed++
					pruneEmpty(filepath.Dir(strmPath(o.OutputDir, old.Rel)), o.OutputDir)
				} else if !os.IsNotExist(err) {
					res.note("删除 %s 失败: %v", old.Rel, err)
				}
			}
		}
	}

	// 写盘：按 Rel 排序，日志和文件系统顺序都稳定。
	ids := make([]string, 0, len(cur))
	for id := range cur {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return cur[ids[i]].Rel < cur[ids[j]].Rel })

	for _, id := range ids {
		it := cur[id]
		dst := strmPath(o.OutputDir, it.Rel)
		want := render(o, it)

		if data, err := os.ReadFile(dst); err == nil && string(data) == want {
			// 内容没变：连 mtime 都不碰，免得媒体库以为库变了。
			if prev != nil {
				if _, ok := prev.Items[id]; ok {
					res.Skipped++
					continue
				}
			}
			res.Skipped++
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.note("创建目录失败 %s: %v", filepath.Dir(dst), err)
			continue
		}
		if err := writeFileAtomic(dst, want); err != nil {
			res.note("写入 %s 失败: %v", dst, err)
			continue
		}
		if prev != nil {
			if _, ok := prev.Items[id]; ok {
				res.Updated++
			} else {
				res.Added++
			}
		} else {
			res.Added++
		}
	}

	st := &State{
		CloudRootID: o.CloudRootID,
		OutputDir:   o.OutputDir,
		Mode:        o.Mode,
		Items:       cur,
		ScannedAt:   time.Now(),
	}
	o.Logf("STRM：扫描完成，共 %d 个视频（新增 %d / 更新 %d / 删除 %d / 未变 %d）",
		res.Total, res.Added, res.Updated, res.Removed, res.Skipped)
	return st, res, nil
}

// walk 广度优先遍历云端目录，把视频文件塞进 out。
func walk(ctx context.Context, o Options, rel, id string, out map[string]Item, res *Result) (bool, error) {
	type qitem struct{ rel, id string }
	queue := []qitem{{rel: rel, id: id}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		files, err := o.Backend.List(ctx, cur.id)
		if err != nil {
			return false, fmt.Errorf("读取云盘目录 %s 失败: %w", displayRel(cur.rel), err)
		}
		for _, f := range files {
			childRel := path.Join(cur.rel, f.Name)
			if f.IsDir {
				queue = append(queue, qitem{rel: childRel, id: f.ID})
				continue
			}
			if !matchExt(f.Name, o.Exts) {
				continue
			}
			if len(out) >= o.MaxEntries {
				return true, nil
			}
			out[f.ID] = Item{ID: f.ID, Rel: childRel, Size: f.Size, Mod: f.Modified}
		}
	}
	return false, nil
}

// render 生成 .strm 的内容（一行 + 换行，UTF-8 无 BOM）。
func render(o Options, it Item) string {
	var line string
	if o.Mode == "url" {
		// 参数一律用 url.Values 拼：早期版本是手工 "%s?a=%s" + "?id=..." 两段接起来的，
		// 结果生成出 ".../play?a=default?id=…&k=…" 这种两个问号的地址，
		// 播放器把 id 当成 a 的一部分，点开就是 400。顺序不敏感，服务端都按 key 取。
		q := url.Values{}
		q.Set("id", it.ID)
		if o.Token != "" {
			q.Set("k", o.Token)
		}
		if o.AccountID != "" {
			q.Set("a", o.AccountID)
		}
		line = o.URLBase + "?" + q.Encode()
	} else {
		// path 模式直接写本地绝对路径（UTF-8 原样）：媒体库读的是文件系统，
		// 百分号编码过的路径在本地是找不到的。中文路径老版本影视解析不了，
		// 那种情况请用 url 模式。
		line = filepath.Join(o.MountPrefix, filepath.FromSlash(it.Rel))
	}
	return line + "\n"
}

// strmPath 把云端相对路径映射到本地 .strm 路径。
func strmPath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel)+".strm")
}

// matchExt 判断文件名是不是白名单里的视频。
func matchExt(name string, exts []string) bool {
	dot := strings.LastIndex(name, ".")
	if dot < 0 || dot == len(name)-1 {
		return false
	}
	ext := strings.ToLower(name[dot+1:])
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// writeFileAtomic 先写 .tmp 再 rename：媒体库轮询时不会读到半个文件。
func writeFileAtomic(dst, content string) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// pruneEmpty 自底向上删掉空目录（只删 OutputDir 以内的，别越界）。
func pruneEmpty(dir, root string) {
	root = filepath.Clean(root)
	for {
		dir = filepath.Clean(dir)
		if dir == root || !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil || len(ents) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func displayRel(rel string) string {
	if rel == "" {
		return "/"
	}
	return "/" + rel
}

func (r *Result) note(format string, args ...any) {
	if len(r.Errors) < 20 {
		r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
	}
}

// LoadState / SaveState 读写快照文件（控制台和调度器共用）。
func LoadState(p string) *State {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil
	}
	if st.Items == nil {
		st.Items = map[string]Item{}
	}
	return &st
}

// SaveState 原子写快照。
func SaveState(p string, st *State) error {
	if st == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
