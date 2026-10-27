package wgnet

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Transport, sunucuya giden kanal (agent WSS'i veya `access` oturum WS'i).
type Transport interface {
	SendControl(ctx context.Context, v any, t protocol.MessageType) error
	SendBinary(ctx context.Context, b []byte) error
}

// Node, tek bir WireGuard dugumu: netstack + ozel bind. Sunucudan gelen
// net_registered/net_map ile yapilandirilir.
type Node struct {
	log  *slog.Logger
	tr   Transport
	key  Key
	name string

	bind *relayBind

	mu       sync.Mutex
	state    *protocol.NetState
	dev      *device.Device
	tnet     *netstack.Net
	selfIP   netip.Addr
	peers    map[string]netip.Addr // yapilandirilmis: wg_pub -> ip
	shares   *shareServer
	pending  map[string]chan protocol.NetShareResult
	closed   bool
	ready    chan struct{} // ilk netmap uygulaninca kapanir
	readyOne sync.Once
	failed   chan struct{}
	failErr  error

	// dialed: DialContext ile acilan baglantilar; paylasim iptal olunca kapatilir.
	dialed map[*trackedConn]struct{}

	stop chan struct{}
	// OnMap, her netmap uygulamasindan sonra cagrilir (nil olabilir).
	OnMap func(*protocol.NetState)
	// DisableShares true ise gelen paylasimlar sunulmaz (yalniz alici).
	DisableShares bool

	// --- W2: dogrudan P2P yol (bkz. direct.go, paths.go) ---
	// DisableDirect true ise yalniz relay (W1 davranisi). ZORVEN_NET_DIRECT=0|off
	// ortam degiskeni de bunu varsayilan yapar. Register'dan ONCE ayarlanmalidir.
	DisableDirect bool
	// AllowLoopbackCandidates: loopback adaylari kabul edilir (testler / ayni makine).
	AllowLoopbackCandidates bool
	// ServerHost: sunucu ana makinesi; NetRegistered.Stun bossa host:StunPort sorgulanir.
	ServerHost string
	// STUNServers: bos degilse sunucunun bildirdigi listenin yerine kullanilir.
	STUNServers []string

	// --- W3: Mod B (OS TUN, bkz. tunmode.go) ---
	serverTun  bool          // sunucu wg_tun pazarladi (NetRegistered.Tun)
	serverALog bool          // Z5: sunucu erisim gunlugunu (wg_alog) kabul ediyor
	alogDone   chan struct{} // Z5: erisim gunlugu dongusunu durdurur
	// ALogInterval: net_access_log gonderim araligi (0 = 30 sn; testler icin).
	ALogInterval time.Duration
	pool         netip.Prefix // kiraci ic adres havuzu
	mtu          int
	tunOn        bool
	filt         *Filter
	tunName      string
	tunUndo      []func()
	tunDNS       DNSSetup
	tunOS        OSNet

	disco      DiscoKey
	pm         *pathMgr
	dc         *directConn
	lastEps    []string
	statusKick chan struct{}
	lastReport string
	pendPunch  []string
	symmetric  bool
}

// NewNode, dugumu kurar; Register ile sunucuya kaydolur.
func NewNode(log *slog.Logger, tr Transport, key Key, name string) *Node {
	if log == nil {
		log = slog.Default()
	}
	dk, derr := GenerateDiscoKey()
	n := &Node{
		log: log, tr: tr, key: key, name: name, bind: newRelayBind(),
		disco: dk, statusKick: make(chan struct{}, 1), DisableDirect: derr != nil || DirectDisabledByEnv(),
		AllowLoopbackCandidates: os.Getenv("ZORVEN_NET_ALLOW_LOOPBACK") == "1",
		state:                   protocol.NewNetState(), peers: map[string]netip.Addr{},
		pending: map[string]chan protocol.NetShareResult{}, dialed: map[*trackedConn]struct{}{},
		ready: make(chan struct{}), failed: make(chan struct{}), stop: make(chan struct{}),
	}
	go n.pumpOut()
	return n
}

// DirectDisabledByEnv: ZORVEN_NET_DIRECT=0|off|false|no ise dogrudan yol kapali.
func DirectDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZORVEN_NET_DIRECT"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

func (n *Node) pumpOut() {
	for {
		select {
		case <-n.stop:
			return
		case bp := <-n.bind.out:
			_ = n.tr.SendBinary(context.Background(), *bp)
			n.bind.release(bp)
		}
	}
}

