package wgnet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/net/dns/dnsmessage"
)

// Sanal DNS (Mod B, tasarim 5.4): gercek bir dinleyici soket YOKTUR. Hedefi
// cozucu IP'si (havuzun .1'i) olan UDP/TCP 53 paketleri paket filtresinde
// yakalanir ve netmap'teki ad tablosundan yanitlanir. Yalniz zorven.internal
// bolgesi cevaplanir (bilinmeyen ad NXDOMAIN); baska alanlar OS'a split-DNS
// kaydi ile hic yonlendirilmez — yine de gelirse REFUSED doner.

const (
	dnsTTL = 30
)

type vDNS struct {
	resolver netip.Addr
	zone     string
	names    atomic.Pointer[map[string]netip.Addr]
	secret   [16]byte // TCP SYN cerezi
}

func newVDNS(resolver netip.Addr, zone string) *vDNS {
	d := &vDNS{resolver: resolver, zone: strings.ToLower(strings.Trim(zone, "."))}
	_, _ = rand.Read(d.secret[:])
	empty := map[string]netip.Addr{}
	d.names.Store(&empty)
	return d
}

func (d *vDNS) setNames(m map[string]netip.Addr) { d.names.Store(&m) }

// DNSNames, netmap durumundan ad tablosunu uretir: <ad>.<zone> ve (suffix varsa)
// <ad>.<suffix>.<zone>. Adlar kucuk harftir.
func DNSNames(st *protocol.NetState) map[string]netip.Addr {
	out := map[string]netip.Addr{}
	if st == nil || st.DNS == nil {
		return out
	}
	zone := strings.ToLower(strings.Trim(st.DNS.Zone, "."))
	if zone == "" {
		return out
	}
	suffix := strings.ToLower(strings.Trim(st.DNS.Suffix, "."))
	add := func(name, ip string) {
		name = strings.ToLower(strings.Trim(name, "."))
		a, err := netip.ParseAddr(ip)
		if name == "" || err != nil || !a.Is4() || strings.ContainsAny(name, ". ") {
			return
		}
		out[name+"."+zone] = a
		if suffix != "" {
			out[name+"."+suffix+"."+zone] = a
		}
	}
	add(st.Self.Name, st.Self.IP)
	for _, p := range st.Peers {
		add(p.Name, p.IP)
	}
	// Z5: ozel kaynak adlari (db.internal -> yayinlayan cihazin sanal IP'si). Cihaz
	// adlariyla cakisirsa cihaz kazanir; ad noktali olabilir.
	for _, r := range st.DNS.Records {
		name := strings.ToLower(strings.Trim(r.Name, "."))
		a, err := netip.ParseAddr(r.IP)
		if name == "" || strings.ContainsAny(name, " \t") || err != nil || !a.Is4() || len(name) > 200 {
			continue
		}
		if _, taken := out[name+"."+zone]; !taken {
			out[name+"."+zone] = a
		}
		if suffix != "" {
			if _, taken := out[name+"."+suffix+"."+zone]; !taken {
				out[name+"."+suffix+"."+zone] = a
			}
		}
	}
	return out
}

func (d *vDNS) inZone(name string) bool {
	return name == d.zone || strings.HasSuffix(name, "."+d.zone)
}

// answer, bir DNS sorgusunu (tel bicimi) yanitlar; ayristirilamazsa nil (dusur).
func (d *vDNS) answer(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil || h.Response {
		return nil
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 {
		return nil
	}
	q := qs[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	rc := dnsmessage.RCodeSuccess
	var ans *netip.Addr
	switch {
	case q.Class != dnsmessage.ClassINET && q.Class != dnsmessage.ClassANY:
		rc = dnsmessage.RCodeNotImplemented
	case !d.inZone(name):
		rc = dnsmessage.RCodeRefused
	default:
		if a, ok := (*d.names.Load())[name]; ok {
			if q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeALL {
				ans = &a
			} // diger tipler: NODATA (NOERROR, cevap yok)
		} else {
			rc = dnsmessage.RCodeNameError
		}
	}
	b := dnsmessage.NewBuilder(make([]byte, 0, 128), dnsmessage.Header{
		ID: h.ID, Response: true, Authoritative: d.inZone(name),
		RecursionDesired: h.RecursionDesired, RCode: rc,
	})
	b.EnableCompression()
	if b.StartQuestions() != nil || b.Question(q) != nil {
		return nil
	}
	if ans != nil {
		if b.StartAnswers() != nil {
			return nil
		}
		if b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: dnsTTL},
			dnsmessage.AResource{A: ans.As4()}) != nil {
			return nil
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// handle, hedefi cozucu IP'si olan bir paketi isler ve (varsa) donecek paketleri
// uretir. lead = Write offset'i icin bos pay. Her durumda paket TUNELE GIRMEZ.
func (d *vDNS) handle(p *pktInfo, lead int) [][]byte {
	if p.dstPort != 53 {
		return nil
	}
	switch p.proto {
	case protoUDP:
		if resp := d.answer(p.payload); resp != nil {
			return [][]byte{buildUDP(lead, d.resolver, p.src, 53, p.srcPort, resp)}
		}
	case protoTCP:
		return d.handleTCP(p, lead)
	}
	return nil
}

func (d *vDNS) isn(p *pktInfo, clientISN uint32) uint32 {
	h := sha256.New()
	h.Write(d.secret[:])
	s := p.src.As4()
	h.Write(s[:])
	var t [8]byte
	binary.BigEndian.PutUint16(t[0:2], p.srcPort)
	binary.BigEndian.PutUint16(t[2:4], p.dstPort)
	binary.BigEndian.PutUint32(t[4:8], clientISN)
	h.Write(t[:])
	return binary.BigEndian.Uint32(h.Sum(nil)[:4])
}

// handleTCP: durumsuz minimal DNS-over-TCP. SYN -> SYN/ACK (cerez ISN); ilk veri
// segmenti (uzunluk onekli sorgu) -> yanit + FIN; FIN -> ACK. Yeniden iletimler
// ayni cevabi uretir (cerez deterministik).
func (d *vDNS) handleTCP(p *pktInfo, lead int) [][]byte {
	reply := func(seq, ack uint32, flags byte, payload []byte) [][]byte {
		return [][]byte{buildTCP(lead, d.resolver, p.src, 53, p.srcPort, seq, ack, flags, payload)}
	}
	switch {
	case p.tcpFlags&tcpRST != 0:
		return nil
	case p.tcpFlags&tcpSYN != 0 && p.tcpFlags&tcpACK == 0:
		return reply(d.isn(p, p.tcpSeq), p.tcpSeq+1, tcpSYN|tcpACK, nil)
	case len(p.payload) >= 2:
		isn := d.isn(p, p.tcpSeq-1)
		if p.tcpAck != isn+1 {
			return nil // cerez uymuyor: sahte/eski segment
		}
		n := int(binary.BigEndian.Uint16(p.payload[:2]))
		if n == 0 || len(p.payload) < 2+n {
			return nil
		}
		resp := d.answer(p.payload[2 : 2+n])
		if resp == nil {
			return nil
		}
		body := make([]byte, 2+len(resp))
		binary.BigEndian.PutUint16(body[:2], uint16(len(resp)))
		copy(body[2:], resp)
		return reply(isn+1, p.tcpSeq+uint32(len(p.payload)), tcpPSH|tcpACK|tcpFIN, body)
	case p.tcpFlags&tcpFIN != 0:
		return reply(p.tcpAck, p.tcpSeq+1, tcpACK, nil)
	}
	return nil
}
