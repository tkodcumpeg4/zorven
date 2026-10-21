package wgnet

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

const udpFlowIdle = 60 * time.Second

// shareServer, paylasilan portlari netstack'te dinler ve YALNIZ loopback'e
// (127.0.0.1:<port>) koprular. Hedef sunucudan/kullanicidan alinmaz: LAN taramasi
// ve SSRF mumkun degildir. Kabul edilen kaynak, sunucunun verdigi izin listesindeki
// es IP'leridir (WireGuard AllowedIPs kaynak IP'yi o ese kilitler).
type shareServer struct {
	log  *slog.Logger
	tnet *netstack.Net

	mu     sync.Mutex
	active map[string]*activeShare
}

func newShareServer(log *slog.Logger, tnet *netstack.Net) *shareServer {
	return &shareServer{log: log, tnet: tnet, active: map[string]*activeShare{}}
}

type activeShare struct {
	out   protocol.NetShareOut
	allow atomic.Pointer[map[netip.Addr]struct{}]
	stop  func()

	mu    sync.Mutex
	conns map[net.Conn]netip.Addr
}

func allowSet(ips []string) *map[netip.Addr]struct{} {
	m := make(map[netip.Addr]struct{}, len(ips))
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil {
			m[a] = struct{}{}
		}
	}
	return &m
}

func (a *activeShare) allowed(ip netip.Addr) bool {
	m := a.allow.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[ip]
	return ok
}

func (a *activeShare) track(c net.Conn, ip netip.Addr) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conns == nil {
		return false
	}
	a.conns[c] = ip
	return true
}

func (a *activeShare) untrack(c net.Conn) {
	a.mu.Lock()
	delete(a.conns, c)
	a.mu.Unlock()
}

// kick: izin listesinden dusen kaynaklarin acik baglantilarini keser.
func (a *activeShare) kick() {
	a.mu.Lock()
	var drop []net.Conn
	for c, ip := range a.conns {
		if !a.allowed(ip) {
			drop = append(drop, c)
		}
	}
	a.mu.Unlock()
	for _, c := range drop {
		c.Close()
	}
}

func (s *shareServer) reconcile(outs []protocol.NetShareOut) {
	want := make(map[string]protocol.NetShareOut, len(outs))
	for _, o := range outs {
		want[o.ID] = o
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, a := range s.active {
		o, ok := want[id]
		if !ok || o.Port != a.out.Port || o.Proto != a.out.Proto {
			a.stop()
			delete(s.active, id)
			s.log.Info("paylasim durduruldu", "ad", a.out.Name)
		}
	}
	for id, o := range want {
		if a, ok := s.active[id]; ok {
			a.out = o
			a.allow.Store(allowSet(o.Allow))
			a.kick()
			continue
		}
		a, err := s.start(o)
		if err != nil {
			s.log.Warn("paylasim baslatilamadi", "ad", o.Name, "port", o.Port, "hata", err)
			continue
		}
		s.active[id] = a
		s.log.Info("paylasim aktif", "ad", o.Name, "proto", o.Proto, "port", o.Port)
	}
}

func (s *shareServer) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, a := range s.active {
		a.stop()
		delete(s.active, id)
	}
}

func (s *shareServer) start(o protocol.NetShareOut) (*activeShare, error) {
	a := &activeShare{out: o, conns: map[net.Conn]netip.Addr{}}
	a.allow.Store(allowSet(o.Allow))
	switch o.Proto {
	case "tcp":
		ln, err := s.tnet.ListenTCP(&net.TCPAddr{Port: o.Port})
		if err != nil {
			return nil, err
		}
		a.stop = func() { ln.Close(); a.closeConns() }
		go s.serveTCP(a, ln)
	case "udp":
		pc, err := s.tnet.ListenUDP(&net.UDPAddr{Port: o.Port})
		if err != nil {
			return nil, err
		}
		a.stop = func() { pc.Close(); a.closeConns() }
		go s.serveUDP(a, pc)
	default:
		return nil, fmt.Errorf("bilinmeyen proto %q", o.Proto)
	}
	return a, nil
}

func (a *activeShare) closeConns() {
	a.mu.Lock()
	conns := a.conns
	a.conns = nil
	a.mu.Unlock()
	for c := range conns {
		c.Close()
	}
}

func remoteIP(addr net.Addr) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}

func (s *shareServer) serveTCP(a *activeShare, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			ip, ok := remoteIP(c.RemoteAddr())
			if !ok || !a.allowed(ip) {
				c.Close()
				return
			}
			local, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", a.out.Port), 5*time.Second)
			if err != nil {
				c.Close()
				return
			}
			if !a.track(c, ip) || !a.track(local, ip) {
				c.Close()
				local.Close()
				return
			}
			pipe(c, local)
			a.untrack(c)
			a.untrack(local)
		}()
	}
}

type udpFlow struct {
	local *net.UDPConn
	last  atomic.Int64
}

func (s *shareServer) serveUDP(a *activeShare, pc net.PacketConn) {
	var mu sync.Mutex
	flows := map[string]*udpFlow{}
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		ip, ok := remoteIP(addr)
		if !ok || !a.allowed(ip) {
			continue
		}
		key := addr.String()
		mu.Lock()
		f := flows[key]
		if f == nil {
			lc, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.out.Port})
			if err != nil {
				mu.Unlock()
				continue
			}
			f = &udpFlow{local: lc}
			if !a.track(lc, ip) {
				mu.Unlock()
				lc.Close()
				return
			}
			flows[key] = f
			go func(f *udpFlow, key string, dst net.Addr) {
				rb := make([]byte, 64*1024)
				for {
					_ = f.local.SetReadDeadline(time.Now().Add(udpFlowIdle))
					m, err := f.local.Read(rb)
					if err != nil {
						if ne, ok := err.(net.Error); ok && ne.Timeout() && time.Since(time.Unix(0, f.last.Load())) < udpFlowIdle {
							continue
						}
						break
					}
					if _, err := pc.WriteTo(rb[:m], dst); err != nil {
						break
					}
				}
				f.local.Close()
				a.untrack(f.local)
				mu.Lock()
				delete(flows, key)
				mu.Unlock()
			}(f, key, addr)
		}
		f.last.Store(time.Now().UnixNano())
		mu.Unlock()
		_, _ = f.local.Write(buf[:n])
	}
}

// ResolveShare, "ad[:port]" hedefini erisilebilir paylasimlardan secer.
func ResolveShare(st *protocol.NetState, target string) (protocol.NetShareIn, error) {
	name, port := target, 0
	if h, p, err := net.SplitHostPort(target); err == nil {
		name = h
		if _, err := fmt.Sscanf(p, "%d", &port); err != nil || port < 1 || port > 65535 {
			return protocol.NetShareIn{}, fmt.Errorf("gecersiz port %q", p)
		}
	}
	var hits []protocol.NetShareIn
	for _, s := range st.SharesIn {
		if (s.Name == name || s.OwnerName == name) && (port == 0 || s.Port == port) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return protocol.NetShareIn{}, errors.New("erisilebilir paylasim bulunamadi: " + target)
	case 1:
		return hits[0], nil
	}
	msg := "birden fazla paylasim eslesti, port belirtin (ad:port) veya paylasim adini kullanin:"
	for _, h := range hits {
		msg += fmt.Sprintf(" %s(%s/%d)", h.Name, h.Proto, h.Port)
	}
	return protocol.NetShareIn{}, errors.New(msg)
}