// Register, net_register gonderir.
func (n *Node) Register(ctx context.Context) error {
	reg := protocol.NetRegister{
		Type: protocol.TypeNetRegister, WGPub: n.key.PublicB64(), Name: n.name,
	}
	if !n.DisableDirect {
		reg.DiscoPub = n.disco.PubB64()
	}
	return n.tr.SendControl(ctx, reg, protocol.TypeNetRegister)
}

// HandleFrame: sunucudan gelen FrameWG.
func (n *Node) HandleFrame(srcID uint64, payload []byte) { n.bind.deliver(srcID, payload) }

// HandleControl: net_* mesajlari.
func (n *Node) HandleControl(ctx context.Context, t protocol.MessageType, data []byte) {
	switch t {
	case protocol.TypeNetRegistered:
		var m protocol.NetRegistered
		if json.Unmarshal(data, &m) != nil {
			return
		}
		if err := n.start(m); err != nil {
			n.fail(err)
		}
	case protocol.TypeNetMap:
		var m protocol.NetMap
		if json.Unmarshal(data, &m) != nil {
			return
		}
		n.applyMap(m)
	case protocol.TypeNetPunch:
		var m protocol.NetPunch
		if json.Unmarshal(data, &m) != nil {
			return
		}
		n.mu.Lock()
		pm := n.pm
		n.mu.Unlock()
		if pm != nil {
			pm.punch(m.PeerWGPub, m.Endpoints)
		}
	case protocol.TypeNetShareResult:
		var m protocol.NetShareResult
		if json.Unmarshal(data, &m) != nil {
			return
		}
		n.mu.Lock()
		ch := n.pending[m.ReqID]
		delete(n.pending, m.ReqID)
		n.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case protocol.TypeNetError:
		var m protocol.NetError
		if json.Unmarshal(data, &m) != nil {
			return
		}
		n.fail(fmt.Errorf("sunucu: %s (%s)", m.Message, m.Code))
	}
}

func (n *Node) fail(err error) {
	n.mu.Lock()
	if n.failErr == nil {
		n.failErr = err
		close(n.failed)
	}
	n.mu.Unlock()
}

// Failed, kayit/yapilandirma hatasi olusursa kapanir.
func (n *Node) Failed() (<-chan struct{}, func() error) {
	return n.failed, func() error { n.mu.Lock(); defer n.mu.Unlock(); return n.failErr }
}

func (n *Node) start(m protocol.NetRegistered) error {
	ip, err := netip.ParseAddr(m.IP)
	if err != nil {
		return err
	}
	mtu := m.MTU
	if mtu < 576 || mtu > 1500 {
		mtu = protocol.WGDefaultMTU
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dev != nil || n.closed {
		return nil
	}
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{ip}, nil, mtu)
	if err != nil {
		return err
	}
	if m.Punch && !n.DisableDirect {
		n.startDirectLocked(m)
	}
	dev, err := n.newDeviceLocked(tdev)
	if err != nil {
		return err
	}
	n.serverTun, n.serverALog, n.mtu = m.Tun, m.ALog, mtu
	if pf, perr := netip.ParsePrefix(m.Pool); perr == nil {
		n.pool = pf.Masked()
	}
	n.dev, n.tnet, n.selfIP = dev, tnet, ip
	n.shares = newShareServer(n.log, tnet)
	n.state.Self = protocol.NetPeer{NodeID: m.NodeID, IP: m.IP, Name: m.Name, Kind: m.Kind}
	n.log.Info("zorven net dugumu hazir", "ip", m.IP, "ad", m.Name, "mtu", mtu)
	return nil
}

// newDeviceLocked, verilen tun aygiti (netstack veya OS TUN/filtre) uzerinde
// wireguard-go cihazini kurar ve acar. Anahtar/bind/peer kodu Mod A ve Mod B
// icin ORTAKTIR.
func (n *Node) newDeviceLocked(tdev tun.Device) (*device.Device, error) {
	lg := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf: func(f string, a ...any) {
			n.log.Debug("wireguard: " + fmt.Sprintf(f, a...))
		},
	}
	dev := device.NewDevice(tdev, n.bind, lg)
	if err := dev.IpcSet("private_key=" + n.key.hex() + "\n"); err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	return dev, nil
}

func hexKey(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return "", errors.New("gecersiz es anahtari")
	}
	return hex.EncodeToString(raw), nil
}

