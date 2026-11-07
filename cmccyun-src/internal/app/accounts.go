package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// 多账号：一个进程里同时挂多台移动云盘。
//
// 设计取舍（见 v1.7.0 的评估）：不做多进程 launcher——飞牛只认一个应用生命周期，
// 多进程会留下僵尸挂载、多份端口和日志。所以账号是 Service 里的一组运行态
// （见 acct），每个账号各有自己的 client / FUSE server / 挂载点 / WebDAV 凭据 /
// STRM 映射；控制台接口用 ?account=<id> 选目标，不带参数就是默认账号，
// 单账号用户的行为和以前完全一样。

// AccountsView 是控制台用的账号列表（不含任何密文）。
func (s *Service) AccountsView() []map[string]any {
	cfg := s.Config()
	out := make([]map[string]any, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		ac := &cfg.Accounts[i]
		st := s.StatusOf(ac.ID)
		out = append(out, map[string]any{
			"id":                ac.ID,
			"name":              ac.DisplayName(),
			"custom_name":       ac.Name,
			"account":           st.Account,
			"mount_point":       ac.MountPoint,
			"read_only":         ac.ReadOnly,
			"authorization_set": ac.Authorization != "",
			"mounted":           st.Mounted,
			"dead_mount":        st.DeadMount,
			"stray_mount":       st.StrayMount,
			"strm_enabled":      ac.StrmEnabled,
			"dav_user":          ac.DAVUser,
			"dav_password_set":  ac.DAVPassword != "",
			"dav_path":          davPathFor(ac.ID),
			"default":           ac.ID == cfg.DefaultAccount,
			"auth_days":         st.Auth.ExpiresDays,
			"last_error":        st.LastError,
		})
	}
	return out
}

// AddAccount 新增一台云盘账号，返回新账号的 ID。
func (s *Service) AddAccount(name, mountPoint string) (string, error) {
	name = strings.TrimSpace(name)
	mountPoint = strings.TrimSpace(mountPoint)
	s.mu.Lock()
	// 账号还没建，先按「新增」规则校验挂载位置：必须是绝对路径、在存储卷里、
	// 且不和别的账号的挂载点互相包含（光靠 config.UpsertAccount 只查得了包含关系，
	// 查不了 /etc 这种位置）。
	if err := s.validateMountPathForNewLocked(mountPoint); err != nil {
		s.mu.Unlock()
		return "", err
	}
	id, err := s.cfg.UpsertAccount(config.AccountConfig{
		ID:              config.NewAccountID(),
		Name:            name,
		MountPoint:      mountPoint,
		DAVUser:         config.DefaultDAVUser,
		StrmMode:        "auto",
		StrmIntervalMin: 5,
	})
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	if s.cfg.DefaultAccount == "" {
		s.cfg.DefaultAccount = id
	}
	snapshot := *s.cfg
	s.mu.Unlock()
	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return "", err
	}
	s.logf("已新增账号「%s」（挂载点 %s）：去「授权」页扫码或粘贴 Authorization 即可", name, mountPoint)
	return id, nil
}

// UpdateAccount 改一个账号的名称 / 挂载点 / 只读。
func (s *Service) UpdateAccount(id string, patch map[string]any) error {
	s.mu.Lock()
	ac, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("账号不存在：%s", id)
	}
	if v, ok := patch["name"].(string); ok {
		ac.Name = strings.TrimSpace(v)
	}
	if v, ok := patch["mount_point"].(string); ok {
		v = strings.TrimSpace(v)
		if err := s.validateMountPathOptLocked(ac.ID, v, false); err != nil {
			s.mu.Unlock()
			return err
		}
		ac.MountPoint = v
	}
	if v, ok := patch["read_only"].(bool); ok {
		ac.ReadOnly = v
		if a := s.accts[ac.ID]; a != nil {
			a.davFS = nil
		}
	}
	snapshot := *s.cfg
	s.mu.Unlock()
	return config.Save(s.cfgPath, &snapshot)
}

