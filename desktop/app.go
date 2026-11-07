package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/metrics"
	"github.com/tkodcumpeg4/zorven/desktop/autostart"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// oldAgentWait, yeni baglanti kurulmadan once eski ajanin Run'dan donmesi icin
// beklenen azami sure (sunucu ayni cihaz icin ikinci oturumu 409 ile reddeder).
const oldAgentWait = 10 * time.Second

// App, arayuze bind edilen Go tarafi.
type App struct {
	ctx context.Context

	mu         sync.Mutex
	cfg        Config
	cfgNotice  *ConfigNotice
	status     agent.Status
	cancel     context.CancelFunc // aktif baglantiyi durdurur
	ag         *agent.Agent       // aktif calisan ajan
	done       chan struct{}      // son baslatilan ajanin Run'i donunce kapanir
	gen        uint64             // Disconnect sayaci (bekleyen connect'i iptal eder)
	target     string             // SharePort ile acikca istenen hedef (yoksa bos)
	secure     bool               // SharePort guvenli modu: YALNIZ bu oturum icin, kalici degil
	takeover   bool               // servis varken masaustu devralsin (oturum ici)
	svc        ServiceState       // CLI ajani/servis durumu
	handoffErr string             // kurulu kip: kimligin servise aktarimi son hata metni
	prevState  agent.State
	wasUp      bool      // bu oturumda en az bir kez baglandi (yeniden baglanma bildirimi icin)
	agDeploy   bool      // calisan gomulu ajan Zorven Apps izniyle mi baslatildi (bkz. wantDeploy)
	connAt     time.Time // gomulu ajanin son (yeniden) baglanma ani (Zorven Net hazirlik suresi icin)

	nt netTrack // Zorven Net: bu oturumda kurulan gecici paylasimlar ve yerel erisimler (netshare.go)

	connMu sync.Mutex // connect cagrilarini siraya dizer
	saveMu sync.Mutex // ayar kaydini siraya dizer (en guncel kopya yazilir)

	// Cihaz girisi (tarayicidan): tek yoklayici, iptal edilebilir.
	loginCancel context.CancelFunc

	visible     atomic.Bool // pencere gorunur mu (telemetri duraklatma)
	startHidden bool

	traffic trafficStats
	upd     updateState

	auto     *autostart.Manager
	tray     *tray
	log      *slog.Logger
	errs     *errRing
	logClose io.Closer
}

func NewApp(startHidden bool) *App {
	log, ring, closer := newDesktopLogger()
	// Gomulu ajanin paket duzeyindeki slog cagrilari da masaustu gunlugune gitsin.
	slog.SetDefault(log)
	cfg, notice, err := loadConfigNotice()
	if err != nil {
		log.Error("ayarlar yuklenemedi", "hata", err)
	}
	// Etkin dil (ajan mesajlari dahil): ayar > ortam > isletim sistemi > en.
	i18n.Set(i18n.Detect(cfg.Language))
	auto, _ := autostart.New()

	a := &App{
		cfg:         cfg,
		cfgNotice:   notice,
		status:      agent.Status{State: agent.StateStopped, Message: tm("status.none")},
		prevState:   agent.StateStopped,
		auto:        auto,
		log:         log,
		errs:        ring,
		logClose:    closer,
		startHidden: startHidden,
	}
	a.visible.Store(!startHidden)
	log.Info("masaustu basladi", "surum", version, "os", runtime.GOOS, "gizli", startHidden)
	return a
}

