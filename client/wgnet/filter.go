package wgnet

import (
	"net/netip"
	"os"
	"sync"
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/tun"
)

// Filter, Mod B paket filtresidir: tun.Device'i sarar (OS TUN <-> wireguard-go).
//
//	Read  (OS -> tunel, "giden"): kaynak yerel IP olmali; hedefi sanal DNS
//	      cozucusu olan paketler tunele GIRMEZ, yerinde yanitlanir; bilinen bir
//	      ese giden akislar conntrack tablosuna yazilir.
//	Write (tunel -> OS, "gelen"): kaynak bilinen bir es olmali (IP sahteciligi),
//	      hedef bu dugum olmali, parcalanmis paket yok; paket ya yerel baslatilan
//	      bir akisin donusudur (conntrack) ya da sunucunun verdigi bir ACL kuralina
//	      uyar. Aksi halde SESSIZCE DUSER (varlik sizmaz).
//
// Kurallar yalniz sunucudan gelir; yerel olarak yalniz DAHA SIKI yapilabilir
// (SetDenyInbound). Saf mantik inbound/outbound'dadir (tun'a baglanmadan test edilir).
type Filter struct {
	inner tun.Device
	self  netip.Addr
	ct    *conntrack
	dns   *vDNS
	alog  accessAgg // Z5 erisim ozeti

	mu     sync.RWMutex
	rules  []aclRule
	peers  map[netip.Addr]struct{}
	denyIn atomic.Bool

	wmu sync.Mutex // inner.Write serilestirme (DNS yaniti + wireguard-go)

	droppedIn, droppedOut, dnsServed atomic.Int64
}

// tunWriteOffset, filtrenin kendi urettigi paketler icin Write offset'i. Linux
// virtio basligi (10) ve macOS AF basligi (4) icin yeterli.
const tunWriteOffset = 16

// FilterStats, filtre sayaclari (teshis).
type FilterStats struct {
	DroppedIn, DroppedOut, DNSServed int64
	Conntrack                        int
}

// NewFilter, inner'i sarar. self = bu dugumun ic IP'si; resolver = sanal DNS adresi
// (gecersiz ise DNS kapali).
func NewFilter(inner tun.Device, self, resolver netip.Addr, zone string) *Filter {
	f := &Filter{inner: inner, self: self, ct: newConntrack(0, nil), peers: map[netip.Addr]struct{}{}}
	if resolver.IsValid() {
		f.dns = newVDNS(resolver, zone)
	}
	return f
}

// SetRules, sunucunun ACL kurallarini degistirir (varsayilan: bos = hepsini dusur).
func (f *Filter) SetRules(in []protocol.NetACLRule) {
	r := compileACL(in)
	f.mu.Lock()
	f.rules = r
	f.mu.Unlock()
}

// SetPeers, bilinen es IP'lerini (netmap) ayarlar.
func (f *Filter) SetPeers(ips []netip.Addr) {
	m := make(map[netip.Addr]struct{}, len(ips))
	for _, a := range ips {
		m[a] = struct{}{}
	}
	f.mu.Lock()
	f.peers = m
	f.mu.Unlock()
}

// SetDNSNames, sanal DNS ad tablosunu degistirir.
func (f *Filter) SetDNSNames(m map[string]netip.Addr) {
	if f.dns != nil {
		f.dns.setNames(m)
	}
}

// SetDenyInbound true ise hicbir yeni gelen baglanti kabul edilmez (yalniz yerel
// baslatilan akislarin donusu). Yerel daraltma: sunucu kurallari bunu gevsetemez.
func (f *Filter) SetDenyInbound(v bool) { f.denyIn.Store(v) }

// Stats, sayaclari verir.
func (f *Filter) Stats() FilterStats {
	return FilterStats{f.droppedIn.Load(), f.droppedOut.Load(), f.dnsServed.Load(), f.ct.size()}
}

func (f *Filter) isPeer(a netip.Addr) bool {
	f.mu.RLock()
	_, ok := f.peers[a]
	f.mu.RUnlock()
	return ok
}

// outbound: OS'tan tunele giden paket. pass=false ise tunele girmez; replies,
// OS'a geri yazilacak paketlerdir (DNS yanitlari).
func (f *Filter) outbound(pkt []byte) (pass bool, replies [][]byte) {
	p, ok := parseIPv4(pkt)
	if !ok {
		f.droppedOut.Add(1)
		return false, nil
	}
	if f.dns != nil && p.dst == f.dns.resolver && p.src == f.self {
		f.dnsServed.Add(1)
		return false, f.dns.handle(&p, tunWriteOffset)
	}
	if p.src != f.self || !f.isPeer(p.dst) {
		f.droppedOut.Add(1)
		return false, nil
	}
	switch p.proto {
	case protoTCP, protoUDP:
		if !p.frag {
			f.ct.track(ctKey{p.proto, f.self, p.dst, p.srcPort, p.dstPort})
		}
	case protoICMP:
		if !p.frag && p.icmpType == icmpEchoRequest {
			f.ct.track(ctKey{protoICMP, f.self, p.dst, p.icmpID, 0})
		}
	}
	return true, nil
}

