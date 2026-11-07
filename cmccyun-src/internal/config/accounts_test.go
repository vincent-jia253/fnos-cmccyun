package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 老配置（v1.6.x，单账号摊在顶层）必须能无损迁移到 accounts[0]，并备份 .bak。
func TestLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	p := writeCfg(t, dir, `{
  "authorization": "cGM6MTM0NzQwODk2MjE6dG9rZW4=",
  "account": "****9621",
  "mount_point": "/vol4/1000/移动云盘",
  "read_only": true,
  "dav_enabled": true,
  "dav_user": "cmccyun",
  "dav_password": "pw123",
  "strm_enabled": true,
  "strm_pairs": [{"cloud":"/01影视","local":"/vol4/1000/strm-test"}],
  "strm_mode": "url",
  "strm_interval_min": 7,
  "dir_cache_ttl": 30
}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Accounts) != 1 {
		t.Fatalf("迁移后账号数 = %d", len(c.Accounts))
	}
	a := c.Accounts[0]
	if a.ID != DefaultAccountID || a.Authorization != "cGM6MTM0NzQwODk2MjE6dG9rZW4=" ||
		a.MountPoint != "/vol4/1000/移动云盘" || !a.ReadOnly || !a.StrmEnabled ||
		a.StrmMode != "url" || a.StrmIntervalMin != 7 || len(a.StrmPairs) != 1 {
		t.Fatalf("迁移丢字段: %+v", a)
	}
	if !c.DAVEnabled || a.DAVPassword != "pw123" {
		t.Fatalf("DAV 迁移失败: %+v", a)
	}
	if c.DirCacheTTL != 30 {
		t.Fatalf("全局项丢失: %d", c.DirCacheTTL)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatalf("没有生成 .bak: %v", err)
	}
	// 迁移后写盘的 JSON 不该再有顶层 authorization
	raw, _ := os.ReadFile(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["authorization"]; ok {
		t.Fatalf("迁移后仍写了顶层 authorization")
	}
	if _, ok := m["accounts"]; !ok {
		t.Fatalf("迁移后没写 accounts")
	}
	// 二次 Load 稳定（不会又迁移一遍）
	c2, err := Load(p)
	if err != nil || len(c2.Accounts) != 1 || c2.Accounts[0].MountPoint != a.MountPoint {
		t.Fatalf("二次加载不稳定: %v %+v", err, c2.Accounts)
	}
}

func TestMaskHidesSecrets(t *testing.T) {
	c := Default()
	c.Accounts = []AccountConfig{{
		ID: "acc1", Name: "家里", MountPoint: "/m/1",
		Authorization: "secret", DAVPassword: "pw", StrmToken: "tk",
	}}
	c.DAVEnabled = true
	c.DefaultAccount = "acc1"
	m := c.Mask()
	if m["account_count"] != 1 || m["default_account"] != "acc1" {
		t.Fatalf("mask 全局项: %+v", m)
	}
	acc := m["accounts"].([]map[string]any)[0]
	if acc["authorization_set"] != true || acc["dav_password_set"] != true || acc["strm_token_set"] != true {
		t.Fatalf("mask 未标记已设置: %+v", acc)
	}
	for k, v := range acc {
		if s, ok := v.(string); ok && (s == "secret" || s == "pw" || s == "tk") {
			t.Fatalf("mask 泄露了明文: %s=%s", k, s)
		}
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/mnt/a", "/mnt/a", true},
		{"/mnt/a/", "/mnt/a", true},
		{"/mnt/a", "/mnt/a/b", true},
		{"/mnt/a/b", "/mnt/a", true},
		{"/mnt/a", "/mnt/ab", false},
		{"/mnt/a", "/mnt/b", false},
		{"", "/mnt/a", false},
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Fatalf("PathsOverlap(%q,%q)=%v，期望 %v", c.a, c.b, got, c.want)
		}
	}
}

func TestUpsertRejectsOverlap(t *testing.T) {
	c := Default()
	c.Accounts = []AccountConfig{{ID: "a", MountPoint: "/vol4/1000/云盘A"}}
	id, err := c.UpsertAccount(AccountConfig{ID: "b", MountPoint: "/vol4/1000/云盘A/sub"})
	if err == nil {
		t.Fatalf("互相包含的挂载点应被拒绝，却返回 %q", id)
	}
	if _, err := c.UpsertAccount(AccountConfig{MountPoint: "/vol4/1000/云盘B"}); err != nil {
		t.Fatalf("正常挂载点被拒: %v", err)
	}
	if len(c.Accounts) != 2 || c.Accounts[1].ID == "" {
		t.Fatalf("新账号没拿到 ID: %+v", c.Accounts)
	}
	if _, err := c.UpsertAccount(AccountConfig{ID: "c", MountPoint: "relative/path"}); err == nil {
		t.Fatalf("相对路径应被拒绝")
	}
}

func TestDeleteAccountKeepsOne(t *testing.T) {
	c := Default()
	c.Accounts = []AccountConfig{{ID: "a", MountPoint: "/m/a"}, {ID: "b", MountPoint: "/m/b"}}
	c.DefaultAccount = "a"
	if err := c.DeleteAccount("a"); err != nil {
		t.Fatal(err)
	}
	if len(c.Accounts) != 1 || c.DefaultAccount != "b" {
		t.Fatalf("删除后状态不对: %+v default=%s", c.Accounts, c.DefaultAccount)
	}
	if err := c.DeleteAccount("b"); err == nil {
		t.Fatalf("最后一条不该被删掉")
	}
}
