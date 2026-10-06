package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestValidateTunnelUDP(t *testing.T) {
	if e := validateTunnelUDP(store.DefaultTunnelUDP("t1")); e != "" {
		t.Fatalf("varsayilan gecerli olmali: %s", e)
	}
	cases := map[string]func(*store.TunnelUDP){
		"idle kucuk":     func(u *store.TunnelUDP) { u.IdleTimeoutSec = 5 },
		"idle buyuk":     func(u *store.TunnelUDP) { u.IdleTimeoutSec = 3601 },
		"paket kucuk":    func(u *store.TunnelUDP) { u.MaxPacketBytes = 10 },
		"paket buyuk":    func(u *store.TunnelUDP) { u.MaxPacketBytes = 70000 },
		"negatif pps":    func(u *store.TunnelUDP) { u.MaxPPS = -1 },
		"flow pps buyuk": func(u *store.TunnelUDP) { u.MaxFlowPPS = 2_000_000 },
		"flow sifir":     func(u *store.TunnelUDP) { u.MaxFlows = 0 },
		"flow cok buyuk": func(u *store.TunnelUDP) { u.MaxFlows = 100_001 },
	}
	for name, mut := range cases {
		u := store.DefaultTunnelUDP("t1")
		mut(&u)
		if validateTunnelUDP(u) == "" {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
	u := store.TunnelUDP{TunnelID: "t1", IdleTimeoutSec: 30, MaxPacketBytes: 1400, MaxPPS: 5000, MaxFlowPPS: 200, MaxFlows: 64}
	if e := validateTunnelUDP(u); e != "" {
		t.Fatalf("oyun sunucusu tipik ayari gecerli olmali: %s", e)
	}
}