// DeleteAccount 删掉一台云盘账号：先卸挂载，再删配置与残留的挂载点记录。
func (s *Service) DeleteAccount(id string) error {
	s.mu.Lock()
	ac, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("账号不存在：%s", id)
	}
	realID := ac.ID
	s.mu.Unlock()

	if err := s.UnmountOf(realID); err != nil {
		// 卸载失败不阻断删除（比如进程重启后残留的挂载），但要记下来。
		s.logf("删除账号前卸载失败（继续删除）：%v", err)
	}

	s.mu.Lock()
	if err := s.cfg.DeleteAccount(realID); err != nil {
		s.mu.Unlock()
		return err
	}
	if a := s.accts[realID]; a != nil {
		delete(s.accts, realID)
	}
	snapshot := *s.cfg
	s.mu.Unlock()

	if err := config.Save(s.cfgPath, &snapshot); err != nil {
		return err
	}
	_ = os.Remove(s.mountStatePath(realID))
	s.logf("已删除账号 %s（云端数据不受影响）", realID)
	return nil
}

// SetDefaultAccount 指定控制台默认展示哪台云盘。
func (s *Service) SetDefaultAccount(id string) error {
	s.mu.Lock()
	ac, ok := s.cfg.AccountByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("账号不存在：%s", id)
	}
	s.cfg.DefaultAccount = ac.ID
	snapshot := *s.cfg
	s.mu.Unlock()
	return config.Save(s.cfgPath, &snapshot)
}

// RemountAll 依次重挂所有账号：换完 139 的密码/令牌以后，不用一台台点「重新挂载」。
//
// 顺序执行而不是并发：每次重挂都要重新走一遍云端登录（有的账号还要等短信/扫码
// 之外的令牌续期），并发只会让日志交错、失败原因难以归到具体账号。单台失败不
// 影响其余的，结果逐台回给界面。
func (s *Service) RemountAll(ctx context.Context) []map[string]any {
	cfg := s.Config()
	out := make([]map[string]any, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		id := cfg.Accounts[i].ID
		name := cfg.Accounts[i].DisplayName()
		row := map[string]any{"id": id, "name": name}
		if err := s.RemountOf(ctx, id); err != nil {
			row["ok"] = false
			row["error"] = err.Error()
			s.logf("批量重挂：账号「%s」失败：%v", name, err)
		} else {
			row["ok"] = true
			s.logf("批量重挂：账号「%s」已重挂", name)
		}
		out = append(out, row)
	}
	return out
}

// AccountExport 是「按账号导出配置」的载荷。
//
// 默认带走的是结构配置（挂载点 / 只读 / STRM / WebDAV 用户名），凭据一律
// 置空并列出被省略的字段：这个 JSON 通常会被贴进工单、放进笔记，登录态
// （authorization）和 WebDAV 密码不该跟着跑。确实要连凭据一起备份时，
// 显式 include_auth=1（界面上是那个勾选框）。
func AccountExport(ac *config.AccountConfig, includeSecrets bool, now time.Time) map[string]any {
	acc := *ac
	omitted := []string{}
	if !includeSecrets {
		if acc.Authorization != "" {
			omitted = append(omitted, "authorization")
		}
		if acc.DAVPassword != "" {
			omitted = append(omitted, "dav_password")
		}
		acc.Authorization = ""
		acc.DAVPassword = ""
	}
	out := map[string]any{
		"app":         "cmccyun",
		"schema":      1,
		"exported_at": now.UTC().Format(time.RFC3339),
		"account":     acc,
	}
	if len(omitted) > 0 {
		out["secrets_omitted"] = omitted
	} else {
		out["warning"] = "含登录凭据（authorization / dav_password），请妥善保管，用完即删"
	}
	return out
}

