package domain

import (
	"context"
	"os"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func buildResp(t *testing.T, id uint16, aa bool, rcode dnsmessage.RCode, ans []dnsmessage.Resource) []byte {
	t.Helper()
	q, _ := dnsmessage.NewName("app.ornek.com.")
	m := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, Response: true, Authoritative: aa, RCode: rcode},
		Questions: []dnsmessage.Question{{Name: q, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
		Answers:   ans,
	}
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseAuthoritative(t *testing.T) {
	n, _ := dnsmessage.NewName("app.ornek.com.")
	tgt, _ := dnsmessage.NewName("cname.zorven.app.")
	hdr := func(ty dnsmessage.Type) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{Name: n, Type: ty, Class: dnsmessage.ClassINET, TTL: 300}
	}
	// A kaydi
	b := buildResp(t, 7, true, dnsmessage.RCodeSuccess, []dnsmessage.Resource{{Header: hdr(dnsmessage.TypeA), Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 7}}}})
	a, err := parseAuthoritative(b, 7)
	if err != nil || len(a.ips) != 1 || a.ips[0].String() != "203.0.113.7" {
		t.Fatalf("A: %+v %v", a, err)
	}
	// CNAME (bolge disi hedef, A yok)
	b = buildResp(t, 8, true, dnsmessage.RCodeSuccess, []dnsmessage.Resource{{Header: hdr(dnsmessage.TypeCNAME), Body: &dnsmessage.CNAMEResource{CNAME: tgt}}})
	a, err = parseAuthoritative(b, 8)
	if err != nil || a.cname != "cname.zorven.app" || len(a.ips) != 0 {
		t.Fatalf("CNAME: %+v %v", a, err)
	}
	// Kayit yok (NXDOMAIN) -> bos, hata yok
	b = buildResp(t, 9, true, dnsmessage.RCodeNameError, nil)
	if a, err = parseAuthoritative(b, 9); err != nil || len(a.ips) != 0 || a.cname != "" {
		t.Fatalf("NXDOMAIN: %+v %v", a, err)
	}
	// Yetkili olmayan cevap reddedilir (onbellek zehirlenmesi/yonlendirme)
	b = buildResp(t, 10, false, dnsmessage.RCodeSuccess, nil)
	if _, err = parseAuthoritative(b, 10); err == nil {
		t.Fatal("AA bitsiz cevap reddedilmeliydi")
	}
	// Yanlis ID reddedilir
	b = buildResp(t, 11, true, dnsmessage.RCodeSuccess, nil)
	if _, err = parseAuthoritative(b, 12); err == nil {
		t.Fatal("yanlis ID reddedilmeliydi")
	}
	// SERVFAIL reddedilir (normal cozumleyiciye dusulsun)
	b = buildResp(t, 13, true, dnsmessage.RCodeServerFailure, nil)
	if _, err = parseAuthoritative(b, 13); err == nil {
		t.Fatal("SERVFAIL reddedilmeliydi")
	}
}

// Gercek DNS'e karsi (ag gerektirir): ZORVEN_TEST_AUTH_DNS=fqdn=beklenen_ip
func TestLookupAuthoritativeLive(t *testing.T) {
	spec := os.Getenv("ZORVEN_TEST_AUTH_DNS")
	if spec == "" {
		t.Skip("ZORVEN_TEST_AUTH_DNS yok")
	}
	var fqdn, want string
	for i := range spec {
		if spec[i] == '=' {
			fqdn, want = spec[:i], spec[i+1:]
		}
	}
	ips, err := lookupAuthoritativeIPs(context.Background(), fqdn)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range ips {
		if ip.IP.String() == want {
			return
		}
	}
	t.Fatalf("%s yetkili cevap %v, beklenen %s", fqdn, ips, want)
}
