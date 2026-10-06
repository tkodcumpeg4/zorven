package api

import (
	"net/netip"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestValidPublishableSubnet(t *testing.T) {
	ok := map[string]string{
		"192.168.1.0/24":  "192.168.1.0/24",
		"192.168.1.77/24": "192.168.1.0/24", // kanoniklestirilir
		"10.0.0.0/8":      "10.0.0.0/8",
		"172.20.0.0/16":   "172.20.0.0/16",
		"100.64.1.0/24":   "100.64.1.0/24",
		"fd00:1234::/48":  "fd00:1234::/48",
	}
	for in, want := range ok {
		p, e := validPublishableSubnet(in)
		if e != "" || p.String() != want {
			t.Errorf("%q = %q, %q; beklenen %q", in, p, e, want)
		}
	}
	// KRITIK: genel internet araliklari yayinlanamaz (acik cikis proxy'si olurdu).
	bad := []string{
		"0.0.0.0/0", "8.8.8.0/24", "172.32.0.0/16", "192.168.0.0/15",
		"10.0.0.0/7", "::/0", "2001:db8::/32", "fd00::/16", "bozuk", "",
	}
	for _, in := range bad {
		if _, e := validPublishableSubnet(in); e == "" {
			t.Errorf("%q kabul edilmemeliydi", in)
		}
	}
}

func sub(id, cidr string) store.Tunnel {
	return store.Tunnel{ID: id, Target: subnetTargetPrefix + cidr}
}

// Cakisan araliklarda EN SPESIFIK olan secilir.
func TestPickSubnetMostSpecific(t *testing.T) {
	tuns := []store.Tunnel{sub("genis", "10.0.0.0/8"), sub("dar", "10.1.2.0/24"), {ID: "tekil", Target: "localhost:22"}}
	got, ok := pickSubnet(netip.MustParseAddr("10.1.2.3"), tuns)
	if !ok || got.ID != "dar" {
		t.Errorf("= %q, en spesifik (dar) beklenirdi", got.ID)
	}
	got, ok = pickSubnet(netip.MustParseAddr("10.9.9.9"), tuns)
	if !ok || got.ID != "genis" {
		t.Errorf("= %q, genis beklenirdi", got.ID)
	}
	if _, ok := pickSubnet(netip.MustParseAddr("192.168.1.1"), tuns); ok {
		t.Error("hicbir araliga uymayan IP eslesti")
	}
	if got, ok := pickSubnet(netip.MustParseAddr("::ffff:10.1.2.3"), tuns); !ok || got.ID != "dar" {
		t.Errorf("v4-mapped = %q", got.ID)
	}
}

func TestParseConnectTargetAcceptsIP(t *testing.T) {
	for _, in := range []string{"192.168.1.10:22", "[fd00::1]:443"} {
		if _, _, ok := parseConnectTarget(in); !ok {
			t.Errorf("%q kabul edilmeliydi", in)
		}
	}
}

func TestSubnetOf(t *testing.T) {
	if _, ok := subnetOf("localhost:5432"); ok {
		t.Error("tekil hedef alt ag sanildi")
	}
	if p, ok := subnetOf("subnet:192.168.1.0/24"); !ok || p.String() != "192.168.1.0/24" {
		t.Errorf("= %v %v", p, ok)
	}
	if _, ok := subnetOf("subnet:bozuk"); ok {
		t.Error("bozuk alt ag kabul edildi")
	}
}
