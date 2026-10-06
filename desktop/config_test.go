package main

import (
	"os"
	"path/filepath"
	"testing"
)

// useTempConfigDir, os.UserConfigDir'i gecici dizine yonlendirir.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

func TestDefaultConfigNoAutoTarget(t *testing.T) {
	cfg := defaultConfig()
	if cfg.LocalURL != "" {
		t.Errorf("LocalURL bos olmali: %q", cfg.LocalURL)
	}
	if cfg.AutoConnect {
		t.Error("AutoConnect varsayilan kapali olmali")
	}
	for _, p := range cfg.RecentPorts {
		if p == 8003 {
			t.Error("8003 varsayilan oneriler arasinda olmamali")
		}
	}
}

// Eski kullanicinin config'inde kayitli localhost:8003 yuklenirken temizlenir.
func TestLoadConfigMigratesLegacyLocalURL(t *testing.T) {
	dir := useTempConfigDir(t)
	p := filepath.Join(dir, "zorven", "config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"server_addr":"zorven.app:443","token":"zrv_live_x","local_url":"http://localhost:8003","last_port":8003}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LocalURL != "" {
		t.Errorf("eski local_url temizlenmedi: %q", cfg.LocalURL)
	}
}
