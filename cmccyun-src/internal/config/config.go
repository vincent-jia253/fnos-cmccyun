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
}

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
		"authorization_set": hasToken,
		"account":           c.Account,
		"mount_point":       c.MountPoint,
		"read_only":         c.ReadOnly,
		"uid":               c.UID,
		"gid":               c.GID,
		"file_perm":         c.FilePerm,
		"dir_perm":          c.DirPerm,
		"dir_cache_ttl":     c.DirCacheTTL,
		"upload_part_mb":    c.UploadPartMB,
		"client_type":       c.ClientType,
		"capacity_gb":       c.CapacityGB,
		"stage_dir":         c.StageDir,
		"log_file":          c.LogFile,
		"web_listen":        c.WebListen,
		"web_port":          c.WebPort,
		"auto_mount":        c.AutoMount,
	}
}
