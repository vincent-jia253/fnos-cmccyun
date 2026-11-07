package app

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
	"github.com/vincent-jia253/fnos-cmccyun/internal/strm"
	"github.com/vincent-jia253/fnos-cmccyun/internal/vfs"
)

// STRM 插件：把云端影视文件映射成本地 .strm，给飞牛影视 / Emby 当索引用。
//
// 为什么做在应用里而不是独立进程：它只要一个 api.Client（同一份授权、同一套
// 自动续期），独立进程得再维护一份登录态，出了问题是双倍的。
//
// 锁：strmMu 只保护「上一轮扫描的状态」这几个字段，和 s.mu 完全无关；
// 扫描过程（网络 + 写盘）一律不持锁，否则控制台又会卡成 v1.2.1 那个样子。

// StrmPairResult 是一条映射的扫描结果。
type StrmPairResult struct {
	Cloud      string   `json:"cloud"`
	Local      string   `json:"local"`
	Total      int      `json:"total"`
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Removed    int      `json:"removed"`
	Skipped    int      `json:"skipped"`
	Truncated  bool     `json:"truncated"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// strmStatePath 每条映射一个快照文件（放在配置目录里，不污染媒体库）。
// 多账号下把账号 ID 也算进哈希，两台云盘的同一路径互不干扰。
func (s *Service) strmStatePath(id string, p config.StrmPair) string {
	sum := sha1.Sum([]byte(id + "|" + p.Cloud + "|" + p.Local))
	return filepath.Join(filepath.Dir(s.cfgPath), "strm-state", hex.EncodeToString(sum[:6])+".json")
}

// migrateLegacyStrmState 把 v1.6 的快照文件改名成带账号 ID 的新名字。
// 不做这一步的话，升级后第一次扫描会因为「找不到旧快照」把所有 .strm 当新增
// 重写一遍——内容一模一样，但 mtime 全变，媒体库白白整库重扫一次。
func (s *Service) migrateLegacyStrmState(id string, p config.StrmPair) {
	if id != "" && id != config.DefaultAccountID {
		return
	}
	cur := s.strmStatePath(id, p)
	if _, err := os.Stat(cur); err == nil {
		return // 已经是新名字（或这台账号扫过一轮了）
	}
	sum := sha1.Sum([]byte(p.Cloud + "|" + p.Local))
	legacy := filepath.Join(filepath.Dir(s.cfgPath), "strm-state", hex.EncodeToString(sum[:6])+".json")
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	if err := os.Rename(legacy, cur); err != nil {
		s.logf("迁移 STRM 快照 %s 失败（本轮会把该映射当首次扫描）：%v", legacy, err)
		return
	}
	s.logf("已把 STRM 快照从旧命名迁移到 %s，本轮不会重写未变动的 .strm", filepath.Base(cur))
}

// strmPlayURL 是 url 模式写进 .strm 的直链前缀。
// 取第一个内网地址：媒体服务一般和本应用在同一台机器/同一网段。
func (s *Service) strmPlayURL(cfg config.Config, id string) string {
	host := "127.0.0.1"
	if ips := localIPv4(); len(ips) > 0 {
		host = ips[0]
	}
	// 只返回前缀：a= / id= / k= 由 strm.render 用 url.Values 一次拼好。
	// 这里原来自己接了 "?a=<id>"，和后面接的 "?id=…" 撞成两个问号（v1.7.0 的 bug）。
	return fmt.Sprintf("http://%s:%d/api/strm/play", host, cfg.WebPort)
}

// strmWaitsMount 判断「现在扫会写错内容，得等挂载就绪」。
//
// auto 模式按「挂载在不在」挑本地路径还是直链，但开机时挂载和 STRM 定时器是同时
// 起步的：先扫一轮就会把 .strm 全改写成直链，等挂载上来又改回路径——媒体库每次
// 开机都整库重扫。auto 且开着自动挂载时，挂载没就绪就先不扫（30 秒后再试）。
func strmWaitsMount(cfg config.Config, ac config.AccountConfig) bool {
	if ac.StrmMode != "" && ac.StrmMode != "auto" {
		return false // 用户明确选了 path / url，尊重它
	}
	if ac.MountPoint == "" || !cfg.AutoMount {
		return false
	}
	return !vfs.IsMountPoint(ac.MountPoint)
}

// errStrmNotMounted 表示这一轮被跳过了（不是失败），调度器别记「到点时间」。
var errStrmNotMounted = errors.New("挂载还没就绪，等挂好再扫")

// strmMode 决定这次用哪种内容形式：auto 时挂载在线用 path，否则退回 url。
func strmMode(ac config.AccountConfig) string {
	switch ac.StrmMode {
	case "path", "url":
		return ac.StrmMode
	}
	if ac.MountPoint != "" && vfs.IsMountPoint(ac.MountPoint) {
		return "path"
	}
	return "url"
}

// resolveCloudPath 把「/01影视/电影」这种路径解析成 fileId。
// 139 没有路径→id 的接口，只能逐级 list。
func resolveCloudPath(ctx context.Context, c *api.Client, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return api.RootID, nil
	}
	id := api.RootID
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", fmt.Errorf("云端路径不能包含 ..")
		}
		files, err := c.List(ctx, id)
		if err != nil {
			return "", fmt.Errorf("读取云端目录失败: %w", err)
		}
		found, isDir := "", false
		for _, f := range files {
			if f.Name == seg {
				found, isDir = f.ID, f.IsDir
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("云端没有 %q 这个目录（路径 %s）", seg, p)
		}
		if !isDir {
			return "", fmt.Errorf("云端路径 %s 里的 %q 是文件，不是目录", p, seg)
		}
		id = found
	}
	return id, nil
}

// runStrm 是真正干活的那一层（不带运行态标记，测试可以直接调）。
// strmMountPrefix 把「挂载点」和「映射的云端目录」拼成 path 模式下 strm 内容的前缀。
//
// strm.Run 拼的是 前缀 + 相对映射根的路径，而挂载点对应的是云端根（「我的文件」）；
// 只拼挂载点会漏掉映射目录本身，生成一堆指向不存在文件的 strm。
// 云端路径为 / 或空时（映射整个云盘）自然退化为挂载点根。
func strmMountPrefix(mountPoint, cloudPath string) string {
	if mountPoint == "" {
		return ""
	}
	rel := strings.Trim(strings.TrimSpace(cloudPath), "/")
	if rel == "" {
		return mountPoint
	}
	return filepath.Join(mountPoint, filepath.FromSlash(rel))
}

func (s *Service) runStrm(ctx context.Context, id string) ([]StrmPairResult, error) {
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok {
		return nil, fmt.Errorf("账号不存在：%s", id)
	}
	if !ac.StrmEnabled {
		return nil, fmt.Errorf("STRM 未启用")
	}
	if len(ac.StrmPairs) == 0 {
		return nil, fmt.Errorf("还没有配置「云端目录 → 本地目录」映射")
	}
	if strmWaitsMount(cfg, *ac) {
		return nil, errStrmNotMounted
	}
	c, err := s.connect(ctx, ac.ID)
	if err != nil {
		return nil, err
	}
	mode := strmMode(*ac)
	urlBase := s.strmPlayURL(cfg, ac.ID)
	exts := splitExts(ac.StrmExt)

	var out []StrmPairResult
	for _, pair := range ac.StrmPairs {
		pr := StrmPairResult{Cloud: pair.Cloud, Local: pair.Local}
		start := time.Now()
		rootID, err := resolveCloudPath(ctx, c, pair.Cloud)
		if err != nil {
			pr.Error = err.Error()
			pr.DurationMS = time.Since(start).Milliseconds()
			out = append(out, pr)
			s.logf("STRM：%s 解析失败：%v", pair.Cloud, err)
			continue
		}
		if err := os.MkdirAll(pair.Local, 0o755); err != nil {
			pr.Error = "创建输出目录失败: " + err.Error()
			pr.DurationMS = time.Since(start).Milliseconds()
			out = append(out, pr)
			continue
		}
		// path 模式：strm 里要写「挂载点 + 云端路径 + 相对路径」。
		// 只拼挂载点是不够的——映射的是云盘子目录，少拼这一段媒体库就找不到文件
		// （真机第一轮就踩到了：内容指向 /vol4/1000/移动云盘/学爸…）。
		mountPrefix := strmMountPrefix(ac.MountPoint, pair.Cloud)
		s.migrateLegacyStrmState(ac.ID, pair)
		statePath := s.strmStatePath(ac.ID, pair)
		prev := strm.LoadState(statePath)
		o := strm.Options{
			Backend:       c,
			CloudRootID:   rootID,
			OutputDir:     pair.Local,
			Mode:          mode,
			MountPrefix:   mountPrefix,
			URLBase:       urlBase,
			AccountID:     ac.ID,
			Token:         ac.StrmToken,
			Exts:          exts,
			DeleteMissing: ac.StrmDeleteMissing,
			Logf:          s.logf,
		}
		st, res, err := strm.Run(ctx, o, prev)
		pr.Total, pr.Added, pr.Updated = res.Total, res.Added, res.Updated
		pr.Removed, pr.Skipped, pr.Truncated = res.Removed, res.Skipped, res.Truncated
		pr.Errors = res.Errors
		pr.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			pr.Error = err.Error()
			s.logf("STRM：%s → %s 失败：%v", pair.Cloud, pair.Local, err)
		} else {
			if err := strm.SaveState(statePath, st); err != nil {
				s.logf("STRM：保存快照失败：%v", err)
			}
			s.logf("STRM：%s → %s 完成，共 %d 个（+%d ~%d -%d），%d ms",
				displayCloud(pair.Cloud), pair.Local, res.Total, res.Added, res.Updated, res.Removed, pr.DurationMS)
		}
		out = append(out, pr)
	}
	return out, nil
}

// StrmScan 触发一次扫描（控制台的「立即扫描」和后台定时都走这里）。
func (s *Service) StrmScan(ctx context.Context, id string) ([]StrmPairResult, error) {
	s.mu.Lock()
	a, ac, err := s.acctLocked(id)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	a.strmMu.Lock()
	if a.strmRunning {
		a.strmMu.Unlock()
		return nil, fmt.Errorf("上一轮扫描还没结束，稍等一下")
	}
	a.strmRunning = true
	a.strmMu.Unlock()

	res, err := s.runStrm(ctx, ac.ID)

	a.strmMu.Lock()
	a.strmRunning = false
	if !errors.Is(err, errStrmNotMounted) {
		a.strmLastAt = time.Now()
	}
	a.strmLast = res
	if err != nil {
		a.strmLastErr = err.Error()
	} else {
		a.strmLastErr = ""
	}
	a.strmMu.Unlock()
	return res, err
}

// StrmLoop 是后台调度：每 30 秒看一次有没有到点，到点就扫一轮。
// 用「看时钟」而不是固定 Ticker，是为了让控制台改间隔后立刻生效。
func (s *Service) StrmLoop(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		// 多账号：每台云盘各自按自己的间隔判定，互不影响。
		for _, ac := range s.Config().Accounts {
			if !s.strmDue(ac.ID) {
				continue
			}
			sctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			if _, err := s.StrmScan(sctx, ac.ID); err != nil && !errors.Is(err, errStrmNotMounted) {
				s.logf("STRM：定时扫描失败（账号 %s）：%v", ac.DisplayName(), err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// strmDue 判断这一轮该不该扫。
func (s *Service) strmDue(id string) bool {
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok || !ac.StrmEnabled || len(ac.StrmPairs) == 0 {
		return false
	}
	iv := time.Duration(ac.StrmIntervalMin) * time.Minute
	if iv <= 0 {
		iv = 5 * time.Minute
	}
	s.mu.Lock()
	a, _, err := s.acctLocked(ac.ID)
	s.mu.Unlock()
	if err != nil {
		return false
	}
	a.strmMu.Lock()
	defer a.strmMu.Unlock()
	if a.strmRunning {
		return false
	}
	return a.strmLastAt.IsZero() || time.Since(a.strmLastAt) >= iv
}

// StrmStatus 给控制台用：配置 + 上一轮结果 + 当前模式/挂载状态。
// StrmAccountsView 是「全部账号的 STRM 一览」：每台账号一行，只给界面要显示的
// 那几个数（开关 / 模式 / 映射数 / .strm 总数 / 上次结果），不带凭据、不带映射明细。
//
// 定时器本来就是逐台判定的（StrmLoop 遍历 accounts、各按自己的间隔），但界面上
// 一次只能看一台；多账号时想确认「第二台到底扫没扫、生成了几个」得来回切，所以
// 单独给一份总览。
func (s *Service) StrmAccountsView() []map[string]any {
	cfg := s.Config()
	out := make([]map[string]any, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		ac := &cfg.Accounts[i]
		row := map[string]any{
			"id":           ac.ID,
			"name":         ac.DisplayName(),
			"account":      ac.Account,
			"default":      ac.ID == cfg.DefaultAccount,
			"enabled":      ac.StrmEnabled,
			"mode":         strmMode(*ac),
			"mode_setting": ac.StrmMode,
			"interval_min": ac.StrmIntervalOr(),
			"pairs":        len(ac.StrmPairs),
			"strm_count":   0,
			"exists":       true,
			"running":      false,
			"last_at":      "",
			"last_error":   "",
		}
		total := 0
		for _, p := range ac.StrmPairs {
			if st, err := os.Stat(p.Local); err == nil && st.IsDir() {
				total += countStrm(p.Local)
			} else {
				row["exists"] = false
			}
		}
		row["strm_count"] = total
		s.mu.Lock()
		a, _, err := s.acctLocked(ac.ID)
		s.mu.Unlock()
		if err == nil {
			a.strmMu.Lock()
			row["running"] = a.strmRunning
			row["last_at"] = a.strmLastAt
			row["last_error"] = a.strmLastErr
			a.strmMu.Unlock()
		}
		out = append(out, row)
	}
	return out
}

func (s *Service) StrmStatus(id string) map[string]any {
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok {
		return map[string]any{"enabled": false, "pairs": []map[string]any{}}
	}
	s.mu.Lock()
	a, _, err := s.acctLocked(ac.ID)
	s.mu.Unlock()
	if err != nil {
		return map[string]any{"enabled": false, "pairs": []map[string]any{}}
	}
	a.strmMu.Lock()
	running, lastAt, lastErr, last := a.strmRunning, a.strmLastAt, a.strmLastErr, a.strmLast
	a.strmMu.Unlock()

	pairs := make([]map[string]any, 0, len(ac.StrmPairs))
	for _, p := range ac.StrmPairs {
		info := map[string]any{"cloud": p.Cloud, "local": p.Local, "exists": false, "strm_count": 0}
		if st, err := os.Stat(p.Local); err == nil && st.IsDir() {
			info["exists"] = true
			info["strm_count"] = countStrm(p.Local)
		}
		pairs = append(pairs, info)
	}
	out := map[string]any{
		"account":        ac.ID,
		"enabled":        ac.StrmEnabled,
		"running":        running,
		"mode":           strmMode(*ac),
		"mode_setting":   ac.StrmMode,
		"interval_min":   ac.StrmIntervalMin,
		"ext":            ac.StrmExt,
		"delete_missing": ac.StrmDeleteMissing,
		"token_set":      ac.StrmToken != "",
		"token":          ac.StrmToken,
		"pairs":          pairs,
		"mount_point":    ac.MountPoint,
		"mount_ready":    ac.MountPoint != "" && vfs.IsMountPoint(ac.MountPoint),
		"play_url":       s.strmPlayURL(cfg, ac.ID),
		"last_error":     lastErr,
	}
	if !lastAt.IsZero() {
		out["last_at"] = lastAt.Format(time.RFC3339)
	}
	if last != nil {
		out["last"] = last
	}
	return out
}

// countStrm 数一下输出目录里现存的 .strm（只数两层以内，别在媒体库上扫全盘）。
func countStrm(root string) int {
	n := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".strm") {
			n++
		}
		if n > 200000 {
			return filepath.SkipAll
		}
		return nil
	})
	return n
}

// SetStrm 应用控制台的补丁。
func (s *Service) SetStrm(id string, patch map[string]any) (map[string]any, error) {
	s.mu.Lock()
	cur, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("账号不存在：%s", id)
	}
	cfg := *cur
	mountPoint := cfg.MountPoint
	id = cfg.ID
	// 多账号：别的账号的挂载点和输出目录也要一起看。否则会出现两种烂事：
	//   1) 把 .strm 写进另一台云盘的挂载点里（等于往人家云盘上传文件）；
	//   2) 两台云盘往同一个本地目录写（双方的「删除缺失」会互相清对方的文件）。
	type otherAcct struct {
		name   string
		mount  string
		locals []string
	}
	var others []otherAcct
	for i := range s.cfg.Accounts {
		o := &s.cfg.Accounts[i]
		if o.ID == id {
			continue
		}
		oa := otherAcct{name: o.DisplayName(), mount: o.MountPoint}
		for _, p := range o.StrmPairs {
			oa.locals = append(oa.locals, p.Local)
		}
		others = append(others, oa)
	}
	s.mu.Unlock()

	if v, ok := patch["enabled"].(bool); ok {
		cfg.StrmEnabled = v
	}
	if v, ok := patch["mode"].(string); ok {
		switch v {
		case "auto", "path", "url":
			cfg.StrmMode = v
		default:
			return nil, fmt.Errorf("未知的内容形式 %q", v)
		}
	}
	if v, ok := patch["ext"].(string); ok {
		cfg.StrmExt = strings.TrimSpace(v)
	}
	if v, ok := num(patch["interval_min"]); ok {
		if v < 1 {
			v = 1
		}
		if v > 1440 {
			v = 1440
		}
		cfg.StrmIntervalMin = int(v)
	}
	if v, ok := patch["delete_missing"].(bool); ok {
		cfg.StrmDeleteMissing = v
	}
	if raw, ok := patch["pairs"].([]any); ok {
		pairs := make([]config.StrmPair, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			cloud, _ := m["cloud"].(string)
			local, _ := m["local"].(string)
			cloud = strings.TrimSpace(cloud)
			local = strings.TrimSpace(local)
			if local == "" {
				continue
			}
			if !filepath.IsAbs(local) {
				return nil, fmt.Errorf("本地输出目录必须是绝对路径：%s", local)
			}
			local = filepath.Clean(local)
			// 输出目录不能在云盘挂载点里面，否则 strm 会写到云端去。
			if mountPoint != "" {
				mp := filepath.Clean(mountPoint)
				if local == mp || strings.HasPrefix(local, mp+string(os.PathSeparator)) {
					return nil, fmt.Errorf("输出目录不能放在云盘挂载点 %s 里面（会写到云端去）", mp)
				}
			}
			if strings.Contains(local, "\x00") {
				return nil, fmt.Errorf("输出目录含非法字符")
			}
			for _, o := range others {
				if o.mount != "" {
					om := filepath.Clean(o.mount)
					if local == om || strings.HasPrefix(local, om+string(os.PathSeparator)) {
						return nil, fmt.Errorf("输出目录不能放在账号「%s」的挂载点 %s 里面（会写到那台云盘去）", o.name, om)
					}
					if strings.HasPrefix(om, local+string(os.PathSeparator)) {
						return nil, fmt.Errorf("输出目录 %s 里面挂着账号「%s」的云盘（%s）：扫进去会把那台云盘的片也写成 .strm，还会印进它的目录里", local, o.name, om)
					}
				}
				for _, l := range o.locals {
					if l == local {
						return nil, fmt.Errorf("输出目录 %s 已被账号「%s」的 STRM 占用：两台云盘别往同一个目录写 .strm，双方的「删除缺失」会互相清文件", local, o.name)
					}
				}
			}
			for _, done := range pairs {
				if done.Local == local {
					return nil, fmt.Errorf("输出目录填了两次：%s", local)
				}
			}
			pairs = append(pairs, config.StrmPair{Cloud: cloud, Local: local})
		}
		cfg.StrmPairs = pairs
	}
	// url 模式要一个口令：空着就生成，免得直链谁都能拉。
	if cfg.StrmMode == "url" && cfg.StrmToken == "" {
		cfg.StrmToken = newDAVPassword()
	}

	s.mu.Lock()
	// 只覆盖 STRM 相关字段，别把并发改的其它设置（比如挂载点）冲掉。
	if dst, ok := s.cfg.AccountByID(id); ok {
		dst.StrmEnabled = cfg.StrmEnabled
		dst.StrmPairs = cfg.StrmPairs
		dst.StrmMode = cfg.StrmMode
		dst.StrmExt = cfg.StrmExt
		dst.StrmIntervalMin = cfg.StrmIntervalMin
		dst.StrmDeleteMissing = cfg.StrmDeleteMissing
		dst.StrmToken = cfg.StrmToken
	}
	snapshot := *s.cfg
	s.mu.Unlock()

	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return nil, err
	}
	return s.StrmStatus(id), nil
}

// splitExts 把 "mp4, mkv" 拆成白名单；空表示用内置列表。
func splitExts(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == ';' || r == '、' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func displayCloud(p string) string {
	if strings.TrimSpace(p) == "" {
		return "/（云盘根目录）"
	}
	return p
}

// registerStrmRoutes 挂控制台接口和播放直链。
func (s *Service) registerStrmRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/strm/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "accounts": s.StrmAccountsView(),
			"current": s.Config().DefaultAccount})
	})

	mux.HandleFunc("/api/strm/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "strm": s.StrmStatus(accID(r)), "config": s.MaskPtrFor(accID(r))})
	})

	mux.HandleFunc("/api/strm/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		aid := accID(r)
		if v, ok := patch["account"].(string); ok && v != "" {
			aid = v
		}
		st, err := s.SetStrm(aid, patch)
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "strm": st, "config": s.MaskPtrFor(aid)})
	})

	mux.HandleFunc("/api/strm/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, "请用 POST")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
		defer cancel()
		aid := accID(r)
		res, err := s.StrmScan(ctx, aid)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "strm": s.StrmStatus(aid), "results": res})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "strm": s.StrmStatus(aid), "results": res})
	})

	// /api/strm/play 是写进 .strm 的直链：校验 k= 口令后 302 到云盘 CDN。
	// 走 302 而不是本机转发，是因为播放器（影视/Emby）拉 4K 时不该经过我们。
	mux.HandleFunc("/api/strm/play", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Config()
		// a= 指定账号（新版 strm 里带），老 strm 没有就按默认账号处理。
		aid := strings.TrimSpace(r.URL.Query().Get("a"))
		ac, ok := cfg.AccountByID(aid)
		if !ok {
			http.Error(w, "账号不存在", http.StatusNotFound)
			return
		}
		aid = ac.ID
		if tok := ac.StrmToken; tok != "" {
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("k")), []byte(tok)) != 1 {
				http.Error(w, "口令不对", http.StatusForbidden)
				return
			}
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "缺少文件 ID", http.StatusBadRequest)
			return
		}
		c, err := s.apiClientOf(aid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		u, err := c.DownloadURL(ctx, id)
		if err != nil {
			s.logf("STRM：取播放直链失败：%v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		http.Redirect(w, r, u, http.StatusFound)
	})
}
