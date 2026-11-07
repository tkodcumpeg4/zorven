package main

// CLI/servis birlikte yasama: ayni makinede 'zorven' CLI ajani ya da sistem
// servisi ayni cihaz anahtariyla bagliysa masaustu gomulu ajani BASLATMAZ
// (sunucu ikinci oturumu 409 ile reddeder). Bu dosya, localctl'in ajan/servis
// durumunu masaustu icin tek bir gorunume cevirir.
//
// ENTEGRASYON NOTU: localctl "status" yanitina yeni alanlar (connected, state,
// last_error, server, started_at, reconnects, disconnects, tunnels) eklendiginde
// asagidaki JSON gidis-donusu bunlari KENDILIGINDEN alir (etiketler ayni
// oldugu surece); ek bir degisiklik gerekmez. 409 icin typed sentinel
// (agent.ErrAlreadyConnected) gelince isAlreadyConnectedMsg yerine
// errors.Is kullanilmali (bkz. isAlreadyConnected).

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// svcTunnel, servis durumundaki bir tunelin ozeti ve canli sayaclari.
type svcTunnel struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Target   string  `json:"target"`
	Protocol string  `json:"protocol"`
	Requests int64   `json:"requests"`
	BytesIn  int64   `json:"bytes_in"`
	BytesOut int64   `json:"bytes_out"`
	Errors   int64   `json:"errors"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

type svcDisconnect struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

// ServiceState, arayuze verilen servis/CLI ajan durumu.
type ServiceState struct {
	Found     bool   `json:"found"`   // calisan bir CLI ajani/servisi var
	Service   bool   `json:"service"` // sistem servisi mi (degilse kullanici CLI'si)
	PID       int    `json:"pid,omitempty"`
	Version   string `json:"version,omitempty"`
	Connected bool   `json:"connected"`
	State     string `json:"state,omitempty"`
	LastError string `json:"last_error,omitempty"`
	Server    string `json:"server,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	// Reconnects/Disconnects: yeni localctl alanlari (yoksa sifir/bos).
	Reconnects  int             `json:"reconnects,omitempty"`
	Disconnects []svcDisconnect `json:"disconnects,omitempty"`
	Tunnels     []svcTunnel     `json:"tunnels,omitempty"`
	// TunActive: servis tam ag katmanini (TUN) acmis; masaustu ikinci bir
	// arayuz acmamali, islemi servise yaptirmali.
	TunActive bool `json:"tun_active"`
	// Takeover: kullanici masaustunun devralmasini istedi (servise ragmen).
	Takeover bool `json:"takeover"`
	// Err: ajan var ama sorgulanamadi (izin vb.).
	Err string `json:"err,omitempty"`
	// Denied: servis kanali var ama bu kullaniciya erisim kapali (operator degil);
	// Onar (operator ekleme) gerekir. Service true doner (Komut satiri istemcisi degil).
	Denied bool `json:"denied,omitempty"`
	// TerminalCap/ScreenCap/DeployOn: servisin gecerli izin tavanlari (kurulu kipte
	// izin anahtarlarinin kaynagi; sistem ayar dosyasi kullaniciya kapali).
	TerminalCap bool `json:"terminal_cap"`
	ScreenCap   bool `json:"screen_cap"`
	DeployOn    bool `json:"deploy_on"`
}

