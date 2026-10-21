package tunnel

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestPercentileNearestRank(t *testing.T) {
	var s []time.Duration
	for i := 1; i <= 100; i++ {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	if got := percentileMs(s, 50); got != 50 {
		t.Fatalf("p50=%v", got)
	}
	if got := percentileMs(s, 95); got != 95 {
		t.Fatalf("p95=%v", got)
	}
	if got := percentileMs([]time.Duration{7 * time.Millisecond}, 95); got != 7 {
		t.Fatalf("tek ornek p95=%v", got)
	}
	if got := percentileMs(nil, 50); got != 0 {
		t.Fatalf("bos=%v", got)
	}
}

func TestProbeRTTAndEWMA(t *testing.T) {
	tr := NewProbeTracker(4 * time.Second)
	t0 := time.Unix(1000, 0)
	rtts := []time.Duration{10, 20, 30, 40, 100}
	for i, r := range rtts {
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		seq := tr.Sent("t1", now)
		if _, ok := tr.Echo("t1", seq, now.Add(r*time.Millisecond)); !ok {
			t.Fatalf("yanki kabul edilmeli (%d)", i)
		}
	}
	w, ok := tr.Drain("t1", t0.Add(time.Minute))
	if !ok || w.Sent != 5 || w.Received != 5 || w.Lost != 0 || !w.HasRTT {
		t.Fatalf("pencere yanlis: %+v", w)
	}
	if w.P50Ms != 30 || w.P95Ms != 100 {
		t.Fatalf("p50/p95 yanlis: %v %v", w.P50Ms, w.P95Ms)
	}
	// EWMA elle: 10 -> 0.2*20+0.8*10=12 -> 15.6 -> 20.48 -> 36.384
	if math.Abs(w.EWMAMs-36.384) > 1e-6 {
		t.Fatalf("ewma=%v", w.EWMAMs)
	}
	if l, ok := w.LossPct(); !ok || l != 0 {
		t.Fatalf("kayip=%v %v", l, ok)
	}
	// Pencere sifirlandi; EWMA surer.
	if v, ok := tr.EWMA("t1"); !ok || math.Abs(v-36.384) > 1e-6 {
		t.Fatalf("ewma kalici olmali: %v", v)
	}
	if _, ok := tr.Drain("t1", t0.Add(2*time.Minute)); ok {
		t.Fatal("bos pencere ok=false olmali")
	}
}

func TestProbeLossTimeout(t *testing.T) {
	tr := NewProbeTracker(4 * time.Second)
	t0 := time.Unix(2000, 0)
	// 4 yoklama: 1'i yanitlanir, 2'si zaman asimina ugrar, 1'i henuz bekliyor.
	s1 := tr.Sent("t", t0)
	if _, ok := tr.Echo("t", s1, t0.Add(20*time.Millisecond)); !ok {
		t.Fatal("yanki bekleniyordu")
	}
	tr.Sent("t", t0.Add(5*time.Second))
	tr.Sent("t", t0.Add(10*time.Second))
	s4 := tr.Sent("t", t0.Add(58*time.Second))
	_ = s4
	w, ok := tr.Drain("t", t0.Add(60*time.Second))
	if !ok || w.Sent != 4 || w.Received != 1 || w.Lost != 2 {
		t.Fatalf("pencere: %+v", w)
	}
	l, ok := w.LossPct()
	if !ok || math.Abs(l-200.0/3.0) > 1e-9 {
		t.Fatalf("kayip%%=%v", l)
	}
	// Bekleyen (s4) sonraki pencerede zaman asimina ugrayinca kayip olur.
	w2, _ := tr.Drain("t", t0.Add(70*time.Second))
	if w2.Lost != 1 {
		t.Fatalf("ikinci pencere: %+v", w2)
	}
}

func TestProbeLateAndUnknownEcho(t *testing.T) {
	tr := NewProbeTracker(4 * time.Second)
	t0 := time.Unix(3000, 0)
	seq := tr.Sent("t", t0)
	if _, ok := tr.Echo("t", seq+99, t0); ok {
		t.Fatal("bilinmeyen seq yok sayilmali")
	}
	if _, ok := tr.Echo("yok", seq, t0); ok {
		t.Fatal("bilinmeyen tunel yok sayilmali")
	}
	if _, ok := tr.Echo("t", seq, t0.Add(9*time.Second)); ok {
		t.Fatal("gec yanki RTT sayilmamali")
	}
	w, _ := tr.Drain("t", t0.Add(10*time.Second))
	if w.Received != 0 || w.Lost != 1 || w.HasRTT {
		t.Fatalf("gec yanki kayip sayilmali: %+v", w)
	}
	// Yinelenen yanki ikinci kez sayilmaz.
	s2 := tr.Sent("t", t0.Add(20*time.Second))
	tr.Echo("t", s2, t0.Add(20*time.Second))
	if _, ok := tr.Echo("t", s2, t0.Add(20*time.Second)); ok {
		t.Fatal("ikinci yanki sayilmamali")
	}
}

func TestProbePendingCap(t *testing.T) {
	tr := NewProbeTracker(time.Hour) // zaman asimi yok: sinir devreye girmeli
	t0 := time.Unix(4000, 0)
	for i := 0; i < probeMaxPending+10; i++ {
		tr.Sent("t", t0.Add(time.Duration(i)*time.Millisecond))
	}
	w, _ := tr.Drain("t", t0.Add(time.Second))
	if w.Lost != 10 {
		t.Fatalf("tasan bekleyenler kayip sayilmali: %+v", w)
	}
}

func TestProbeDroppedCounters(t *testing.T) {
	tr := NewProbeTracker(0)
	tr.AddDropped("t", 3)
	tr.AddDropped("t", 2)
	tr.AddDropped("", 5)                // bos kimlik yok sayilir
	tr.addDroppedKnown("bilinmeyen", 9) // durumu olmayan tunel olusturmaz
	w, ok := tr.Drain("t", time.Now())
	if !ok || w.Dropped != 5 {
		t.Fatalf("dropped=%+v", w)
	}
	if _, ok := tr.Drain("bilinmeyen", time.Now()); ok {
		t.Fatal("bilinmeyen tunel olusmamali")
	}
}

func newProbeSession() *Session {
	return NewSession("s1", "c1", "c", "127.0.0.1", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSessionProbeFeatureGate(t *testing.T) {
	s := newProbeSession()
	if s.UDPProbeSupported() {
		t.Fatal("pazarlanmadan destek yok")
	}
	// Eski istemci: yoklama yollanmaz, hata da yok (conn nil'e dokunulmaz).
	if sent, err := s.SendUDPProbe(context.Background(), "t"); sent || err != nil {
		t.Fatalf("destek yokken gonderilmemeli: %v %v", sent, err)
	}
	// Destek yokken gelen yanki / sayac yok sayilir.
	s.handleUDPProbeEcho(protocol.UDPProbeEcho{TunnelID: "t", Seq: 1}, time.Now())
	s.handleUDPAgentStats(protocol.UDPAgentStats{Dropped: map[string]int64{"t": 5}})
	if _, ok := s.UDPProbes().Drain("t", time.Now()); ok {
		t.Fatal("destek yokken durum olusmamali")
	}
}

func TestHandshakeNegotiatesUDPProbe(t *testing.T) {
	s, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.3.0", Platform: "linux/amd64",
		Features: []string{protocol.FeatureUDPProbe},
	})
	if !s.UDPProbeSupported() {
		t.Fatal("istemci ilan edince etkinlesmeli")
	}
	// Eski istemci Features gondermez -> olcum kapali, el sikisma yine basarili.
	old, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.1.0", Platform: "linux/amd64",
	})
	if old.UDPProbeSupported() {
		t.Fatal("eski istemcide olcum kapali kalmali")
	}
}