func (n *Node) applyMap(m protocol.NetMap) {
	n.mu.Lock()
	if n.dev == nil || n.closed {
		n.mu.Unlock()
		return
	}
	n.state.Apply(m)
	n.syncPeersLocked()
	var outs []protocol.NetShareOut
	if !n.DisableShares {
		for _, s := range n.state.SharesOut {
			outs = append(outs, s)
		}
	}
	ss := n.shares
	n.updateFilterLocked()
	st := n.snapshotLocked()
	var kill []*trackedConn
	for tc := range n.dialed {
		ok := false
		for _, sh := range n.state.SharesIn {
			if sh.OwnerIP == tc.ip && sh.Port == tc.port && sh.Proto == tc.proto {
				ok = true
				break
			}
		}
		if !ok {
			kill = append(kill, tc)
		}
	}
	cb := n.OnMap
	punchReqs := n.pendPunch
	n.pendPunch = nil
	n.mu.Unlock()

	for _, pub := range punchReqs {
		go n.requestPunch(pub)
	}
	n.kickStatus()
	for _, tc := range kill {
		tc.Close()
	}
	if ss != nil {
		ss.reconcile(outs)
	}
	n.readyOne.Do(func() { close(n.ready) })
	if cb != nil {
		cb(st)
	}
}

func (n *Node) syncPeersLocked() {
	// Kaldirilanlar
	for pub, ip := range n.peers {
		if p, ok := n.state.Peers[pub]; !ok || p.IP != ip.String() {
			if h, err := hexKey(pub); err == nil {
				_ = n.dev.IpcSet("public_key=" + h + "\nremove=true\n")
			}
			if n.pm != nil {
				n.pm.removePeer(ip)
			}
			delete(n.peers, pub)
		}
	}
	// Eklenenler
	for pub, p := range n.state.Peers {
		if pip, perr := netip.ParseAddr(p.IP); perr == nil && n.pm != nil {
			// Disco anahtari degisti/yeni es: dogrudan yol icin punch iste.
			if n.pm.setPeer(pip, pub, p.DiscoPub) {
				n.pendPunch = append(n.pendPunch, pub)
			}
		}
		if _, ok := n.peers[pub]; ok {
			continue
		}
		ip, err := netip.ParseAddr(p.IP)
		if err != nil {
			continue
		}
		h, err := hexKey(pub)
		if err != nil {
			continue
		}
		cfg := fmt.Sprintf("public_key=%s\nallowed_ip=%s/32\nendpoint=%s\npersistent_keepalive_interval=0\n",
			h, ip, netip.AddrPortFrom(ip, 1))
		if err := n.dev.IpcSet(cfg); err != nil {
			n.log.Warn("es eklenemedi", "ip", p.IP, "hata", err)
			continue
		}
		n.peers[pub] = ip
	}
}

func (n *Node) snapshotLocked() *protocol.NetState {
	cp := protocol.NewNetState()
	cp.Self, cp.MTU, cp.Epoch = n.state.Self, n.state.MTU, n.state.Epoch
	cp.ACL = append([]protocol.NetACLRule(nil), n.state.ACL...)
	if n.state.DNS != nil {
		d := *n.state.DNS
		cp.DNS = &d
	}
	for k, v := range n.state.Peers {
		cp.Peers[k] = v
	}
	for k, v := range n.state.SharesIn {
		cp.SharesIn[k] = v
	}
	for k, v := range n.state.SharesOut {
		cp.SharesOut[k] = v
	}
	return cp
}

// State, netmap'in anlik kopyasi.
func (n *Node) State() *protocol.NetState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.snapshotLocked()
}

