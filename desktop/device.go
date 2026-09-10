package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// serverBaseURL, cfg.ServerAddr'dan (host:port) https tabanini uretir.
// Bos ise platform varsayilani kullanilir.
func (a *App) serverBaseURL() string {
	addr := strings.TrimSpace(a.cfg.ServerAddr)
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
	return "masaustu"
}

// ResizeWindow, pencereyi verilen boyuta getirir ve ortalar. Log paneli
// acilinca genis moda, kapaninca dar moda gecmek icin arayuz cagirir.
func (a *App) ResizeWindow(w, h int) {
	if a.ctx == nil {
		return
	}
	wailsrt.WindowSetSize(a.ctx, w, h)
	wailsrt.WindowCenter(a.ctx)
}

// hideToTrayEnabled, pencere kapatilinca tepsiye gizlensin mi (main.go OnBeforeClose).
func (a *App) hideToTrayEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.HideToTray
}

// UpdateBehavior, cihaz davranis ayarlarini (CLI/ekran kapatma, tepsiye gizle)
// gunceller. no_terminal/no_screen degisirse aktif baglantiyi yeniden kurar ki
// yeni yetki ayari hemen gecerli olsun.
func (a *App) UpdateBehavior(noTerminal, noScreen, hideToTray bool) string {
	a.mu.Lock()
	permsChanged := a.cfg.NoTerminal != noTerminal || a.cfg.NoScreen != noScreen
	wasConnected := a.ag != nil
	a.cfg.NoTerminal = noTerminal
	a.cfg.NoScreen = noScreen
	a.cfg.HideToTray = hideToTray
	cfg := a.cfg
	a.mu.Unlock()

	if err := saveConfig(cfg); err != nil {
		return "Ayarlar kaydedilemedi: " + err.Error()
	}
	if permsChanged && wasConnected && cfg.Token != "" {
		go func() { a.Disconnect(); a.Connect() }()
	}
	return ""
}

// IsAuthed, kayitli bir token var mi (arayuz giris ekranini buna gore gosterir).
func (a *App) IsAuthed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.Token) != ""
}

// SetToken, elle token girisini kaydeder ve baglanir. Hata mesaji doner (bos = ok).
func (a *App) SetToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "Token bos olamaz."
	}
	if !strings.HasPrefix(token, "zrv_live_") && !strings.HasPrefix(token, "rpsh_live_") {
		return "Token \"zrv_live_\" ile baslamalidir."
	}
	a.mu.Lock()
	a.cfg.Token = token
	if a.cfg.ServerAddr == "" {
		a.cfg.ServerAddr = "zorven.app:443"
	}
	a.cfg.CACertPath = ""
	a.cfg.Insecure = false
	cfg := a.cfg
	a.mu.Unlock()
	if err := saveConfig(cfg); err != nil {
		return "Ayarlar kaydedilemedi: " + err.Error()
	}
	go a.Connect()
	return ""
}

// Logout, kayitli token'i siler ve baglantiyi keser (arayuz giris ekranina doner).
func (a *App) Logout() {
	a.Disconnect()
	a.mu.Lock()
	a.cfg.Token = ""
	cfg := a.cfg
	a.mu.Unlock()
	_ = saveConfig(cfg)
	if a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "auth:changed")
	}
}

// StartDeviceLogin, "tarayicidan giris" akisini baslatir: sunucudan bir kod
// alir, tarayiciyi onay sayfasina yonlendirir ve arka planda onayi bekler.
// Arayuze JSON {"user_code":..} veya {"error":..} doner.
func (a *App) StartDeviceLogin() string {
	base := a.serverBaseURL()
	reqBody, _ := json.Marshal(map[string]string{"name": deviceHostname()})
	resp, err := http.Post(base+"/api/v1/device/code", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return mustJSON(map[string]string{"error": "sunucuya ulasilamadi: " + err.Error()})
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
		return mustJSON(map[string]string{"error": "sunucu yaniti cozulemedi"})
	}

	verify := base + out.VerificationURI + "?code=" + out.UserCode
	if a.ctx != nil {
		wailsrt.BrowserOpenURL(a.ctx, verify)
	}
	go a.pollDevice(out.DeviceCode, out.Interval, out.ExpiresIn)

	return mustJSON(map[string]string{"user_code": out.UserCode, "verify_url": verify})
}

func (a *App) pollDevice(deviceCode string, interval, expiresIn int) {
	if interval <= 0 {
		interval = 3
	}
	if expiresIn <= 0 {
		expiresIn = 600
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
		resp, err := http.Post(a.serverBaseURL()+"/api/v1/device/token", "application/json", bytes.NewReader(body))
		if err != nil {
			continue
		}
		var out struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()

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
			a.cfg.CACertPath = ""
			a.cfg.Insecure = false
			cfg := a.cfg
			a.mu.Unlock()
			_ = saveConfig(cfg)
			if a.ctx != nil {
				wailsrt.EventsEmit(a.ctx, "device:approved")
				wailsrt.EventsEmit(a.ctx, "auth:changed")
			}
			go a.Connect()
			return
		case "denied":
			if a.ctx != nil {
				wailsrt.EventsEmit(a.ctx, "device:error", "Onay reddedildi.")
			}
			return
		}
	}
	if a.ctx != nil {
		wailsrt.EventsEmit(a.ctx, "device:error", "Kodun suresi doldu, tekrar deneyin.")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
