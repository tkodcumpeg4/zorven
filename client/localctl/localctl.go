// Package localctl, CLI ile CALISAN ajan arasindaki yerel kontrol kanalidir
// (IPC). Yalnizca isletim sistemi duzeyinde kimlik dogrulama kullanir:
//
//   - Linux/macOS: Unix alan soketi, dizin 0700 + soket 0600 (+ Linux'ta
//     SO_PEERCRED ile es uid denetimi; root her zaman kabul edilir).
//   - macOS: LOCAL_PEERCRED ile ayni uid denetimi.
//   - Windows: named pipe (\\.\pipe\zorven-ctl-<yol karmasi>); acik guvenlik
//     tanimlayicisi (SYSTEM + Administrators [+ kullanici ajaninda sahibi]) ve
//     GetNamedPipeClientProcessId -> token SID dogrulamasi. Eski ajanlarla
//     uyum icin istemci, pipe yoksa eski AF_UNIX dosyasina duser.
//
// Ag dinleyicisi YOKTUR; sunucudan gelen hicbir mesaj buraya ulasamaz.
package localctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tkodcumpeg4/zorven/client/config"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"
)

const (
	// maxRequestBytes, tek IPC isteginin okunabilecek en buyuk boyutu.
	maxRequestBytes = 64 << 10
	// maxConns, es zamanli islenen IPC baglanti tavani.
	maxConns = 32
)

// CodeDenied, Response.Code: istemcinin bu islem icin yetkisi yok.
const CodeDenied = "denied"

// ErrAccessDenied, kontrol kanalina erisim (ya da islem yetkisi) reddedildi.
// Yanit/baglanma hatasi errors.Is ile bununla eslesir.
var ErrAccessDenied = errors.New("erisim reddedildi")

// Caps, ajanin izin durumu.
type Caps struct {
	TerminalCap bool `json:"terminal_cap"`
	ScreenCap   bool `json:"screen_cap"`
	TerminalEff bool `json:"terminal_eff"`
	ScreenEff   bool `json:"screen_eff"`
}

// Controller, ajanin IPC'ye actigi yuzey.
type Controller interface {
	Current() Caps
	Set(terminal, screen *bool) Caps
}

// Updater, ajanin "update" islemini destekledigini belirtir (istege bagli).
// Cagri hemen doner; guncelleme arka planda imzali manifest ile yapilir ve
// yeni surum varsa ajan kendini yeniden baslatir.
type Updater interface {
	TriggerUpdate()
}

// Request / Response satir tabanli JSON protokolu (tek istek, tek yanit).
type Request struct {
	Op       string `json:"op"` // "status" | "set" | "update" | "reload" | ...
	Terminal *bool  `json:"terminal,omitempty"`
	Screen   *bool  `json:"screen,omitempty"`

	// Zorven Net islemleri: share_create | share_delete | share_list |
	// access_open | access_close.
	Share   *ShareSpec `json:"share,omitempty"`
	ShareID string     `json:"share_id,omitempty"` // share_delete: id veya ad
	Target  string     `json:"target,omitempty"`   // access_open: "ad[:port]"
	Local   string     `json:"local,omitempty"`    // access_open/close: "127.0.0.1:15432"
}

