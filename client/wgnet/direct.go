package wgnet

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/stun"
)

// directConn: dogrudan P2P yolun UDP soketi. Tek soket hem WireGuard paketleri
// hem disco yoklamasi hem STUN icindir (NAT haritasi tek: STUN'un gordugu adres
// WireGuard'in kullanacagi adrestir).
type directConn struct {
	log           *slog.Logger
	pc            *net.UDPConn
	pm            *pathMgr
	deliver       func(ip netip.Addr, pkt []byte)
	allowLoopback bool

	mu      sync.Mutex
	waiters map[stun.TxID]chan netip.AddrPort
	closed  atomic.Bool

	// blackhole: gelen/giden UDP dusurulur (test/teshis: guvenlik duvari benzetimi).
	blackhole atomic.Bool

	RxPkts atomic.Int64
	TxPkts atomic.Int64
}

func newDirectConn(log *slog.Logger, pm *pathMgr, deliver func(netip.Addr, []byte), allowLoopback bool) (*directConn, error) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	d := &directConn{log: log, pc: pc, pm: pm, deliver: deliver, allowLoopback: allowLoopback,
		waiters: map[stun.TxID]chan netip.AddrPort{}}
	go d.readLoop()
	return d, nil
}

func (d *directConn) port() uint16 { return uint16(d.pc.LocalAddr().(*net.UDPAddr).Port) }

func (d *directConn) Close() {
	if d.closed.CompareAndSwap(false, true) {
		_ = d.pc.Close()
	}
}

func (d *directConn) sendTo(ap netip.AddrPort, pkt []byte) error {
	if d.blackhole.Load() {
		return nil
	}
	_, err := d.pc.WriteToUDPAddrPort(pkt, ap)
	if err == nil {
		d.TxPkts.Add(1)
	}
	return err
}

func (d *directConn) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, src, err := d.pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			if d.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(20 * time.Millisecond) // gecici hata (orn. Windows ICMP reset)
			continue
		}
		pkt := buf[:n]
		if d.blackhole.Load() {
			continue
		}
		switch {
		case stun.IsSTUN(pkt):
			d.handleSTUN(pkt)
		case isDisco(pkt):
			d.pm.handleDisco(pkt, src)
		default:
			if ip, ok := d.pm.lookupSrc(src); ok {
				d.RxPkts.Add(1)
				d.deliver(ip, pkt)
			}
		}
	}
}

func (d *directConn) handleSTUN(pkt []byte) {
	var tx stun.TxID
	copy(tx[:], pkt[8:20])
	d.mu.Lock()
	ch := d.waiters[tx]
	d.mu.Unlock()
	if ch == nil {
		return
	}
	if ap, ok := stun.ParseBindingResponse(pkt, tx); ok {
		select {
		case ch <- ap:
		default:
		}
	}
}

// stunQuery, server'a Binding istegi atip yansima adresini doner.
func (d *directConn) stunQuery(ctx context.Context, server netip.AddrPort) (netip.AddrPort, error) {
	tx := stun.NewTxID()
	ch := make(chan netip.AddrPort, 1)
	d.mu.Lock()
	d.waiters[tx] = ch
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.waiters, tx); d.mu.Unlock() }()
	req := stun.NewBindingRequest(tx)
	for try := 0; try < 3; try++ {
		if _, err := d.pc.WriteToUDPAddrPort(req, server); err != nil {
			return netip.AddrPort{}, err
		}
		select {
		case ap := <-ch:
			return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), nil
		case <-time.After(600 * time.Millisecond):
		case <-ctx.Done():
			return netip.AddrPort{}, ctx.Err()
		}
	}
	return netip.AddrPort{}, errors.New("stun yaniti yok")
}

// localCandidates, yerel arayuz adreslerini (soket portuyla) doner.
func (d *directConn) localCandidates() []netip.AddrPort {
	var out []netip.AddrPort
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	port := d.port()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipn.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		if ip.IsLoopback() && !d.allowLoopback {
			continue
		}
		out = append(out, netip.AddrPortFrom(ip, port))
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// resolveSTUN, "host:port" listesini UDP adreslerine cevirir (IPv4 tercihli).
func resolveSTUN(ctx context.Context, servers []string) []netip.AddrPort {
	var out []netip.AddrPort
	for _, s := range servers {
		host, ps, err := net.SplitHostPort(s)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(ps)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
		cancel()
		if err != nil || len(ips) == 0 {
			continue
		}
		pick := ips[0]
		for _, ip := range ips {
			if ip.Unmap().Is4() {
				pick = ip.Unmap()
				break
			}
		}
		out = append(out, netip.AddrPortFrom(pick, uint16(port)))
	}
	return out
}

// gather, aday listesini toplar: yerel adresler + STUN yansimalari. symmetric,
// farkli STUN sunucularinin farkli adres/port gordugunu belirtir (adres-
// bagimli eslesme: dogrudan yol buyuk olasilikla kurulamaz, relay kalir).
func (d *directConn) gather(ctx context.Context, servers []string) (eps []string, symmetric bool) {
	set := map[netip.AddrPort]bool{}
	for _, c := range d.localCandidates() {
		set[c] = true
	}
	var seen []netip.AddrPort
	for _, sv := range resolveSTUN(ctx, servers) {
		qctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		ap, err := d.stunQuery(qctx, sv)
		cancel()
		if err != nil {
			continue
		}
		seen = append(seen, ap)
		set[ap] = true
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[0] {
			symmetric = true
		}
	}
	for ap := range set {
		eps = append(eps, ap.String())
	}
	sort.Strings(eps)
	return eps, symmetric
}
