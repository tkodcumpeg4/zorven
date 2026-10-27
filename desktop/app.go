package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/metrics"
	"github.com/tkodcumpeg4/zorven/desktop/autostart"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App, arayuze bind edilen Go tarafi.
type App struct {
	ctx context.Context

	mu     sync.Mutex
	cfg    Config
	status agent.Status
	cancel context.CancelFunc // aktif baglantiyi durdurur
	ag     *agent.Agent       // aktif calisan ajan
	target string             // SharePort ile acikca istenen hedef (yoksa bos)

	auto *autostart.Manager
	tray *tray
	log  *slog.Logger
}

func NewApp() *App {
	cfg, _ := loadConfig() // hata olsa da varsayilanla devam edilir
	// Etkin dil (ajan mesajlari dahil): ayar > ortam > isletim sistemi > en.
	i18n.Set(i18n.Detect(cfg.Language))
	auto, _ := autostart.New()

	return &App{
		cfg:    cfg,
		status: agent.Status{State: agent.StateStopped, Message: tm("status.none")},
		auto:   auto,
		log:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

// startup, Wails tarafindan pencere hazir oldugunda cagrilir.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startTray()
	go a.telemetryLoop(ctx)
	if a.cfg.AutoConnect && a.cfg.Token != "" {
		go a.Connect()
	}
}

func (a *App) telemetryLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := metrics.Collect()
			if m != nil && a.ctx != nil {
				wailsrt.EventsEmit(a.ctx, "telemetry", m)
			}
		}
	}
}

// --- Arayuze acilan metotlar ----------------------------------------------

// GetConfig, kayitli ayarlari doner.
func (a *App) GetConfig() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// SaveConfig, ayarlari kaydeder. Bagliyken cagrilirsa baglanti KOPARILMAZ;
// yeni ayarlar bir sonraki baglantida gecerli olur (kullanici "Yeniden baglan"
// diyene kadar mevcut tunel calismaya devam eder).
func (a *App) SaveConfig(cfg Config) string {
	cfg.ServerAddr = strings.TrimSpace(cfg.ServerAddr)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.LocalURL = strings.TrimSpace(cfg.LocalURL)

	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}

	a.mu.Lock()
	cfg.Language = a.cfg.Language // dil yalnizca SetLanguage ile degisir
	a.cfg = cfg
	a.mu.Unlock()

	if err := saveConfig(cfg); err != nil {
		return err.Error()
	}
	return ""
}

// Connect, tunel baglantisini baslatir. Zaten bagliysa once koparir.
//
// Bu cagri HICBIR yerel port icin tunel acmaz: istemci yalnizca sunucuda
// tanimli tunelleri alir. Belirli bir portu paylasmak icin SharePort kullanilir.
func (a *App) Connect() string {
	return a.connect("")
}

// connect, baglantiyi kurar. target bos degilse (yalnizca SharePort'tan,
// kullanicinin acik istegiyle) sunucudan o hedef icin tunel istenir.
func (a *App) connect(target string) string {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	cfg := a.cfg
	a.mu.Unlock()

	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}
	if cfg.Token == "" {
		return tm("app.tokenEmpty")
	}

	ctx, cancel := context.WithCancel(context.Background())

	var ag *agent.Agent
	ag = &agent.Agent{
		ServerAddr:        cfg.ServerAddr,
		Token:             cfg.Token,
		LocalURL:          target,
		RequestedTarget:   target,
		Insecure:          cfg.Insecure,
		CACertPath:        cfg.CACertPath,
		NoTerminal:        cfg.NoTerminal,
		NoScreen:          cfg.NoScreen,
		TerminalPlainZsh:  cfg.TerminalPlainZsh,
		TerminalPlainBash: cfg.TerminalPlainBash,
		Version:           version,
		AppKind:           "desktop",
		// Masaustu kendi guncelleyicisini kullanir (desktop.json); gomulu ajan
		// CLI ikilisini bu exe'nin uzerine yazmamali.
		NoAutoUpdate: true,
		Log:          a.log,
		// Eski (iptal edilmis) ajanin gec gelen "durduruldu" durumu yeni
		// baglantinin durumunu ezmesin: yalnizca aktif ajan yayin yapar.
		OnStatus: func(s agent.Status) {
			if a.isCurrent(ag) {
				a.publish(s)
			}
		},
		OnRequest: func(method, path string, status int, duration time.Duration) {
			if a.ctx != nil {
				wailsrt.EventsEmit(a.ctx, "request_log", map[string]any{
					"time":        time.Now().Format("15:04:05"),
					"method":      method,
					"path":        path,
					"status":      status,
					"duration_ms": duration.Milliseconds(),
				})
			}
		},
	}

	a.mu.Lock()
	a.cancel = cancel
	a.ag = ag
	a.target = target
	a.mu.Unlock()

	go func() {
		defer cancel()
		if err := ag.Run(ctx); err != nil && a.isCurrent(ag) {
			a.publish(agent.Status{State: agent.StateFatal, Message: err.Error()})
		}
	}()
	return ""
}