type Response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Caps    Caps   `json:"caps"`
	Service bool   `json:"service,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`

	Shares []ShareInfo `json:"shares,omitempty"`
	Local  string      `json:"local,omitempty"` // access_open: gercek dinleme adresi
	// Paths (net_paths): netmap'teki esler ve her biri icin secili yol.
	Paths []PathInfo `json:"paths,omitempty"`
	// Tun (net_up/net_down/net_status): Mod B (OS TUN) durumu.
	Tun *TunInfo `json:"tun,omitempty"`
	// File (debug_stacks): yazilan tani dosyasinin yolu.
	File string `json:"file,omitempty"`

	// "status" gozlemlenebilirlik alanlari (bkz. status.go). Diger islemlerde bos.
	Connected   bool             `json:"connected"`
	State       string           `json:"state"`
	LastError   string           `json:"last_error"`
	Server      string           `json:"server"`
	StartedAt   string           `json:"started_at"`
	Reconnects  int              `json:"reconnects"`
	Disconnects []DisconnectInfo `json:"disconnects"`
	Tunnels     []TunnelInfo     `json:"tunnels"`
	NodePinned  bool             `json:"node_pinned,omitempty"`
	Nodes       []NodeInfo       `json:"nodes,omitempty"`
	// Deploy: ajan baslarken Zorven Apps izni (enable_deploy) aciktaydi. Eski
	// ajanlar bu alani yazmaz (kapali sayilir).
	Deploy bool `json:"deploy,omitempty"`
	// Code: hata turu; yalniz "denied" (erisim reddedildi).
	Code string `json:"code,omitempty"`
	// Reload ("reload" islemi): uygulanan degisiklikler.
	Reload *ReloadInfo `json:"reload,omitempty"`
}

// Endpoint, bir kontrol soketi konumu.
type Endpoint struct {
	Path    string
	Service bool // sistem servisi mi (system-wide konum)
}

// UserEndpoint, kullanici oturumundaki ajanin soketi.
func UserEndpoint() (Endpoint, error) {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && runtime.GOOS == "linux" {
		return Endpoint{Path: filepath.Join(d, "zorven", "ctl.sock")}, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Path: filepath.Join(d, "zorven", "ctl", "ctl.sock")}, nil
}

// ServiceEndpoint, sistem servisinin soketi (root/SYSTEM sahibi).
func ServiceEndpoint() Endpoint {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return Endpoint{Path: filepath.Join(pd, "zorven", "ctl", "ctl.sock"), Service: true}
	case "darwin":
		return Endpoint{Path: "/var/run/zorven/ctl.sock", Service: true}
	}
	return Endpoint{Path: "/run/zorven/ctl.sock", Service: true}
}

// Candidates, istemcinin sirayla deneyecegi konumlar.
func Candidates() []Endpoint {
	var out []Endpoint
	if u, err := UserEndpoint(); err == nil {
		out = append(out, u)
	}
	return append(out, ServiceEndpoint())
}

// Serve, ep uzerinde dinler ve ctx bitene kadar istekleri isler.
func Serve(ctx context.Context, ep Endpoint, ctl Controller, version string, onChange func(string)) error {
	ln, err := listen(ep)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); _ = ln.Close(); _ = os.Remove(ep.Path) }()
	sem := make(chan struct{}, maxConns)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			select {
			case sem <- struct{}{}:
			default:
				_ = c.Close() // es zamanli baglanti tavani asildi
				continue
			}
			go func() {
				defer func() { <-sem }()
				handle(c, ep, ctl, version, onChange)
			}()
		}
	}()
	return nil
}

