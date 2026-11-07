package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// AccountConfig 是一个云盘账号的配置。
//
// 单账号时代这些东西都摊在 Config 顶层（authorization / mount_point /
// dav_* / strm_*）。v1.7.0 起改成数组：顶层只保留全局项（uid、权限位、
// 缓存 TTL、端口…），账号相关的一律搬到这里。老配置在 Load 时自动迁移。
type AccountConfig struct {
	// ID 是稳定标识，出现在 URL（/dav/<id>/、?account=<id>）里，不随改名变化。
	ID string `json:"id"`
	// Name 是给人看的备注，空则界面显示账号尾号。
	Name string `json:"name,omitempty"`

	// Authorization 是 base64("pc:<账号>:<令牌>") 那条网页登录态。
	Authorization string `json:"authorization,omitempty"`
	// Account 由 Authorization 解析得到（如 ****9621），只读。
	Account string `json:"account,omitempty"`

	MountPoint    string `json:"mount_point"`
	AllowNonEmpty bool   `json:"allow_non_empty,omitempty"`
	ReadOnly      bool   `json:"read_only,omitempty"`

	DAVUser     string `json:"dav_user,omitempty"`
	DAVPassword string `json:"dav_password,omitempty"`
	DAVRedirect *bool  `json:"dav_redirect,omitempty"`

	StrmEnabled       bool       `json:"strm_enabled,omitempty"`
	StrmPairs         []StrmPair `json:"strm_pairs,omitempty"`
	StrmMode          string     `json:"strm_mode,omitempty"`
	StrmExt           string     `json:"strm_ext,omitempty"`
	StrmIntervalMin   int        `json:"strm_interval_min,omitempty"`
	StrmDeleteMissing bool       `json:"strm_delete_missing,omitempty"`
	StrmToken         string     `json:"strm_token,omitempty"`
}

// DAVRedirectOn reports whether GETs should 302 to the signed CDN link.
func (a *AccountConfig) DAVRedirectOn() bool {
	if a.DAVRedirect == nil {
		return true
	}
	return *a.DAVRedirect
}

// DAVUserOr 返回 WebDAV 用户名，未配置时给默认值。
func (a *AccountConfig) DAVUserOr() string {
	if a.DAVUser == "" {
		return DefaultDAVUser
	}
	return a.DAVUser
}

// StrmModeOr 返回 STRM 模式，未配置时 auto。
func (a *AccountConfig) StrmModeOr() string {
	if a.StrmMode == "" {
		return "auto"
	}
	return a.StrmMode
}

// StrmIntervalOr 返回同步间隔（分钟），未配置时 5。
func (a *AccountConfig) StrmIntervalOr() int {
	if a.StrmIntervalMin <= 0 {
		return 5
	}
	return a.StrmIntervalMin
}

// DisplayName 是界面上显示的名字：备注 > 账号 > ID。
func (a *AccountConfig) DisplayName() string {
	if s := strings.TrimSpace(a.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.Account); s != "" {
		return s
	}
	return a.ID
}

// NewAccountID 生成一个稳定的账号标识（6 字节随机 hex），避免把手机号
// 这类信息写进 URL。
func NewAccountID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极端情况下退回固定前缀，调用方还会做去重。
		return "acc000000000"
	}
	return "acc" + hex.EncodeToString(b[:])
}

// List 返回账号列表。迁移之后至少有一条；万一配置里既没有 accounts 也没有
// 老字段（全新安装），返回一条空账号，界面会引导用户去授权。
func (c *Config) List() []AccountConfig {
	if len(c.Accounts) > 0 {
		return c.Accounts
	}
	return []AccountConfig{{ID: DefaultAccountID, DAVUser: DefaultDAVUser}}
}

// DefaultAccountID 是迁移出来的第一条账号的 ID。
const DefaultAccountID = "default"

