// Package config, Zorven istemcisinin yerel yapilandirmasini ve token yonetimini saglar.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config, kullanicinin yerel istemci ayarlari.
type Config struct {
	ServerAddr string `json:"server_addr"`
	// Servers (istege bagli): ek sunucu adresleri (host:port). Ajan ServerAddr
	// ile birlikte aday olarak kullanir, ilk baglantida sunucunun bildirdigi
	// dugum listesiyle birlestirir. ServerPinned ise kullanilmaz.
	Servers []string `json:"servers,omitempty"`
	// ServerPinned: kullanici sunucuyu acikca sabitledi (`zorven config set
	// server`, set-server). Sabitken dugum listesi/yuk devri kullanilmaz.
	ServerPinned bool   `json:"server_pinned,omitempty"`
	Token        string `json:"token"`
	// LocalURL, `zorven` komutu hedef verilmeden calistirildiginda KULLANILMAZ.
	// Varsayilan BOS'tur: kullanici acikca bir hedef vermedikce hicbir yerel
	// port icin tunel acilmaz. Alan yalnizca eski config dosyalariyla
	// uyumluluk icin tutulur.
	LocalURL   string `json:"local_url,omitempty"`
	CACertPath string `json:"ca_cert_path,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`
	// EnableTerminal / EnableScreen: kalici yerel izin tavani (zorven enable ... --persist,
	// service install --enable-*). Varsayilan KAPALI. Sunucudan degistirilemez.
	EnableTerminal bool `json:"enable_terminal,omitempty"`
	EnableScreen   bool `json:"enable_screen,omitempty"`
	// EnableDeploy: Zorven Apps (Docker ile uygulama calistirma) yerel izin tavani.
	// Varsayilan KAPALI; acik olsa bile Docker erisilebilir degilse ilan edilmez.
	EnableDeploy bool `json:"enable_deploy,omitempty"`
	// Operators: yalniz Windows servisi icin; masaustu uygulamasinin servis kontrol
	// kanalini tam yetkiyle kullanabilen etkilesimli kullanici SID'leri (kurulumu
	// baslatan kullanici). Yalniz yonetici yazabilen sistem dosyasinda tutulur;
	// kurulum/onarimda eklenir, silinmez.
	Operators []string `json:"operators,omitempty"`
	// Language: CLI dili (tr/en/ja/zh). Bossa ortamdan/isletim sisteminden secilir.
	Language string `json:"language,omitempty"`
}

// AddOperator, SID'i (buyuk/kucuk harf duyarsiz tekil) Operators listesine ekler;
// eklendiyse true doner. Bos SID eklenmez. Listeden silme yoktur.
func (c *Config) AddOperator(sid string) bool {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return false
	}
	for _, o := range c.Operators {
		if strings.EqualFold(o, sid) {
			return false
		}
	}
	c.Operators = append(c.Operators, sid)
	return true
}

// legacyDefaultLocalURLs, eski surumlerin config dosyasina KENDILIGINDEN
// yazdigi varsayilan hedeflerdir. Bunlar kullanicinin secimi degildi; yuklenirken
// temizlenir ki eski kullanicilarda istenmeyen tunel acilmasin.
var legacyDefaultLocalURLs = map[string]bool{
	"http://localhost:8003": true,
	"http://localhost:8000": true,
	"http://localhost:3000": true,
}

// IsLegacyDefaultLocalURL, verilen hedefin eski otomatik varsayilanlardan
// biri olup olmadigini soyler (masaustu uygulamasi da kullanir).
func IsLegacyDefaultLocalURL(u string) bool {
	return legacyDefaultLocalURLs[strings.TrimRight(strings.TrimSpace(u), "/")]
}

