package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// deviceHTTP, cihaz girisi cagrilari icin zaman asimli istemci (ortam vekili korunur).
var deviceHTTP = newDeviceHTTP()

func newDeviceHTTP() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// serverBaseURL, cfg.ServerAddr'dan (host:port) https tabanini uretir.
// Bos ise platform varsayilani kullanilir.
func (a *App) serverBaseURL() string {
	a.mu.Lock()
	addr := strings.TrimSpace(a.cfg.ServerAddr)
	a.mu.Unlock()
	if addr == "" {
		addr = "zorven.app:443"
	}
	host := strings.TrimSuffix(addr, ":443")
	return "https://" + host
}

func deviceHostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return tm("dev.hostFallback")
}

// ResizeWindow, pencereyi verilen boyuta getirir ve ortalar (arayuz artik
// kullanmaz: Loglar ana pencerede ayri bir sayfadir; baglama uyumluluk icin kalir). Boyut
// ekranin calisma alanina sigdirilir (kucuk ekranlarda tasmaz).
func (a *App) ResizeWindow(w, h int) {
	if a.ctx == nil {
		return
	}
	w, h = fitToScreen(a.ctx, w, h)
	wailsrt.WindowSetSize(a.ctx, w, h)
	wailsrt.WindowCenter(a.ctx)
}

// fitToScreen, istenen boyutu mevcut ekranin boyutuna sinirlar (kenar boslugu birakir).
func fitToScreen(ctx context.Context, w, h int) (int, int) {
	screens, err := wailsrt.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return w, h
	}
	sc := screens[0]
	for _, s := range screens {
		if s.IsCurrent {
			sc = s
			break
		}
	}
	maxW, maxH := sc.Size.Width-40, sc.Size.Height-80 // gorev cubugu/baslik payi
	if maxW > 0 && w > maxW {
		w = maxW
	}
	if maxH > 0 && h > maxH {
		h = maxH
	}
	return w, h
}

// hideToTrayEnabled, pencere kapatilinca tepsiye gizlensin mi (main.go OnBeforeClose).
func (a *App) hideToTrayEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.HideToTray
}

// UpdateBehavior, cihaz davranis ayarlarini (CLI/ekran kapatma, tepsiye gizle)
// gunceller. no_terminal/no_screen degisirse aktif baglantiyi yeniden kurar ki
// yeni yetki ayari hemen gecerli olsun (paylasim hedefi ve guvenli mod korunur).
// Kaydedilemezse bellekteki ayar geri alinir ve hata metni doner.
func (a *App) UpdateBehavior(noTerminal, noScreen, hideToTray bool) string {
	if installedMode() {
		return a.updateBehaviorInstalled(noTerminal, noScreen, hideToTray)
	}
	a.mu.Lock()
	prevTarget, prevSecure := a.target, a.secure
	prev := a.cfg
	permsChanged := a.cfg.NoTerminal != noTerminal || a.cfg.NoScreen != noScreen
	wasConnected := a.ag != nil
	a.cfg.NoTerminal = noTerminal
	a.cfg.NoScreen = noScreen
	a.cfg.HideToTray = hideToTray
	hasToken := a.cfg.Token != ""
	a.mu.Unlock()

	if err := a.persist(); err != nil {
		a.mu.Lock()
		a.cfg.NoTerminal, a.cfg.NoScreen, a.cfg.HideToTray = prev.NoTerminal, prev.NoScreen, prev.HideToTray
		a.mu.Unlock()
		return tm("app.saveFail", err.Error())
	}
	if permsChanged && wasConnected && hasToken {
		go a.connect(prevTarget, prevSecure)
	}
	return ""
}

// updateBehaviorInstalled, kurulu kipte terminal/ekran izinlerini servisin sistem
// ayarina (UAC'li yardimci) yazar; tepsi ayari kullanici ayarinda kalir. Izinler
// zaten gecerliyse UAC sorulmaz. UAC reddi/hata: hicbir sey degismez, hata doner.
func (a *App) updateBehaviorInstalled(noTerminal, noScreen, hideToTray bool) string {
	if msg := a.setServicePerms(map[string]bool{permTerminal: !noTerminal, permScreen: !noScreen}); msg != "" {
		return msg
	}
	a.mu.Lock()
	prev := a.cfg.HideToTray
	a.cfg.HideToTray = hideToTray
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		a.mu.Lock()
		a.cfg.HideToTray = prev
		a.mu.Unlock()
		return tm("app.saveFail", err.Error())
	}
	return ""
}

