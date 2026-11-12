// Package rawproxy, ham TCP/UDP tunellerinin (FAZ 3 / D2, Mod A: rezerve-port)
// sunucu tarafi giris duzlemidir. Her rezerve-port tunel icin bir dinleyici acar;
// gelen baglantiyi/datagrami tunelin ajan oturumuna StreamOpen ile koprüler.
//
// TCP: rawproxy.go (baglanti bazli). UDP: udp.go (kaynak-adres bazli flow'lar).
package rawproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/server/tunsec"
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

	// Replicas, birincil ClientID'ye EK olarak ayni tunele hizmet eden replika
	// istemciler (FAZ 5 / HA). Bos ise tunel yalnizca ClientID ile calisir
	// (replikasiz yol, eskisi gibi). Doluysa TCP baglantilari / UDP flow'lari
	// adaylar arasinda dagitilir (bkz. replicas.go).
	Replicas []string

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

	// Geo, IP -> ulke cozumleyicisi (ZTNA Z3; nil = ulke her zaman "unknown").
	Geo tunsec.CountryResolver
	// sec: tunel kimligi -> derlenmis Geo ayari (SetSecurity). live: kabul edilen
	// TUM acik akislar (Reevaluate icin).
	sec  atomic.Pointer[map[string]*tunsec.Compiled]
	live liveRegistry

	// tcpStats, tunel kimligi -> *tcpStat (tcpstats.go).
	tcpStats sync.Map

	// limits, eszamanli baglanti ve bosta kalma sinirlari (limits.go). nil = varsayilan.
	limitsOnce sync.Once
	limits     *connLimits

	// openStream nil degilse bridgeTCP ajan akisini Hub yerine buradan alir (testler).
	// Yalnizca replikasiz tunel yolunu etkiler.
	openStream func(ctx context.Context, ti TunnelInfo, remote string) (bridgeStream, bool, error)

	// Balancer, replikali tunellerde (TunnelInfo.Replicas) aday istemci secimini
	// yapar; ingress.Router uygular (HTTP ile ayni algoritma ve saglik tablosu).
	// nil ise cevrimici adaylar arasinda sirali dagitim yapilir. Replikasiz
	// tunellerde kullanilmaz. Start'tan ONCE ayarlanmali.
	Balancer Balancer
	// rr: tunel kimligi -> *atomic.Uint64, replika round-robin sayaci.
	rr sync.Map
	// streams nil degilse replikali yollar aday oturumlari Hub yerine buradan alir (testler).
	streams clientStreams

	// Remote, yerel Hub'da oturumu olmayan istemciler icin dugumler arasi akis tunelini
	// kullanir (cluster_dial.go). nil = tek dugum; hicbir kume yoluna girilmez.
	Remote *RemoteDialer
}

