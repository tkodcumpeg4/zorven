// Package stun, RFC 5389 "Binding" istek/yanitinin asgari uygulamasidir.
// Zorven sunucusu (yansima adresi sunar) ve istemci (kendi genel adresini
// ogrenir) ayni kodu kullanir. Kimlik dogrulama, FINGERPRINT, TURN yok:
// yalniz Binding. Amac NAT haritasini gozlemlemektir.
package stun

import (
	"crypto/rand"
	"encoding/binary"
	"net/netip"
)

const (
	// HeaderSize: mesaj basligi (tip 2, uzunluk 2, cookie 4, txid 12).
	HeaderSize = 20
	// Cookie: RFC 5389 sihirli degeri.
	Cookie uint32 = 0x2112A442

	TypeBindingRequest  uint16 = 0x0001
	TypeBindingResponse uint16 = 0x0101

	attrMappedAddress    uint16 = 0x0001
	attrXorMappedAddress uint16 = 0x0020

	// MaxRequestSize: sunucunun kabul ettigi en buyuk istek.
	MaxRequestSize = 128
)

// TxID, 96 bitlik islem kimligi.
type TxID [12]byte

// NewTxID, rastgele bir islem kimligi uretir.
func NewTxID() TxID {
	var t TxID
	_, _ = rand.Read(t[:])
	return t
}

// NewBindingRequest, nitelikssiz bir Binding istegi uretir (20 bayt).
func NewBindingRequest(tx TxID) []byte {
	b := make([]byte, HeaderSize)
	binary.BigEndian.PutUint16(b[0:2], TypeBindingRequest)
	binary.BigEndian.PutUint32(b[4:8], Cookie)
	copy(b[8:20], tx[:])
	return b
}

// IsSTUN, paketin STUN mesaji olup olmadigini hizlica soyler. WireGuard
// paketleri (ilk bayt 1-4, sonraki uc bayt 0) ile karismaz: STUN'da 4..8
// baytlar cookie'dir ve uzunluk alani paket boyutuyla eslesir.
func IsSTUN(b []byte) bool {
	if len(b) < HeaderSize || b[0]&0xC0 != 0 || binary.BigEndian.Uint32(b[4:8]) != Cookie {
		return false
	}
	l := int(binary.BigEndian.Uint16(b[2:4]))
	return l%4 == 0 && len(b) == HeaderSize+l
}

// ParseBindingRequest, gecerli bir Binding istegiyse islem kimligini doner.
func ParseBindingRequest(b []byte) (TxID, bool) {
	var tx TxID
	if len(b) > MaxRequestSize || !IsSTUN(b) || binary.BigEndian.Uint16(b[0:2]) != TypeBindingRequest {
		return tx, false
	}
	copy(tx[:], b[8:20])
	return tx, true
}

// NewBindingResponse, src yansima adresini XOR-MAPPED-ADDRESS ile doner.
// Yanit boyutu IPv4 icin 32, IPv6 icin 44 bayttir.
func NewBindingResponse(tx TxID, src netip.AddrPort) []byte {
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	var val []byte
	xport := src.Port() ^ uint16(Cookie>>16)
	if src.Addr().Is4() {
		val = make([]byte, 8)
		val[1] = 0x01
		binary.BigEndian.PutUint16(val[2:4], xport)
		a := src.Addr().As4()
		var c [4]byte
		binary.BigEndian.PutUint32(c[:], Cookie)
		for i := range a {
			val[4+i] = a[i] ^ c[i]
		}
	} else {
		val = make([]byte, 20)
		val[1] = 0x02
		binary.BigEndian.PutUint16(val[2:4], xport)
		a := src.Addr().As16()
		var mask [16]byte
		binary.BigEndian.PutUint32(mask[0:4], Cookie)
		copy(mask[4:], tx[:])
		for i := range a {
			val[4+i] = a[i] ^ mask[i]
		}
	}
	b := make([]byte, HeaderSize+4+len(val))
	binary.BigEndian.PutUint16(b[0:2], TypeBindingResponse)
	binary.BigEndian.PutUint16(b[2:4], uint16(4+len(val)))
	binary.BigEndian.PutUint32(b[4:8], Cookie)
	copy(b[8:20], tx[:])
	binary.BigEndian.PutUint16(b[20:22], attrXorMappedAddress)
	binary.BigEndian.PutUint16(b[22:24], uint16(len(val)))
	copy(b[24:], val)
	return b
}

// ParseBindingResponse, yaniti cozer. want ile islem kimligi eslesmeli.
func ParseBindingResponse(b []byte, want TxID) (netip.AddrPort, bool) {
	if !IsSTUN(b) || binary.BigEndian.Uint16(b[0:2]) != TypeBindingResponse {
		return netip.AddrPort{}, false
	}
	var tx TxID
	copy(tx[:], b[8:20])
	if tx != want {
		return netip.AddrPort{}, false
	}
	attrs := b[HeaderSize:]
	var mapped netip.AddrPort
	var haveMapped bool
	for len(attrs) >= 4 {
		t := binary.BigEndian.Uint16(attrs[0:2])
		l := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+l > len(attrs) {
			return netip.AddrPort{}, false
		}
		v := attrs[4 : 4+l]
		switch t {
		case attrXorMappedAddress:
			if ap, ok := decodeAddr(v, tx, true); ok {
				return ap, true
			}
		case attrMappedAddress:
			if ap, ok := decodeAddr(v, tx, false); ok {
				mapped, haveMapped = ap, true
			}
		}
		adv := 4 + (l+3)&^3
		if adv > len(attrs) {
			break
		}
		attrs = attrs[adv:]
	}
	return mapped, haveMapped
}

func decodeAddr(v []byte, tx TxID, xor bool) (netip.AddrPort, bool) {
	if len(v) < 4 {
		return netip.AddrPort{}, false
	}
	port := binary.BigEndian.Uint16(v[2:4])
	if xor {
		port ^= uint16(Cookie >> 16)
	}
	switch v[1] {
	case 0x01:
		if len(v) != 8 {
			return netip.AddrPort{}, false
		}
		var a [4]byte
		copy(a[:], v[4:8])
		if xor {
			var c [4]byte
			binary.BigEndian.PutUint32(c[:], Cookie)
			for i := range a {
				a[i] ^= c[i]
			}
		}
		return netip.AddrPortFrom(netip.AddrFrom4(a), port), true
	case 0x02:
		if len(v) != 20 {
			return netip.AddrPort{}, false
		}
		var a [16]byte
		copy(a[:], v[4:20])
		if xor {
			var mask [16]byte
			binary.BigEndian.PutUint32(mask[0:4], Cookie)
			copy(mask[4:], tx[:])
			for i := range a {
				a[i] ^= mask[i]
			}
		}
		return netip.AddrPortFrom(netip.AddrFrom16(a), port), true
	}
	return netip.AddrPort{}, false
}