// inbound: tunelden OS'a gelen paket kabul edilmeli mi.
func (f *Filter) inbound(pkt []byte) bool {
	p, ok := parseIPv4(pkt)
	if !ok || p.frag || p.dst != f.self || !f.isPeer(p.src) {
		f.droppedIn.Add(1)
		return false
	}
	if f.replyOK(&p) {
		return true
	}
	if !f.denyIn.Load() {
		f.mu.RLock()
		rules := f.rules
		f.mu.RUnlock()
		for i := range rules {
			if rules[i].match(&p) {
				f.alog.record(&p, len(pkt), true, rules[i].id)
				return true
			}
		}
	}
	f.alog.record(&p, len(pkt), false, "")
	f.droppedIn.Add(1)
	return false
}

// replyOK: paket, yerel baslatilan bir akisin donusu mu.
func (f *Filter) replyOK(p *pktInfo) bool {
	switch p.proto {
	case protoTCP, protoUDP:
		return f.ct.seen(ctKey{p.proto, f.self, p.src, p.dstPort, p.srcPort})
	case protoICMP:
		switch p.icmpType {
		case icmpEchoReply:
			return f.ct.seen(ctKey{protoICMP, f.self, p.src, p.icmpID, 0})
		case icmpDestUnreach, icmpTimeExceed, icmpParamProb:
			// Hata mesaji: gomulu orijinal paket bizim giden akisimiza ait olmali.
			if len(p.l4) < 8+20 {
				return false
			}
			q, ok := parseIPv4Prefix(p.l4[8:])
			if !ok || q.src != f.self || q.dst != p.src {
				return false
			}
			switch q.proto {
			case protoTCP, protoUDP:
				return f.ct.seen(ctKey{q.proto, f.self, q.dst, q.srcPort, q.dstPort})
			case protoICMP:
				return f.ct.seen(ctKey{protoICMP, f.self, q.dst, q.icmpID, 0})
			}
		}
	}
	return false
}

// parseIPv4Prefix, ICMP hata mesajlarinda gomulu (kesik) orijinal paketi ayristirir:
// IP basligi + en az 8 bayt tasiyici yeterlidir (toplam uzunluk denetlenmez).
func parseIPv4Prefix(b []byte) (p pktInfo, ok bool) {
	if len(b) < 28 || b[0]>>4 != 4 {
		return p, false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl+8 {
		return p, false
	}
	p.proto = b[9]
	p.src = netip.AddrFrom4([4]byte(b[12:16]))
	p.dst = netip.AddrFrom4([4]byte(b[16:20]))
	l4 := b[ihl:]
	switch p.proto {
	case protoTCP, protoUDP:
		p.srcPort = uint16(l4[0])<<8 | uint16(l4[1])
		p.dstPort = uint16(l4[2])<<8 | uint16(l4[3])
	case protoICMP:
		p.icmpType = l4[0]
		p.icmpID = uint16(l4[4])<<8 | uint16(l4[5])
	}
	return p, true
}

// --- tun.Device ---------------------------------------------------------------------

func (f *Filter) File() *os.File           { return f.inner.File() }
func (f *Filter) MTU() (int, error)        { return f.inner.MTU() }
func (f *Filter) Name() (string, error)    { return f.inner.Name() }
func (f *Filter) Events() <-chan tun.Event { return f.inner.Events() }
func (f *Filter) Close() error             { return f.inner.Close() }
func (f *Filter) BatchSize() int           { return f.inner.BatchSize() }

// Read: OS TUN'dan paketleri okur, filtreden gecenleri toplu olarak dondurur.
func (f *Filter) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		n, err := f.inner.Read(bufs, sizes, offset)
		j := 0
		for i := 0; i < n; i++ {
			pkt := bufs[i][offset : offset+sizes[i]]
			pass, replies := f.outbound(pkt)
			if len(replies) > 0 {
				f.wmu.Lock()
				_, _ = f.inner.Write(replies, tunWriteOffset)
				f.wmu.Unlock()
			}
			if !pass {
				continue
			}
			// Tampon sahipligi wireguard-go'dadir: yer degistirmek yerine KOPYALA.
			if j != i {
				copy(bufs[j][offset:], pkt)
			}
			sizes[j] = sizes[i]
			j++
		}
		if j > 0 || err != nil {
			return j, err
		}
	}
}

// Write: tunelden gelen paketleri filtreler, kabul edilenleri OS TUN'a yazar.
func (f *Filter) Write(bufs [][]byte, offset int) (int, error) {
	kept := make([][]byte, 0, len(bufs))
	for _, b := range bufs {
		if len(b) >= offset && f.inbound(b[offset:]) {
			kept = append(kept, b)
		}
	}
	if len(kept) == 0 {
		return len(bufs), nil
	}
	f.wmu.Lock()
	_, err := f.inner.Write(kept, offset)
	f.wmu.Unlock()
	return len(bufs), err
}

var _ tun.Device = (*Filter)(nil)
