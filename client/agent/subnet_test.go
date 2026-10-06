package agent

import "testing"

// Tekil kaynakta istekteki dest YOK SAYILIR: kullanici ajan uzerinden
// keyfi adres tarayamaz.
func TestResolveStreamAddrSingleIgnoresDest(t *testing.T) {
	got, err := resolveStreamAddr("localhost:5432", "10.0.0.99:22")
	if err != nil || got != "localhost:5432" {
		t.Errorf("= %q, %v; tunel hedefi kullanilmaliydi", got, err)
	}
}

func TestResolveStreamAddrSubnet(t *testing.T) {
	got, err := resolveStreamAddr("subnet:192.168.1.0/24", "192.168.1.10:22")
	if err != nil || got != "192.168.1.10:22" {
		t.Errorf("= %q, %v", got, err)
	}
}

// Alt agda KRITIK kontroller: aralik disi, ad, eksik dest, bozuk port.
func TestResolveStreamAddrSubnetRejects(t *testing.T) {
	cases := map[string]string{
		"aralik disi":      "192.168.2.10:22",
		"ad (DNS kacisi)":  "nas.internal:22",
		"bos dest":         "",
		"portsuz":          "192.168.1.10",
		"port sifir":       "192.168.1.10:0",
		"port asiri":       "192.168.1.10:70000",
		"metadata benzeri": "169.254.169.254:80",
	}
	for name, dest := range cases {
		if _, err := resolveStreamAddr("subnet:192.168.1.0/24", dest); err == nil {
			t.Errorf("%s (%q) reddedilmeliydi", name, dest)
		}
	}
}

// Bozuk alt ag tanimi baglantiya izin vermez.
func TestResolveStreamAddrBadSubnetDefinition(t *testing.T) {
	if _, err := resolveStreamAddr("subnet:bozuk", "192.168.1.10:22"); err == nil {
		t.Error("bozuk alt ag tanimi kabul edildi")
	}
}

// IPv4-mapped IPv6 hedef, IPv4 araligina gore degerlendirilir.
func TestResolveStreamAddrV4Mapped(t *testing.T) {
	got, err := resolveStreamAddr("subnet:10.0.0.0/8", "[::ffff:10.1.2.3]:443")
	if err != nil || got != "10.1.2.3:443" {
		t.Errorf("= %q, %v", got, err)
	}
}
