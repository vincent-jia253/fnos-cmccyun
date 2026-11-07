// Package config holds the on-disk settings for the 移动云盘挂载 service.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is persisted as JSON next to the app's configuration directory.
//
// v1.7.0 起分两层：顶层是全局项（权限位、缓存、端口、打包参数…），
// 账号相关的一切（授权、挂载点、WebDAV 凭据、STRM 映射）都在 Accounts 里。
// 老版本把账号项摊在顶层，Load 会自动迁移（并备份 .bak）。
type Config struct {
	// Accounts 是云盘账号列表，至少一条（见 List()）。
	Accounts []AccountConfig `json:"accounts,omitempty"`
	// DefaultAccount 是「不带 ?account= 时用哪个」，界面上也叫「当前账号」。
	DefaultAccount string `json:"default_account,omitempty"`

	// DAVEnabled 打开内置 WebDAV 服务端（/dav/ 与 /dav/<账号>/）。
	DAVEnabled bool `json:"dav_enabled"`

	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
	FilePerm string `json:"file_perm"`
	DirPerm  string `json:"dir_perm"`

	// DirCacheTTL is the directory listing cache in seconds.
	DirCacheTTL int `json:"dir_cache_ttl"`

	// UploadPartMB is the multipart chunk size used when uploading.
	UploadPartMB int `json:"upload_part_mb"`

	// ClientType is the third field of the X-Yun-Client-Info header.
	// 13 = PC client (500 GB single file), 9 = web (50 GB).
	ClientType string `json:"client_type"`

	// CapacityGB is what statfs reports back; the personal-cloud API exposes no
	// quota endpoint, so this is user-supplied.
	CapacityGB int `json:"capacity_gb"`

	StageDir string `json:"stage_dir"`
	LogFile  string `json:"log_file"`

	WebListen string `json:"web_listen"`
	WebPort   int    `json:"web_port"`

	// ---- 控制台访问密码（v1.8.0）----
	// WebUser 是控制台登录名，空 = admin。
	WebUser string `json:"web_user,omitempty"`
	// WebPassword 是控制台密码的 PBKDF2 哈希，空 = 默认密码 admin。
	WebPassword string `json:"web_password,omitempty"`
	// WebAuthDisabled 只给测试/纯内网直连用：true 时控制台不再要求登录。
	WebAuthDisabled bool `json:"web_auth_disabled,omitempty"`

	AutoMount bool `json:"auto_mount"`

	// ---- 以下只用于读取 v1.6.x 及更早的配置，Load 迁移后会被清空 ----

	// LegacyAuthorization 是 base64("pc:<account>:<token>")。
	LegacyAuthorization string `json:"authorization,omitempty"`
	LegacyAccount       string `json:"account,omitempty"`
	LegacyMountPoint    string `json:"mount_point,omitempty"`
	LegacyAllowNonEmpty bool   `json:"allow_non_empty,omitempty"`
	LegacyReadOnly      bool   `json:"read_only,omitempty"`
	LegacyDAVUser       string `json:"dav_user,omitempty"`
	LegacyDAVPassword   string `json:"dav_password,omitempty"`
	LegacyDAVRedirect   *bool  `json:"dav_redirect,omitempty"`
	LegacyStrmEnabled   bool   `json:"strm_enabled,omitempty"`
	LegacyStrmMode      string `json:"strm_mode,omitempty"`
	LegacyStrmExt       string `json:"strm_ext,omitempty"`
	LegacyStrmInterval  int    `json:"strm_interval_min,omitempty"`
	LegacyStrmDelete    bool   `json:"strm_delete_missing,omitempty"`
	LegacyStrmToken     string `json:"strm_token,omitempty"`

	// legacyPairs 接住 JSON 里的 strm_pairs（上面那行的字段名不好直接挂 tag，
	// 单独用一个内部字段，序列化由 UnmarshalJSON 处理）。
	legacyPairs []StrmPair
}

// StrmPair 是一条「云端目录 → 本地输出目录」的映射。
type StrmPair struct {
	// Cloud 是云端路径（形如 /01影视/电影），空表示云盘根目录。
	Cloud string `json:"cloud"`
	// Local 是本地输出根目录（绝对路径）。
	Local string `json:"local"`
}

// DefaultDAVUser 是 WebDAV 的默认用户名；密码在第一次打开开关时随机生成。
const DefaultDAVUser = "cmccyun"