// filterLogsForAccount 只留下跟某个账号有关的日志行。
//
// 守护进程只写一个日志文件，行里没有账号字段；能用来定位一台账号的线索是
// 账号 ID（acc…，出现在 /dav/<id>/ 与 STRM 快照里）、账号尾号（****9621）、
// 备注名和挂载点。命中任意一个就算这台账号的行——挂载/卸载/STRM/续期都会把
// 挂载点或尾号写进日志，所以过滤后通常就够看了。
func filterLogsForAccount(lines []string, ac *config.AccountConfig) []string {
	if ac == nil {
		return lines
	}
	needles := make([]string, 0, 5+len(ac.StrmPairs))
	candidates := []string{ac.ID, ac.Account, ac.Name, ac.MountPoint, "/dav/" + ac.ID}
	// STRM 的日志行写的是本地输出目录，不是挂载点，不带上的话「这台账号的
	// STRM 扫描」永远查不到。
	for _, pair := range ac.StrmPairs {
		candidates = append(candidates, pair.Local)
	}
	for _, v := range candidates {
		if v = strings.TrimSpace(v); v != "" {
			needles = append(needles, v)
		}
	}
	if len(needles) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, needle := range needles {
			if containsToken(line, needle) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// containsToken 找 needle，但要求两侧不是「标识符字符」。
//
// 光用 strings.Contains 会把别的账号的日志捞进来：挂载点
// /vol4/1000/移动云盘 是 /vol4/1000/移动云盘-2 的前缀，前缀命中就等于把第二台
// 账号的挂载/STRM 行混进了第一台的视图。所以要求匹配后面（或前面）紧跟的是
// 空格、括号、斜杠、标点这类分隔符，而不是字母数字、- 或 _。
func containsToken(line, needle string) bool {
	for offset := 0; offset <= len(line); {
		i := strings.Index(line[offset:], needle)
		if i < 0 {
			return false
		}
		start := offset + i
		end := start + len(needle)
		leftOK := start == 0 || !isWordByte(line[start-1])
		rightOK := end == len(line) || !isWordByte(line[end])
		if leftOK && rightOK {
			return true
		}
		offset = start + len(needle)
	}
	return false
}

func isWordByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	case b == '-' || b == '_':
		return true
	}
	return false
}

// registerAccountRoutes 挂账号管理接口。
func (s *Service) registerAccountRoutes(mux *http.ServeMux) {
	// 按账号导出配置：默认不含凭据（见 AccountExport）。下载文件名带账号 ID，
	// 免得一次导好几台时分不清谁是谁。
	mux.HandleFunc("/api/accounts/export", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		cfg := s.Config()
		ac, ok := cfg.AccountByID(id)
		if !ok {
			writeErr(w, "账号不存在："+id)
			return
		}
		include := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("include_auth")))
		payload := AccountExport(ac, include == "1" || include == "true", time.Now())
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition",
			`attachment; filename="cmccyun-`+ac.ID+`.json"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(body, '\n'))
	})

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, map[string]any{"ok": true, "accounts": s.AccountsView(),
				"default": s.Config().DefaultAccount})
			return
		}
		var body struct {
			Action     string `json:"action"` // add | update | delete | default
			ID         string `json:"id"`
			Name       string `json:"name"`
			MountPoint string `json:"mount_point"`
			ReadOnly   *bool  `json:"read_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, "请求格式错误: "+err.Error())
			return
		}
		var err error
		switch body.Action {
		case "add":
			var id string
			if id, err = s.AddAccount(body.Name, body.MountPoint); err == nil {
				writeJSON(w, map[string]any{"ok": true, "id": id,
					"accounts": s.AccountsView(), "default": s.Config().DefaultAccount})
				return
			}
		case "update":
			patch := map[string]any{"name": body.Name}
			if body.MountPoint != "" {
				patch["mount_point"] = body.MountPoint
			}
			if body.ReadOnly != nil {
				patch["read_only"] = *body.ReadOnly
			}
			err = s.UpdateAccount(body.ID, patch)
		case "delete":
			err = s.DeleteAccount(body.ID)
		case "default":
			err = s.SetDefaultAccount(body.ID)
		case "remount_all":
			// 所有账号挨个重挂：慢（每个都要重新登录一次云端），所以单独给
			// 一个宽松的超时，避免界面上点完等到 120 秒被掐断。
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
			defer cancel()
			results := s.RemountAll(ctx)
			writeJSON(w, map[string]any{"ok": true, "results": results,
				"accounts": s.AccountsView(), "default": s.Config().DefaultAccount})
			return
		default:
			err = fmt.Errorf("未知操作 %q", body.Action)
		}
		if err != nil {
			writeErr(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "accounts": s.AccountsView(),
			"default": s.Config().DefaultAccount})
	})
}
