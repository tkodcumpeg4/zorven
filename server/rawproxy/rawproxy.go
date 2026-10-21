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

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// bridgeStream, bridgeTCP'nin ajan akisindan bekledigi yuzeydir (*tunnel.RawStream
// uygular). Testler gercek bir ajan oturumu olmadan sahte akis verebilsin diye ayridir.
type bridgeStream interface {
	Accept() <-chan protocol.StreamAck
	FromLocal() <-chan []byte
	Closed() <-chan struct{}
	Send(ctx context.Context, d []byte) error
	Close(reason string)
}

// streamAckTimeout, ajanin yerel hedefe baglanip StreamAck dondurmesi icin sure.
const streamAckTimeout = 15 * time.Second

// TunnelInfo, tek bir rezerve-port tunelin dinleyici icin gereken bilgisi.
type TunnelInfo struct {
	TunnelID string
	ClientID string
	TenantID string
	Proto    string    // tcp | udp
	Port     int       // genel (tunel adresinde gorunen) port
	UDP      UDPLimits // yalnizca udp (FAZ 4 / F24)

	// K3: cok IP'li havuz. BindAddr bos ise tum arayuzler; ListenPort 0 ise Port.
	// (docker bridge'de ikinci IP'nin portu kaydirilir: ListenPort = Port+offset.)
	BindAddr   string
	ListenPort int
}

// listenKey, dinleyicinin kimligi: ayni port numarasi farkli IP'lerde ayri
// dinleyicidir.
type listenKey struct {
	addr string
	port int
}

func (ti TunnelInfo) key() listenKey {
	p := ti.ListenPort
	if p == 0 {
		p = ti.Port
	}
	return listenKey{addr: ti.BindAddr, port: p}
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
	// Usage, kiracinin aylik kullanimini yazan ve hiz sinirini veren kaynak
	// (nil = olcum/sinir yok). main.go LocalUsageRecorder baglar.
	Usage bandwidth.UsageSource

	mu      sync.Mutex
	tcpLn   map[listenKey]net.Listener
	udpLn   map[listenKey]*udpListener
	byPort  map[listenKey]TunnelInfo
	baseCtx context.Context

	// doorConns, grant ile kabul edilen acik baglantilar (grant iptalinde kesilir).
	doorConns connRegistry

	// tcpStats, tunel kimligi -> *tcpStat (tcpstats.go).
	tcpStats sync.Map

	// openStream nil degilse bridgeTCP ajan akisini Hub yerine buradan alir (testler).
	openStream func(ctx context.Context, ti TunnelInfo, remote string) (bridgeStream, bool, error)
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
		tcpLn:  map[listenKey]net.Listener{},
		udpLn:  map[listenKey]*udpListener{},
		byPort: map[listenKey]TunnelInfo{},
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
	want := map[listenKey]TunnelInfo{}
	for _, ti := range desired {
		if ti.Port > 0 && (ti.Proto == "tcp" || ti.Proto == "udp") {
			want[ti.key()] = ti
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
			ln, err := net.Listen("tcp", net.JoinHostPort(port.addr, strconv.Itoa(port.port)))
			if err != nil {
				m.Log.Warn("rawproxy: TCP port dinlenemedi", "bind", port.addr, "port", port.port, "hata", err)
				continue
			}
			m.tcpLn[port] = ln
			m.byPort[port] = ti
			m.Log.Info("rawproxy: TCP dinleyici acildi", "bind", port.addr, "port", port.port, "tunnel", ti.TunnelID)
			go m.acceptLoop(ln, port)
		} else { // udp
			if ul, ok := m.udpLn[port]; ok {
				ul.setInfo(ti) // sinir/istemci degisikligi dinleyiciyi yeniden acmadan uygulanir
				m.byPort[port] = ti
				continue
			}
			ul, err := m.newUDPListener(port.addr, port.port, ti)
			if err != nil {
				m.Log.Warn("rawproxy: UDP port dinlenemedi", "bind", port.addr, "port", port.port, "hata", err)
				continue
			}
			m.udpLn[port] = ul
			m.byPort[port] = ti
			m.Log.Info("rawproxy: UDP dinleyici acildi", "bind", port.addr, "port", port.port, "tunnel", ti.TunnelID)
		}
	}
}

func (m *Manager) acceptLoop(ln net.Listener, port listenKey) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // dinleyici kapandi
		}
		go m.handleTCP(conn, port)
	}
}

func (m *Manager) handleTCP(conn net.Conn, port listenKey) {
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
	st := m.tcpStatFor(ti)
	ip := hostOf(conn.RemoteAddr().String())
	ok, grantExp := m.admit(m.ctx(), ti, ip)
	if !ok {
		if m.Door != nil && m.Door.DoorEnabled(ti.TunnelID) {
			st.reject(TCPRejectDoor)
		} else {
			st.reject(TCPRejectIP)
		}
		m.Log.Warn("rawproxy: IP engellendi", "ip", ip, "tunnel", ti.TunnelID)
		return
	}

	var sess *tunnel.Session
	online := true
	if m.openStream == nil {
		sess, online = m.Hub.Get(ti.ClientID)
	}
	if !online {
		st.reject(TCPRejectOffline)
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

	var stream bridgeStream
	var err error
	if m.openStream != nil {
		var off bool
		stream, off, err = m.openStream(ctx, ti, conn.RemoteAddr().String())
		if off {
			st.reject(TCPRejectOffline)
			return
		}
	} else {
		stream, err = sess.OpenStream(ctx, ti.TunnelID, "tcp", conn.RemoteAddr().String())
	}
	if err != nil {
		st.reject(TCPRejectDial)
		m.Log.Warn("rawproxy: akis acilamadi", "tunnel", ti.TunnelID, "hata", err)
		return
	}
	defer stream.Close("closed")

	// Baglanti sonucunu bekle.
	select {
	case ack := <-stream.Accept():
		if ack.Code != "" {
			st.reject(TCPRejectDial)
			m.Log.Debug("rawproxy: yerel hedefe baglanilamadi", "tunnel", ti.TunnelID, "code", ack.Code, "msg", ack.Message)
			return
		}
	case <-time.After(streamAckTimeout):
		st.reject(TCPRejectDial)
		return
	case <-ctx.Done():
		return
	}

	// Aylik kullanim + kiraci hiz siniri (HTTP ile ayni sayac/limiter). Baglanti
	// basina tek Batcher: sicak yolda atomik, kapanista flush.
	meter := bandwidth.NewBatcher(m.Usage, ti.TenantID, bandwidth.UsageTypeTCP)
	defer meter.Flush()

	st.open()
	began := time.Now()
	defer func() { st.closeConn(time.Since(began)) }()

	// ziyaretci -> ajan
	go func() {
		defer meter.Flush() // bridge donduktan sonra gelen son baytlar da yazilir
		buf := make([]byte, 32*1024)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				if bandwidth.WaitN(ctx, meter.Limiter(ctx), n) != nil {
					break
				}
				if serr := stream.Send(context.Background(), buf[:n]); serr != nil {
					break
				}
				st.bytesIn.Add(int64(n))
				meter.Add(int64(n), 0)
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
			if bandwidth.WaitN(ctx, meter.Limiter(ctx), len(data)) != nil {
				return
			}
			n, werr := conn.Write(data)
			st.bytesOut.Add(int64(n))
			meter.Add(0, int64(n))
			if werr != nil {
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
	m.byPort = map[listenKey]TunnelInfo{}
}

// --- yardimcilar ---

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
