package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
)

// Config, kullanicinin girdigi baglanti ayarlari.
type Config struct {
	ServerAddr string `json:"server_addr"`
	Token      string `json:"token"`
	LocalURL   string `json:"local_url"`
	CACertPath string `json:"ca_cert_path,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`

	// AutoConnect, uygulama acilinca kendiliginden baglansin mi.
	AutoConnect bool `json:"auto_connect"`

	// Guvenlik: Uzak kabuk ve ekran paylasimini devre disi birakma
	NoTerminal bool `json:"no_terminal"`
	NoScreen   bool `json:"no_screen"`

	// HideToTray, pencere kapatilinca cikmak yerine sistem tepsisine gizlensin mi.
	HideToTray bool `json:"hide_to_tray"`

	// Hizli Port Paylasimi gecmisi
	LastPort    int   `json:"last_port,omitempty"`
	RecentPorts []int `json:"recent_ports,omitempty"`
}

func defaultConfig() Config {
	return Config{
		ServerAddr:  "zorven.app:443",
		LocalURL:    "http://localhost:8003",
		AutoConnect: true,
		NoTerminal:  false,
		NoScreen:    false,
		HideToTray:  true,
		LastPort:    8003,
		RecentPorts: []int{8003, 3000, 5173, 8080},
	}
}

// configPath, ayar dosyasinin isletim sistemine uygun yolu.
//
//	Windows : %AppData%\rpshell\config.json
//	macOS   : ~/Library/Application Support/rpshell/config.json
//	Linux   : ~/.config/rpshell/config.json
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("yapilandirma dizini bulunamadi: %w", err)
	}
	p := filepath.Join(dir, "zorven", "config.json")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	legacy := filepath.Join(dir, "rpshell", "config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy, nil
	}
	return p, nil
}

func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return defaultConfig(), err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaultConfig(), nil // ilk calistirma
	}
	if err != nil {
		return defaultConfig(), fmt.Errorf("ayarlar okunamadi: %w", err)
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Bozuk dosya kullaniciyi kilitlemesin: varsayilana don.
		return defaultConfig(), fmt.Errorf("ayar dosyasi bozuk, varsayilanlara donuldu: %w", err)
	}

	// Token yoksa CLI yapilandirmasindan al
	if cfg.Token == "" {
		if cliCfg, err := clientcfg.Load(); err == nil && cliCfg.Token != "" {
			cfg.Token = cliCfg.Token
			if cliCfg.ServerAddr != "" {
				cfg.ServerAddr = cliCfg.ServerAddr
			}
		}
	}
	if len(cfg.RecentPorts) == 0 {
		cfg.RecentPorts = []int{8003, 3000, 5173, 8080}
	}
	if cfg.LastPort == 0 || cfg.LastPort == 3000 {
		cfg.LastPort = 8003
	}
	if cfg.LocalURL == "" || cfg.LocalURL == "http://localhost:3000" {
		cfg.LocalURL = "http://localhost:8003"
	}
	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("yapilandirma dizini olusturulamadi: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// 0600: token iceriyor, yalnizca sahibi okuyabilmeli.
	// NOT: Windows'ta POSIX izinleri uygulanmaz (bkz. server/tlscert).
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("ayarlar yazilamadi: %w", err)
	}
	return nil
}
