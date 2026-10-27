package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Zorven Net tam ag katmani (Mod B, OS TUN) icin masaustu baglari. Masaustu
// uygulamasi kullanici yetkisiyle calisir ve TUN ACAMAZ; komutu cihazda calisan
// ayricalikli Zorven servisine (yalniz servis soketi: localctl.ServiceEndpoint)
// yerel IPC (net_up / net_down / net_status) ile yaptirir. Servis yoksa veya
// erisim yoksa kullaniciya acik hata, kurulum ipucu ve eylem bayraklari doner
// (arayuz bunlari gosterir). Mod A (port paylasimi) bundan bagimsizdir.

// NetTunPeer, tam ag katmanindaki bir es.
type NetTunPeer struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Online bool   `json:"online"`
	Path   string `json:"path"` // direct | relay
	RTTMs  int    `json:"rtt_ms,omitempty"`
}

// NetTunView, arayuze donen Mod B durumu.
type NetTunView struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Hint: sorun giderme / kurulum ipucu (Error doluysa).
	Hint string `json:"hint,omitempty"`
	// ServiceFound: servis soketine ulasildi ve cevap verildi.
	ServiceFound bool `json:"service_found"`
	// NeedService: servis kurulu/calisir degil (soket yok).
	NeedService bool `json:"need_service"`
	// NeedElevation: servis var ama bu kullanici erisemiyor (Windows: yonetici gerekir).
	NeedElevation bool `json:"need_elevation"`
	// CanInstall / CanElevate: arayuz "Servisi kur" / "Yonetici olarak yeniden baslat"
	// eylemlerini gosterebilir (yalniz Windows'ta UAC ile).
	CanInstall bool `json:"can_install"`
	CanElevate bool `json:"can_elevate"`
	Active     bool `json:"active"`
	// Alanlar localctl.TunInfo'dan.
	Interface  string `json:"interface,omitempty"`
	Name       string `json:"name,omitempty"`
	IP         string `json:"ip,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Resolver   string `json:"resolver,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
	DNSOK      bool   `json:"dns_ok"`
	DNSMethod  string `json:"dns_method,omitempty"`
	DNSNote    string `json:"dns_note,omitempty"`
	DroppedIn  int64  `json:"dropped_in"`
	DroppedOut int64  `json:"dropped_out"`
	ServerTun  bool   `json:"server_tun"`
	Connected  bool   `json:"connected"`
	// Embedded: islem bu uygulamanin kendi istemcisinde yapildi (cihaz = masaustu).
	Embedded bool         `json:"embedded"`
	Reason   string       `json:"reason,omitempty"`
	Peers    []NetTunPeer `json:"peers"`
}

// NetTunActionResult, eylem (servis kur / yonetici olarak yeniden baslat) sonucu.
type NetTunActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func tunInstallHint() string {
	if runtime.GOOS == "windows" {
		return tm("net.hintInstallWin")
	}
	return tm("net.hintInstallNix")
}

func tunElevateHint() string {
	if runtime.GOOS == "windows" {
		return tm("net.hintElevWin")
	}
	return tm("net.hintElevNix")
}

// tunErrView, servis uc noktasina erisim hatasindan gorunum uretir.
func tunErrView(err error) NetTunView {
	v := NetTunView{Peers: []NetTunPeer{}, CanInstall: runtime.GOOS == "windows", CanElevate: runtime.GOOS == "windows"}
	switch {
	case errors.Is(err, os.ErrNotExist):
		v.NeedService = true
		v.Error = tm("net.noService")
		v.Hint = tunInstallHint()
	case errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "access is denied") || strings.Contains(err.Error(), "yetkisiz"):
		v.NeedElevation = true
		v.ServiceFound = true
		v.Error = tm("net.noAccess")
		v.Hint = tunElevateHint()
	default:
		v.NeedService = true
		v.Error = tm("net.unreachable", err.Error())
		v.Hint = tunInstallHint()
	}
	return v
}

