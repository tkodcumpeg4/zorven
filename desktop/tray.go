//go:build windows || linux

package main

import (
	"embed"
	"fmt"
	"runtime"
	"sync"

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

// tray, sistem tepsisi simgesini ve menusunu yonetir.
type tray struct {
	app *App

	mu      sync.Mutex
	started bool

	activeURL string

	mShow         *systray.MenuItem
	mTunnelURL    *systray.MenuItem
	mCopyURL      *systray.MenuItem
	mOpenBrowser  *systray.MenuItem
	mSessionAlert *systray.MenuItem
	mCloseSession *systray.MenuItem
	mQuickShare   *systray.MenuItem
	mPort8003     *systray.MenuItem
	mPort8080     *systray.MenuItem
	mPort5173     *systray.MenuItem
	mStatus       *systray.MenuItem
	mConnect      *systray.MenuItem
	mStop         *systray.MenuItem
}

// startTray, tepsi simgesini AYRI BIR GOROUTINE'de baslatir.
func (a *App) startTray() {
	t := &tray{app: a}
	a.tray = t
	go systray.Run(t.onReady, func() {})
}

func (t *tray) onReady() {
	systray.SetTitle("Zorven")
	systray.SetTooltip("Zorven - Güvenli Tünel İstemcisi")
	t.setState(agent.StateStopped)

	t.mShow = systray.AddMenuItem("Pencereyi göster", "Uygulama penceresini öne getir")
	systray.AddSeparator()

	// Aktif tünel URL'si ve hızlı tarayıcı/kopyalama işlemleri
	t.mTunnelURL = systray.AddMenuItem("Tünel hazır değil", "")
	t.mTunnelURL.Disable()
	t.mTunnelURL.Hide()

	t.mOpenBrowser = systray.AddMenuItem("Tarayıcıda aç", "Tünel adresini varsayılan tarayıcıda aç")
	t.mOpenBrowser.Hide()

	t.mCopyURL = systray.AddMenuItem("Adresi kopyala", "Tünel adresini panoya kopyala")
	t.mCopyURL.Hide()

	// Uzak oturum uyarısı ve kapatma butonu
	t.mSessionAlert = systray.AddMenuItem("Uzak Oturum Aktif", "Terminal veya ekran paylaşımı bağlı")
	t.mSessionAlert.Disable()
	t.mSessionAlert.Hide()

	t.mCloseSession = systray.AddMenuItem("✕ Uzak oturumları sonlandır", "Uzak terminal ve ekran paylaşımını hemen kapat")
	t.mCloseSession.Hide()

	systray.AddSeparator()

	// Hızlı port paylaşımı
	t.mQuickShare = systray.AddMenuItem("Hızlı Port Paylaş", "Sık kullanılan bir yerel portu hemen tünelle")
	t.mPort8003 = t.mQuickShare.AddSubMenuItem("Port 8003 (Yerel Servis)", "localhost:8003 tünelle")
	t.mPort5173 = t.mQuickShare.AddSubMenuItem("Port 5173 (Vite / Nuxt)", "localhost:5173 tünelle")
	t.mPort8080 = t.mQuickShare.AddSubMenuItem("Port 8080 (Backend / API)", "localhost:8080 tünelle")

	systray.AddSeparator()

	// Durum satiri tiklanabilir degil, yalnizca bilgi.
	t.mStatus = systray.AddMenuItem("Bağlı değil", "")
	t.mStatus.Disable()

	t.mConnect = systray.AddMenuItem("Bağlan", "Tüneli başlat")
	t.mStop = systray.AddMenuItem("Bağlantıyı kes", "Tüneli durdur")
	t.mStop.Hide()

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Çıkış", "Uygulamayı tamamen kapat")

	t.mu.Lock()
	t.started = true
	t.mu.Unlock()

	// Baslangicta mevcut durumu yansit.
	t.update(t.app.GetStatus())

	for {
		select {
		case <-t.mShow.ClickedCh:
			t.app.ShowWindow()

		case <-t.mOpenBrowser.ClickedCh:
			t.mu.Lock()
			url := t.activeURL
			t.mu.Unlock()
			if url != "" {
				t.app.OpenBrowser(url)
			}

		case <-t.mCopyURL.ClickedCh:
			t.mu.Lock()
			url := t.activeURL
			t.mu.Unlock()
			if url != "" {
				_ = t.app.CopyClipboard(url)
			}

		case <-t.mCloseSession.ClickedCh:
			t.app.CloseSessions()

		case <-t.mPort8003.ClickedCh:
			go t.app.SharePort(8003, false)

		case <-t.mPort5173.ClickedCh:
			go t.app.SharePort(5173, false)

		case <-t.mPort8080.ClickedCh:
			go t.app.SharePort(8080, false)

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

// update, tepsi simgesini ve menusunu duruma gore tazeler.
func (t *tray) update(s agent.Status) {
	t.mu.Lock()
	ready := t.started
	t.mu.Unlock()
	if !ready {
		return // menu henuz kurulmadi
	}

	t.setState(s.State)

	label := map[agent.State]string{
		agent.StateConnected:    "Bağlı",
		agent.StateConnecting:   "Bağlanıyor…",
		agent.StateReconnecting: "Yeniden bağlanıyor",
		agent.StateStopped:      "Bağlı değil",
		agent.StateFatal:        "Bağlanamadı",
	}[s.State]
	if label == "" {
		label = "Bağlı değil"
	}
	t.mStatus.SetTitle(label)

	tooltip := "Zorven — " + label
	if s.Message != "" {
		tooltip += "\n" + s.Message
	}

	// Aktif tünel URL kontrolü
	activeURL := ""
	if len(s.Tunnels) > 0 && len(s.Tunnels[0].Hostnames) > 0 {
		activeURL = "https://" + s.Tunnels[0].Hostnames[0]
	}

	t.mu.Lock()
	t.activeURL = activeURL
	t.mu.Unlock()

	if activeURL != "" {
		t.mTunnelURL.SetTitle(activeURL)
		t.mTunnelURL.Show()
		t.mOpenBrowser.Show()
		t.mCopyURL.Show()
		tooltip += "\n" + activeURL
	} else {
		t.mTunnelURL.Hide()
		t.mOpenBrowser.Hide()
		t.mCopyURL.Hide()
	}

	// Uzak oturum kontrolü
	totSessions := s.TerminalActive + s.ScreenActive
	if totSessions > 0 {
		t.mSessionAlert.SetTitle(fmt.Sprintf("%d Uzak Oturum Aktif", totSessions))
		t.mSessionAlert.Show()
		t.mCloseSession.Show()
	} else {
		t.mSessionAlert.Hide()
		t.mCloseSession.Hide()
	}

	systray.SetTooltip(tooltip)

	// Bagliyken "Baglan" gizlenir; pencerede oldugu gibi tek eylem gorunur.
	live := s.State == agent.StateConnected ||
		s.State == agent.StateConnecting ||
		s.State == agent.StateReconnecting
	if live {
		t.mConnect.Hide()
		t.mStop.Show()
	} else {
		t.mStop.Hide()
		t.mConnect.Show()
	}
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

// ShowWindow, pencereyi gosterir ve one getirir.
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	wailsrt.WindowShow(a.ctx)
	wailsrt.WindowUnminimise(a.ctx)
}

// HideWindow, pencereyi gizler (uygulama tepside calismaya devam eder).
func (a *App) HideWindow() {
	if a.ctx == nil {
		return
	}
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
