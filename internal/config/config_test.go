package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvRejectsPlaceholder(t *testing.T) {
	_, err := FromEnv(env(nil))
	if err == nil || !strings.Contains(err.Error(), PlaceholderPassword) {
		t.Fatalf("expected placeholder error, got %v", err)
	}
}

func TestFromEnvDefaultsAndOverrides(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{
		"DATABASE_DSN":         "app:not-a-real-password@tcp(db:3306)/warehouse?parseTime=true",
		"LOG_LEVEL":            "debug",
		"RECEIPT_TOLERANCE_BP": "250",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.LogLevel != slog.LevelDebug || cfg.ReceiptToleranceBP != 250 || !cfg.AutoMigrate {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestFromEnvValidates(t *testing.T) {
	base := map[string]string{"DATABASE_DSN": "u:p@tcp(h:1)/d"}
	for key, val := range map[string]string{
		"LOG_LEVEL":            "loud",
		"AUTO_MIGRATE":         "maybe",
		"RECEIPT_TOLERANCE_BP": "20000",
		"SHUTDOWN_TIMEOUT":     "soon",
	} {
		m := map[string]string{key: val}
		for k, v := range base {
			m[k] = v
		}
		if _, err := FromEnv(env(m)); err == nil {
			t.Errorf("%s=%s: expected error", key, val)
		}
	}
}
