package app

import (
	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// acct0 返回默认账号配置的可写指针：多账号重构后，账号相关字段都挂在
// cfg.Accounts[i] 上，测试里改配置统一走这里。
func acct0(cfg *config.Config) *config.AccountConfig {
	cfg.Accounts = append([]config.AccountConfig(nil), cfg.Accounts...)
	if len(cfg.Accounts) == 0 {
		cfg.Accounts = []config.AccountConfig{{ID: config.DefaultAccountID}}
	}
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == config.DefaultAccountID || cfg.DefaultAccount == "" {
			return &cfg.Accounts[i]
		}
	}
	return &cfg.Accounts[0]
}

// acctView 返回默认账号配置的副本（用于读 s.Config() 这种值快照）。
func acctView(cfg config.Config) config.AccountConfig {
	if ac, ok := cfg.AccountByID(""); ok {
		return *ac
	}
	return config.AccountConfig{}
}

// setMountedAt 直接改「实际挂载点」这条运行态（测试用来模拟已挂载）。
func setMountedAt(s *Service, p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, _, err := s.acctLocked(""); err == nil {
		a.mountedAt = p
	}
}

// davReset 让 WebDAV 那层重建文件系统（只读开关变了以后测试要用）。
func davReset(s *Service) {
	s.mu.Lock()
	if a, _, err := s.acctLocked(""); err == nil {
		a.davFS = nil
	}
	s.mu.Unlock()
}