// DefaultConfig, varsayilan istemci ayarlarini doner.
func DefaultConfig() Config {
	return Config{
		ServerAddr: "zorven.app:443",
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
//   - Windows: %ProgramData%\zorven\config.json
//   - Linux/macOS: /etc/zorven/config.json
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
	// Eski surumlerin otomatik yazdigi hedefi (or. localhost:8003) at: kullanici
	// secimi degildi ve istemciyi o porta kendiliginden tunel acmaya iterdi.
	if IsLegacyDefaultLocalURL(cfg.LocalURL) {
		cfg.LocalURL = ""
	}
	return cfg, nil
}

// corruptWarned, bozuk ayar dosyasi uyarisinin surec basina bir kez basilmasini saglar.
var corruptWarned sync.Map

// backupCorrupt, ayrisamayan ayar dosyasini <dosya>.bak olarak kopyalar ve
// kullaniciyi stderr'de uyarir (surec basina dosya basina bir kez). Dosya
// yerinde birakilir; kullanici elle duzeltebilsin.
func backupCorrupt(path string, perr error) {
	if _, loaded := corruptWarned.LoadOrStore(path, true); loaded {
		return
	}
	bak := path + ".bak"
	note := ""
	if data, err := os.ReadFile(path); err == nil {
		if werr := os.WriteFile(bak, data, 0o600); werr == nil {
			_ = os.Chmod(bak, 0o600)
			note = " (backup: " + bak + ")"
		}
	}
	fmt.Fprintf(os.Stderr, "zorven: WARNING: config file unreadable, using defaults: %s: %v%s\n", path, perr, note)
}

// Load, ayar dosyasini okur.
// Once kullanici ayarlarini arar; token bos ise sistem geneli ayarlarina (Service config) bakar.
// Kullanici dosyasi bozuksa sessizce yok sayilmaz: stderr'e uyari basilir,
// <dosya>.bak olarak yedeklenir ve hata doner (cagirani varsayilanlarla surebilir).
func Load() (Config, error) {
	p, err := Path()
	var userCfg Config
	var userErr error
	if err == nil {
		userCfg, userErr = LoadFile(p)
		if userErr != nil {
			backupCorrupt(p, userErr)
		} else if userCfg.Token != "" {
			return userCfg, nil
		}
	}

	// Kullanici ayarlarinda token yoksa sistem ayarlarina bak
	sysCfg, sysErr := LoadFile(SystemPath())
	if sysErr == nil && sysCfg.Token != "" {
		return sysCfg, userErr
	}

	if err != nil {
		return DefaultConfig(), err
	}
	return userCfg, userErr
}

// SaveFile, ayarlari belirtilen dosyaya yazar (0600 izni). Dosya masaustu
// uygulamasiyla ORTAKTIR: bu yapinin bilmedigi alanlar (masaustunun auto_connect,
// token_enc... ya da gelecekte eklenecekler) mevcut dosyadan korunur; yalniz
// Config'in kendi anahtarlari guncellenir (bkz. MarshalMerged).
func SaveFile(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("yapilandirma dizini olusturulamadi: %w", err)
	}

	res, err := MarshalMerged(path, cfg)
	if err != nil {
		return fmt.Errorf("ayar verisi serilestirilemedi: %w", err)
	}
	if res.Corrupt {
		// Ezilecek dosya okunamiyordu: icerigi (belki token) kaybolmasin diye yedeklenir.
		backupBeforeOverwrite(path)
	}
	data := res.Data

	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("ayar dosyasina yazilamadi: %w", err)
	}
	// WriteFile mevcut dosyanin iznini DEGISTIRMEZ: eskiden gevsek izinle
	// olusmus bir dosyada token herkese okunur kalirdi. Her yazimda daralt.
	_ = os.Chmod(path, 0o600)
	// Windows'ta chmod ACL'i daraltmaz; dosyaya korumali DACL uygulanir.
	if err := restrictFile(path, isSystemPath(path)); err != nil {
		return fmt.Errorf("ayar dosyasi izinleri daraltilamadi: %w", err)
	}
	return nil
}

// writeFileAtomic, veriyi ayni dizinde gecici dosyaya yazar, fsync eder ve
// hedefin uzerine rename eder: yarim yazilmis/kesilmis ayar dosyasi olusmaz.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
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

// SetToken, token'i gunceller ve YALNIZ kullanici ayar dosyasina kaydeder
// (sistem ayarlari kullanici dosyasina kopyalanmaz). Dosya bozuksa hata doner.
func SetToken(token string) error {
	p, err := Path()
	if err != nil {
		return err
	}
	cfg, lerr := LoadFile(p)
	if lerr != nil {
		// Bozuk dosya yedeklenir, temiz ayarla devam edilir (token/sunucu yazilabilsin).
		backupCorrupt(p, lerr)
		cfg = DefaultConfig()
	}
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
	p, err := Path()
	if err != nil {
		return err
	}
	cfg, lerr := LoadFile(p)
	if lerr != nil {
		// Bozuk dosya yedeklenir, temiz ayarla devam edilir (token/sunucu yazilabilsin).
		backupCorrupt(p, lerr)
		cfg = DefaultConfig()
	}
	cfg.ServerAddr = strings.TrimSpace(server)
	cfg.ServerPinned = true
	return Save(cfg)
}

// SetServerSystem, sunucu adresini sistem ayarlarina kaydeder.
func SetServerSystem(server string) error {
	cfg, _ := LoadFile(SystemPath())
	cfg.ServerAddr = strings.TrimSpace(server)
	cfg.ServerPinned = true
	return SaveSystem(cfg)
}

// WriteSecretFile, gizli veriyi (or. cihaz ozel anahtari) 0600 izniyle yazar;
// Windows'ta korumali DACL (yalniz SYSTEM, Administrators, sahibi) uygular.
func WriteSecretFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("dizin olusturulamadi: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("dosya yazilamadi: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	if err := restrictFile(path, isSystemPath(path)); err != nil {
		return fmt.Errorf("dosya izinleri daraltilamadi: %w", err)
	}
	return nil
}