// WaitReady, kayit + ilk netmap gelene kadar bekler.
func (n *Node) WaitReady(ctx context.Context) error {
	select {
	case <-n.ready:
		return nil
	case <-n.failed:
		_, f := n.Failed()
		return f()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DialContext, netstack uzerinden ic aga baglanir ("tcp"/"udp", "ip:port").
func (n *Node) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	n.mu.Lock()
	tnet, tunOn := n.tnet, n.tunOn
	n.mu.Unlock()
	var c net.Conn
	var err error
	switch {
	case tunOn:
		// Mod B: ic ag OS yigininda; adres TUN rotasindan gider.
		c, err = (&net.Dialer{}).DialContext(ctx, network, addr)
	case tnet != nil:
		c, err = tnet.DialContext(ctx, network, addr)
	default:
		return nil, errors.New("wg dugumu hazir degil")
	}
	if err != nil {
		return nil, err
	}
	host, ps, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(ps)
	tc := &trackedConn{Conn: c, n: n, ip: host, port: port, proto: network}
	n.mu.Lock()
	n.dialed[tc] = struct{}{}
	n.mu.Unlock()
	return tc, nil
}

type trackedConn struct {
	net.Conn
	n     *Node
	ip    string
	port  int
	proto string
}

func (t *trackedConn) Close() error {
	t.n.mu.Lock()
	delete(t.n.dialed, t)
	t.n.mu.Unlock()
	return t.Conn.Close()
}

// Resolve, "paylasim-adi", "cihaz-adi" veya "...:port" hedefini bir erisilebilir paylasima cevirir.
func (n *Node) Resolve(target string) (protocol.NetShareIn, error) {
	return ResolveShare(n.State(), target)
}

// CreateShare, bu cihazin bir portunu paylasir (cihaz dugumu).
func (n *Node) CreateShare(ctx context.Context, req protocol.NetShareCreate) (string, error) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	req.Type = protocol.TypeNetShareCreate
	req.ReqID = hex.EncodeToString(b[:])
	ch := make(chan protocol.NetShareResult, 1)
	n.mu.Lock()
	n.pending[req.ReqID] = ch
	n.mu.Unlock()
	if err := n.tr.SendControl(ctx, req, protocol.TypeNetShareCreate); err != nil {
		return "", err
	}
	select {
	case r := <-ch:
		if !r.OK {
			return "", fmt.Errorf("%s (%s)", r.Message, r.Code)
		}
		return r.ShareID, nil
	case <-time.After(15 * time.Second):
		return "", errors.New("sunucu paylasim yanitini vermedi")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// DeleteShare, kendi paylasimini iptal eder.
func (n *Node) DeleteShare(ctx context.Context, id string) error {
	return n.tr.SendControl(ctx, protocol.NetShareDelete{
		Type: protocol.TypeNetShareDelete, ReqID: "d-" + id, ShareID: id,
	}, protocol.TypeNetShareDelete)
}

// Close, dugumu kapatir.
func (n *Node) Close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	dev, ss := n.dev, n.shares
	undo := n.takeTunUndoLocked()
	n.mu.Unlock()
	close(n.stop)
	if n.dc != nil {
		n.dc.Close()
	}
	if ss != nil {
		ss.closeAll()
	}
	if dev != nil {
		dev.Close()
	}
	for _, u := range undo {
		u()
	}
}

// Dropped, bind kuyruk tasmasi sayaci (teshis).
func (n *Node) Dropped() int64 { return n.bind.Dropped.Load() }

// DialShare, "ad[:port]" hedefini simdiki netmap'ten cozup ic aga baglanir.
func (n *Node) DialShare(ctx context.Context, target string) (net.Conn, error) {
	s, err := n.Resolve(target)
	if err != nil {
		return nil, err
	}
	return n.DialContext(ctx, s.Proto, net.JoinHostPort(s.OwnerIP, fmt.Sprint(s.Port)))
}

// ShareProto, hedefin paylasim protokolu (yerel dinleyici turunu secmek icin).
func (n *Node) ShareProto(target string) (string, error) {
	s, err := n.Resolve(target)
	return s.Proto, err
}

// --- W2: dogrudan yol ---------------------------------------------------------

func (n *Node) stunServers(m protocol.NetRegistered) []string {
	if len(n.STUNServers) > 0 {
		return n.STUNServers
	}
	if len(m.Stun) > 0 {
		return m.Stun
	}
	if m.StunPort > 0 && n.ServerHost != "" {
		return []string{net.JoinHostPort(n.ServerHost, strconv.Itoa(m.StunPort))}
	}
	return nil
}

// startDirectLocked: UDP soketi + yol yoneticisi; hata olursa yalniz relay.
// n.mu altinda ve wireguard cihazi acilmadan once cagrilir.
func (n *Node) startDirectLocked(m protocol.NetRegistered) {
	pm := newPathMgr(n.disco, nil)
	pm.allowLoopback = n.AllowLoopbackCandidates
	dc, err := newDirectConn(n.log, pm, n.bind.deliverIP, n.AllowLoopbackCandidates)
	if err != nil {
		n.log.Warn("dogrudan UDP soketi acilamadi; yalniz relay", "hata", err)
		return
	}
	pm.sendUDP = dc.sendTo
	pm.onChange = n.kickStatus
	pm.requestPunch = func(pub string) { go n.requestPunch(pub) }
	n.pm, n.dc = pm, dc
	n.bind.pm, n.bind.dsend = pm, dc.sendTo
	servers := n.stunServers(m)
	go n.stepLoop(pm)
	go n.endpointLoop(dc, servers)
	go n.statusLoop(pm)
}

func (n *Node) stepLoop(pm *pathMgr) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
			pm.step(pm.now())
		}
	}
}

