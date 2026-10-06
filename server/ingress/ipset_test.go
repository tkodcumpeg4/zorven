package ingress

import (
	"net/netip"
	"strings"
	"testing"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("adres cozulemedi %q: %v", s, err)
	}
	return a
}

func TestParseIPSetFormats(t *testing.T) {
	src := `
# yorum satiri
; baska bir yorum

185.220.101.5
10.0.0.0/8
2001:db8::/32
203.0.113.7  # satir sonu aciklamasi
198.51.100.9	tab ile aciklama
bu-satir-bozuk
`
	set := parseIPSet(strings.NewReader(src))
	if got := set.size(); got != 5 {
		t.Fatalf("prefix sayisi = %d, beklenen 5", got)
	}

	in := []string{"185.220.101.5", "10.1.2.3", "2001:db8::1", "203.0.113.7", "198.51.100.9"}
	for _, ip := range in {
		if !set.contains(mustAddr(t, ip)) {
			t.Errorf("%s kumede bulunmali", ip)
		}
	}
	out := []string{"185.220.101.6", "11.0.0.1", "2001:db9::1", "203.0.113.8"}
	for _, ip := range out {
		if set.contains(mustAddr(t, ip)) {
			t.Errorf("%s kumede OLMAMALI", ip)
		}
	}
}

func TestIPSetEmptyAndNil(t *testing.T) {
	var nilSet *ipSet
	if nilSet.contains(mustAddr(t, "1.2.3.4")) {
		t.Error("nil kume hicbir seyi icermemeli")
	}
	if nilSet.size() != 0 {
		t.Error("nil kume boyutu 0 olmali")
	}
	empty := parseIPSet(strings.NewReader("# sadece yorum\n\n"))
	if empty.size() != 0 {
		t.Errorf("bos kume boyutu = %d, beklenen 0", empty.size())
	}
	if empty.contains(mustAddr(t, "1.2.3.4")) {
		t.Error("bos kume hicbir seyi icermemeli")
	}
}

// IPv4-mapped IPv6 adresleri (::ffff:a.b.c.d) IPv4 kovasinda aranmali.
func TestIPSetUnmapsV4MappedAddr(t *testing.T) {
	set := parseIPSet(strings.NewReader("203.0.113.0/24\n"))
	if !set.contains(mustAddr(t, "::ffff:203.0.113.5")) {
		t.Error("v4-mapped adres IPv4 prefix'ine eslesmeli")
	}
}

// Ayni prefix iki kez gecerse tek kayit gibi davranmali degil — sayac artsa
// bile arama dogru calismali. Burada asil kilitlenen sey: farkli uzunluklarin
// bir arada dogru aranmasi.
func TestIPSetMixedPrefixLengths(t *testing.T) {
	set := parseIPSet(strings.NewReader("10.0.0.0/8\n10.1.2.3/32\n192.168.1.0/24\n"))
	for _, ip := range []string{"10.255.255.255", "10.1.2.3", "192.168.1.1"} {
		if !set.contains(mustAddr(t, ip)) {
			t.Errorf("%s bulunmali", ip)
		}
	}
	if set.contains(mustAddr(t, "192.168.2.1")) {
		t.Error("192.168.2.1 bulunmamali")
	}
}