// IsAuthed, kayitli bir token var mi (arayuz giris ekranini buna gore gosterir).
func (a *App) IsAuthed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.Token) != ""
}

// afterLogin, giris basarili olunca (token ile de tarayicidan da) ayni yolu
// izler: arayuze oturum degisti bilgisi (ayarlar + tam ag katmani yuklenir) ve
// baglanti.
func (a *App) afterLogin() {
	emit(a.ctx, "auth:changed")
	go a.Connect()
}

// SetToken, elle token girisini kaydeder ve baglanir. Hata mesaji doner (bos = ok).
func (a *App) SetToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return tm("dev.tokenEmpty")
	}
	if !strings.HasPrefix(token, "zrv_live_") && !strings.HasPrefix(token, "rpsh_live_") {
		return tm("dev.tokenPrefix")
	}
	a.mu.Lock()
	a.cfg.Token = token
	if a.cfg.ServerAddr == "" {
		a.cfg.ServerAddr = "zorven.app:443"
	}
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		return tm("app.saveFail", err.Error())
	}
	a.afterLogin()
	return ""
}

// Logout, kayitli token'i siler ve baglantiyi keser (arayuz giris ekranina doner).
func (a *App) Logout() {
	a.CancelDeviceLogin()
	a.Disconnect()
	a.mu.Lock()
	a.cfg.Token = ""
	a.cfg.SvcTokenSig = ""
	a.takeover = false
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		a.log.Warn("cikis kaydedilemedi", "hata", err)
	}
	a.traffic.reset()
	emit(a.ctx, "auth:changed")
}

// CancelDeviceLogin, bekleyen tarayicidan giris yoklamasini iptal eder.
func (a *App) CancelDeviceLogin() {
	a.mu.Lock()
	c := a.loginCancel
	a.loginCancel = nil
	a.mu.Unlock()
	if c != nil {
		c()
	}
}

// StartDeviceLogin, "tarayicidan giris" akisini baslatir: sunucudan bir kod
// alir, tarayiciyi onay sayfasina yonlendirir ve arka planda onayi bekler.
// Daha onceki bir yoklama varsa iptal edilir (tek yoklayici).
// Arayuze JSON {"user_code":..} veya {"error":..} doner.
func (a *App) StartDeviceLogin() string {
	a.CancelDeviceLogin()
	base := a.serverBaseURL()
	reqBody, _ := json.Marshal(map[string]string{"name": deviceHostname()})
	resp, err := deviceHTTP.Post(base+"/api/v1/device/code", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return mustJSON(map[string]string{"error": tm("dev.unreachable", err.Error())})
	}
	defer resp.Body.Close()
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.DeviceCode == "" {
		return mustJSON(map[string]string{"error": tm("dev.badResponse")})
	}

	verify := base + out.VerificationURI + "?code=" + out.UserCode
	if a.ctx != nil {
		wailsrt.BrowserOpenURL(a.ctx, verify)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.loginCancel = cancel
	a.mu.Unlock()
	go a.pollDevice(ctx, out.DeviceCode, out.Interval, out.ExpiresIn)

	return mustJSON(map[string]string{"user_code": out.UserCode, "verify_url": verify})
}

// sleepCtx, d kadar bekler; ctx iptal edilirse false doner.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (a *App) pollDevice(ctx context.Context, deviceCode string, interval, expiresIn int) {
	if interval <= 0 {
		interval = 3
	}
	if expiresIn <= 0 {
		expiresIn = 600
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, time.Duration(interval)*time.Second) {
			return
		}
		body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.serverBaseURL()+"/api/v1/device/token", bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := deviceHTTP.Do(req)
		if err != nil {
			continue
		}
		var out struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if ctx.Err() != nil {
			return
		}

		switch out.Status {
		case "approved":
			if out.Token == "" {
				continue
			}
			a.mu.Lock()
			a.cfg.Token = out.Token
			if a.cfg.ServerAddr == "" {
				a.cfg.ServerAddr = "zorven.app:443"
			}
			a.loginCancel = nil
			a.mu.Unlock()
			if err := a.persist(); err != nil {
				a.log.Warn("giris kaydedilemedi", "hata", err)
			}
			emit(a.ctx, "device:approved")
			a.afterLogin()
			return
		case "denied":
			emit(a.ctx, "device:error", tm("dev.denied"))
			return
		}
	}
	if ctx.Err() == nil {
		emit(a.ctx, "device:error", tm("dev.expired"))
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
