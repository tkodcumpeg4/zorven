package wgnet

import (
	"net/netip"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/net/dns/dnsmessage"
)

func findEntry(es []protocol.NetAccessEntry, src string, port int, dec string) *protocol.NetAccessEntry {
	for i := range es {
		if es[i].SrcIP == src && es[i].Port == port && es[i].Decision == dec {
			return &es[i]
		}
	}
	return nil
}

func TestAccessLogAggregatesAllowAndDeny(t *testing.T) {
	f := newTestFilter(t)
	f.SetRules([]protocol.NetACLRule{{ID: "pol:p1:res:db.internal", Src: []string{peerA.String()}, Proto: "tcp", Ports: "5432"}})
	// Kapaliyken biriktirme yok.
	f.inbound(tcpPkt(peerA, selfIP, 40000, 5432, tcpSYN))
	if es, _ := f.DrainAccessLog(); len(es) != 0 {
		t.Fatal("kapaliyken kayit tutulmamali")
	}
	f.SetAccessLog(true)
	for i := 0; i < 3; i++ {
		if !f.inbound(tcpPkt(peerA, selfIP, 40000, 5432, tcpSYN)) {
			t.Fatal("izinli paket gecmeli")
		}
	}
	f.inbound(tcpPkt(peerA, selfIP, 40001, 5432, tcpACK))
	if f.inbound(tcpPkt(peerB, selfIP, 40002, 22, tcpSYN)) {
		t.Fatal("izinsiz paket dusmeli")
	}
	es, trunc := f.DrainAccessLog()
	if trunc != 0 || len(es) != 2 {
		t.Fatalf("beklenen 2 girdi: %+v (trunc=%d)", es, trunc)
	}
	a := findEntry(es, peerA.String(), 5432, "allow")
	if a == nil || a.Rule != "pol:p1:res:db.internal" || a.Packets != 4 || a.Flows != 3 || a.Proto != "tcp" || a.Bytes <= 0 {
		t.Fatalf("allow ozeti: %+v", a)
	}
	d := findEntry(es, peerB.String(), 22, "deny")
	if d == nil || d.Packets != 1 || d.Flows != 1 || d.Rule != "" {
		t.Fatalf("deny ozeti: %+v", d)
	}
	// Bosaltma sifirlar.
	if es, _ = f.DrainAccessLog(); len(es) != 0 {
		t.Fatal("drain sonrasi bos olmali")
	}
}

// Bellek siniri: pencere basina sinirli farkli anahtar; fazlasi truncated sayacina gider.
func TestAccessLogBoundedKeys(t *testing.T) {
	var a accessAgg
	a.on.Store(true)
	for i := 0; i < alogMaxKeys+300; i++ {
		p := &pktInfo{src: peerA, proto: protoTCP, dstPort: uint16(1 + i%60000), tcpFlags: tcpSYN}
		p.dstPort = uint16(1000 + i)
		a.record(p, 60, false, "")
	}
	es, trunc := a.drain()
	if len(es) != protocol.MaxAccessLogEntries {
		t.Fatalf("mesaj basina sinir uygulanmadi: %d", len(es))
	}
	if want := alogMaxKeys + 300 - protocol.MaxAccessLogEntries; trunc != want {
		t.Fatalf("truncated=%d, beklenen %d", trunc, want)
	}
}

func TestAccessLogNoPayloadFields(t *testing.T) {
	// Girdi tipi yalniz ust veri alanlari tasir (icerik alani yok): derleme zamani belgesi.
	e := protocol.NetAccessEntry{SrcIP: "1.1.1.1", Proto: "udp", Port: 53, Decision: "allow", Flows: 1, Packets: 1, Bytes: 80}
	if e.Bytes != 80 {
		t.Fatal()
	}
}

func TestDNSNamesPrivateResourceRecords(t *testing.T) {
	st := protocol.NewNetState()
	st.Self = protocol.NetPeer{Name: "laptop", IP: "100.64.0.10"}
	st.Peers["k"] = protocol.NetPeer{WGPub: "k", Name: "nas", IP: "100.64.0.20"}
	st.DNS = &protocol.NetDNS{Resolver: "100.64.0.1", Zone: "zorven.internal", Suffix: "acme", Records: []protocol.NetDNSRecord{
		{Name: "db.internal", IP: "100.64.0.20"},
		{Name: "NAS", IP: "100.64.0.99"}, // cihaz adiyla cakisir: cihaz kazanir
		{Name: "bad name", IP: "100.64.0.21"},
		{Name: "v6", IP: "::1"},
		{Name: "x", IP: "nonsense"},
	}}
	m := DNSNames(st)
	want := map[string]string{
		"db.internal.zorven.internal":      "100.64.0.20",
		"db.internal.acme.zorven.internal": "100.64.0.20",
		"nas.zorven.internal":              "100.64.0.20",
	}
	for k, v := range want {
		if got := m[k]; got != netip.MustParseAddr(v) {
			t.Errorf("%s -> %v, beklenen %s", k, got, v)
		}
	}
	for _, k := range []string{"bad name.zorven.internal", "v6.zorven.internal", "x.zorven.internal"} {
		if _, ok := m[k]; ok {
			t.Errorf("gecersiz kayit eklendi: %s", k)
		}
	}
}

func TestVirtualDNSAnswersPrivateResource(t *testing.T) {
	f, _ := newDNSFilter(t)
	st := protocol.NewNetState()
	st.DNS = &protocol.NetDNS{Resolver: resolv.String(), Zone: "zorven.internal", Records: []protocol.NetDNSRecord{{Name: "db.internal", IP: peerA.String()}}}
	f.SetDNSNames(DNSNames(st))
	resp := f.dns.answer(dnsQuery(t, "db.internal.zorven.internal.", dnsmessage.TypeA))
	_, ans := parseDNS(t, resp)
	if len(ans) != 1 {
		t.Fatalf("db.internal cozulmeli: %d cevap", len(ans))
	}
}