func handle(c net.Conn, ep Endpoint, ctl Controller, version string, onChange func(string)) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	acc, err := peerAccess(c, ep)
	if err != nil {
		logReject(err)
		return // baglanti sessizce kapatilir (defer)
	}
	var req Request
	// Istek boyutu sinirli: yerel olsa da tek istek kontrol kanalini sisiremez.
	if err := json.NewDecoder(bufio.NewReader(io.LimitReader(c, maxRequestBytes))).Decode(&req); err != nil {
		_ = json.NewEncoder(c).Encode(Response{Error: "gecersiz istek"})
		return
	}
	_ = c.SetDeadline(time.Now().Add(opTimeout(req.Op) + 2*time.Second))
	if !opAllowed(acc, req.Op) {
		logReject(fmt.Errorf("islem %q bu erisim duzeyi icin izinli degil", req.Op))
		_ = json.NewEncoder(c).Encode(Response{Error: "erisim reddedildi: bu islem icin yetki yok", Code: CodeDenied})
		return
	}
	resp := Response{OK: true, Service: ep.Service, PID: os.Getpid(), Version: version}
	switch req.Op {
	case "status":
		resp.Caps = ctl.Current()
		if sr, ok := ctl.(StatusReporter); ok {
			resp.fillStatus(sr.StatusInfo())
		}
		if acc == accessReadOnly {
			redactStatus(&resp)
		}
	case "reload":
		rl, ok := ctl.(Reloader)
		if !ok {
			resp = Response{Error: "bu istemci ayarlari yeniden yuklemeyi desteklemiyor"}
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout(req.Op))
		info, err := rl.Reload(ctx)
		cancel()
		if err != nil {
			resp = Response{Error: err.Error()}
			break
		}
		resp.Caps = ctl.Current()
		resp.Reload = &info
		if onChange != nil && len(info.Changed) > 0 {
			onChange("reload: " + strings.Join(info.Changed, ","))
		}
	case "set":
		resp.Caps = ctl.Set(req.Terminal, req.Screen)
		if onChange != nil {
			onChange(fmt.Sprintf("terminal=%s screen=%s", ptr(req.Terminal), ptr(req.Screen)))
		}
	case "update":
		u, ok := ctl.(Updater)
		if !ok {
			resp = Response{Error: "bu istemci uzaktan guncelleme tetiklemeyi desteklemiyor"}
			break
		}
		resp.Caps = ctl.Current()
		if onChange != nil {
			onChange("guncelleme tetiklendi")
		}
		// Yanit once gitsin: ajan guncellenirse kendini yeniden baslatir.
		defer u.TriggerUpdate()
	case "net_up", "net_down", "net_status":
		resp.Caps = ctl.Current()
		if err := ep.tunOp(ctl, req, &resp); err != nil {
			resp = Response{Error: err.Error()}
		} else if onChange != nil && req.Op != "net_status" {
			onChange(req.Op)
		}
	case "share_create", "share_delete", "share_list", "access_open", "access_close", "net_paths":
		resp.Caps = ctl.Current()
		if err := ep.netOp(ctl, req, &resp); err != nil {
			resp = Response{Error: err.Error()}
		} else if onChange != nil && req.Op != "share_list" && req.Op != "net_paths" {
			onChange(req.Op)
		}
	case "debug_stacks":
		// Tani: tum goroutine yiginlarini dosyaya yazar (ajan kilitlendiginde
		// nerede beklendigini gormek icin). Soket ACL'i zaten yonetici/sahip ile sinirli.
		p, err := writeStackDump()
		if err != nil {
			resp = Response{Error: err.Error()}
		} else {
			resp.File = p
		}
	default:
		resp = Response{Error: "bilinmeyen islem"}
	}
	_ = json.NewEncoder(c).Encode(resp)
}

func ptr(b *bool) string {
	if b == nil {
		return "-"
	}
	if *b {
		return "acik"
	}
	return "kapali"
}

// Call, ep'deki ajana tek bir istek yollar.
func Call(ep Endpoint, req Request) (Response, error) {
	c, err := dial(ep, 2*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(opTimeout(req.Op)))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		if resp.Code == CodeDenied {
			return resp, fmt.Errorf("%w: %s", ErrAccessDenied, resp.Error)
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Find, calisan ajani bulur (once kullanici, sonra servis). Bulamazsa
// (nil, nil, nil) doner; izin hatasi gibi gercek hatalar ucuncu dondedir.
func Find() (*Endpoint, *Response, error) {
	var firstErr error
	for _, ep := range Candidates() {
		if !present(ep) {
			continue
		}
		r, err := Call(ep, Request{Op: "status"})
		if err != nil {
			if absent(err) {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", ep.Path, err)
			}
			continue
		}
		e := ep
		return &e, &r, nil
	}
	return nil, nil, firstErr
}

// LogDir, servis/ajan tani dosyalarinin dizini (sistem ayar dosyasinin yaninda logs/).
func LogDir() string {
	return filepath.Join(filepath.Dir(config.SystemPath()), "logs")
}

func writeStackDump() (string, error) {
	dir := LogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, "stacks-"+time.Now().UTC().Format("20060102-150405")+".txt")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
		return "", err
	}
	return p, nil
}
