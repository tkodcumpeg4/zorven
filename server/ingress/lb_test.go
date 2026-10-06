package ingress

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type fakeView struct {
	off       map[string]bool
	unhealthy map[string]bool
	lat       map[string]time.Duration
	inf       map[string]int64
}

func (f fakeView) online(c string) bool           { return !f.off[c] }
func (f fakeView) healthy(c string) bool          { return !f.unhealthy[c] }
func (f fakeView) latency(c string) time.Duration { return f.lat[c] }
func (f fakeView) inflight(c string) int64        { return f.inf[c] }

var abc = []string{"a", "b", "c"}

func cfg(strategy string, health bool) store.TunnelLB {
	c := store.DefaultTunnelLB("t1")
	c.Strategy = strategy
	c.HealthEnabled = health
	return c
}

func TestRoundRobinRotatesAndSkipsOffline(t *testing.T) {
	v := fakeView{off: map[string]bool{"b": true}}
	if got := orderBackends(abc, cfg(store.LBRoundRobin, false), 0, v); got[0] != "a" || len(got) != 2 {
		t.Errorf("start=0 -> %v", got)
	}
	if got := orderBackends(abc, cfg(store.LBRoundRobin, false), 1, v); got[0] != "c" {
		t.Errorf("start=1 -> %v, c beklenirdi", got)
	}
}

func TestAllOfflineReturnsNil(t *testing.T) {
	v := fakeView{off: map[string]bool{"a": true, "b": true, "c": true}}
	if got := orderBackends(abc, cfg(store.LBRoundRobin, true), 0, v); got != nil {
		t.Errorf("= %v, nil beklenirdi", got)
	}
}

func TestHealthFiltersUnhealthy(t *testing.T) {
	v := fakeView{unhealthy: map[string]bool{"a": true}}
	for s := uint64(0); s < 6; s++ {
		if got := orderBackends(abc, cfg(store.LBRoundRobin, true), s, v); got[0] == "a" {
			t.Fatalf("sagliksiz backend secildi (start=%d): %v", s, got)
		}
	}
}

// Saglik denetimi KAPALIYSA saglik durumu yok sayilir.
func TestHealthIgnoredWhenDisabled(t *testing.T) {
	v := fakeView{unhealthy: map[string]bool{"a": true}}
	if got := orderBackends(abc, cfg(store.LBRoundRobin, false), 0, v); got[0] != "a" {
		t.Errorf("= %v", got)
	}
}

// PANIK MODU: hepsi sagliksizsa trafik kesilmez, cevrimici olanlara gider.
func TestPanicModeWhenAllUnhealthy(t *testing.T) {
	v := fakeView{unhealthy: map[string]bool{"a": true, "b": true, "c": true}}
	if got := orderBackends(abc, cfg(store.LBRoundRobin, true), 0, v); len(got) != 3 {
		t.Errorf("panik modunda = %v, 3 aday beklenirdi", got)
	}
}

func TestLeastConnections(t *testing.T) {
	v := fakeView{inf: map[string]int64{"a": 5, "b": 1, "c": 3}}
	got := orderBackends(abc, cfg(store.LBLeastConnections, false), 0, v)
	if got[0] != "b" || got[1] != "c" || got[2] != "a" {
		t.Errorf("= %v, [b c a] beklenirdi", got)
	}
}

func TestLatencyPrefersFastestAndUnmeasuredLast(t *testing.T) {
	v := fakeView{lat: map[string]time.Duration{"a": 80 * time.Millisecond, "c": 20 * time.Millisecond}}
	got := orderBackends(abc, cfg(store.LBLatency, false), 0, v)
	if got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Errorf("= %v, [c a b] beklenirdi (b olculmedi)", got)
	}
}

// Agirlikli: 3:1 oraninda dagitim.
func TestWeightedDistribution(t *testing.T) {
	c := cfg(store.LBWeighted, false)
	c.Weights = map[string]int{"a": 3, "b": 1}
	count := map[string]int{}
	for s := uint64(0); s < 400; s++ {
		count[orderBackends([]string{"a", "b"}, c, s, fakeView{})[0]]++
	}
	if count["a"] != 300 || count["b"] != 100 {
		t.Errorf("dagilim = %v, a:300 b:100 beklenirdi", count)
	}
}

// Agirlik 0 = trafik almaz, ama son care failover olarak listede kalir.
func TestWeightedZeroIsLastResort(t *testing.T) {
	c := cfg(store.LBWeighted, false)
	c.Weights = map[string]int{"a": 1, "b": 0}
	for s := uint64(0); s < 10; s++ {
		got := orderBackends([]string{"a", "b"}, c, s, fakeView{})
		if got[0] != "a" || got[len(got)-1] != "b" {
			t.Fatalf("= %v", got)
		}
	}
}

// Hepsi 0 ise esit dagitima duser (yanlis ayar trafigi kesmesin).
func TestWeightedAllZeroFallsBack(t *testing.T) {
	c := cfg(store.LBWeighted, false)
	c.Weights = map[string]int{"a": 0, "b": 0}
	if got := orderBackends([]string{"a", "b"}, c, 1, fakeView{}); len(got) != 2 {
		t.Errorf("= %v", got)
	}
}

// Esik davranisi: tek hata durumu degistirmez; esik kadar ardisik hata degistirir.
func TestRecordThresholds(t *testing.T) {
	var s lbState
	c := store.DefaultTunnelLB("t1") // unhealthy 3, healthy 2
	for i := 1; i <= 2; i++ {
		if changed, _ := s.record("t1", "a", false, 0, "timeout", c); changed {
			t.Fatalf("%d. hatada durum degisti", i)
		}
	}
	if changed, healthy := s.record("t1", "a", false, 0, "timeout", c); !changed || healthy {
		t.Fatal("3. hatada sagliksiz olmaliydi")
	}
	if changed, _ := s.record("t1", "a", true, 10*time.Millisecond, "", c); changed {
		t.Fatal("tek basarida saglikliya donmemeliydi")
	}
	if changed, healthy := s.record("t1", "a", true, 10*time.Millisecond, "", c); !changed || !healthy {
		t.Fatal("2. basarida saglikliya donmeliydi")
	}
}

// Araya giren basari hata sayacini sifirlar (dalgalanma sagliksiz yapmaz).
func TestRecordFlapDoesNotTrip(t *testing.T) {
	var s lbState
	c := store.DefaultTunnelLB("t1")
	for i := 0; i < 10; i++ {
		s.record("t1", "a", false, 0, "x", c)
		s.record("t1", "a", false, 0, "x", c)
		s.record("t1", "a", true, time.Millisecond, "", c)
	}
	if s.healthOf("t1", "a").unhealthy.Load() {
		t.Error("dalgalanan backend sagliksiz isaretlendi")
	}
}