// Default returns a config with safe defaults.
func Default() *Config {
	return &Config{
		FilePerm:     "0666",
		DirPerm:      "0777",
		DirCacheTTL:  60,
		UploadPartMB: 100,
		ClientType:   "13",
		CapacityGB:   2048,
		WebListen:    "0.0.0.0",
		WebPort:      19898,
		AutoMount:    true,
		// 注意：这里不预置账号。preset 了的话，老配置的顶层字段就再也不会
		// 被迁移（migrateLegacy 看到 accounts 非空会直接返回）。空账号由
		// normalize() 在迁移之后补上。
	}
}

// UnmarshalJSON 只是为了让老的 strm_pairs 也能读进来（它是数组，不能挂到
// 顶层字段以外的地方），其余字段走默认解码。
func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	aux := struct {
		*alias
		StrmPairs []StrmPair `json:"strm_pairs"`
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	c.legacyPairs = aux.StrmPairs
	return nil
}

// HasLegacyAccount 报告配置里是否还有 v1.6 时代的单账号字段。
func (c *Config) HasLegacyAccount() bool {
	return strings.TrimSpace(c.LegacyAuthorization) != "" ||
		strings.TrimSpace(c.LegacyMountPoint) != "" ||
		c.LegacyDAVPassword != "" || c.LegacyStrmToken != "" ||
		len(c.legacyPairs) > 0
}

// migrateLegacy 把顶层的单账号字段搬进 Accounts[0]。只在还没有 accounts
// 数组时调用，调用方负责备份原文件。
func (c *Config) migrateLegacy() (changed bool) {
	if len(c.Accounts) > 0 || !c.HasLegacyAccount() {
		return false
	}
	a := AccountConfig{
		ID:                DefaultAccountID,
		Authorization:     c.LegacyAuthorization,
		Account:           c.LegacyAccount,
		MountPoint:        c.LegacyMountPoint,
		AllowNonEmpty:     c.LegacyAllowNonEmpty,
		ReadOnly:          c.LegacyReadOnly,
		DAVUser:           c.LegacyDAVUser,
		DAVPassword:       c.LegacyDAVPassword,
		DAVRedirect:       c.LegacyDAVRedirect,
		StrmEnabled:       c.LegacyStrmEnabled,
		StrmPairs:         c.legacyPairs,
		StrmMode:          c.LegacyStrmMode,
		StrmExt:           c.LegacyStrmExt,
		StrmIntervalMin:   c.LegacyStrmInterval,
		StrmDeleteMissing: c.LegacyStrmDelete,
		StrmToken:         c.LegacyStrmToken,
	}
	if a.MountPoint == "" {
		a.MountPoint = "/vol1/1000/移动云盘"
	}
	c.Accounts = []AccountConfig{a}
	c.DefaultAccount = a.ID
	// 清掉老字段，避免 Save 之后出现两份真相。
	c.LegacyAuthorization, c.LegacyAccount = "", ""
	c.LegacyMountPoint, c.LegacyAllowNonEmpty = "", false
	c.LegacyReadOnly = false
	c.LegacyDAVUser, c.LegacyDAVPassword, c.LegacyDAVRedirect = "", "", nil
	c.LegacyStrmEnabled = false
	c.LegacyStrmMode, c.LegacyStrmExt = "", ""
	c.LegacyStrmInterval, c.LegacyStrmDelete, c.LegacyStrmToken = 0, false, ""
	c.legacyPairs = nil
	return true
}

// Load reads path, creating it with defaults when missing.
func Load(path string) (*Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// 全新安装：先补上那条空账号再落盘，保证「至少有一条账号」的不变式。
		c.normalize()
		if err := Save(path, c); err != nil {
			return nil, err
		}
		return c, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		c.normalize()
		return c, nil
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	if c.migrateLegacy() {
		// 迁移前先留一份原样备份，万一有字段解析偏了还能手工翻。
		_ = os.WriteFile(path+".bak", data, 0o600)
		if err := Save(path, c); err != nil {
			return nil, err
		}
	}
	c.normalize()
	return c, nil
}