func TestHandleControlRoutesEchoAndStats(t *testing.T) {
	s := newProbeSession()
	s.SetUDPProbe(true)
	seq := s.UDPProbes().Sent("t", time.Now())
	echo, _ := protocol.Marshal(protocol.UDPProbeEcho{
		Type: protocol.TypeUDPProbeEcho, TunnelID: "t", Seq: seq, TS: time.Now().UnixNano(),
	}, protocol.TypeUDPProbeEcho)
	if err := s.handleControl(context.Background(), echo); err != nil {
		t.Fatal(err)
	}
	st, _ := protocol.Marshal(protocol.UDPAgentStats{
		Type: protocol.TypeUDPAgentStats, Dropped: map[string]int64{"t": 7},
	}, protocol.TypeUDPAgentStats)
	if err := s.handleControl(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	w, ok := s.UDPProbes().Drain("t", time.Now())
	if !ok || w.Received != 1 || w.Dropped != 7 || !w.HasRTT {
		t.Fatalf("pencere: %+v", w)
	}
}

func TestSessionAgentStatsOnlyKnownTunnels(t *testing.T) {
	s := newProbeSession()
	s.SetUDPProbe(true)
	s.UDPProbes().Sent("t", time.Now())
	s.handleUDPAgentStats(protocol.UDPAgentStats{Dropped: map[string]int64{
		"t": 4, "uydurma": 100, "t2": -5,
	}})
	s.handleUDPAgentStats(protocol.UDPAgentStats{Dropped: map[string]int64{"t": 1 << 40}})
	w, _ := s.UDPProbes().Drain("t", time.Now())
	if w.Dropped != 4+maxAgentDropReport {
		t.Fatalf("dropped=%d", w.Dropped)
	}
	if _, ok := s.UDPProbes().Drain("uydurma", time.Now()); ok {
		t.Fatal("istemci keyfi tunel durumu olusturamamali")
	}
}
