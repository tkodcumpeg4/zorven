package wgnet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"github.com/tkodcumpeg4/zorven/shared/stun"
)

// miniSrv: sunucunun W2'ye iliskin davranisini taklit eden asgari kontrol
// duzlemi (netctl bu modulde import edilemez; gercek sunucuyla uctan uca test
// tests/netshare altindadir). Gercek wireguard-go + gercek UDP (loopback).
type miniSrv struct {
	t     *testing.T
	mu    sync.Mutex
	nodes map[string]*mnode
	order []string
}

type mnode struct {
	id       string
	node     *Node
	ip       netip.Addr
	wgPub    string
	discoPub string
	punch    bool
	eps      []string
	q        chan func()
	status   []protocol.NetPathStatus
}

type miniTr struct {
	s  *miniSrv
	id string
}

func (m miniTr) SendControl(_ context.Context, v any, mt protocol.MessageType) error {
	b, err := protocol.Marshal(v, mt)
	if err != nil {
		return err
	}
	m.s.fromNode(m.id, mt, b)
	return nil
}

func (m miniTr) SendBinary(_ context.Context, b []byte) error {
	f, err := protocol.DecodeBodyFrame(b)
	if err != nil || f.FrameType != protocol.FrameWG {
		return nil
	}
	dst := protocol.RelayIDToIP(f.ReqID)
	m.s.mu.Lock()
	var to, from *mnode
	for _, n := range m.s.nodes {
		if n.ip == dst {
			to = n
		}
		if n.id == m.id {
			from = n
		}
	}
	m.s.mu.Unlock()
	if to != nil && from != nil {
		to.node.HandleFrame(protocol.IPToRelayID(from.ip), f.Payload)
	}
	return nil
}

func newMini(t *testing.T) *miniSrv { return &miniSrv{t: t, nodes: map[string]*mnode{}} }

func (s *miniSrv) add(id string, disableDirect bool) *Node {
	k, _ := GenerateKey()
	mn := &mnode{id: id, q: make(chan func(), 64)}
	n := NewNode(slog.New(slog.NewTextHandler(io.Discard, nil)), miniTr{s, id}, k, id)
	n.DisableDirect = disableDirect
	n.AllowLoopbackCandidates = true
	mn.node = n
	s.mu.Lock()
	s.nodes[id] = mn
	s.order = append(s.order, id)
	s.mu.Unlock()
	go func() {
		for f := range mn.q {
			f()
		}
	}()
	t := s.t
	t.Cleanup(func() { n.Close(); close(mn.q) })
	if err := n.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return n
}

func (s *miniSrv) enqueue(mn *mnode, mt protocol.MessageType, v any) {
	b, _ := protocol.Marshal(v, mt)
	defer func() { _ = recover() }() // kapanis sirasinda kuyruk kapali olabilir
	mn.q <- func() { mn.node.HandleControl(context.Background(), mt, b) }
}

func (s *miniSrv) fromNode(id string, mt protocol.MessageType, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mn := s.nodes[id]
	switch mt {
	case protocol.TypeNetRegister:
		var r protocol.NetRegister
		_ = json.Unmarshal(data, &r)
		mn.wgPub, mn.discoPub = r.WGPub, r.DiscoPub
		mn.ip = netip.MustParseAddr(fmt.Sprintf("100.64.1.%d", len(s.order)+9))
		mn.punch = r.DiscoPub != ""
		s.enqueue(mn, protocol.TypeNetRegistered, protocol.NetRegistered{
			Type: protocol.TypeNetRegistered, NodeID: id, IP: mn.ip.String(), Name: id, MTU: 1380, Kind: "device", Punch: mn.punch,
			Tun: true, Pool: "100.64.0.0/10",
		})
		if len(s.order) == 2 {
			s.sendMaps()
		}
	case protocol.TypeNetEndpoints:
		var e protocol.NetEndpoints
		_ = json.Unmarshal(data, &e)
		mn.eps = e.Endpoints
		s.punchAll()
	case protocol.TypeNetPunchReq:
		s.punchAll()
	case protocol.TypeNetStatus:
		var st protocol.NetStatus
		_ = json.Unmarshal(data, &st)
		mn.status = st.Paths
	}
}

