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

// Dosyada sunucu yokken SaveFile varsayilan sunucuyu yazmaz; varsa korur.
func TestSaveFileOmitsDefaultServerWhenAbsent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	cfg, _ := LoadFile(p)
	cfg.Token = "t"
	if err := SaveFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if FileHasServer(p) {
		t.Fatal("varsayilan sunucu dosyaya yazildi")
	}
	cfg.ServerAddr = "a.example:1"
	if err := SaveFile(p, cfg); err != nil {
		t.Fatal(err)
	}
	if !FileHasServer(p) {
		t.Fatal("acik sunucu yazilmadi")
	}
}
