package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadSave(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("APPDATA", tempDir)
	t.Setenv("HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", tempDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerAddr != "zorven.app:443" {
		t.Errorf("expected default server zorven.app:443, got %s", cfg.ServerAddr)
	}

	if err := SetToken("zrv_live_test_123"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if err := SetServer("api.zorven.app"); err != nil {
		t.Fatalf("SetServer: %v", err)
	}

	updated, err := Load()
	if err != nil {
		t.Fatalf("Load updated: %v", err)
	}
	if updated.Token != "zrv_live_test_123" {
		t.Errorf("expected token zrv_live_test_123, got %s", updated.Token)
	}
	if updated.ServerAddr != "api.zorven.app" {
		t.Errorf("expected server api.zorven.app, got %s", updated.ServerAddr)
	}

	p, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("expected absolute path, got %s", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file not written: %v", err)
	}
}