func (a *App) tunOp(op string) NetTunView {
	// Tam ag katmanini once BU uygulamanin kendi istemcisi acar (cihaz = bu
	// masaustu istemcisi). TUN ayricalik ister: surec yonetici degilse ve bagli
	// bir servis de yoksa "yonetici olarak yeniden baslat" onerilir.
	a.mu.Lock()
	ag := a.ag
	a.mu.Unlock()
	if ag != nil && processElevated() {
		return a.embeddedTunOp(ag, op)
	}
	if v, ok := a.serviceTunOp(op); ok {
		return v
	}
	v := NetTunView{OK: true, Peers: []NetTunPeer{}, NeedElevation: true, CanElevate: runtime.GOOS == "windows"}
	if ag == nil {
		v.Error = tm("net.connectFirst")
		return v
	}
	if op != "net_status" {
		v.Error = tm("net.needAdmin")
	}
	v.Hint = tm("net.joinHint")
	return v
}

// embeddedTunOp, islemi bu uygulamanin gomulu ajaninda yapar.
func (a *App) embeddedTunOp(ag *agent.Agent, op string) NetTunView {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var ti localctl.TunInfo
	var err error
	switch op {
	case "net_up":
		ti, err = ag.NetUp(ctx)
	case "net_down":
		ti, err = ag.NetDown(ctx)
	default:
		ti, err = ag.NetTunStatus()
	}
	v := NetTunView{OK: err == nil, Peers: []NetTunPeer{}, Embedded: true}
	if err != nil {
		v.Error = err.Error()
	}
	fillTunView(&v, &ti)
	return v
}

// serviceTunOp, ayni makinedeki Zorven servisine sorar. ok=false: servis yok,
// erisilemiyor ya da sunucuya bagli degil (o zaman gomulu yol onerilir).
func (a *App) serviceTunOp(op string) (NetTunView, bool) {
	ep := localctl.ServiceEndpoint()
	if _, err := os.Stat(ep.Path); err != nil {
		return NetTunView{}, false
	}
	resp, err := localctl.Call(ep, localctl.Request{Op: op})
	if err != nil || resp.Tun == nil || (!resp.Tun.Connected && resp.Tun.Reason != "") {
		return NetTunView{}, false
	}
	return a.tunOpService(op), true
}

func (a *App) tunOpService(op string) NetTunView {
	ep := localctl.ServiceEndpoint()
	if _, err := os.Stat(ep.Path); err != nil {
		return tunErrView(err)
	}
	resp, err := localctl.Call(ep, localctl.Request{Op: op})
	if err != nil {
		if resp.Error == "" { // aktarim hatasi (dial/izin), servis cevabi degil
			return tunErrView(err)
		}
		v := NetTunView{ServiceFound: true, Error: err.Error(), Peers: []NetTunPeer{}}
		switch {
		case localctl.IsUnsupported(err):
			v.Error = tm("net.unsupported")
		case strings.Contains(err.Error(), "yetkisiz"):
			return tunErrView(err)
		default:
			v.Hint = tunInstallHint()
		}
		return v
	}
	v := NetTunView{OK: true, ServiceFound: true, Peers: []NetTunPeer{}}
	fillTunView(&v, resp.Tun)
	return v
}

// fillTunView, ajan/servis TunInfo'sunu gorunume kopyalar.
func fillTunView(v *NetTunView, ti *localctl.TunInfo) {
	if ti != nil {
		v.Active, v.Interface, v.Name, v.IP, v.Pool = ti.Active, ti.Interface, ti.Name, ti.IP, ti.Pool
		v.Resolver, v.MTU = ti.Resolver, ti.MTU
		v.DNSOK, v.DNSMethod, v.DNSNote, v.ServerTun = ti.DNSOK, ti.DNSMethod, ti.DNSNote, ti.ServerTun
		v.Connected, v.Reason = ti.Connected, ti.Reason
		v.DroppedIn, v.DroppedOut = ti.DroppedIn, ti.DroppedOut
		for _, p := range ti.Peers {
			v.Peers = append(v.Peers, NetTunPeer{Name: p.Name, IP: p.IP, Online: p.Online, Path: p.Path, RTTMs: p.RTTMs})
		}
	}
}

// NetTunStatus, tam ag katmani durumunu verir.
func (a *App) NetTunStatus() NetTunView { return a.tunOp("net_status") }

// NetTunUp, tam ag katmanini servise actirir.
func (a *App) NetTunUp() NetTunView { return a.tunOp("net_up") }

