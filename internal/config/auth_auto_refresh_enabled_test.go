package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_AuthAutoRefreshEnabled_DefaultFalse(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
port: 8080
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if cfg.AuthAutoRefreshEnabled {
		t.Fatal("AuthAutoRefreshEnabled = true, want false")
	}
}

func TestLoadConfigOptional_AuthAutoRefreshEnabled_ExplicitTrue(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
auth-auto-refresh-enabled: true
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if !cfg.AuthAutoRefreshEnabled {
		t.Fatal("AuthAutoRefreshEnabled = false, want true")
	}
}
