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
type Config struct {
	// Authorization is the raw value of the `Authorization` header used by the
	// 中国移动云盘 web client, i.e. base64("pc:<account>:<token>").
	Authorization string `json:"authorization"`

	// Account is filled in automatically when Authorization is parsed.
	Account string `json:"account,omitempty"`

	// MountPoint 是本地挂载目录，可以是任意位置（例如用户「我的文件」下的目录）。
	MountPoint string `json:"mount_point"`
	// AllowNonEmpty 允许挂到已经有内容的目录上。默认 false：挂载会遮住原有
	// 内容，所以要用户在管理台里显式确认过才放行。
	AllowNonEmpty bool `json:"allow_non_empty,omitempty"`
	ReadOnly      bool `json:"read_only"`

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

	AutoMount bool `json:"auto_mount"`

	// DAVEnabled 打开内置 WebDAV 服务端（/dav/），供飞牛「远程挂载」等
	// 标准 WebDAV 客户端使用。和 FUSE 挂载互不影响，可以同时开。
	DAVEnabled bool `json:"dav_enabled"`
	// DAVUser / DAVPassword 是 WebDAV 的 BasicAuth 凭据。它刻意和
	// Authorization 分开：改密码不需要重新扫码登录，泄露了也只影响这一个入口。
	DAVUser     string `json:"dav_user"`
	DAVPassword string `json:"dav_password"`
	// DAVRedirect 让 GET 直接 302 到云盘 CDN 签名链接（不占用 NAS 带宽）。
	// 用指针是为了区分「没配过」和「配成 false」，默认开。
	DAVRedirect *bool `json:"dav_redirect,omitempty"`

	// STRM* 是「把云端影视文件生成 .strm 给影视/Emby 用」的配置。
	StrmEnabled       bool       `json:"strm_enabled"`
	StrmPairs         []StrmPair `json:"strm_pairs,omitempty"`
	StrmMode          string     `json:"strm_mode,omitempty"`           // auto | path | url
	StrmExt           string     `json:"strm_ext,omitempty"`            // 逗号分隔，空=内置白名单
	StrmIntervalMin   int        `json:"strm_interval_min,omitempty"`   // 0 = 5 分钟
	StrmDeleteMissing bool       `json:"strm_delete_missing,omitempty"` // 云端没了就删本地 strm
	StrmToken         string     `json:"strm_token,omitempty"`          // url 模式的访问口令
}

// StrmPair 是一条「云端目录 → 本地输出目录」的映射。
type StrmPair struct {
	// Cloud 是云端路径（形如 /01影视/电影），空表示云盘根目录。
	Cloud string `json:"cloud"`
	// Local 是本地输出根目录（绝对路径）。
	Local string `json:"local"`
}

// DAVRedirectOn reports whether GETs should be answered with a redirect to the
// signed CDN link instead of being proxied through this process.
func (c *Config) DAVRedirectOn() bool {
	if c.DAVRedirect == nil {
		return true
	}
	return *c.DAVRedirect
}

// DefaultDAVUser 是 WebDAV 的默认用户名；密码在第一次打开开关时随机生成。
const DefaultDAVUser = "cmccyun"

// Default returns a config with safe defaults.
func Default() *Config {
	return &Config{
		MountPoint:   "",
		ReadOnly:     false,
		UID:          0,
		GID:          0,
		FilePerm:     "0666",
		DirPerm:      "0777",
		DirCacheTTL:  60,
		UploadPartMB: 100,
		ClientType:   "13",
		CapacityGB:   2048,
		WebListen:    "0.0.0.0",
		WebPort:      19898,
		AutoMount:    true,
		DAVUser:      DefaultDAVUser,
	}
}

// Load reads path, creating it with defaults when missing.
func Load(path string) (*Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err := Save(path, c); err != nil {
			return nil, err
		}
		return c, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
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
	if c.DAVUser == "" {
		c.DAVUser = DefaultDAVUser
	}
	if c.StrmMode == "" {
		c.StrmMode = "auto"
	}
	if c.StrmIntervalMin <= 0 {
		c.StrmIntervalMin = 5
	}
	return c, nil
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

// Mask returns a JSON-safe copy whose Authorization is replaced by "***" when
// set, so the web UI can show whether a token exists without leaking it.
func (c *Config) Mask() map[string]any {
	hasToken := c.Authorization != ""
	return map[string]any{
		"authorization_set":   hasToken,
		"account":             c.Account,
		"mount_point":         c.MountPoint,
		"read_only":           c.ReadOnly,
		"uid":                 c.UID,
		"gid":                 c.GID,
		"file_perm":           c.FilePerm,
		"dir_perm":            c.DirPerm,
		"dir_cache_ttl":       c.DirCacheTTL,
		"upload_part_mb":      c.UploadPartMB,
		"client_type":         c.ClientType,
		"capacity_gb":         c.CapacityGB,
		"stage_dir":           c.StageDir,
		"log_file":            c.LogFile,
		"web_listen":          c.WebListen,
		"web_port":            c.WebPort,
		"auto_mount":          c.AutoMount,
		"dav_enabled":         c.DAVEnabled,
		"dav_user":            c.DAVUser,
		"dav_redirect":        c.DAVRedirectOn(),
		"dav_password_set":    c.DAVPassword != "",
		"strm_enabled":        c.StrmEnabled,
		"strm_mode":           c.StrmMode,
		"strm_ext":            c.StrmExt,
		"strm_interval_min":   c.StrmIntervalMin,
		"strm_delete_missing": c.StrmDeleteMissing,
		"strm_token_set":      c.StrmToken != "",
		"strm_pairs":          c.StrmPairs,
	}
}
