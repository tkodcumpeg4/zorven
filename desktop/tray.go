//go:build windows || linux

package main

import (
	"embed"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"fyne.io/systray"
	"github.com/tkodcumpeg4/zorven/client/agent"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Ikonlar icons/generate.py ile uretilir. Windows .ico, Linux .png bekler.
//
//go:embed icons/tray-connected.ico icons/tray-idle.ico icons/tray-error.ico
//go:embed icons/tray-connected.png icons/tray-idle.png icons/tray-error.png
var trayIcons embed.FS

// trayEnabled, main.go'nun kapatma davranisini platforma gore ayarlamasi icin.
const trayEnabled = true

const (
	maxShareSlots  = 6 // hizli paylasim alt menusundeki en fazla port
	maxTunnelSlots = 5 // tepside listelenen en fazla tunel adresi
)

// defaultSharePorts, kullanicinin gecmisi yetmedikce onerilen portlar.
var defaultSharePorts = []int{3000, 5173, 8080}

// tray, sistem tepsisi simgesini ve menusunu yonetir.
type tray struct {
	app *App

	mu      sync.Mutex
	started bool

	sharePorts [maxShareSlots]int
	tunnelURLs [maxTunnelSlots]string

	mShow         *systray.MenuItem
	mOpenBrowser  *systray.MenuItem // alt menu: tunel adresleri (ac)
	mCopyURL      *systray.MenuItem // alt menu: tunel adresleri (kopyala)
	openSlots     [maxTunnelSlots]*systray.MenuItem
	copySlots     [maxTunnelSlots]*systray.MenuItem
	mSessionAlert *systray.MenuItem
	mCloseSession *systray.MenuItem
	mQuickShare   *systray.MenuItem
	shareSlots    [maxShareSlots]*systray.MenuItem
	mCurrentShare *systray.MenuItem
	mStopShare    *systray.MenuItem
	mStatus       *systray.MenuItem
	mConnect      *systray.MenuItem
	mStop         *systray.MenuItem
	mUpdate       *systray.MenuItem
	mQuit         *systray.MenuItem
	updateVersion string
	ready         atomic.Bool // simge sistem tepsisine eklendi (onReady calisti)
}

// isReady, tepsi simgesi eklendi mi.
func (t *tray) isReady() bool { return t.ready.Load() }

// startTray, tepsi simgesini AYRI BIR GOROUTINE'de baslatir.
func (a *App) startTray() {
	t := &tray{app: a}
	a.tray = t
	go systray.Run(t.onReady, func() {})
}

// sharePortList, tepsi hizli paylasim listesi: son kullanilan + gecmis portlar,
// ardindan varsayilanlar; tekrarsiz, en fazla maxShareSlots.
func sharePortList(last int, recent []int) []int {
	seen := map[int]bool{}
	var out []int
	add := func(p int) {
		if p >= 1 && p <= 65535 && !seen[p] && len(out) < maxShareSlots {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(last)
	for _, p := range recent {
		add(p)
	}
	for _, p := range defaultSharePorts {
		add(p)
	}
	return out
}

func (t *tray) onReady() {
	t.ready.Store(true)
	// Sol ve sag tik: standart menu yerine dinamik tepsi paneli (bkz. flyout.go).
	systray.SetOnTapped(func() { t.app.ToggleFlyout() })
	systray.SetOnSecondaryTapped(func() { t.app.ToggleFlyout() })
	systray.SetTitle("Zorven")
	systray.SetTooltip(tm("tray.tooltip"))
	t.setState(agent.StateStopped)

	t.mShow = systray.AddMenuItem(tm("tray.show"), tm("tray.show.tip"))

	t.mUpdate = systray.AddMenuItem("", tm("tray.update.tip"))
	t.mUpdate.Hide()
	systray.AddSeparator()

	// Tum tunel adresleri: acmak ve kopyalamak icin iki alt menu.
	t.mOpenBrowser = systray.AddMenuItem(tm("tray.open"), tm("tray.open.tip"))
	t.mCopyURL = systray.AddMenuItem(tm("tray.copy"), tm("tray.copy.tip"))
	for i := range t.openSlots {
		t.openSlots[i] = t.mOpenBrowser.AddSubMenuItem("", "")
		t.copySlots[i] = t.mCopyURL.AddSubMenuItem("", "")
		t.openSlots[i].Hide()
		t.copySlots[i].Hide()
	}
	t.mOpenBrowser.Hide()
	t.mCopyURL.Hide()

	// Uzak oturum uyarısı ve kapatma butonu
	t.mSessionAlert = systray.AddMenuItem(tm("tray.session"), tm("tray.session.tip"))
	t.mSessionAlert.Disable()
	t.mSessionAlert.Hide()

	t.mCloseSession = systray.AddMenuItem(tm("tray.close"), tm("tray.close.tip"))
	t.mCloseSession.Hide()

	systray.AddSeparator()

	// Hızlı port paylaşımı: yuvalar config'ten (gecmis + varsayilan) doldurulur.
	t.mQuickShare = systray.AddMenuItem(tm("tray.quick"), tm("tray.quick.tip"))
	for i := range t.shareSlots {
		t.shareSlots[i] = t.mQuickShare.AddSubMenuItem("", "")
		t.shareSlots[i].Hide()
	}
	t.mCurrentShare = systray.AddMenuItem("", "")
	t.mCurrentShare.Disable()
	t.mCurrentShare.Hide()
	t.mStopShare = systray.AddMenuItem(tm("tray.stopShare"), tm("tray.stopShare.tip"))
	t.mStopShare.Hide()

	systray.AddSeparator()

	// Durum satiri tiklanabilir degil, yalnizca bilgi.
	t.mStatus = systray.AddMenuItem(tm("state.stopped"), "")
	t.mStatus.Disable()

	t.mConnect = systray.AddMenuItem(tm("tray.connect"), tm("tray.connect.tip"))
	t.mStop = systray.AddMenuItem(tm("tray.stop"), tm("tray.stop.tip"))
	t.mStop.Hide()

	systray.AddSeparator()
	t.mQuit = systray.AddMenuItem(tm("tray.quit"), tm("tray.quit.tip"))
	mQuit := t.mQuit

	t.mu.Lock()
	t.started = true
	t.mu.Unlock()

	t.refreshShare()
	// Baslangicta mevcut durumu yansit.
	t.update(t.app.GetStatus())

	// Dinamik yuvalar: her biri kendi goroutine'inde tiklamayi bekler.
	for i := 0; i < maxShareSlots; i++ {
		go func(i int) {
			for range t.shareSlots[i].ClickedCh {
				t.mu.Lock()
				p := t.sharePorts[i]
				t.mu.Unlock()
				if p > 0 {
					go t.app.SharePort(p, false)
				}
			}
		}(i)
	}
	for i := 0; i < maxTunnelSlots; i++ {
		go func(i int) {
			for range t.openSlots[i].ClickedCh {
				if u := t.tunnelURL(i); u != "" {
					t.app.OpenBrowser(u)
				}
			}
		}(i)
		go func(i int) {
			for range t.copySlots[i].ClickedCh {
				if u := t.tunnelURL(i); u != "" {
					_ = t.app.CopyClipboard(u)
				}
			}
		}(i)
	}

	for {
		select {
		case <-t.mShow.ClickedCh:
			t.app.ShowWindow()

		case <-t.mUpdate.ClickedCh:
			t.app.ShowWindow()
			emit(t.app.ctx, "update:focus")

		case <-t.mCloseSession.ClickedCh:
			t.app.CloseSessions()

		case <-t.mStopShare.ClickedCh:
			go t.app.StopSharing()

		case <-t.mConnect.ClickedCh:
			go t.app.Connect()

		case <-t.mStop.ClickedCh:
			t.app.Disconnect()

		case <-mQuit.ClickedCh:
			t.app.QuitApp()
			return
		}
	}
}

func (t *tray) tunnelURL(i int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tunnelURLs[i]
}

// refreshShare, hizli paylasim alt menusunu config'teki port gecmisinden yeniler.
func (t *tray) refreshShare() {
	t.mu.Lock()
	ready := t.started
	t.mu.Unlock()
	if !ready {
		return
	}
	cfg := t.app.cfgCopy()
	ports := sharePortList(cfg.LastPort, cfg.RecentPorts)
	t.mu.Lock()
	for i := range t.sharePorts {
		if i < len(ports) {
			t.sharePorts[i] = ports[i]
		} else {
			t.sharePorts[i] = 0
		}
	}
	t.mu.Unlock()
	for i, m := range t.shareSlots {
		if i < len(ports) {
			m.SetTitle(tm("tray.sharePort", ports[i]))
			m.SetTooltip(tm("tray.sharePort.tip", ports[i]))
			m.Show()
		} else {
			m.Hide()
		}
	}
}

// setUpdate, "Guncelleme var (vX)" tepsi ogesini gosterir ("" = gizle).
func (t *tray) setUpdate(ver string) {
	t.mu.Lock()
	ready := t.started
	t.updateVersion = ver
	t.mu.Unlock()
	if !ready {
		return
	}
	if ver == "" {
		t.mUpdate.Hide()
		return
	}
	t.mUpdate.SetTitle(tm("tray.update", ver))
	t.mUpdate.Show()
}

// tunnelURLsOf, durumdaki tum tunel adreslerini https/tcp bicimine cevirir.
func tunnelURLsOf(s agent.Status) []string {
	var out []string
	for _, tn := range s.Tunnels {
		for _, h := range tn.Hostnames {
			out = append(out, hostURL(h))
		}
	}
	return out
}

// update, tepsi simgesini ve menusunu duruma gore tazeler.
func (t *tray) update(s agent.Status) {
	t.mu.Lock()
	ready := t.started
	t.mu.Unlock()
	if !ready {
		return // menu henuz kurulmadi
	}

	svcMode := t.app.serviceModeActive()
	t.setState(s.State)

	label := map[agent.State]string{
		agent.StateConnected:    tm("state.connected"),
		agent.StateConnecting:   tm("state.connecting"),
		agent.StateReconnecting: tm("state.reconnecting"),
		agent.StateStopped:      tm("state.stopped"),
		agent.StateFatal:        tm("state.fatal"),
	}[s.State]
	if label == "" {
		label = tm("state.stopped")
	}
	if svcMode {
		label = tm("svc.badge")
	}
	t.mStatus.SetTitle(label)

	tooltip := "Zorven - " + label
	if s.Message != "" && !svcMode {
		tooltip += "\n" + s.Message
	}

	// Tum tunel adresleri (ilk birkac tanesi)
	urls := tunnelURLsOf(s)
	t.mu.Lock()
	for i := range t.tunnelURLs {
		if i < len(urls) {
			t.tunnelURLs[i] = urls[i]
		} else {
			t.tunnelURLs[i] = ""
		}
	}
	t.mu.Unlock()
	if len(urls) > 0 {
		t.mOpenBrowser.Show()
		t.mCopyURL.Show()
		for i := range t.openSlots {
			if i < len(urls) {
				t.openSlots[i].SetTitle(urls[i])
				t.copySlots[i].SetTitle(urls[i])
				t.openSlots[i].Show()
				t.copySlots[i].Show()
			} else {
				t.openSlots[i].Hide()
				t.copySlots[i].Hide()
			}
		}
		tooltip += "\n" + urls[0]
		if len(urls) > 1 {
			tooltip += fmt.Sprintf(" (+%d)", len(urls)-1)
		}
	} else {
		t.mOpenBrowser.Hide()
		t.mCopyURL.Hide()
	}

	// Uzak oturum kontrolü
	totSessions := s.TerminalActive + s.ScreenActive
	if totSessions > 0 {
		t.mSessionAlert.SetTitle(tm("tray.sessionN", totSessions))
		t.mSessionAlert.Show()
		t.mCloseSession.Show()
	} else {
		t.mSessionAlert.Hide()
		t.mCloseSession.Hide()
	}

	// Mevcut paylasim + Durdur
	live := s.State == agent.StateConnected ||
		s.State == agent.StateConnecting ||
		s.State == agent.StateReconnecting
	if share := t.app.CurrentShare(); share != "" && live {
		t.mCurrentShare.SetTitle(tm("tray.sharing", strings.TrimPrefix(share, "http://")))
		t.mCurrentShare.Show()
		t.mStopShare.Show()
	} else {
		t.mCurrentShare.Hide()
		t.mStopShare.Hide()
	}

	systray.SetTooltip(tooltip)

	// Bagliyken "Baglan" gizlenir; servis modunda ikisi de gizli (servis yonetir).
	switch {
	case svcMode:
		t.mConnect.Hide()
		t.mStop.Hide()
		t.mQuickShare.Hide()
	case live:
		t.mConnect.Hide()
		t.mStop.Show()
		t.mQuickShare.Show()
	default:
		t.mStop.Hide()
		t.mConnect.Show()
		t.mQuickShare.Show()
	}
}

// relabel, dil degisince sabit menu etiketlerini ve ipuclarini yeniden yazar;
// durum satiri, oturum sayisi ve genel ipucu update ile tazelenir.
func (t *tray) relabel() {
	t.mu.Lock()
	ready := t.started
	ver := t.updateVersion
	t.mu.Unlock()
	if !ready {
		return
	}
	set := func(m *systray.MenuItem, title, tip string) {
		m.SetTitle(title)
		m.SetTooltip(tip)
	}
	systray.SetTooltip(tm("tray.tooltip"))
	set(t.mShow, tm("tray.show"), tm("tray.show.tip"))
	set(t.mOpenBrowser, tm("tray.open"), tm("tray.open.tip"))
	set(t.mCopyURL, tm("tray.copy"), tm("tray.copy.tip"))
	set(t.mSessionAlert, tm("tray.session"), tm("tray.session.tip"))
	set(t.mCloseSession, tm("tray.close"), tm("tray.close.tip"))
	set(t.mQuickShare, tm("tray.quick"), tm("tray.quick.tip"))
	set(t.mStopShare, tm("tray.stopShare"), tm("tray.stopShare.tip"))
	set(t.mConnect, tm("tray.connect"), tm("tray.connect.tip"))
	set(t.mStop, tm("tray.stop"), tm("tray.stop.tip"))
	set(t.mQuit, tm("tray.quit"), tm("tray.quit.tip"))
	t.mUpdate.SetTooltip(tm("tray.update.tip"))
	if ver != "" {
		t.mUpdate.SetTitle(tm("tray.update", ver))
	}
	t.refreshShare()
}

// setState, duruma karsilik gelen simgeyi yukler.
func (t *tray) setState(state agent.State) {
	name := "idle"
	switch state {
	case agent.StateConnected:
		name = "connected"
	case agent.StateFatal:
		name = "error"
	}

	ext := ".ico"
	if runtime.GOOS != "windows" {
		ext = ".png"
	}
	data, err := trayIcons.ReadFile("icons/tray-" + name + ext)
	if err != nil {
		return // simge yuklenemezse mevcut kalir; islevi etkilemez
	}
	systray.SetIcon(data)
}

// stop, uygulama kapanirken tepsi simgesini kaldirir.
func (t *tray) stop() { systray.Quit() }

// --- App tarafindaki pencere yardimcilari ---------------------------------

// ShowWindow, pencereyi gosterir, simge durumundan cikarir ve one getirir.
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	a.leaveFlyout()
	a.visible.Store(true)
	wailsrt.WindowShow(a.ctx)
	wailsrt.WindowUnminimise(a.ctx)
	// Windows on plana alma kisitini asmak icin kisa bir "hep ustte" gecisi.
	wailsrt.WindowSetAlwaysOnTop(a.ctx, true)
	wailsrt.WindowSetAlwaysOnTop(a.ctx, false)
}

// HideWindow, pencereyi gizler (uygulama tepside calismaya devam eder).
func (a *App) HideWindow() {
	if a.ctx == nil {
		return
	}
	a.visible.Store(false)
	wailsrt.WindowHide(a.ctx)
}

// QuitApp, uygulamayi tamamen kapatir.
func (a *App) QuitApp() {
	a.Disconnect()
	if a.tray != nil {
		a.tray.stop()
	}
	if a.ctx != nil {
		wailsrt.Quit(a.ctx)
	}
}
