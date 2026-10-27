package wgnet

import (
	"encoding/binary"
	"net/netip"
)

// W3 / Mod B: IP paketi ayristirma ve olusturma yardimcilari (paket filtresi,
// sanal DNS). Yalniz IPv4 (v1); IPv6 paketleri filtrede dusurulur.

const (
	protoICMP uint8 = 1
	protoTCP  uint8 = 6
	protoUDP  uint8 = 17

	icmpEchoReply   uint8 = 0
	icmpDestUnreach uint8 = 3
	icmpEchoRequest uint8 = 8
	icmpTimeExceed  uint8 = 11
	icmpParamProb   uint8 = 12

	tcpFIN byte = 0x01
	tcpSYN byte = 0x02
	tcpRST byte = 0x04
	tcpPSH byte = 0x08
	tcpACK byte = 0x10
)

// pktInfo, bir IPv4 paketinin filtre icin gereken alanlari.
type pktInfo struct {
	proto            uint8
	src, dst         netip.Addr
	srcPort, dstPort uint16
	hdrLen           int // IP basligi uzunlugu
	total            int // IP toplam uzunlugu
	frag             bool
	tcpFlags         byte
	tcpSeq, tcpAck   uint32
	icmpType         uint8
	icmpCode         uint8
	icmpID           uint16 // echo request/reply
	payload          []byte // tasiyici (TCP/UDP) yuku; yoksa nil
	l4               []byte // IP yukunun tamami
}

// parseIPv4 paketi ayristirir; bozuk/kesik/IPv6 ise ok=false. Parcalanmis
// (MF veya ofset != 0) paketlerde frag=true ve port bilgisi YOKTUR.
func parseIPv4(b []byte) (p pktInfo, ok bool) {
	if len(b) < 20 || b[0]>>4 != 4 {
		return p, false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl {
		return p, false
	}
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if total < ihl || total > len(b) {
		return p, false
	}
	b = b[:total]
	p.hdrLen, p.total = ihl, total
	p.proto = b[9]
	p.src = netip.AddrFrom4([4]byte(b[12:16]))
	p.dst = netip.AddrFrom4([4]byte(b[16:20]))
	fo := binary.BigEndian.Uint16(b[6:8])
	if fo&0x3fff != 0 { // MF veya parca ofseti
		p.frag = true
		return p, true
	}
	l4 := b[ihl:]
	p.l4 = l4
	switch p.proto {
	case protoTCP:
		if len(l4) < 20 {
			return p, false
		}
		doff := int(l4[12]>>4) * 4
		if doff < 20 || doff > len(l4) {
			return p, false
		}
		p.srcPort = binary.BigEndian.Uint16(l4[0:2])
		p.dstPort = binary.BigEndian.Uint16(l4[2:4])
		p.tcpSeq = binary.BigEndian.Uint32(l4[4:8])
		p.tcpAck = binary.BigEndian.Uint32(l4[8:12])
		p.tcpFlags = l4[13]
		p.payload = l4[doff:]
	case protoUDP:
		if len(l4) < 8 {
			return p, false
		}
		p.srcPort = binary.BigEndian.Uint16(l4[0:2])
		p.dstPort = binary.BigEndian.Uint16(l4[2:4])
		ul := int(binary.BigEndian.Uint16(l4[4:6]))
		if ul < 8 || ul > len(l4) {
			return p, false
		}
		p.payload = l4[8:ul]
	case protoICMP:
		if len(l4) < 8 {
			return p, false
		}
		p.icmpType, p.icmpCode = l4[0], l4[1]
		if p.icmpType == icmpEchoRequest || p.icmpType == icmpEchoReply {
			p.icmpID = binary.BigEndian.Uint16(l4[4:6])
		}
	}
	return p, true
}

// csum16, RFC 1071 ek toplami (baslangic degeriyle).
func csum16(init uint32, b []byte) uint16 {
	sum := init
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func pseudoSum(src, dst netip.Addr, proto uint8, l int) uint32 {
	s, d := src.As4(), dst.As4()
	var sum uint32
	sum += uint32(binary.BigEndian.Uint16(s[0:2])) + uint32(binary.BigEndian.Uint16(s[2:4]))
	sum += uint32(binary.BigEndian.Uint16(d[0:2])) + uint32(binary.BigEndian.Uint16(d[2:4]))
	sum += uint32(proto) + uint32(l)
	return sum
}

// buildIPv4, lead bayt bos pay birakarak (tun Write offset'i) IPv4 paketi uretir.
// Donen dilim: buf[lead:] paket. Baslik sum'u hesaplanir.
func buildIPv4(lead int, src, dst netip.Addr, proto uint8, l4 []byte) []byte {
	total := 20 + len(l4)
	buf := make([]byte, lead+total)
	b := buf[lead:]
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(total))
	b[6] = 0x40 // DF
	b[8] = 64
	b[9] = proto
	s, d := src.As4(), dst.As4()
	copy(b[12:16], s[:])
	copy(b[16:20], d[:])
	binary.BigEndian.PutUint16(b[10:12], csum16(0, b[:20]))
	copy(b[20:], l4)
	return buf
}

// buildUDP: UDP datagrami (IPv4 UDP sum'u sifir = yok; gecerlidir).
func buildUDP(lead int, src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	l4 := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(l4[0:2], sport)
	binary.BigEndian.PutUint16(l4[2:4], dport)
	binary.BigEndian.PutUint16(l4[4:6], uint16(len(l4)))
	copy(l4[8:], payload)
	return buildIPv4(lead, src, dst, protoUDP, l4)
}

// buildTCP: secenek yok, 20 bayt baslik.
func buildTCP(lead int, src, dst netip.Addr, sport, dport uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	l4 := make([]byte, 20+len(payload))
	binary.BigEndian.PutUint16(l4[0:2], sport)
	binary.BigEndian.PutUint16(l4[2:4], dport)
	binary.BigEndian.PutUint32(l4[4:8], seq)
	binary.BigEndian.PutUint32(l4[8:12], ack)
	l4[12] = 5 << 4
	l4[13] = flags
	binary.BigEndian.PutUint16(l4[14:16], 8192)
	copy(l4[20:], payload)
	binary.BigEndian.PutUint16(l4[16:18], csum16(pseudoSum(src, dst, protoTCP, len(l4)), l4))
	return buildIPv4(lead, src, dst, protoTCP, l4)
}
