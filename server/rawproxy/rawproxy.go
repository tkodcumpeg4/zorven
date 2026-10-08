// Package rawproxy, ham TCP/UDP tunellerinin (FAZ 3 / D2, Mod A: rezerve-port)
// sunucu tarafi giris duzlemidir. Her rezerve-port tunel icin bir dinleyici acar;
// gelen baglantiyi/datagrami tunelin ajan oturumuna StreamOpen ile koprüler.
//
// TCP: rawproxy.go (baglanti bazli). UDP: udp.go (kaynak-adres bazli flow'lar).
package rawproxy

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// streamAckTimeout, ajanin yerel hedefe baglanip StreamAck dondurmesi icin sure.
const streamAckTimeout = 15 * time.Second

// TunnelInfo, tek bir rezerve-port tunelin dinleyici icin gereken bilgisi.
type TunnelInfo struct {
	TunnelID string
	ClientID string
	TenantID string
	Proto    string // tcp | udp
	Port     int
	UDP      UDPLimits // yalnizca udp (FAZ 4 / F24)
}

// Lister, aktif (enabled) rezerve-port tunelleri doner. main.go store'a baglar.
type Lister func(ctx context.Context) ([]TunnelInfo, error)

// Manager, rezerve-port dinleyicilerini yonetir.
type Manager struct {
	Hub      *tunnel.Hub
	IPFilter *ipfilter.Engine
	Log      *slog.Logger
	List     Lister
	// Door, web ile kapi acma durumu (nil = ozellik kapali). Bkz. door.go.
	Door DoorGate

	mu      sync.Mutex
	tcpLn   map[int]net.Listener
	udpLn   map[int]*udpListener
	byPort  map[int]TunnelInfo
	baseCtx context.Context

	// doorConns, grant ile kabul edilen acik baglantilar (grant iptalinde kesilir).
	doorConns connRegistry
}

// ctx, baslatilmamis yoneticide (testler) bile bos olmayan bir context doner.
func (m *Manager) ctx() context.Context {
	if m.baseCtx != nil {
		return m.baseCtx
	}
	return context.Background()
}

func New(hub *tunnel.Hub, ipf *ipfilter.Engine, log *slog.Logger, list Lister) *Manager {
	return &Manager{
		Hub: hub, IPFilter: ipf, Log: log, List: list,
		tcpLn:  map[int]net.Listener{},
		udpLn:  map[int]*udpListener{},
		byPort: map[int]TunnelInfo{},
	}
}

// Start, yoneticiyi baslatir ve ilk yuklemeyi yapar.
func (m *Manager) Start(ctx context.Context) {
	m.baseCtx = ctx
	m.Reload()
	go func() {
		<-ctx.Done()
		m.closeAll()
	}()
}