func (c *Config) normalize() {
	if c.DirCacheTTL <= 0 {
		c.DirCacheTTL = 60
	}
	if c.UploadPartMB <= 0 {
		c.UploadPartMB = 100
	}
	if c.ClientType == "" {
		c.ClientType = "13"
	}
	if c.CapacityGB <= 0 {
		c.CapacityGB = 2048
	}
	if c.WebPort == 0 {
		c.WebPort = 19898
	}
	if c.FilePerm == "" {
		c.FilePerm = "0666"
	}
	if c.DirPerm == "" {
		c.DirPerm = "0777"
	}
	// 不变式：至少有一条账号，且 DefaultAccount 指向存在的账号。
	if len(c.Accounts) == 0 {
		c.Accounts = []AccountConfig{{ID: DefaultAccountID}}
	}
	if c.DefaultAccount == "" {
		c.DefaultAccount = c.Accounts[0].ID
	}
	for i := range c.Accounts {
		a := &c.Accounts[i]
		if a.ID == "" {
			a.ID = NewAccountID()
		}
		if a.DAVUser == "" {
			a.DAVUser = DefaultDAVUser
		}
		if a.StrmMode == "" {
			a.StrmMode = "auto"
		}
		if a.StrmIntervalMin <= 0 {
			a.StrmIntervalMin = 5
		}
	}
	// DefaultAccount 必须指向一条真实存在的账号（比如那条账号被删了）。
	if _, ok := c.AccountByID(c.DefaultAccount); !ok {
		c.DefaultAccount = c.Accounts[0].ID
	}
}

// Save writes the config atomically.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseMode converts an octal string such as "0644" into a permission bitmask.
func ParseMode(s string, fallback uint32) uint32 {
	s = strings.TrimSpace(strings.TrimPrefix(s, "0o"))
	s = strings.TrimPrefix(s, "0")
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return fallback
	}
	return uint32(v)
}

// MaskAccount 返回一条账号的脱敏视图（不含明文授权/密码）。
func (a *AccountConfig) MaskAccount() map[string]any {
	return map[string]any{
		"id":                  a.ID,
		"name":                a.Name,
		"display_name":        a.DisplayName(),
		"account":             a.Account,
		"authorization_set":   a.Authorization != "",
		"mount_point":         a.MountPoint,
		"allow_non_empty":     a.AllowNonEmpty,
		"read_only":           a.ReadOnly,
		"dav_user":            a.DAVUserOr(),
		"dav_password_set":    a.DAVPassword != "",
		"dav_redirect":        a.DAVRedirectOn(),
		"strm_enabled":        a.StrmEnabled,
		"strm_mode":           a.StrmModeOr(),
		"strm_ext":            a.StrmExt,
		"strm_interval_min":   a.StrmIntervalOr(),
		"strm_delete_missing": a.StrmDeleteMissing,
		"strm_token_set":      a.StrmToken != "",
		"strm_pairs":          a.StrmPairs,
	}
}

// Mask returns a JSON-safe copy of the global settings plus a masked account
// list, so the web UI can show whether tokens exist without leaking them.
// WebUserMasked 返回界面上显示的登录名（空配置时是 admin）。
func (c *Config) WebUserMasked() string {
	if u := strings.TrimSpace(c.WebUser); u != "" {
		return u
	}
	return "admin"
}

func (c *Config) Mask() map[string]any {
	list := make([]map[string]any, 0, len(c.List()))
	for i := range c.List() {
		a := c.List()[i]
		list = append(list, a.MaskAccount())
	}
	def := c.DefaultAccount
	if def == "" && len(list) > 0 {
		def = list[0]["id"].(string)
	}
	return map[string]any{
		"accounts":        list,
		"default_account": def,
		"account_count":   len(list),
		"dav_enabled":     c.DAVEnabled,
		"uid":             c.UID,
		"gid":             c.GID,
		"file_perm":       c.FilePerm,
		"dir_perm":        c.DirPerm,
		"dir_cache_ttl":   c.DirCacheTTL,
		"upload_part_mb":  c.UploadPartMB,
		"client_type":     c.ClientType,
		"capacity_gb":     c.CapacityGB,
		"stage_dir":       c.StageDir,
		"log_file":        c.LogFile,
		"web_listen":      c.WebListen,
		"web_port":        c.WebPort,
		"auto_mount":      c.AutoMount,
		"web_user":        c.WebUserMasked(),
		"web_password_set": c.WebPassword != "",
		"web_auth_disabled": c.WebAuthDisabled,
	}
}
