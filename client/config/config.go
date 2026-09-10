// Package config, Zorven istemcisinin yerel yapilandirmasini ve token yonetimini saglar.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config, kullanicinin yerel istemci ayarlari.
type Config struct {
	ServerAddr string `json:"server_addr"`
	Token      string `json:"token"`
	LocalURL   string `json:"local_url"`
	CACertPath string `json:"ca_cert_path,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
}

// DefaultConfig, varsayilan istemci ayarlarini doner.
func DefaultConfig() Config {
	return Config{
		ServerAddr: "zorven.app:443",
		LocalURL:   "http://localhost:8003",
		Insecure:   false,
	}
}

// Path, isletim sistemine uygun kullanici yapilandirma dosyasinin yolunu belirler.
// Oncelik:
//  1. %APPDATA%\zorven\config.json (veya ~/.config/zorven/config.json)
//  2. Eski %APPDATA%\rpshell\config.json (geriye donuk uyumluluk)
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("yapilandirma dizini alinamadi: %w", err)
	}

	zorvenPath := filepath.Join(dir, "zorven", "config.json")
	if _, err := os.Stat(zorvenPath); err == nil {
		return zorvenPath, nil
	}

	legacyPath := filepath.Join(dir, "rpshell", "config.json")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}

	return zorvenPath, nil
}

// SystemPath, sistem geneli (service modu) yapilandirma dosyasinin yolunu doner.
//  - Windows: %ProgramData%\zorven\config.json
//  - Linux/macOS: /etc/zorven/config.json
func SystemPath() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "zorven", "config.json")
	}
	if os.PathSeparator == '\\' {
		return filepath.Join(`C:\ProgramData`, "zorven", "config.json")
	}
	return "/etc/zorven/config.json"
}

// LoadFile, belirtilen yoldaki ayar dosyasini okur.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return DefaultConfig(), err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("ayar dosyasi bozuk: %w", err)
	}
	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}
	return cfg, nil
}

// Load, ayar dosyasini okur.
// Once kullanici ayarlarini arar; token bos ise sistem geneli ayarlarina (Service config) bakar.
func Load() (Config, error) {
	p, err := Path()
	var userCfg Config
	if err == nil {
		userCfg, _ = LoadFile(p)
		if userCfg.Token != "" {
			return userCfg, nil
		}
	}

	// Kullanici ayarlarinda token yoksa sistem ayarlarina bak
	sysCfg, sysErr := LoadFile(SystemPath())
	if sysErr == nil && sysCfg.Token != "" {
		return sysCfg, nil
	}

	if err != nil {
		return DefaultConfig(), err
	}
	return userCfg, nil
}

// SaveFile, ayarlari belirtilen dosyaya yazar (0600 izni).
func SaveFile(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("yapilandirma dizini olusturulamadi: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("ayar verisi serilestirilemedi: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("ayar dosyasina yazilamadi: %w", err)
	}
	return nil
}

// Save, ayarlari kullanici ayar dosyasina yazar.
func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return SaveFile(p, cfg)
}

// SaveSystem, ayarlari sistem geneli ayar dosyasina yazar.
func SaveSystem(cfg Config) error {
	return SaveFile(SystemPath(), cfg)
}

// SetToken, token'i gunceller ve kullanici ayarlarina kaydeder.
func SetToken(token string) error {
	cfg, _ := Load()
	cfg.Token = strings.TrimSpace(token)
	return Save(cfg)
}

// SetTokenSystem, token'i sistem ayarlarina kaydeder.
func SetTokenSystem(token string) error {
	cfg, _ := LoadFile(SystemPath())
	cfg.Token = strings.TrimSpace(token)
	return SaveSystem(cfg)
}

// SetServer, sunucu adresini gunceller ve kaydeder.
func SetServer(server string) error {
	cfg, _ := Load()
	cfg.ServerAddr = strings.TrimSpace(server)
	return Save(cfg)
}

// SetServerSystem, sunucu adresini sistem ayarlarina kaydeder.
func SetServerSystem(server string) error {
	cfg, _ := LoadFile(SystemPath())
	cfg.ServerAddr = strings.TrimSpace(server)
	return SaveSystem(cfg)
}