// Reload, istenen tunel kumesini mevcut dinleyicilerle karsilastirir; yeni
// portlar icin dinleyici acar, kaldirilanları kapatir. tunelChanged'de cagrilir.
func (m *Manager) Reload() {
	if m.baseCtx == nil {
		return
	}
	desired, err := m.List(m.baseCtx)
	if err != nil {
		m.Log.Warn("rawproxy: tunel listesi alinamadi", "hata", err)
		return
	}
	want := map[int]TunnelInfo{}
	for _, ti := range desired {
		if ti.Port > 0 && (ti.Proto == "tcp" || ti.Proto == "udp") {
			want[ti.Port] = ti
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Kaldirilan/degisen TCP portlarini kapat.
	for port, ln := range m.tcpLn {
		if cur, ok := want[port]; !ok || cur.Proto != "tcp" || cur.TunnelID != m.byPort[port].TunnelID {
			ln.Close()
			delete(m.tcpLn, port)
			delete(m.byPort, port)
		}
	}
	// Kaldirilan/degisen UDP portlarini kapat.
	for port, ul := range m.udpLn {
		if cur, ok := want[port]; !ok || cur.Proto != "udp" || cur.TunnelID != m.byPort[port].TunnelID {
			ul.close()
			delete(m.udpLn, port)
			delete(m.byPort, port)
		}
	}
	// Yeni portlari ac.
	for port, ti := range want {
		if ti.Proto == "tcp" {
			if _, ok := m.tcpLn[port]; ok {
				m.byPort[port] = ti
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
			if err != nil {
				m.Log.Warn("rawproxy: TCP port dinlenemedi", "port", port, "hata", err)
				continue
			}
			m.tcpLn[port] = ln
			m.byPort[port] = ti
			m.Log.Info("rawproxy: TCP dinleyici acildi", "port", port, "tunnel", ti.TunnelID)
			go m.acceptLoop(ln, port)
		} else { // udp
			if ul, ok := m.udpLn[port]; ok {
				ul.setInfo(ti) // sinir/istemci degisikligi dinleyiciyi yeniden acmadan uygulanir
				m.byPort[port] = ti
				continue
			}
			ul, err := m.newUDPListener(port, ti)
			if err != nil {
				m.Log.Warn("rawproxy: UDP port dinlenemedi", "port", port, "hata", err)
				continue
			}
			m.udpLn[port] = ul
			m.byPort[port] = ti
			m.Log.Info("rawproxy: UDP dinleyici acildi", "port", port, "tunnel", ti.TunnelID)
		}
	}
}

func (m *Manager) acceptLoop(ln net.Listener, port int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // dinleyici kapandi
		}
		go m.handleTCP(conn, port)
	}
}

func (m *Manager) handleTCP(conn net.Conn, port int) {
	defer conn.Close()

	m.mu.Lock()
	ti, ok := m.byPort[port]
	m.mu.Unlock()
	if !ok {
		return
	}
	m.bridgeTCP(conn, ti)
}

// HandleSNIConn, Mod B (SNI) icin: TLS handshake sonrasi ServerName bir ham
// TCP-tuneline eslesince cagrilir. `zorven forward`'un actigi TLS conn'unu
// dogrudan agent akisina koprüler (her forward baglantisi = bir stream).
// conn ARAMADAN once kapatilmamalidir; bridgeTCP defer ile kapatir.
func (m *Manager) HandleSNIConn(conn net.Conn, ti TunnelInfo) {
	defer conn.Close()
	m.bridgeTCP(conn, ti)
}

// bridgeTCP, verilen conn'u tunelin ajan oturumuna bir ham TCP akisiyla baglar.
func (m *Manager) bridgeTCP(conn net.Conn, ti TunnelInfo) {
	// IP izin listesi ve/veya web-door grant'i.
	ip := hostOf(conn.RemoteAddr().String())
	ok, grantExp := m.admit(m.ctx(), ti, ip)
	if !ok {
		m.Log.Warn("rawproxy: IP engellendi", "ip", ip, "tunnel", ti.TunnelID)
		return
	}

	sess, online := m.Hub.Get(ti.ClientID)
	if !online {
		m.Log.Debug("rawproxy: istemci cevrimdisi", "client", ti.ClientID, "tunnel", ti.TunnelID)
		return
	}

	ctx, cancel := context.WithCancel(m.ctx())
	defer cancel()

	// Yalnizca grant sayesinde kabul edildiyse: grant iptalinde veya suresi
	// dolunca baglanti kesilir.
	if !grantExp.IsZero() {
		kill := func() {
			cancel()
			conn.Close()
		}
		defer m.doorConns.add(ti.TunnelID, ip, kill)()
		t := time.AfterFunc(time.Until(grantExp), kill)
		defer t.Stop()
	}

	stream, err := sess.OpenStream(ctx, ti.TunnelID, "tcp", conn.RemoteAddr().String())
	if err != nil {
		m.Log.Warn("rawproxy: akis acilamadi", "tunnel", ti.TunnelID, "hata", err)
		return
	}
	defer stream.Close("closed")

	// Baglanti sonucunu bekle.
	select {
	case ack := <-stream.Accept():
		if ack.Code != "" {
			m.Log.Debug("rawproxy: yerel hedefe baglanilamadi", "tunnel", ti.TunnelID, "code", ack.Code, "msg", ack.Message)
			return
		}
	case <-time.After(streamAckTimeout):
		return
	case <-ctx.Done():
		return
	}

	// ziyaretci -> ajan
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				if serr := stream.Send(context.Background(), buf[:n]); serr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		stream.Close("visitor closed")
	}()

	// ajan -> ziyaretci
	for {
		select {
		case data := <-stream.FromLocal():
			if _, werr := conn.Write(data); werr != nil {
				return
			}
		case <-stream.Closed():
			return
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, ln := range m.tcpLn {
		ln.Close()
		delete(m.tcpLn, port)
	}
	for port, ul := range m.udpLn {
		ul.close()
		delete(m.udpLn, port)
	}
	m.byPort = map[int]TunnelInfo{}
}

// --- yardimcilar ---

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
