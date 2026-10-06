package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestValidateTunnelLB(t *testing.T) {
	cands := []string{"c1", "c2"}
	base := func() store.TunnelLB {
		lb := store.DefaultTunnelLB("t1")
		lb.HealthEnabled = true
		return lb
	}
	if e := validateTunnelLB(base(), cands); e != "" {
		t.Fatalf("varsayilan gecerli olmali: %s", e)
	}
	cases := map[string]func(*store.TunnelLB){
		"kotu strateji":     func(l *store.TunnelLB) { l.Strategy = "random" },
		"yabanci istemci":   func(l *store.TunnelLB) { l.Weights = map[string]int{"cx": 1} },
		"negatif agirlik":   func(l *store.TunnelLB) { l.Weights = map[string]int{"c1": -1} },
		"cok buyuk agirlik": func(l *store.TunnelLB) { l.Weights = map[string]int{"c1": 5000} },
		"yol slash yok":     func(l *store.TunnelLB) { l.HealthPath = "health" },
		"yolda bosluk":      func(l *store.TunnelLB) { l.HealthPath = "/a b" },
		"aralik kucuk":      func(l *store.TunnelLB) { l.IntervalSec = 1 },
		"zaman asimi buyuk": func(l *store.TunnelLB) { l.TimeoutSec = 31 },
		"timeout>=interval": func(l *store.TunnelLB) { l.IntervalSec = 3; l.TimeoutSec = 3 },
		"esik sifir":        func(l *store.TunnelLB) { l.UnhealthyThreshold = 0 },
		"esik buyuk":        func(l *store.TunnelLB) { l.HealthyThreshold = 11 },
	}
	for name, mut := range cases {
		lb := base()
		mut(&lb)
		if validateTunnelLB(lb, cands) == "" {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
	// Saglik kapaliyken saglik alanlari denetlenmez.
	lb := store.DefaultTunnelLB("t1")
	lb.IntervalSec = 0
	lb.Strategy = store.LBWeighted
	lb.Weights = map[string]int{"c1": 3, "c2": 0}
	if e := validateTunnelLB(lb, cands); e != "" {
		t.Fatalf("saglik kapali + gecerli agirlik kabul edilmeli: %s", e)
	}
}
