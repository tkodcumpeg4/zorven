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
	"strconv"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/device"
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
}

// NewNode, dugumu kurar; Register ile sunucuya kaydolur.
func NewNode(log *slog.Logger, tr Transport, key Key, name string) *Node {
	if log == nil {
		log = slog.Default()
	}
	n := &Node{
		log: log, tr: tr, key: key, name: name, bind: newRelayBind(),
		state: protocol.NewNetState(), peers: map[string]netip.Addr{},
		pending: map[string]chan protocol.NetShareResult{}, dialed: map[*trackedConn]struct{}{},
		ready: make(chan struct{}), failed: make(chan struct{}), stop: make(chan struct{}),
	}
	go n.pumpOut()
	return n
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
	return n.tr.SendControl(ctx, protocol.NetRegister{
		Type: protocol.TypeNetRegister, WGPub: n.key.PublicB64(), Name: n.name,
	}, protocol.TypeNetRegister)
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
	lg := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf: func(f string, a ...any) {
			n.log.Debug("wireguard: " + fmt.Sprintf(f, a...))
		},
	}
	dev := device.NewDevice(tdev, n.bind, lg)
	if err := dev.IpcSet("private_key=" + n.key.hex() + "\n"); err != nil {
		dev.Close()
		return err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return err
	}
	n.dev, n.tnet, n.selfIP = dev, tnet, ip
	n.shares = newShareServer(n.log, tnet)
	n.state.Self = protocol.NetPeer{NodeID: m.NodeID, IP: m.IP, Name: m.Name, Kind: m.Kind}
	n.log.Info("zorven net dugumu hazir", "ip", m.IP, "ad", m.Name, "mtu", mtu)
	return nil
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
	n.mu.Unlock()

	for _, tc := range kill {
		tc.Close()
	}
	ss.reconcile(outs)
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
			delete(n.peers, pub)
		}
	}
	// Eklenenler
	for pub, p := range n.state.Peers {
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
	tnet := n.tnet
	n.mu.Unlock()
	if tnet == nil {
		return nil, errors.New("wg dugumu hazir degil")
	}
	c, err := tnet.DialContext(ctx, network, addr)
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
	n.mu.Unlock()
	close(n.stop)
	if ss != nil {
		ss.closeAll()
	}
	if dev != nil {
		dev.Close()
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