func (s *miniSrv) sendMaps() {
	for _, id := range s.order {
		self := s.nodes[id]
		nm := protocol.NetMap{Type: protocol.TypeNetMap, Epoch: 1, Full: true, MTU: 1380,
			Self: protocol.NetPeer{WGPub: self.wgPub, IP: self.ip.String(), Name: id, Kind: "device", Online: true}}
		for _, oid := range s.order {
			if oid == id {
				continue
			}
			o := s.nodes[oid]
			nm.Peers = append(nm.Peers, protocol.NetPeer{WGPub: o.wgPub, IP: o.ip.String(), Name: oid, Kind: "device", Online: true, DiscoPub: o.discoPub})
		}
		s.enqueue(self, protocol.TypeNetMap, nm)
	}
}

func (s *miniSrv) punchAll() {
	if len(s.order) != 2 {
		return
	}
	a, b := s.nodes[s.order[0]], s.nodes[s.order[1]]
	if !a.punch || !b.punch || len(a.eps) == 0 || len(b.eps) == 0 {
		return
	}
	s.enqueue(a, protocol.TypeNetPunch, protocol.NetPunch{Type: protocol.TypeNetPunch, PeerWGPub: b.wgPub, PeerDiscoPub: b.discoPub, Endpoints: b.eps})
	s.enqueue(b, protocol.TypeNetPunch, protocol.NetPunch{Type: protocol.TypeNetPunch, PeerWGPub: a.wgPub, PeerDiscoPub: a.discoPub, Endpoints: a.eps})
}

func fastTiming(t *testing.T) {
	old := [7]time.Duration{probeInterval, probeWindow, keepalive, directTimeout, pingRetry, retryMin, retryMax}
	probeInterval, probeWindow, keepalive, directTimeout = 150*time.Millisecond, 2*time.Second, 700*time.Millisecond, 2*time.Second
	pingRetry, retryMin, retryMax = 250*time.Millisecond, 400*time.Millisecond, 2*time.Second
	t.Cleanup(func() {
		probeInterval, probeWindow, keepalive, directTimeout, pingRetry, retryMin, retryMax = old[0], old[1], old[2], old[3], old[4], old[5], old[6]
	})
}

func waitUntil(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", what)
}

func pathTo(n *Node, peerIP string) string {
	for _, p := range n.Paths() {
		if p.IP == peerIP {
			return p.Path
		}
	}
	return ""
}

func nodeIP(n *Node) string { n.mu.Lock(); defer n.mu.Unlock(); return n.selfIP.String() }

