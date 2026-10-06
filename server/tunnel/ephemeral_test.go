package tunnel

import "testing"

// clampTunnelTTL, istemciden gelen degere guvenmez: 0/negatif kalici tunel
// demektir, arali disi degerler REDDEDILMEZ siniga cekilir.
func TestClampTunnelTTL(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"sifir kalici", 0, 0},
		{"negatif kalici", -5, 0},
		{"alt sinirin altinda yukari cekilir", 1, minTunnelTTLSec},
		{"alt sinir aynen", minTunnelTTLSec, minTunnelTTLSec},
		{"arada aynen", 3600, 3600},
		{"ust sinir aynen", maxTunnelTTLSec, maxTunnelTTLSec},
		{"ust sinirin ustunde asagi cekilir", maxTunnelTTLSec + 1, maxTunnelTTLSec},
		{"absurt buyuk deger asagi cekilir", 1 << 30, maxTunnelTTLSec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampTunnelTTL(c.in); got != c.want {
				t.Errorf("clampTunnelTTL(%d) = %d, beklenen %d", c.in, got, c.want)
			}
		})
	}
}

// Sinirlar REST ucundaki sinirlarla ayni olmali; iki giris yolu tek sozlesme.
func TestTTLLimitsAreSane(t *testing.T) {
	if minTunnelTTLSec != 60 {
		t.Errorf("minTunnelTTLSec = %d, beklenen 60", minTunnelTTLSec)
	}
	if maxTunnelTTLSec != 24*60*60 {
		t.Errorf("maxTunnelTTLSec = %d, beklenen 86400", maxTunnelTTLSec)
	}
}
