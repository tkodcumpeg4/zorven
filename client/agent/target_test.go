package agent

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Sunucu hedef bildirmedi ve kullanici hedef vermediyse bos doner; bos hedef
// hicbir yerel porta yonlenmez.
func TestTargetForNoFallbackWithoutExplicitTarget(t *testing.T) {
	cs := newClientSession(nil, "", nil)
	if got := cs.targetFor("tun_yok"); got != "" {
		t.Fatalf("hedefsiz oturumda targetFor = %q, bos olmali", got)
	}
	cs.SetTargets([]protocol.TunnelSpec{{ID: "tun_1", Target: "http://localhost:9000/"}})
	if got := cs.targetFor("tun_1"); got != "http://localhost:9000" {
		t.Fatalf("sunucu hedefi = %q", got)
	}
	if got := cs.targetFor("tun_2"); got != "" {
		t.Fatalf("bilinmeyen tunel = %q, bos olmali", got)
	}
}

// config_update tunel silince eski hedef artik cozulmez.
func TestSetTargetsReplacesMap(t *testing.T) {
	cs := newClientSession(nil, "", nil)
	cs.SetTargets([]protocol.TunnelSpec{{ID: "a", Target: "http://localhost:1"}})
	cs.SetTargets([]protocol.TunnelSpec{{ID: "b", Target: "http://localhost:2"}})
	if cs.targetFor("a") != "" || cs.targetFor("b") != "http://localhost:2" {
		t.Fatalf("a=%q b=%q", cs.targetFor("a"), cs.targetFor("b"))
	}
}

func TestIdleTimeout(t *testing.T) {
	if got := idleTimeout(0); got != 90*time.Second {
		t.Errorf("0 -> %v", got)
	}
	if got := idleTimeout(30); got != 90*time.Second {
		t.Errorf("30 -> %v", got)
	}
	if got := idleTimeout(60); got != 180*time.Second {
		t.Errorf("60 -> %v", got)
	}
}

func TestBackoffShouldReset(t *testing.T) {
	if backoffShouldReset(2 * time.Second) {
		t.Error("kisa baglanti backoff'u sifirlamamali")
	}
	if !backoffShouldReset(time.Minute) {
		t.Error("uzun baglanti backoff'u sifirlamali")
	}
}