// serviceFromResponse, localctl yanitindan ServiceState uretir. Yanit once
// JSON'a cevrilip geri okunur: Response'a sonradan eklenen alanlar otomatik gelir.
func serviceFromResponse(resp localctl.Response, ep *localctl.Endpoint) ServiceState {
	s := ServiceState{Found: true, Service: resp.Service || (ep != nil && ep.Service), PID: resp.PID, Version: resp.Version,
		TerminalCap: resp.Caps.TerminalCap, ScreenCap: resp.Caps.ScreenCap, DeployOn: resp.Deploy}
	// Eski ajan baglanti durumunu bildirmez: calisiyor = baglanmaya calisiyor.
	s.Connected = true
	if resp.Tun != nil {
		s.Connected = resp.Tun.Connected || resp.Tun.Reason == ""
		s.TunActive = resp.Tun.Active
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return s
	}
	var raw struct {
		Connected   *bool           `json:"connected"`
		State       string          `json:"state"`
		LastError   string          `json:"last_error"`
		Server      string          `json:"server"`
		StartedAt   json.RawMessage `json:"started_at"`
		Reconnects  int             `json:"reconnects"`
		Disconnects []svcDisconnect `json:"disconnects"`
		Tunnels     []svcTunnel     `json:"tunnels"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return s
	}
	// Yeni ajan "connected" alanini her zaman yazar ve state'i doldurur; eski ajanda
	// state bos gelir, o zaman alan sifir degerdir ve yok sayilir.
	if raw.Connected != nil && raw.State != "" {
		s.Connected = *raw.Connected
	}
	s.State, s.LastError, s.Server = raw.State, raw.LastError, raw.Server
	s.Reconnects, s.Disconnects, s.Tunnels = raw.Reconnects, raw.Disconnects, raw.Tunnels
	s.StartedAt = strings.Trim(string(raw.StartedAt), `"`)
	if s.StartedAt == "null" {
		s.StartedAt = ""
	}
	if s.State == "" {
		if s.Connected {
			s.State = "connected"
		} else {
			s.State = "reconnecting"
		}
	}
	return s
}

// detectService, ayni makinedeki CLI ajanini/servisi arar. Stat KULLANILMAZ:
// Windows'ta kanal adlari dosya sistemi yolu degildir; yalnizca Call denenir.
func detectService() ServiceState {
	ep, resp, err := localctl.Find()
	if ep == nil || resp == nil {
		if err != nil {
			// Ajan var ama konusamiyor (izin): yine de cakisma ihtimali var.
			// Erisim reddi servis kanalidir (kendi kullanici kanali reddetmez).
			return ServiceState{Err: err.Error(), Denied: errors.Is(err, localctl.ErrAccessDenied), Service: errors.Is(err, localctl.ErrAccessDenied)}
		}
		return ServiceState{}
	}
	s := serviceFromResponse(*resp, ep)
	// TUN durumunu ayrica sor (status yaniti Tun tasimayabilir).
	if resp.Tun == nil {
		if tr, terr := localctl.Call(*ep, localctl.Request{Op: "net_status"}); terr == nil && tr.Tun != nil {
			s.TunActive = tr.Tun.Active
			if s.State == "" || s.State == "connected" || s.State == "reconnecting" {
				s.Connected = tr.Tun.Connected || tr.Tun.Reason == ""
			}
		}
	}
	return s
}

// isAlreadyConnectedMsg, "baska istemci bagli (409)" hatasini metinden tanir.
// TODO(entegrasyon): agent.ErrAlreadyConnected gelince errors.Is ile degistir.
func isAlreadyConnectedMsg(msg string) bool {
	return strings.Contains(msg, "(409)") || strings.Contains(strings.ToLower(msg), "already connected")
}

// svcEqual, iki durumun arayuzce gorunur kisimlarinin ayni olup olmadigi.
func svcEqual(a, b ServiceState) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// serviceLoop, servis/CLI ajaninin durumunu periyodik izler ve degisince arayuze bildirir.
func (a *App) serviceLoopTick() {
	cur := detectService()
	a.mu.Lock()
	cur.Takeover = a.takeover && cur.Found
	if !cur.Found {
		a.takeover = false
	}
	changed := !svcEqual(a.svc, cur)
	prev := a.svc
	a.svc = cur
	a.mu.Unlock()
	if changed {
		a.emitService(cur)
		if prev.Found && !cur.Found {
			a.log.Info("servis/CLI ajani kapandi")
		}
	}
}

func (a *App) emitService(s ServiceState) {
	if a.ctx != nil {
		emit(a.ctx, "service:state", s)
	}
}

// GetServiceState, arayuz acilisinda servis/CLI ajan durumunu doner.
func (a *App) GetServiceState() ServiceState {
	a.serviceLoopTick()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.svc
}

// serviceBlocks: gomulu ajan baslatilmamali mi (servis var ve devralma istenmedi).
func (a *App) serviceBlocks() bool {
	a.mu.Lock()
	takeover := a.takeover
	a.mu.Unlock()
	if takeover {
		return false
	}
	s := detectService()
	a.mu.Lock()
	s.Takeover = false
	a.svc = s
	a.mu.Unlock()
	a.emitService(s)
	return s.Found
}

// TakeOver, kullanicinin "masaustu devralsin" tercihini isaretler ve baglanir.
// Servis hala calisiyorsa sunucu 409 doner; arayuz servisi durdurma yolunu
// gosterir (servisi durdurmak yonetici ister, masaustu yapmaz).
func (a *App) TakeOver() string {
	if installedMode() {
		return tm("svc.noTakeover")
	}
	a.mu.Lock()
	a.takeover = true
	a.mu.Unlock()
	a.serviceLoopTick()
	return a.Connect()
}

// ReleaseTakeover, devralma tercihini sifirlar (servis modu geri gelir).
func (a *App) ReleaseTakeover() {
	a.mu.Lock()
	a.takeover = false
	a.mu.Unlock()
	a.serviceLoopTick()
}

var _ = time.Second