func (m *Manager) connLimits() *connLimits {
	m.limitsOnce.Do(func() {
		if m.limits == nil {
			m.limits = newConnLimits()
		}
	})
	return m.limits
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
			want[ti.key()] = ti.normalized()
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
			// B2: kabul dongusu paniklerse sure dusmez; dongu ussel bekleyip
			// yeniden baslar (dinleyici kapandiysa yeni dongu hemen normal doner).
			obs.GoRestart(m.ctx(), m.Log, "rawproxy.accept", func(context.Context) { m.acceptLoop(ln, port) })
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

// acceptLoop, rezerve port dinleyicisinden kabul eder. Gecici hatada bekleyip
// devam eder; eskiden ilk hatada sessizce cikiyordu ve m.tcpLn kaydi durdugu
// icin Reload portu bir daha acmiyordu (tunel kalici olarak sagir kaliyordu).
func (m *Manager) acceptLoop(ln net.Listener, port listenKey) {
	var bo acceptBackoff
	for {
		conn, err := ln.Accept()
		if err != nil {
			if isClosedErr(err) {
				return // dinleyici kapandi (Reload/closeAll)
			}
			wait := bo.next()
			acceptLog.warn(m.Log, "rawproxy: TCP kabul hatasi, yeniden denenecek", err, wait, "port", port.port)
			time.Sleep(wait)
			continue
		}
		bo.reset()
		go m.handleTCP(conn, port)
	}
}

func (m *Manager) handleTCP(conn net.Conn, port listenKey) {
	defer conn.Close()
	// B2: baglanti basina goroutine; panikte yalniz bu baglanti kapanir
	// (bridgeTCP'nin kendi defer'lari akisi kapatir, sayac/kullanim yazilir).
	defer obs.Recover(m.Log, "rawproxy.tcp", nil)

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
	m.bridgeTCP(conn, ti.normalized())
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
	lim := m.connLimits()
	release, okLim := lim.acquire(ti.TunnelID, ip)
	if !okLim {
		m.Log.Debug("rawproxy: eszamanli baglanti siniri", "ip", ip, "tunnel", ti.TunnelID)
		return
	}
	defer release()

	var sess *tunnel.Session
	var order []string // replikali tunel: bu baglanti icin aday secim sirasi (replicas.go)
	online := true
	if len(ti.Replicas) > 0 {
		order = m.pickOrder(ti, m.clients())
		online = len(order) > 0
	} else if m.openStream == nil {
		sess, online = m.Hub.Get(ti.ClientID)
		if !online && m.remoteOnline(ti.ClientID) {
			// Oturum baska dugumde: tek adayli sira ile dugumler arasi tunelden ac (cluster_dial.go).
			order, online = []string{ti.ClientID}, true
		}
	}
	if !online {
		st.reject(TCPRejectOffline)
		m.Log.Debug("rawproxy: istemci cevrimdisi", "client", ti.ClientID, "tunnel", ti.TunnelID)
		return
	}

	ctx, cancel := context.WithCancel(m.ctx())
	defer cancel()

	// Z2: kabul edilen her baglanti yeniden degerlendirme icin kaydedilir.
	defer m.registerLive(ti, ip, ti.Port > 0, func() {
		cancel()
		conn.Close()
	})()

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
	acked := false // replika yolunda onay failover dongusunde alinir
	if len(order) > 0 {
		var relClient func() // istemcinin acik-baglanti sayacini birakir (yukaridaki release baglanti sinirinindir)
		var rej string
		stream, relClient, rej = m.openReplicaTCP(ctx, ti, order, conn.RemoteAddr().String())
		if stream == nil {
			if rej != "" {
				st.reject(rej)
			}
			return
		}
		defer relClient()
		acked = true
	} else if m.openStream != nil {
		var off bool
		stream, off, err = m.openStream(ctx, ti, conn.RemoteAddr().String())
		if off {
			st.reject(TCPRejectOffline)
			return
		}
	} else {
		stream, err = sess.OpenStreamHalf(ctx, ti.TunnelID, "tcp", conn.RemoteAddr().String())
	}
	if err != nil {
		st.reject(TCPRejectDial)
		m.Log.Warn("rawproxy: akis acilamadi", "tunnel", ti.TunnelID, "hata", err)
		return
	}
	defer stream.Close("closed")

	// Baglanti sonucunu bekle.
	if !acked {
		select {
		case ack := <-stream.Accept():
			if ack.Code != "" {
				st.reject(TCPRejectDial)
				m.Log.Debug("rawproxy: yerel hedefe baglanilamadi", "tunnel", ti.TunnelID, "kod", ack.Code, "mesaj", ack.Message)
				return
			}
		case <-time.After(streamAckTimeout):
			st.reject(TCPRejectDial)
			return
		case <-ctx.Done():
			return
		}
	}

	// Aylik kullanim + kiraci hiz siniri (HTTP ile ayni sayac/limiter). Baglanti
	// basina tek Batcher: sicak yolda atomik, kapanista flush.
	meter := bandwidth.NewBatcher(m.Usage, ti.TenantID, bandwidth.UsageTypeTCP)
	defer meter.Flush()

	st.open()
	began := time.Now()
	defer func() { st.closeConn(time.Since(began)) }()

	// Bosta kalma: iki yonde de lim.idle boyunca veri yoksa baglanti kapanir.
	var lastAct atomic.Int64
	touch := func() { lastAct.Store(time.Now().UnixNano()) }
	touch()
	if lim.idle > 0 {
		go func() {
			tk := time.NewTicker(idleCheckEvery(lim.idle))
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					if time.Since(time.Unix(0, lastAct.Load())) >= lim.idle {
						cancel()
						conn.Close()
						return
					}
				}
			}
		}()
	}

	// Akis iptal edilirse (sunucu kuyrugu tasti, oturum dustu) ziyaretciye bloklu
	// bir yazim olabilir (okumayan ziyaretci): baglantiyi kapatmak yazimi cozer.
	if an, ok := stream.(abortNotifier); ok {
		go func() {
			select {
			case <-an.Aborted():
				conn.Close()
			case <-ctx.Done():
			}
		}()
	}
	hs, _ := stream.(halfStream)

	// ziyaretci -> ajan. Ziyaretci yazmayi duzgun bitirirse (EOF) ve akis yari
	// kapamayi destekliyorsa akis YIKILMAZ: ajana yalniz yazma yonunun bittigi
	// bildirilir, yanit akmaya devam eder (O12).
	visitorEOF := make(chan bool, 1) // deger: yari kapama pazarlandi mi
	go func() {
		defer meter.Flush() // bridge donduktan sonra gelen son baytlar da yazilir
		// Panik: akis ve baglanti kapatilir; ana dongu Closed/ctx ile doner.
		defer obs.Recover(m.Log, "rawproxy.tcp_pump", func(any) {
			cancel()
			conn.Close()
			stream.Close("panic")
		})
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
				touch()
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) && hs != nil && ctx.Err() == nil {
					visitorEOF <- hs.CloseWrite()
					return
				}
				break
			}
		}
		stream.Close("visitor closed")
	}()

	// ajan -> ziyaretci
	var remoteEOF <-chan struct{}
	if hs != nil {
		remoteEOF = hs.RemoteEOF()
	}
	var vEOF <-chan bool = visitorEOF
	var linger <-chan time.Time
	remoteDone, visitorDone := false, false
	for {
		select {
		case data := <-stream.FromLocal():
			if bandwidth.WaitN(ctx, meter.Limiter(ctx), len(data)) != nil {
				return
			}
			n, werr := conn.Write(data)
			st.bytesOut.Add(int64(n))
			meter.Add(0, int64(n))
			touch()
			if werr != nil {
				return
			}
		case <-remoteEOF:
			// Ajan tarafi yazmayi bitirdi; kuyruktaki tum veri yukarida yazildi
			// (RemoteEOF sirali kapanir). Ziyaretciye de FIN gonder, okumaya devam.
			remoteEOF = nil
			remoteDone = true
			if visitorDone {
				return
			}
			cw, ok := conn.(interface{ CloseWrite() error })
			if !ok || cw.CloseWrite() != nil {
				return
			}
		case half := <-vEOF:
			vEOF = nil
			visitorDone = true
			if remoteDone {
				return
			}
			if !half {
				// Eski ajan: StreamClose aldi, hedefe CloseWrite yapip ~3 sn yanit
				// akitir, sonra kendisi kapatir. Gelmezse burada keseriz.
				t := time.NewTimer(legacyHalfCloseLinger)
				defer t.Stop()
				linger = t.C
			}
		case <-linger:
			return
		case <-stream.Closed():
			// Duzgun kapanista Closed, FromLocal'daki tum veri alindiktan SONRA
			// kapanir (tunnel/streamq.go); burada kayip yok.
			return
		case <-ctx.Done():
			return
		}
	}
}

// legacyHalfCloseLinger, yari kapamayi bilmeyen ajanda ziyaretci EOF'undan sonra
// yanitin akmasi icin beklenen ust sure (ajanin 3 sn'lik bekleme suresi + pay).
const legacyHalfCloseLinger = 5 * time.Second

// halfStream, yari kapamayi destekleyen akis (*tunnel.RawStream). Uygulamayan
// akislarda (kume aktarimi, test sahteleri) ziyaretci EOF'u akisi kapatir.
type halfStream interface {
	CloseWrite() bool
	RemoteEOF() <-chan struct{}
}

// abortNotifier, iptali ayrica bildiren akis (*tunnel.RawStream).
type abortNotifier interface {
	Aborted() <-chan struct{}
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

// idleCheckEvery, bosta kalma denetim araligi (sure/4, 1 sn - 1 dk arasi).
func idleCheckEvery(idle time.Duration) time.Duration {
	d := idle / 4
	if d < time.Second {
		d = time.Second
	}
	if d > time.Minute {
		d = time.Minute
	}
	return d
}
