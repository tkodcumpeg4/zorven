package ingress

// Test yardimcisi: kucuk bir MaxMind DB (mmdb) yazicisi. Dis bagimlilik gerektirmez;
// yalniz IPv4/IPv6 on-eklerini {country.iso_code, continent.code} kaydina esler.

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"
	"testing"
)

type mmdbNode struct {
	child [2]int // >= 0: dugum indeksi; -1: bos; <= -2: veri (-(off+2))
}

type mmdbWriter struct {
	nodes []mmdbNode
	data  bytes.Buffer
}

func newMMDBWriter() *mmdbWriter {
	w := &mmdbWriter{}
	w.nodes = append(w.nodes, mmdbNode{child: [2]int{-1, -1}})
	return w
}

func mmdbCtl(buf *bytes.Buffer, typ, size int) {
	buf.WriteByte(byte(typ<<5 | size))
}

func mmdbString(buf *bytes.Buffer, s string) {
	mmdbCtl(buf, 2, len(s))
	buf.WriteString(s)
}

// addRecord, {country:{iso_code}, continent:{code}} kaydini veri bolumune ekler.
func (w *mmdbWriter) addRecord(country, continent string) int {
	off := w.data.Len()
	mmdbCtl(&w.data, 7, 2)
	mmdbString(&w.data, "country")
	mmdbCtl(&w.data, 7, 1)
	mmdbString(&w.data, "iso_code")
	mmdbString(&w.data, country)
	mmdbString(&w.data, "continent")
	mmdbCtl(&w.data, 7, 1)
	mmdbString(&w.data, "code")
	mmdbString(&w.data, continent)
	return off
}

// insert, on-eki (IPv4 ise ::/96 altina gomulur) verilen veri ofsetine esler.
func (w *mmdbWriter) insert(p netip.Prefix, off int) {
	addr := p.Addr()
	bits := p.Bits()
	var raw [16]byte
	if addr.Is4() {
		a := addr.As4()
		copy(raw[12:], a[:])
		bits += 96
	} else {
		raw = addr.As16()
	}
	cur := 0
	for i := 0; i < bits; i++ {
		bit := int(raw[i/8]>>(7-uint(i%8))) & 1
		if i == bits-1 {
			w.nodes[cur].child[bit] = -(off + 2)
			return
		}
		next := w.nodes[cur].child[bit]
		if next < 0 {
			w.nodes = append(w.nodes, mmdbNode{child: [2]int{-1, -1}})
			next = len(w.nodes) - 1
			w.nodes[cur].child[bit] = next
		}
		cur = next
	}
}

func (w *mmdbWriter) bytes() []byte {
	var out bytes.Buffer
	n := len(w.nodes)
	rec := func(c int) uint32 {
		switch {
		case c >= 0:
			return uint32(c)
		case c == -1:
			return uint32(n)
		default:
			return uint32(n) + 16 + uint32(-c-2)
		}
	}
	for _, nd := range w.nodes {
		for _, c := range nd.child {
			v := rec(c)
			out.Write([]byte{byte(v >> 16), byte(v >> 8), byte(v)})
		}
	}
	out.Write(make([]byte, 16))
	out.Write(w.data.Bytes())
	out.WriteString("\xAB\xCD\xEFMaxMind.com")
	// metadata
	u32 := func(buf *bytes.Buffer, v uint32) {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], v)
		mmdbCtl(buf, 6, 4)
		buf.Write(b[:])
	}
	u16 := func(buf *bytes.Buffer, v uint16) {
		mmdbCtl(buf, 5, 2)
		buf.Write([]byte{byte(v >> 8), byte(v)})
	}
	mmdbCtl(&out, 7, 6)
	mmdbString(&out, "node_count")
	u32(&out, uint32(n))
	mmdbString(&out, "record_size")
	u16(&out, 24)
	mmdbString(&out, "ip_version")
	u16(&out, 6)
	mmdbString(&out, "binary_format_major_version")
	u16(&out, 2)
	mmdbString(&out, "binary_format_minor_version")
	u16(&out, 0)
	mmdbString(&out, "database_type")
	mmdbString(&out, "Test-Country")
	return out.Bytes()
}

// writeTestMMDB, {on-ek: "ULKE:KITA"} haritasindan mmdb dosyasi yazar.
func writeTestMMDB(t testing.TB, path string, entries map[string]string) {
	t.Helper()
	w := newMMDBWriter()
	for pfx, cc := range entries {
		p := netip.MustParsePrefix(pfx)
		country, continent := cc, "XX"
		for i := 0; i < len(cc); i++ {
			if cc[i] == ':' {
				country, continent = cc[:i], cc[i+1:]
			}
		}
		w.insert(p, w.addRecord(country, continent))
	}
	if err := os.WriteFile(path, w.bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
