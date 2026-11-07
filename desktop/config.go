package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
)

// Config, kullanicinin girdigi baglanti ayarlari.
type Config struct {
	ServerAddr string `json:"server_addr"`
	Token      string `json:"token"`
	// LocalURL ARTIK tunel acmak icin kullanilmaz (yalnizca eski config'lerle
	// uyumluluk). Hedef sadece SharePort ile, o oturum icin ve acikca verilir.
	LocalURL   string `json:"local_url,omitempty"`
	CACertPath string `json:"ca_cert_path,omitempty"`
	Insecure   bool   `json:"insecure,omitempty"`

	// AutoConnect, uygulama acilinca (bilgisayar acilisinda --hidden ile
	// baslatildiginda da) kayitli hesapla kendiliginden baglansin mi. Ayarlar >
	// Genel'deki anahtar bunu yonetir.
	AutoConnect bool `json:"auto_connect"`

	// Guvenlik: Uzak kabuk ve ekran paylasimini devre disi birakma
	NoTerminal bool `json:"no_terminal"`
	NoScreen   bool `json:"no_screen"`

	// EnableDeploy, bu cihazda Zorven Apps dagitimina (Docker ile uygulama
	// calistirma) izin verir. CLI'nin `enable_deploy` ayari / --enable-deploy
	// bayragi ile AYNI anahtardir (ayar dosyasi ortak): varsayilan KAPALI; acik
	// olsa bile Docker'a erisilemezse ajan Apps ozelligini ilan etmez. Yalniz
	// SetEnableDeploy ile degisir (SaveConfig dokunmaz).
	EnableDeploy bool `json:"enable_deploy,omitempty"`

	// TerminalPlainZsh / TerminalPlainBash: uzak terminalde Zorven kabuk profilini kapat.
	TerminalPlainZsh  bool `json:"terminal_plain_zsh,omitempty"`
	TerminalPlainBash bool `json:"terminal_plain_bash,omitempty"`

	// TokenEnc, token'in isletim sistemi korumali (Windows DPAPI) hali. Doluysa
	// diskte Token bos yazilir; yuklenirken cozulur. Korumasiz platformlarda
	// kullanilmaz (Token acik metin kalir).
	TokenEnc string `json:"token_enc,omitempty"`

	// SvcTokenSig, kimligin Windows servisine aktarildigi (token+sunucu) imzasi;
	// ayni kimlik icin UAC bir daha sorulmaz. Token degisince bos/eski kalir.
	SvcTokenSig string `json:"svc_token_sig,omitempty"`

	// NotifyDisabled, isletim sistemi bildirimlerini (baglandi/koptu/guncelleme) kapatir.
	NotifyDisabled bool `json:"notify_disabled,omitempty"`

	// Theme, arayuz temasi: "" (isletim sistemini izle), "light" ya da "dark".
	// Pencere arka plani ve Windows baslik cubugu acilista buna gore boyanir.
	Theme string `json:"theme,omitempty"`

	// HideToTray, pencere kapatilinca cikmak yerine sistem tepsisine gizlensin mi.
	HideToTray bool `json:"hide_to_tray"`

	// Language, arayuz dili: "tr", "en", "ja", "zh"; bos = otomatik (ZORVEN_LANG /
	// LC_* / LANG / Windows arayuz dili, bkz. client/i18n.Detect).
	Language string `json:"language,omitempty"`

	// Hizli Port Paylasimi gecmisi
	LastPort    int   `json:"last_port,omitempty"`
	RecentPorts []int `json:"recent_ports,omitempty"`
}

func defaultConfig() Config {
	return Config{
		ServerAddr: "zorven.app:443",
		// AutoConnect VARSAYILAN KAPALI. Açıksa uygulama açılışta yalnızca
		// sunucuda tanımlı tünellere bağlanır; hiçbir yerel port kendiliğinden
		// tünellenmez (LocalURL boş, RequestedTarget gönderilmez).
		AutoConnect: false,
		NoTerminal:  false,
		NoScreen:    false,
		HideToTray:  true,
		// Port geçmişi yalnızca arayüzde öneri içindir; tünel açmaz.
		RecentPorts: []int{3000, 5173, 8080},
	}
}

