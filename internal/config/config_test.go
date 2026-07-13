package config

import (
	"strings"
	"testing"
	"time"
)

// setRequiredEnv 置齐 Load 的必填项，便于聚焦单个可选项的行为。
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
	t.Setenv("S3_ENDPOINT", "localhost:9000")
	t.Setenv("S3_ACCESS_KEY", "key")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_BUCKET", "bucket")
	t.Setenv("CDN_BASE_URL", "http://cdn.example.com")
}

// 安全回归：mock 支付渠道绝不能在生产自动启用。未显式置 PAYMENT_ENABLE_MOCK
// 时必须为 false——否则任意登录客户可经 mock 回调自助充值刷钱。
func TestMockProviderDisabledByDefault(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PaymentEnableMock {
		t.Fatal("PaymentEnableMock must default to false — mock must never auto-enable in production")
	}
}

func TestMockProviderEnabledWhenSet(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PAYMENT_ENABLE_MOCK", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.PaymentEnableMock {
		t.Fatal("PAYMENT_ENABLE_MOCK=true should enable the mock provider")
	}
}

func TestRechargeSweepDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RechargeTimeout != 10*time.Minute {
		t.Fatalf("RechargeTimeout = %s, want 10m", cfg.RechargeTimeout)
	}
	if cfg.RechargeSweepSpec != "*/5 * * * *" {
		t.Fatalf("RechargeSweepSpec = %q, want %q", cfg.RechargeSweepSpec, "*/5 * * * *")
	}
}

func TestRechargeSweepEnvOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("RECHARGE_TIMEOUT_SECONDS", "900")
	t.Setenv("RECHARGE_SWEEP_CRON", "*/3 * * * *")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RechargeTimeout != 15*time.Minute {
		t.Fatalf("RechargeTimeout = %s, want 15m", cfg.RechargeTimeout)
	}
	if cfg.RechargeSweepSpec != "*/3 * * * *" {
		t.Fatalf("RechargeSweepSpec = %q, want %q", cfg.RechargeSweepSpec, "*/3 * * * *")
	}
}

func TestRechargeTimeoutRejectsInvalidValue(t *testing.T) {
	for _, value := range []string{"invalid", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("RECHARGE_TIMEOUT_SECONDS", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "RECHARGE_TIMEOUT_SECONDS must be a positive integer") {
				t.Fatalf("Load error = %v, want positive integer validation error", err)
			}
		})
	}
}
