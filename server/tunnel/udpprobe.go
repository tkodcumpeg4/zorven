package tunnel

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// UDP tunel olcumu (FeatureUDPProbe). Sunucu her UDPProbeInterval'de tunel
// basina bir udp_probe yollar; istemci aynen yankilar. RTT = yanki ani - TS
// (yalnizca sunucu saati). UDPProbeTimeout icinde yanki gelmeyen yoklama KAYIP
// sayilir. Olcum tunel WS'i uzerindedir: "tunel ayagindaki" (sunucu <-> istemci)
// gecikme ve kayiptir; istemci -> yerel servis ayagi olculmez.

const (
	// UDPProbeInterval, tunel basina yoklama araligi.
	UDPProbeInterval = 5 * time.Second
	// UDPProbeTimeout, yankinin beklenecegi azami sure; asilirsa kayip.
	UDPProbeTimeout = 4 * time.Second

	// probeEWMAAlpha, gecikme ussel ortalamasinin yeni ornek agirligi.
	probeEWMAAlpha = 0.2
	// probeMaxSamples, bir pencerede tutulan en fazla RTT ornegi (bellek ustu siniri).
	probeMaxSamples = 1024
	// probeMaxPending, yanitsiz yoklama sayisi ust siniri (olu istemciye karsi).
	probeMaxPending = 64
)

// ProbeWindow, bir tunelin son Drain'den bu yana olcum ozeti.
type ProbeWindow struct {
	Sent     int
	Received int
	Lost     int
	// HasRTT false ise hic yanki gelmedi; P50/P95/EWMA anlamsizdir.
	HasRTT bool
	P50Ms  float64
	P95Ms  float64
	// EWMAMs, oturum boyunca ussel ortalama gecikme (pencereler arasi surer).
	EWMAMs float64
	// Dropped, kuyruk tasmasi nedeniyle dusurulen datagram (sunucu + istemci).
	Dropped int64
}

// LossPct, kayip yuzdesi; yanitlanmis/zaman asimina ugramis yoklama yoksa false.
func (w ProbeWindow) LossPct() (float64, bool) {
	n := w.Received + w.Lost
	if n == 0 {
		return 0, false
	}
	return 100 * float64(w.Lost) / float64(n), true
}

type probeState struct {
	nextSeq  uint64
	pending  map[uint64]time.Time
	rtts     []time.Duration
	received int
	lost     int
	sent     int
	ewmaMs   float64
	hasEWMA  bool
	dropped  int64
}

// ProbeTracker, oturumun tunel basina yoklama durumunu tutar. Es zamanli guvenlidir.
type ProbeTracker struct {
	mu      sync.Mutex
	timeout time.Duration
	tunnels map[string]*probeState
}

// NewProbeTracker, verilen zaman asimiyla izleyici olusturur (<=0 ise varsayilan).
func NewProbeTracker(timeout time.Duration) *ProbeTracker {
	if timeout <= 0 {
		timeout = UDPProbeTimeout
	}
	return &ProbeTracker{timeout: timeout, tunnels: map[string]*probeState{}}
}

func (t *ProbeTracker) state(id string) *probeState {
	st := t.tunnels[id]
	if st == nil {
		st = &probeState{pending: map[uint64]time.Time{}}
		t.tunnels[id] = st
	}
	return st
}

// expire, zaman asimina ugrayan bekleyen yoklamalari kayip sayar (mu altinda).
func (t *ProbeTracker) expire(st *probeState, now time.Time) {
	for seq, at := range st.pending {
		if now.Sub(at) >= t.timeout {
			delete(st.pending, seq)
			st.lost++
		}
	}
}

// Sent, yeni bir yoklamayi kaydeder ve seq doner.
func (t *ProbeTracker) Sent(tunnelID string, now time.Time) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(tunnelID)
	t.expire(st, now)
	if len(st.pending) >= probeMaxPending {
		// Olu istemci: en eski bekleyenleri kayip sayip yer ac.
		var oldest uint64
		var oldestAt time.Time
		for seq, at := range st.pending {
			if oldestAt.IsZero() || at.Before(oldestAt) {
				oldest, oldestAt = seq, at
			}
		}
		delete(st.pending, oldest)
		st.lost++
	}
	st.nextSeq++
	st.pending[st.nextSeq] = now
	st.sent++
	return st.nextSeq
}

// Echo, gelen yankiyi isler. Bilinmeyen / zaman asimina ugramis seq yok sayilir.
func (t *ProbeTracker) Echo(tunnelID string, seq uint64, now time.Time) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.tunnels[tunnelID]
	if st == nil {
		return 0, false
	}
	at, ok := st.pending[seq]
	if !ok {
		return 0, false
	}
	delete(st.pending, seq)
	rtt := now.Sub(at)
	if rtt < 0 {
		rtt = 0
	}
	if rtt > t.timeout {
		// Gec kalan yanki: yine de kayip (zaman asimi sonrasi) sayilir.
		st.lost++
		return rtt, false
	}
	st.received++
	if len(st.rtts) < probeMaxSamples {
		st.rtts = append(st.rtts, rtt)
	}
	ms := float64(rtt) / float64(time.Millisecond)
	if st.hasEWMA {
		st.ewmaMs = probeEWMAAlpha*ms + (1-probeEWMAAlpha)*st.ewmaMs
	} else {
		st.ewmaMs, st.hasEWMA = ms, true
	}
	return rtt, true
}