// endpointLoop: aday adreslerini toplar ve degistiginde sunucuya bildirir.
// Her 30 sn'de yeniden toplanir (NAT haritasi/arayuz degisimi).
func (n *Node) endpointLoop(dc *directConn, servers []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-n.stop; cancel() }()
	for {
		eps, sym := dc.gather(ctx, servers)
		if ctx.Err() != nil {
			return
		}
		n.mu.Lock()
		changed := !equalStr(eps, n.lastEps)
		if changed {
			n.lastEps = eps
		}
		n.symmetric = sym
		n.mu.Unlock()
		if changed {
			if sym {
				n.log.Debug("adres-bagimli NAT (simetrik) gozlendi; dogrudan yol zor olabilir")
			}
			sctx, sc := context.WithTimeout(ctx, 10*time.Second)
			_ = n.tr.SendControl(sctx, protocol.NetEndpoints{Type: protocol.TypeNetEndpoints, Endpoints: eps}, protocol.TypeNetEndpoints)
			sc()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (n *Node) requestPunch(pub string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = n.tr.SendControl(ctx, protocol.NetPunchReq{Type: protocol.TypeNetPunchReq, PeerWGPub: pub}, protocol.TypeNetPunchReq)
}

func (n *Node) kickStatus() {
	select {
	case n.statusKick <- struct{}{}:
	default:
	}
}

// statusLoop: yol degisimlerini sunucuya (panel gorunurlugu) raporlar; birlestirir.
func (n *Node) statusLoop(pm *pathMgr) {
	for {
		select {
		case <-n.stop:
			return
		case <-n.statusKick:
		}
		select {
		case <-n.stop:
			return
		case <-time.After(300 * time.Millisecond):
		}
		snap := pm.snapshot()
		st := protocol.NetStatus{Type: protocol.TypeNetStatus, Paths: make([]protocol.NetPathStatus, 0, len(snap))}
		var sig strings.Builder
		for _, p := range snap {
			ps := protocol.NetPathStatus{PeerWGPub: p.WGPub, Path: p.Path, RTTMs: int(p.RTT / time.Millisecond)}
			st.Paths = append(st.Paths, ps)
			sig.WriteString(p.WGPub + "|" + p.Path + ";")
		}
		n.mu.Lock()
		same := n.lastReport == sig.String()
		n.lastReport = sig.String()
		n.mu.Unlock()
		if same {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = n.tr.SendControl(ctx, st, protocol.TypeNetStatus)
		cancel()
	}
}

// PathInfo, bir esle secili yol (CLI/ajan durum ciktisi).
type PathInfo struct {
	Name string
	IP   string
	// Path: "direct" | "relay"
	Path string
	// Addr: dogrudan yolun uzak adresi (yalniz direct).
	Addr string
	RTT  time.Duration
	// DirectCapable: es dogrudan yolu destekliyor (disco anahtari var).
	DirectCapable bool
	// Online: sunucuya gore es cevrimici.
	Online bool
}

// Paths, esler icin gecerli yolu doner (netmap'te gorunen esler).
func (n *Node) Paths() []PathInfo {
	n.mu.Lock()
	pm := n.pm
	names := map[string]string{}
	online := map[string]bool{}
	for _, p := range n.state.Peers {
		names[p.IP] = p.Name
		online[p.IP] = p.Online
	}
	var peers []protocol.NetPeer
	for _, p := range n.state.Peers {
		peers = append(peers, p)
	}
	n.mu.Unlock()
	var out []PathInfo
	if pm == nil {
		for _, p := range peers {
			out = append(out, PathInfo{Name: p.Name, IP: p.IP, Path: protocol.PathRelay, Online: p.Online})
		}
	} else {
		have := map[string]bool{}
		for _, s := range pm.snapshot() {
			pi := PathInfo{Name: names[s.IP.String()], IP: s.IP.String(), Path: s.Path, RTT: s.RTT, DirectCapable: s.HasDisco, Online: online[s.IP.String()]}
			if s.Addr.IsValid() {
				pi.Addr = s.Addr.String()
			}
			have[pi.IP] = true
			out = append(out, pi)
		}
		for _, p := range peers {
			if !have[p.IP] {
				out = append(out, PathInfo{Name: p.Name, IP: p.IP, Path: protocol.PathRelay, Online: p.Online})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DirectEnabled: dogrudan yol bu dugumde calisiyor mu (sunucu pazarladi, soket acildi).
func (n *Node) DirectEnabled() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.pm != nil
}

// TrafficStats: yola gore paket sayaclari (teshis).
func (n *Node) TrafficStats() (directTx, relayTx, relayRx int64) {
	return n.bind.DirectTx.Load(), n.bind.RelayTx.Load(), n.bind.RelayRx.Load()
}