// configPath, ayar dosyasinin isletim sistemine uygun yolu.
//
//	Windows : %AppData%\zorven\config.json
//	macOS   : ~/Library/Application Support/zorven/config.json
//	Linux   : ~/.config/zorven/config.json
//
// Eski rpshell yolu yalnizca yeni dosya yokken okunur.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%s: %w", tm("cfg.noDir"), err)
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

// ConfigNotice, ayar dosyasi yuklenirken olusan ve kullaniciya bildirilmesi
// gereken durum (bozuk dosya yedeklendi, token cozulemedi...).
type ConfigNotice struct {
	Kind   string `json:"kind"` // corrupt | read | decrypt
	Backup string `json:"backup,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Message, bildirimin kullaniciya gosterilecek (yerellestirilmis) metni.
func (n ConfigNotice) Message() string {
	switch n.Kind {
	case "corrupt":
		return tm("cfg.corrupt", n.Backup)
	case "decrypt":
		return tm("cfg.decrypt", n.Backup)
	default:
		return tm("cfg.readFail", n.Detail)
	}
}

// tokenProtect, token'i diske yazmadan once koruyan/cozen katman (Windows'ta
// DPAPI; diger platformlarda nil). Testler sahte bir uygulama takabilir.
type tokenProtect interface {
	Protect(plain []byte) ([]byte, error)
	Unprotect(enc []byte) ([]byte, error)
}

var tokenProtector = platformTokenProtector()

const tokenEncPrefix = "dpapi:"

// sealToken, cfg'nin diske yazilacak kopyasini uretir: koruyucu varsa Token
// sifrelenip TokenEnc'e konur; koruma basarisizsa acik metin kalir (kayip yok).
func sealToken(cfg Config) Config {
	cfg.TokenEnc = ""
	if tokenProtector == nil || cfg.Token == "" {
		return cfg
	}
	enc, err := tokenProtector.Protect([]byte(cfg.Token))
	if err != nil {
		return cfg
	}
	cfg.TokenEnc = tokenEncPrefix + base64.StdEncoding.EncodeToString(enc)
	cfg.Token = ""
	return cfg
}

// openToken, diskten okunan cfg'de TokenEnc varsa Token'i cozer. Acik metin
// token (eski surum) oldugu gibi kalir; bir sonraki kayitta sifrelenir.
func openToken(cfg *Config) error {
	if cfg.TokenEnc == "" {
		return nil
	}
	if cfg.Token != "" { // ikisi de varsa acik metin gecerli
		cfg.TokenEnc = ""
		return nil
	}
	if tokenProtector == nil || !strings.HasPrefix(cfg.TokenEnc, tokenEncPrefix) {
		return errors.New("token cannot be decrypted on this system")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cfg.TokenEnc, tokenEncPrefix))
	if err != nil {
		return err
	}
	plain, err := tokenProtector.Unprotect(raw)
	if err != nil {
		return err
	}
	cfg.Token = string(plain)
	cfg.TokenEnc = ""
	return nil
}

// backupConfigFile, okunamayan/bozuk ayar dosyasini yan yana .bak olarak saklar
// (sonraki kayit onun uzerine yazmasin, token kaybolmasin).
func backupConfigFile(path string) string {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		bak = fmt.Sprintf("%s.%s.bak", path, time.Now().Format("20060102-150405"))
	}
	if err := os.Rename(path, bak); err != nil {
		return ""
	}
	return bak
}

func loadConfig() (Config, error) {
	cfg, _, err := loadConfigNotice()
	return cfg, err
}

// loadConfigNotice, ayarlari okur. Dosya bozuksa .bak olarak yedeklenir ve
// varsayilanlarla devam edilir; bu durum notice ile bildirilir.
func loadConfigNotice() (Config, *ConfigNotice, error) {
	path, err := configPath()
	if err != nil {
		return defaultConfig(), nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return withCLIFallback(defaultConfig()), nil, nil // ilk calistirma
	}
	if err != nil {
		n := &ConfigNotice{Kind: "read", Detail: err.Error()}
		return defaultConfig(), n, errors.New(n.Message())
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		bak := backupConfigFile(path)
		n := &ConfigNotice{Kind: "corrupt", Backup: bak, Detail: err.Error()}
		return withCLIFallback(defaultConfig()), n, errors.New(n.Message())
	}
	var notice *ConfigNotice
	if err := openToken(&cfg); err != nil {
		// Token cozulemedi (baska kullanici/makine profili). Dosyayi yedekle ki
		// bir sonraki kayit sifreli degeri silmesin.
		bak := backupConfigFile(path)
		notice = &ConfigNotice{Kind: "decrypt", Backup: bak, Detail: err.Error()}
		cfg.Token, cfg.TokenEnc = "", ""
	}
	cfg = withCLIFallback(cfg)
	if len(cfg.RecentPorts) == 0 {
		cfg.RecentPorts = []int{3000, 5173, 8080}
	}
	// Migrasyon: eski surumler config'e kendiliginden "http://localhost:8003"
	// yaziyor ve her Baglan'da bu porta tunel actiriyordu. Kullanici secimi
	// olmadigi icin temizle; artik hedef yalnizca SharePort ile acikca verilir.
	if clientcfg.IsLegacyDefaultLocalURL(cfg.LocalURL) {
		cfg.LocalURL = ""
	}
	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}
	return cfg, notice, nil
}

// withCLIFallback, token yoksa CLI yapilandirmasindan alir.
func withCLIFallback(cfg Config) Config {
	if cfg.Token == "" {
		if cliCfg, err := clientcfg.Load(); err == nil && cliCfg.Token != "" {
			cfg.Token = cliCfg.Token
			if cliCfg.ServerAddr != "" {
				cfg.ServerAddr = cliCfg.ServerAddr
			}
		}
	}
	return cfg
}

// cfgFileMu, ayar dosyasina eszamanli yazimlari sirayla yapar.
var cfgFileMu sync.Mutex

// saveConfig, ayarlari ATOMIK yazar (gecici dosya + rename): yazma yarida
// kesilse bile eski dosya bozulmaz.
//
// Dosya CLI ile ORTAKTIR (client/config SaveFile da ayni dosyaya yazar): bu yapinin
// bilmedigi alanlar (CLI'nin enable_terminal/enable_screen'i ya da gelecekte eklenecek
// her alan) mevcut dosyadan korunur; yalniz Config'in kendi anahtarlari guncellenir
// (bkz. clientcfg.MarshalMerged).
func saveConfig(cfg Config) error {
	cfgFileMu.Lock()
	defer cfgFileMu.Unlock()
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%s: %w", tm("cfg.mkdirFail"), err)
	}

	res, err := clientcfg.MarshalMerged(path, sealToken(cfg))
	if err != nil {
		return err
	}
	if res.Corrupt {
		// Ezilecek dosya okunamiyor (baska bir arac bozdu): yuklemedeki ile ayni
		// politika, yan yana yedeklenir ki icerigi (belki token) kaybolmasin.
		_ = backupConfigFile(path)
	}
	data := res.Data

	// 0600: token iceriyor, yalnizca sahibi okuyabilmeli.
	// NOT: Windows'ta POSIX izinleri uygulanmaz (bkz. server/tlscert).
	fail := func(err error) error { return fmt.Errorf("%s: %w", tm("cfg.writeFail"), err) }
	tmp, err := os.CreateTemp(filepath.Dir(path), "config-*.tmp")
	if err != nil {
		return fail(err)
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fail(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fail(err)
	}
	return nil
}