// AddDropped, tunel icin kuyruk tasmasiyla dusurulen datagram sayisini arttirir.
func (t *ProbeTracker) AddDropped(tunnelID string, n int64) {
	if n <= 0 || tunnelID == "" {
		return
	}
	t.mu.Lock()
	t.state(tunnelID).dropped += n
	t.mu.Unlock()
}

// maxAgentDropReport, istemcinin tek bildirimde bildirebilecegi azami dusurme.
const maxAgentDropReport = 10_000_000

// addDroppedKnown, yalnizca durumu zaten bulunan tunel icin sayar.
func (t *ProbeTracker) addDroppedKnown(tunnelID string, n int64) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	if st := t.tunnels[tunnelID]; st != nil {
		st.dropped += n
	}
	t.mu.Unlock()
}

// Drain, tunelin penceresini ozetler ve sifirlar (ussel ortalama ve bekleyen
// yoklamalar surer). Hic olcum/olay yoksa ok=false.
func (t *ProbeTracker) Drain(tunnelID string, now time.Time) (ProbeWindow, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.tunnels[tunnelID]
	if st == nil {
		return ProbeWindow{}, false
	}
	t.expire(st, now)
	w := ProbeWindow{
		Sent: st.sent, Received: st.received, Lost: st.lost,
		EWMAMs: st.ewmaMs, Dropped: st.dropped,
	}
	if len(st.rtts) > 0 {
		w.HasRTT = true
		w.P50Ms = percentileMs(st.rtts, 50)
		w.P95Ms = percentileMs(st.rtts, 95)
	}
	empty := st.sent == 0 && st.received == 0 && st.lost == 0 && st.dropped == 0
	st.sent, st.received, st.lost, st.dropped = 0, 0, 0, 0
	st.rtts = st.rtts[:0]
	return w, !empty
}

// EWMA, tunelin guncel ussel ortalama gecikmesi (ms); olcum yoksa ok=false.
func (t *ProbeTracker) EWMA(tunnelID string) (float64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st := t.tunnels[tunnelID]; st != nil && st.hasEWMA {
		return st.ewmaMs, true
	}
	return 0, false
}

// Forget, artik var olmayan bir tunelin durumunu siler.
func (t *ProbeTracker) Forget(tunnelID string) {
	t.mu.Lock()
	delete(t.tunnels, tunnelID)
	t.mu.Unlock()
}

// percentileMs, en yakin sira (nearest-rank) yontemiyle p. yuzdelik (ms). Girdiyi bozmaz.
func percentileMs(samples []time.Duration, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(samples))
	copy(cp, samples)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	rank := int(math.Ceil(p / 100 * float64(len(cp))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(cp) {
		rank = len(cp)
	}
	return float64(cp[rank-1]) / float64(time.Millisecond)
}

// --- Session entegrasyonu ----------------------------------------------------

// SetUDPProbe, el sikismada FeatureUDPProbe pazarlandigini isaretler.
func (s *Session) SetUDPProbe(on bool) { s.udpProbe = on }

// UDPProbeSupported, istemci udp_probe'u destekliyorsa true.
func (s *Session) UDPProbeSupported() bool { return s.udpProbe }

// UDPProbes, oturumun yoklama izleyicisi (hicbir zaman nil degil).
func (s *Session) UDPProbes() *ProbeTracker {
	s.probeOnce.Do(func() {
		if s.probes == nil {
			s.probes = NewProbeTracker(UDPProbeTimeout)
		}
	})
	return s.probes
}

// SendUDPProbe, tunel icin tek yoklama yollar. Istemci desteklemiyorsa false.
func (s *Session) SendUDPProbe(ctx context.Context, tunnelID string) (bool, error) {
	if !s.udpProbe {
		return false, nil
	}
	now := time.Now()
	seq := s.UDPProbes().Sent(tunnelID, now)
	return true, s.Send(ctx, protocol.UDPProbe{
		Type: protocol.TypeUDPProbe, TunnelID: tunnelID, Seq: seq, TS: now.UnixNano(),
	}, protocol.TypeUDPProbe)
}

// handleUDPProbeEcho, yankiyi izleyiciye isler (okuma dongusunden).
func (s *Session) handleUDPProbeEcho(m protocol.UDPProbeEcho, now time.Time) {
	if !s.udpProbe {
		return
	}
	s.UDPProbes().Echo(m.TunnelID, m.Seq, now)
}

// handleUDPAgentStats, istemcinin bildirdigi dusurme farklarini isler.
func (s *Session) handleUDPAgentStats(m protocol.UDPAgentStats) {
	if !s.udpProbe {
		return
	}
	// Yalnizca bu oturumda yoklanan tunellerin sayaci kabul edilir (istemci
	// keyfi kimlikle bellek sisirmesin) ve tek bildirimin degeri sinirlanir.
	for id, n := range m.Dropped {
		if n > maxAgentDropReport {
			n = maxAgentDropReport
		}
		s.UDPProbes().addDroppedKnown(id, n)
	}
}