// AccountByID 按 ID 取账号；id 为空时取默认账号（DefaultAccount 或第一条）。
func (c *Config) AccountByID(id string) (*AccountConfig, bool) {
	list := c.Accounts
	if len(list) == 0 {
		return nil, false
	}
	if strings.TrimSpace(id) == "" {
		id = c.DefaultAccount
	}
	if id == "" {
		return &c.Accounts[0], true
	}
	for i := range c.Accounts {
		if c.Accounts[i].ID == id {
			return &c.Accounts[i], true
		}
	}
	return nil, false
}

// DefaultAccountPtr 返回默认账号（必要时创建一条）。
func (c *Config) DefaultAccountPtr() *AccountConfig {
	if len(c.Accounts) == 0 {
		c.Accounts = []AccountConfig{{ID: DefaultAccountID, DAVUser: DefaultDAVUser}}
	}
	if a, ok := c.AccountByID(c.DefaultAccount); ok {
		return a
	}
	c.DefaultAccount = c.Accounts[0].ID
	return &c.Accounts[0]
}

// UpsertAccount 新增或按 ID 覆盖一条账号，返回写入后的账号 ID。
// ID 为空时生成新的；同名 ID 存在则覆盖（用于改备注/挂载点/授权）。
func (c *Config) UpsertAccount(a AccountConfig) (string, error) {
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	if a.DAVUser == "" {
		a.DAVUser = DefaultDAVUser
	}
	if a.StrmMode == "" {
		a.StrmMode = "auto"
	}
	if a.StrmIntervalMin <= 0 {
		a.StrmIntervalMin = 5
	}
	if a.ID == "" {
		a.ID = NewAccountID()
		for c.hasAccount(a.ID) {
			a.ID = NewAccountID()
		}
	}
	// 挂载点不能为空（账号建好就要有目标），且不能和别的账号互相包含。
	if strings.TrimSpace(a.MountPoint) == "" {
		return "", fmt.Errorf("挂载点不能为空")
	}
	if !isAbsPath(a.MountPoint) {
		return "", fmt.Errorf("挂载点必须是绝对路径：%s", a.MountPoint)
	}
	for _, other := range c.Accounts {
		if other.ID == a.ID {
			continue
		}
		if PathsOverlap(a.MountPoint, other.MountPoint) {
			return "", fmt.Errorf("挂载点 %s 与账号 %s 的 %s 互相包含，请换一个目录",
				a.MountPoint, other.DisplayName(), other.MountPoint)
		}
	}
	for i := range c.Accounts {
		if c.Accounts[i].ID == a.ID {
			c.Accounts[i] = a
			return a.ID, nil
		}
	}
	c.Accounts = append(c.Accounts, a)
	if c.DefaultAccount == "" {
		c.DefaultAccount = a.ID
	}
	return a.ID, nil
}

// DeleteAccount 删掉一条账号；不允许删掉最后一条（那等于把应用配置清空，
// 用户会以为数据丢了）。
func (c *Config) DeleteAccount(id string) error {
	if len(c.Accounts) <= 1 {
		return fmt.Errorf("至少要保留一个账号")
	}
	idx := -1
	for i := range c.Accounts {
		if c.Accounts[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("账号不存在：%s", id)
	}
	c.Accounts = append(c.Accounts[:idx], c.Accounts[idx+1:]...)
	if c.DefaultAccount == id {
		c.DefaultAccount = c.Accounts[0].ID
	}
	return nil
}

func (c *Config) hasAccount(id string) bool {
	for i := range c.Accounts {
		if c.Accounts[i].ID == id {
			return true
		}
	}
	return false
}

func isAbsPath(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, "/")
}

// PathsOverlap 判断两个目录是否相同、或一个包含另一个。
// 挂载点互相包含会让其中一个账号的文件「长在」另一个账号的云盘里，
// 卸载顺序一变就会把数据写到错的地方，所以启动和保存时都要挡住。
func PathsOverlap(a, b string) bool {
	a = cleanPath(a)
	b = cleanPath(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p
}