// NetTunDown, tam ag katmanini kapatir.
func (a *App) NetTunDown() NetTunView { return a.tunOp("net_down") }

var (
	svcTokenRe  = regexp.MustCompile(`^(?:zrv|rpsh|zorven)_live_[A-Za-z0-9_-]{1,128}$`)
	svcServerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(?::[0-9]{1,5})?$`)
)

// buildServiceInstallParams, UAC ile yukseltilmis cmd.exe icin parametre dizgisi
// uretir: "zorven service install --token-file F --server S" ardindan "service start".
// Token komut satirina YAZILMAZ (yerel islemler komut satirlarini gorebilir):
// yalniz kullanicinin okuyabildigi gecici dosyadan okunur ve CLI dosyayi siler.
// Girdiler katı bicimle dogrulanir (komut enjeksiyonu yok).
func buildServiceInstallParams(cliPath, tokenFile, server string) (string, error) {
	if strings.ContainsAny(cliPath, "\"&|<>^%\r\n") || cliPath == "" {
		return "", errors.New(tm("net.badCLIPath"))
	}
	if strings.ContainsAny(tokenFile, "\"&|<>^%\r\n") || tokenFile == "" {
		return "", errors.New(tm("net.badTokenFile"))
	}
	if !svcServerRe.MatchString(server) {
		return "", errors.New(tm("net.badServer"))
	}
	// cmd /c ""exe" a b && "exe" c": dis tirnak cmd'in ilk/son tirnak kuralini korur.
	return fmt.Sprintf(`/c ""%[1]s" service install --token-file "%[2]s" --server %[3]s && "%[1]s" service start"`,
		cliPath, tokenFile, server), nil
}

// NetTunInstallService, Zorven servisini UAC (yonetici onayi) ile kurar ve
// baslatir. Windows disinda terminal komutunu ipucu olarak doner.
func (a *App) NetTunInstallService() NetTunActionResult {
	if runtime.GOOS != "windows" {
		return NetTunActionResult{Error: tm("net.installNotWin"), Message: tunInstallHint()}
	}
	cfg := a.GetConfig()
	if cfg.Token == "" {
		return NetTunActionResult{Error: tm("net.noToken")}
	}
	cli := findCLIExe()
	if cli == "" {
		return NetTunActionResult{Error: tm("net.noCLI")}
	}
	if !svcTokenRe.MatchString(cfg.Token) {
		return NetTunActionResult{Error: tm("net.badToken")}
	}
	tf, err := writeInstallTokenFile(cfg.Token)
	if err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	params, err := buildServiceInstallParams(cli, tf, cfg.ServerAddr)
	if err != nil {
		_ = os.Remove(tf)
		return NetTunActionResult{Error: err.Error()}
	}
	if err := runElevated("cmd.exe", params); err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	return NetTunActionResult{OK: true, Message: tm("net.adminAsked")}
}

// NetTunRelaunchElevated, uygulamayi yonetici olarak yeniden baslatir (servis
// kontrol soketine erisim icin) ve mevcut ornegi kapatir. Yalniz Windows.
func (a *App) NetTunRelaunchElevated() NetTunActionResult {
	if runtime.GOOS != "windows" {
		return NetTunActionResult{Error: tm("net.winOnly")}
	}
	exe, err := os.Executable()
	if err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	if err := runElevated(exe, ""); err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	a.QuitApp()
	return NetTunActionResult{OK: true}
}

// writeInstallTokenFile, token'i yalniz bu kullanicinin (ve SYSTEM/Administrators)
// okuyabildigi gecici bir dosyaya yazar; yukseltilmis CLI okuyup siler. CLI
// hic calismazsa (UAC reddi) dosya 2 dk sonra temizlenir.
func writeInstallTokenFile(token string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	rb := make([]byte, 12)
	if _, err := rand.Read(rb); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "zorven", "svc-token-"+hex.EncodeToString(rb))
	if err := clientcfg.WriteSecretFile(p, []byte(token)); err != nil {
		return "", errors.New(tm("net.tokenFileFail", err))
	}
	time.AfterFunc(2*time.Minute, func() { _ = os.Remove(p) })
	return p, nil
}