// echoOnce: dialer -> listener (netstack icinde) 'size' bayt gidis-donus, sha256 dogrular.
func echoThrough(t *testing.T, srv, cli *Node, port int, size int) {
	t.Helper()
	ln, err := srv.tnet.ListenTCP(&net.TCPAddr{Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := cli.DialContext(ctx, "tcp", net.JoinHostPort(nodeIP(srv), fmt.Sprint(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	payload := make([]byte, size)
	_, _ = rand.Read(payload)
	want := sha256.Sum256(payload)
	go func() { _, _ = c.Write(payload) }()
	got := sha256.New()
	c.SetReadDeadline(time.Now().Add(20 * time.Second))
	if _, err := io.CopyN(got, c, int64(size)); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if [32]byte(got.Sum(nil)) != want {
		t.Fatal("veri bozuldu")
	}
}

func TestDirectUpgradeFallbackAndRecovery(t *testing.T) {
	fastTiming(t)
	s := newMini(t)
	a := s.add("a", false)
	b := s.add("b", false)
	if err := a.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.DirectEnabled() || !b.DirectEnabled() {
		t.Fatal("dogrudan yol baslamadi")
	}
	aIP, bIP := nodeIP(a), nodeIP(b)

	// 1) Ilk paketler relay'den akar (daha yol dogrulanmadan); veri bozulmaz.
	echoThrough(t, a, b, 7001, 64<<10)

	// 2) Dogrulaninca dogrudan yola gecilir.
	waitUntil(t, "iki yon de dogrudan", 10*time.Second, func() bool {
		return pathTo(a, bIP) == protocol.PathDirect && pathTo(b, aIP) == protocol.PathDirect
	})
	d0, _, _ := a.TrafficStats()
	_, r0, _ := a.TrafficStats()
	echoThrough(t, a, b, 7002, 1<<20)
	d1, r1, _ := a.TrafficStats()
	if d1-d0 < 100 {
		t.Fatalf("1 MB'lik aktarim dogrudan yoldan gitmeli: directTx +%d", d1-d0)
	}
	if r1-r0 > 50 {
		t.Fatalf("dogrudan yolda relay kullanimi beklenmez: relayTx +%d", r1-r0)
	}
	// Durum sunucuya raporlandi (panel).
	waitUntil(t, "net_status raporu", 5*time.Second, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, p := range s.nodes["a"].status {
			if p.Path == protocol.PathDirect {
				return true
			}
		}
		return false
	})

	// 3) UDP kesilir (guvenlik duvari): relay'e dusulur, trafik surer.
	a.dc.blackhole.Store(true)
	b.dc.blackhole.Store(true)
	waitUntil(t, "relay'e dusus", 8*time.Second, func() bool {
		return pathTo(a, bIP) == protocol.PathRelay && pathTo(b, aIP) == protocol.PathRelay
	})
	_, rr0, _ := a.TrafficStats()
	echoThrough(t, a, b, 7003, 256<<10)
	_, rr1, _ := a.TrafficStats()
	if rr1-rr0 < 50 {
		t.Fatalf("dususten sonra trafik relay'den gitmeli: relayTx +%d", rr1-rr0)
	}

	// 4) UDP geri gelir: yeniden dogrudan.
	a.dc.blackhole.Store(false)
	b.dc.blackhole.Store(false)
	waitUntil(t, "yeniden dogrudan", 15*time.Second, func() bool {
		return pathTo(a, bIP) == protocol.PathDirect && pathTo(b, aIP) == protocol.PathDirect
	})
	echoThrough(t, a, b, 7004, 128<<10)
}

// Bir taraf dogrudan yolu desteklemiyorsa (eski surum / --no-p2p) W1 gibi
// yalniz relay calisir ve hicbir yoklama yapilmaz.
func TestOldPeerStaysRelayOnly(t *testing.T) {
	fastTiming(t)
	s := newMini(t)
	a := s.add("a", false)
	b := s.add("b", true)
	if err := a.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.DirectEnabled() {
		t.Fatal("DisableDirect dugumde dogrudan yol acik")
	}
	echoThrough(t, a, b, 7010, 128<<10)
	time.Sleep(2500 * time.Millisecond)
	if pathTo(a, nodeIP(b)) != protocol.PathRelay {
		t.Fatal("destek vermeyen esle yol relay kalmali")
	}
	if d, _, _ := a.TrafficStats(); d != 0 {
		t.Fatalf("dogrudan paket gitmemeli: %d", d)
	}
	echoThrough(t, b, a, 7011, 64<<10)
}

func TestGatherUsesSTUNAndToleratesDeadServer(t *testing.T) {
	// Sahte STUN yanit veren UDP sunucu: "genel" adres 203.0.113.9:4000 dondurur.
	spc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer spc.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := spc.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			if tx, ok := stun.ParseBindingRequest(buf[:n]); ok {
				_, _ = spc.WriteToUDPAddrPort(stun.NewBindingResponse(tx, netip.MustParseAddrPort("203.0.113.9:4000")), from)
			}
		}
	}()
	k, _ := GenerateDiscoKey()
	pm := newPathMgr(k, nil)
	dc, err := newDirectConn(slog.Default(), pm, func(netip.Addr, []byte) {}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	eps, sym := dc.gather(context.Background(), []string{spc.LocalAddr().String()})
	found := false
	for _, e := range eps {
		if e == "203.0.113.9:4000" {
			found = true
		}
	}
	if !found || sym {
		t.Fatalf("STUN yansima adresi adaylarda yok: %v sym=%v", eps, sym)
	}
	// Yanit vermeyen sunucu: yerel adaylar yine doner, takilmaz.
	dead, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer dead.Close()
	start := time.Now()
	eps2, _ := dc.gather(context.Background(), []string{dead.LocalAddr().String()})
	if len(eps2) == 0 {
		t.Fatal("yerel adaylar bos")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("olu STUN sunucusu cok bekletti: %v", time.Since(start))
	}
	// Iki sunucu farkli adres gorurse adres-bagimli (simetrik) NAT sezilir.
	spc2, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer spc2.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := spc2.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			if tx, ok := stun.ParseBindingRequest(buf[:n]); ok {
				_, _ = spc2.WriteToUDPAddrPort(stun.NewBindingResponse(tx, netip.MustParseAddrPort("203.0.113.9:4001")), from)
			}
		}
	}()
	_, sym2 := dc.gather(context.Background(), []string{spc.LocalAddr().String(), spc2.LocalAddr().String()})
	if !sym2 {
		t.Fatal("farkli yansima adresleri simetrik NAT olarak raporlanmali")
	}
}
