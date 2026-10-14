package config

import (
	"testing"
	"time"
)

// 登录防爆破参数通过 AUTH_LOGIN_* 环境变量注入，这里锁住解析与默认值回退。
func TestLoadAuthGuardEnv(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "0123456789abcdef")
	t.Setenv("AUTH_LOGIN_MAX_FAILURES_PER_IP", "3")
	t.Setenv("AUTH_LOGIN_MAX_FAILURES_PER_ACCOUNT", "7")
	t.Setenv("AUTH_LOGIN_WINDOW_MINUTES", "2")
	t.Setenv("AUTH_LOGIN_BASE_LOCK_MINUTES", "5")
	t.Setenv("AUTH_LOGIN_MAX_LOCK_MINUTES", "60")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.Enabled {
		t.Fatal("AUTH_ENABLED=true 未生效")
	}
	got := cfg.Auth.ToGuardConfig()
	want := struct {
		ip, account       int
		window, base, max time.Duration
	}{3, 7, 2 * time.Minute, 5 * time.Minute, 60 * time.Minute}
	if got.MaxFailuresPerIP != want.ip || got.MaxFailuresPerAccount != want.account ||
		got.Window != want.window || got.BaseLock != want.base || got.MaxLock != want.max {
		t.Fatalf("防爆破参数解析错误: %+v", got)
	}
}

// Compose 里会把未设置的变量传成空串，此时应回退到默认值而不是解析失败。
func TestLoadAuthGuardEmptyEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv("AUTH_LOGIN_MAX_FAILURES_PER_IP", "")
	t.Setenv("AUTH_LOGIN_WINDOW_MINUTES", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("空环境变量不应导致加载失败: %v", err)
	}
	def := DefaultGuardConfigForTest()
	if cfg.Auth.LoginMaxFailuresPerIP != def.LoginMaxFailuresPerIP ||
		cfg.Auth.LoginWindowMinutes != def.LoginWindowMinutes {
		t.Fatalf("空环境变量应回退默认值: %+v", cfg.Auth)
	}
}

// DefaultGuardConfigForTest 只是让断言里的默认值集中在一处。
func DefaultGuardConfigForTest() AuthConfig {
	return AuthConfig{LoginMaxFailuresPerIP: 5, LoginWindowMinutes: 10}
}

// 未配置 trustedProxies 时应带上本机 + 私有网段的默认值。
func TestLoadTrustedProxiesDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Server.TrustedProxies) == 0 {
		t.Fatal("trustedProxies 默认值不应为空")
	}
	found := false
	for _, p := range cfg.Server.TrustedProxies {
		if p == "127.0.0.1/32" {
			found = true
		}
	}
	if !found {
		t.Fatalf("默认 trustedProxies 应包含回环地址: %v", cfg.Server.TrustedProxies)
	}
}