// startup, Wails tarafindan pencere hazir oldugunda cagrilir.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := wailsrt.InitializeNotifications(ctx); err != nil {
		a.log.Warn("bildirimler baslatilamadi", "hata", err)
	}
	cleanupOldBinary()
	if a.auto != nil {
		if err := a.auto.Refresh(); err != nil {
			a.log.Warn("otomatik baslatma kaydi tazelenemedi", "hata", err)
		}
	}
	// Kucuk ekranlarda baslangic boyutu calisma alanina sigsin.
	if w, h := fitToScreen(ctx, 1040, 680); h < 680 || w < 1040 {
		wailsrt.WindowSetSize(ctx, w, h)
	}
	a.startTray()
	go a.telemetryLoop(ctx)
	go a.serviceLoop(ctx)
	go a.updateLoop(ctx)
	if a.cfgNotice != nil {
		a.log.Warn("ayar bildirimi", "tur", a.cfgNotice.Kind, "yedek", a.cfgNotice.Backup)
	}
	// Otomatik baglanma (Ayarlar > Genel): acilista ve bilgisayar acilisinda
	// --hidden ile baslatildiginda da ayni yol. Hicbir yerel port tunellenmez.
	// Kurulu kipte oturum varsa kimligi servise aktarmak icin de bu yol calisir.
	if a.shouldAutoConnect() || (installedMode() && a.IsAuthed()) {
		go a.Connect()
	}
}

// shouldAutoConnect, acilista kayitli hesapla kendiliginden baglanilmali mi:
// ayar acik ve kayitli bir token var. Varsayilan KAPALI (bkz. defaultConfig).
func (a *App) shouldAutoConnect() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.AutoConnect && strings.TrimSpace(a.cfg.Token) != ""
}

// shutdown, Wails kapanirken: gunluk dosyasini kapatir.
func (a *App) shutdown(_ context.Context) {
	a.log.Info("masaustu kapaniyor")
	if a.logClose != nil {
		_ = a.logClose.Close()
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
			// Pencere gizliyken ya da oturum kapaliyken telemetri toplanmaz:
			// gereksiz CPU/pil kullanimi ve bos olay trafigi olmasin.
			if !a.visible.Load() || !a.IsAuthed() {
				continue
			}
			if m := metrics.Collect(); m != nil {
				emit(a.ctx, "telemetry", m)
			}
			if snap, changed := a.traffic.snapshot(); changed {
				emit(a.ctx, "traffic", snap)
			}
		}
	}
}

// serviceLoop, CLI ajani/servis durumunu 5 sn'de bir izler.
func (a *App) serviceLoop(ctx context.Context) {
	a.serviceLoopTick()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !a.IsAuthed() {
				continue
			}
			a.serviceLoopTick()
			if a.tray != nil {
				a.tray.update(a.GetStatus())
			}
		}
	}
}

// emit, arayuze olay yollar (pencere hazir degilse sessizce gecer).
func emit(ctx context.Context, name string, data ...any) {
	if ctx != nil {
		wailsrt.EventsEmit(ctx, name, data...)
	}
}

// persist, en guncel ayarlari diske yazar (kayitlar sirayla; son yazan en yeni kopyadir).
func (a *App) persist() error {
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	return saveConfig(cfg)
}

// --- Arayuze acilan metotlar ----------------------------------------------

// GetConfig, kayitli ayarlari doner (token arayuze verilmez).
func (a *App) GetConfig() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cfg
	c.Token = ""
	c.TokenEnc = ""
	// Kurulu kipte izinlerin gercek kaynagi servistir (kullanici ayari yalniz yansima).
	if installedMode() && a.svc.Found && a.svc.Service {
		c.NoTerminal, c.NoScreen, c.EnableDeploy = !a.svc.TerminalCap, !a.svc.ScreenCap, a.svc.DeployOn
	}
	return c
}

// cfgCopy, token dahil ayar kopyasi (yalniz Go ici; arayuze verilmez).
func (a *App) cfgCopy() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// GetConfigNotice, ayar yuklenirken olusan bildirimi (yoksa bos) yerellestirilmis doner.
func (a *App) GetConfigNotice() string {
	a.mu.Lock()
	n := a.cfgNotice
	a.mu.Unlock()
	if n == nil {
		return ""
	}
	return n.Message()
}

var serverAddrRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(?::[0-9]{1,5})?$`)

// validateServerAddr, "host" ya da "host:port" bicimini dogrular; bos = varsayilan.
func validateServerAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "https://"), "wss://")
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" || addr == "localhost:8443" {
		return "zorven.app:443", nil
	}
	if !serverAddrRe.MatchString(addr) {
		return "", fmt.Errorf("%s", tm("adv.badServer"))
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		var port int
		fmt.Sscanf(addr[i+1:], "%d", &port)
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("%s", tm("adv.badServer"))
		}
	}
	return addr, nil
}

// validateCAPath, bos ya da gecerli PEM sertifika dosyasi yolu olmali.
func validateCAPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("%s", tm("adv.caRead", err))
	}
	if !x509.NewCertPool().AppendCertsFromPEM(data) {
		return fmt.Errorf("%s", tm("adv.caBad"))
	}
	return nil
}

// SaveAdvanced, sunucu adresi ve ozel CA yolunu dogrulayip kaydeder. Hata
// metni doner (bos = basari). Yeni degerler bir sonraki baglantida gecerli olur.
func (a *App) SaveAdvanced(server, caPath string) string {
	addr, err := validateServerAddr(server)
	if err != nil {
		return err.Error()
	}
	caPath = strings.TrimSpace(caPath)
	if err := validateCAPath(caPath); err != nil {
		return err.Error()
	}
	a.mu.Lock()
	a.cfg.ServerAddr = addr
	a.cfg.CACertPath = caPath
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return tm("app.saveFail", err.Error())
	}
	return ""
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
	// Zorven Apps izni guvenlik ayaridir: yalnizca SetEnableDeploy ile (acik
	// kullanici eylemiyle) degisir, genel kayit ona dokunamaz.
	cfg.EnableDeploy = a.cfg.EnableDeploy
	if cfg.Token == "" {
		cfg.Token = a.cfg.Token // arayuz token gormez; bos gelen degeri silme
	}
	a.cfg = cfg
	a.mu.Unlock()

	if err := a.persist(); err != nil {
		return err.Error()
	}
	return ""
}

// Connect, tunel baglantisini baslatir. Zaten bagliysa once koparir.
//
// Bu cagri HICBIR yerel port icin tunel acmaz: istemci yalnizca sunucuda
// tanimli tunelleri alir. Belirli bir portu paylasmak icin SharePort kullanilir.
func (a *App) Connect() string {
	return a.connect("", false)
}

// connect, baglantiyi kurar. target bos degilse (yalnizca SharePort'tan,
// kullanicinin acik istegiyle) sunucudan o hedef icin tunel istenir. secure,
// SharePort guvenli modudur: terminal/ekran bu OTURUM icin kapatilir, ayara
// yazilmaz.
func (a *App) connect(target string, secure bool) string {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	// Kurulu Windows kipi: HER ZAMAN servis uzerinden; gomulu ajan yok.
	if installedMode() {
		return a.connectInstalled(target)
	}
	// Ayni makinede CLI ajani/servis varsa gomulu ajan baslatilmaz (409 olur).
	if a.serviceBlocks() {
		a.log.Info("baglanti atlandi: CLI ajani/servis calisiyor")
		return tm("svc.active")
	}

	a.mu.Lock()
	cfg := a.cfg
	gen := a.gen
	oldCancel := a.cancel
	prevDone := a.done
	a.cancel = nil
	a.ag = nil // eski ajanin gec durum olaylari yayinlanmasin
	a.mu.Unlock()

	if cfg.ServerAddr == "" || cfg.ServerAddr == "localhost:8443" {
		cfg.ServerAddr = "zorven.app:443"
	}
	if cfg.Token == "" {
		return tm("app.tokenEmpty")
	}

	// Eski ajani durdur ve Run'in donmesini bekle: aksi halde sunucu henuz
	// kapanmamis oturum yuzunden yenisini 409 ile reddeder.
	if oldCancel != nil {
		oldCancel()
	}
	if !waitDone(prevDone, oldAgentWait) {
		a.log.Warn("eski ajan zamaninda kapanmadi, yine de baglaniliyor")
	}
	a.mu.Lock()
	if a.gen != gen { // bekleme sirasinda Disconnect cagrildi
		a.mu.Unlock()
		return ""
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	a.traffic.reset()

	ag := embeddedAgent(cfg, target, secure)
	ag.Log = a.log
	// Eski (iptal edilmis) ajanin gec gelen "durduruldu" durumu yeni
	// baglantinin durumunu ezmesin: yalnizca aktif ajan yayin yapar.
	ag.OnStatus = func(s agent.Status) {
		if a.isCurrent(ag) {
			a.publish(s)
		}
	}
	ag.OnRequest = func(method, path string, status int, duration time.Duration) {
		a.traffic.add(status, duration)
		now := time.Now()
		emit(a.ctx, "request_log", map[string]any{
			"ts":          now.Format(time.RFC3339),
			"time":        now.Format("15:04:05"),
			"method":      method,
			"path":        path,
			"status":      status,
			"duration_ms": duration.Milliseconds(),
		})
	}

	done := make(chan struct{})
	a.mu.Lock()
	a.cancel = cancel
	a.ag = ag
	a.done = done
	a.target = target
	a.secure = secure
	a.agDeploy = ag.EnableDeploy
	a.mu.Unlock()

	go func() {
		defer close(done)
		defer cancel()
		if err := ag.Run(ctx); err != nil && a.isCurrent(ag) {
			a.publish(agent.Status{State: agent.StateFatal, Message: err.Error()})
		}
	}()
	return ""
}

// embeddedAgent, kayitli ayarlardan gomulu ajani kurar; durum ve istek geri
// cagrilari connect'te baglanir. target bos degilse (yalnizca SharePort'tan)
// sunucudan o hedef icin tunel istenir; secure, SharePort guvenli modudur.
func embeddedAgent(cfg Config, target string, secure bool) *agent.Agent {
	return &agent.Agent{
		ServerAddr:        cfg.ServerAddr,
		Token:             cfg.Token,
		LocalURL:          target,
		RequestedTarget:   target,
		Insecure:          cfg.Insecure,
		CACertPath:        cfg.CACertPath,
		NoTerminal:        cfg.NoTerminal || secure,
		NoScreen:          cfg.NoScreen || secure,
		EnableDeploy:      wantDeploy(cfg, secure),
		TerminalPlainZsh:  cfg.TerminalPlainZsh,
		TerminalPlainBash: cfg.TerminalPlainBash,
		Version:           version,
		AppKind:           "desktop",
		// Masaustu kendi guncelleyicisini kullanir (desktop.json); gomulu ajan
		// CLI ikilisini bu exe'nin uzerine yazmamali.
		NoAutoUpdate: true,
	}
}

// wantDeploy, gomulu ajanin Zorven Apps izniyle baslatilip baslatilmayacagi:
// ayar acik ve guvenli mod (terminal/ekran/Apps kapali oturum) degil.
func wantDeploy(cfg Config, secure bool) bool { return cfg.EnableDeploy && !secure }

// waitDone, ch kapanana ya da sure dolana kadar bekler; kapandiysa true. nil = bekleme yok.
func waitDone(ch <-chan struct{}, d time.Duration) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
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
	// Guvenli mod da KALICI degildir (NoTerminal/NoScreen ayara yazilmaz).
	target := fmt.Sprintf("http://localhost:%d", port)
	newRecents := []int{port}
	for _, p := range a.cfg.RecentPorts {
		if p != port && len(newRecents) < 6 {
			newRecents = append(newRecents, p)
		}
	}
	a.cfg.RecentPorts = newRecents
	a.mu.Unlock()

	if err := a.persist(); err != nil {
		a.log.Warn("port gecmisi kaydedilemedi", "hata", err)
	}
	if a.tray != nil {
		a.tray.refreshShare()
	}
	return a.connect(target, secureMode)
}

// StopSharing, paylasilan portu birakir (hesap tunellerine yeniden baglanir).
func (a *App) StopSharing() string {
	return a.connect("", false)
}

// CurrentShare, o an paylasilan hedefi doner (yoksa bos).
func (a *App) CurrentShare() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ag == nil {
		return ""
	}
	return a.target
}

// GetHardwareMetrics, sistem telemetrisini anlik olarak ceker.
func (a *App) GetHardwareMetrics() *protocol.Metrics {
	return metrics.Collect()
}

// OpenBrowser, verilen linki sistem tarayicisinda acar (yalniz http/https).
func (a *App) OpenBrowser(urlStr string) {
	if a.ctx == nil {
		return
	}
	if !strings.HasPrefix(urlStr, "https://") && !strings.HasPrefix(urlStr, "http://") {
		return
	}
	wailsrt.BrowserOpenURL(a.ctx, urlStr)
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
	a.target = ""
	a.secure = false
	a.gen++
	a.mu.Unlock()
	// Baglantiyi kesmek paylasimi da bitirir (CLI'de Ctrl+C gibi): gecici
	// paylasim ve yerel erisim kayitlari, sonraki baglanmada kendiliginden
	// geri gelmesin diye silinir. Uygulama ici yeniden baslatmalar etkilenmez.
	a.nt.clear()
	a.publish(agent.Status{State: agent.StateStopped, Message: tm("status.cut")})
}

// disconnectAndWait, baglantiyi keser ve ajanin gercekten kapanmasini (kisa sure) bekler.
func (a *App) disconnectAndWait(d time.Duration) {
	a.mu.Lock()
	done := a.done
	a.mu.Unlock()
	a.Disconnect()
	waitDone(done, d)
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
		a.log.Warn("otomatik baslatma ayarlanamadi", "hata", err)
		return err.Error()
	}
	return ""
}

// GetAutoConnect, acilista kayitli hesapla otomatik baglanma acik mi.
func (a *App) GetAutoConnect() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.AutoConnect
}

// SetAutoConnect, acilista (bilgisayar acilisinda baslatildiginda da) kayitli
// hesapla otomatik baglanmayi acar/kapatir. Aktif baglantiya dokunmaz; ayar bir
// sonraki acilista gecerlidir. Kaydedilemezse bellekteki deger geri alinir ve
// hata metni doner (bos = basari).
func (a *App) SetAutoConnect(on bool) string {
	a.mu.Lock()
	prev := a.cfg.AutoConnect
	a.cfg.AutoConnect = on
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		a.mu.Lock()
		a.cfg.AutoConnect = prev
		a.mu.Unlock()
		return tm("app.saveFail", err.Error())
	}
	return ""
}

// LanguageInfo, arayuze dil ayarini ve etkin dili bildirir.
type LanguageInfo struct {
	Setting   string `json:"setting"`   // kayitli deger: "", "tr", "en", "ja", "zh"
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
	st := a.status
	a.mu.Unlock()

	i18n.Set(i18n.Detect(lang))
	if err := a.persist(); err != nil {
		return tm("app.saveFail", err.Error())
	}
	if a.tray != nil {
		a.tray.relabel()
		a.tray.update(st)
	}
	return ""
}

// GetNotifications / SetNotifications: OS bildirimleri ayari.
func (a *App) GetNotifications() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.cfg.NotifyDisabled
}

func (a *App) SetNotifications(on bool) string {
	a.mu.Lock()
	a.cfg.NotifyDisabled = !on
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return tm("app.saveFail", err.Error())
	}
	return ""
}

// SetTheme: arayuzdeki tema secimini saklar ve pencere cercevesine (Windows baslik
// cubugu) uygular. mode: "" (sistemi izle), "light", "dark".
func (a *App) SetTheme(mode string) string {
	if mode != "" && mode != "light" && mode != "dark" {
		return "gecersiz tema"
	}
	a.mu.Lock()
	changed := a.cfg.Theme != mode
	a.cfg.Theme = mode
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		switch mode {
		case "light":
			wailsrt.WindowSetLightTheme(ctx)
		case "dark":
			wailsrt.WindowSetDarkTheme(ctx)
		default:
			wailsrt.WindowSetSystemDefaultTheme(ctx)
		}
	}
	if changed {
		if err := a.persist(); err != nil {
			return tm("app.saveFail", err.Error())
		}
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

// serviceModeActive: servis/CLI ajani calisiyor, devralma istenmedi ve gomulu ajan yok.
func (a *App) serviceModeActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.svc.Found && !a.takeover && a.ag == nil
}

// publish, durumu saklar ve arayuze olay olarak gonderir.
func (a *App) publish(s agent.Status) {
	a.mu.Lock()
	a.status = s
	prev := a.prevState
	a.prevState = s.State
	wasUp := a.wasUp
	if s.State == agent.StateConnected {
		a.wasUp = true
		if prev != agent.StateConnected {
			a.connAt = time.Now()
		}
	}
	if s.State == agent.StateStopped {
		a.wasUp = false
	}
	a.mu.Unlock()

	emit(a.ctx, "status", s)
	if a.tray != nil {
		a.tray.update(s)
	}
	a.notifyTransition(prev, s, wasUp)
	// Zorven Net: baglanti (yeniden) kurulunca bu oturumda kurulan gecici
	// paylasim ve yerel erisimler yeniden kurulur (sunucu gecici paylasimlari
	// oturumla birlikte siler, yerel dinleyiciler ajanla birlikte kapanir).
	if prev != agent.StateConnected && s.State == agent.StateConnected {
		a.nt.bumpEpoch() // senkron: Zorven Net dugumu kaydedilmeden once
		go a.netReapply()
	}
	// 409: baska bir istemci (CLI/servis) bagli. Servis modunu hemen goster.
	if s.State == agent.StateFatal && isAlreadyConnectedMsg(s.Message) {
		go a.serviceLoopTick()
	}
}

// --- canli trafik ozeti -----------------------------------------------------

// trafficStats, gomulu ajanin ilettigi isteklerin hafif ozeti (OnRequest
// geri cagrisi tunel adini vermedigi icin tunel basina degil toplamdir).
type trafficStats struct {
	mu       sync.Mutex
	requests int64
	errors   int64 // 5xx
	durs     [512]int64
	n        int // doldurulan sayi (<=512)
	pos      int
	dirty    bool
}

// TrafficSnapshot, arayuze giden ozet.
type TrafficSnapshot struct {
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
	P50Ms    int64 `json:"p50_ms"`
	P95Ms    int64 `json:"p95_ms"`
}

func (t *trafficStats) reset() {
	t.mu.Lock()
	t.requests, t.errors, t.n, t.pos, t.dirty = 0, 0, 0, 0, true
	t.mu.Unlock()
}

func (t *trafficStats) add(status int, d time.Duration) {
	t.mu.Lock()
	t.requests++
	if status >= 500 {
		t.errors++
	}
	t.durs[t.pos] = d.Milliseconds()
	t.pos = (t.pos + 1) % len(t.durs)
	if t.n < len(t.durs) {
		t.n++
	}
	t.dirty = true
	t.mu.Unlock()
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

// snapshot, ozeti verir; changed son cagridan beri degisiklik oldu mu.
func (t *trafficStats) snapshot() (TrafficSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := t.dirty
	t.dirty = false
	d := append([]int64(nil), t.durs[:t.n]...)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return TrafficSnapshot{Requests: t.requests, Errors: t.errors, P50Ms: percentile(d, 0.5), P95Ms: percentile(d, 0.95)}, ch
}

// GetTraffic, canli trafik ozetini doner.
func (a *App) GetTraffic() TrafficSnapshot {
	s, _ := a.traffic.snapshot()
	return s
}
