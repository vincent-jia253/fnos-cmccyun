package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

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

// registerAccountRoutes 挂账号管理接口。
func (s *Service) registerAccountRoutes(mux *http.ServeMux) {
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