// SharePort, belirtilen portu tek tikla paylasir.
func (a *App) SharePort(port int, secureMode bool) string {
	if port < 1 || port > 65535 {
		return tm("app.badPort")
	}
	a.mu.Lock()
	a.cfg.LastPort = port
	// Hedef KALICI olarak config'e yazilmaz: sonraki Baglan/otomatik baglanma
	// bu portu yeniden tunellemesin. Yalnizca bu cagri icin kullanilir.
	target := fmt.Sprintf("http://localhost:%d", port)
	newRecents := []int{port}
	for _, p := range a.cfg.RecentPorts {
		if p != port && len(newRecents) < 6 {
			newRecents = append(newRecents, p)
		}
	}
	a.cfg.RecentPorts = newRecents
	if secureMode {
		a.cfg.NoTerminal = true
		a.cfg.NoScreen = true
	}
	cfg := a.cfg
	a.mu.Unlock()

	_ = saveConfig(cfg)
	return a.connect(target)
}

// GetHardwareMetrics, sistem telemetrisini anlik olarak ceker.
func (a *App) GetHardwareMetrics() *protocol.Metrics {
	return metrics.Collect()
}

// OpenBrowser, verilen linki sistem tarayicisinda acar.
func (a *App) OpenBrowser(urlStr string) {
	if a.ctx != nil && urlStr != "" {
		wailsrt.BrowserOpenURL(a.ctx, urlStr)
	}
}

// CopyClipboard, metni panoya kopyalar.
func (a *App) CopyClipboard(text string) error {
	if a.ctx != nil {
		return wailsrt.ClipboardSetText(a.ctx, text)
	}
	return nil
}

// Disconnect, baglantiyi keser.
func (a *App) Disconnect() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.ag = nil
	a.mu.Unlock()
	a.publish(agent.Status{State: agent.StateStopped, Message: tm("status.cut")})
}

// CloseSessions, aktif uzak terminal ve ekran oturumlarını sonlandırır.
func (a *App) CloseSessions() {
	a.mu.Lock()
	ag := a.ag
	a.mu.Unlock()
	if ag != nil {
		ag.CloseSessions()
	}
}

// GetStatus, o anki baglanti durumu (arayuz ilk acilista bunu cagirir).
func (a *App) GetStatus() agent.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// GetAutostart, acilista otomatik baslatma acik mi.
func (a *App) GetAutostart() bool {
	if a.auto == nil {
		return false
	}
	on, err := a.auto.Enabled()
	if err != nil {
		return false
	}
	return on
}

// SetAutostart, acilista otomatik baslatmayi acar/kapatir.
// Hata mesajini doner; bos string basari demektir.
func (a *App) SetAutostart(on bool) string {
	if a.auto == nil {
		return tm("app.noAutostart")
	}
	if err := a.auto.Set(on); err != nil {
		return err.Error()
	}
	return ""
}

// LanguageInfo, arayuze dil ayarini ve etkin dili bildirir.
type LanguageInfo struct {
	Setting   string `json:"setting"`   // kayitli deger: "", "tr", "en", "ja"
	Effective string `json:"effective"` // gercekte kullanilan dil
}

// GetLanguage, kayitli dil ayarini ve etkin dili doner.
func (a *App) GetLanguage() LanguageInfo {
	a.mu.Lock()
	s := a.cfg.Language
	a.mu.Unlock()
	return LanguageInfo{Setting: s, Effective: i18n.Lang()}
}

// SetLanguage, arayuz dilini kaydeder ("" = otomatik) ve hemen uygular:
// client/i18n etkin dili degisir (ajan mesajlari dahil), tepsi menusu
// yeniden etiketlenir. Hata metni doner; bos = basari.
func (a *App) SetLanguage(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang != "" && i18n.Normalize(lang) == "" {
		return tm("app.badLanguage", lang)
	}
	lang = i18n.Normalize(lang)

	a.mu.Lock()
	a.cfg.Language = lang
	cfg := a.cfg
	st := a.status
	a.mu.Unlock()

	i18n.Set(i18n.Detect(lang))
	if err := saveConfig(cfg); err != nil {
		return tm("app.saveFail", err.Error())
	}
	if a.tray != nil {
		a.tray.relabel()
		a.tray.update(st)
	}
	return ""
}

// Platform, arayuzun platforma ozel metin gostermesi icin.
func (a *App) Platform() string { return runtime.GOOS }

// --- ic yardimcilar --------------------------------------------------------

// isCurrent, verilen ajanin hala aktif baglanti olup olmadigini soyler.
func (a *App) isCurrent(ag *agent.Agent) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ag == ag
}

// publish, durumu saklar ve arayuze olay olarak gonderir.
func (a *App) publish(s agent.Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()

	if a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "status", s)
	}
	if a.tray != nil {
		a.tray.update(s)
	}
}
