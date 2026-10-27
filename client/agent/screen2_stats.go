package agent

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Canli olcum (FeatureScreen2Stats). Boru hatti asamalari sure orneklerini
// s2Metrics'e yazar; ~1 sn'de bir pencere ozeti protocol.Screen2Stats olarak
// sunucuya gonderilir ve sifirlanir. Kayit ucuzdur (kilit + dilime ekleme) ve
// ornek sayisi pencere basina sinirlidir.

const screen2StatsMaxSamp = 600 // asama basina pencere ornek ust siniri

// screen2StatsEvery, ozet gonderme araligi (testler kisaltabilir).
var screen2StatsEvery = time.Second

// s2Stage, olculen asama.
type s2Stage int

const (
	stCapture s2Stage = iota
	stScale
	stDiff
	stEncode
	stPack
	stWrite
	stCount
)

// s2Metrics, bir penceredeki sayaclar ve asama ornekleri. Goroutine-guvenli.
type s2Metrics struct {
	mu      sync.Mutex
	started time.Time
	samples [stCount][]time.Duration
	// sayaclar
	captured int   // yakalanan tur sayisi (degisim olmayan turlar dahil degil)
	idle     int   // degisim olmadigi icin atlanan tur
	sent     int   // gonderilen kare (mesaj grubu)
	bytes    int64 // gonderilen bayt
	// anlik degerler (pencere sonunda okunur)
	srcW, srcH, w, h int
	method           string
}

func newS2Metrics(now time.Time) *s2Metrics { return &s2Metrics{started: now} }

// observe, bir asama suresini kaydeder.
func (m *s2Metrics) observe(st s2Stage, d time.Duration) {
	m.mu.Lock()
	if len(m.samples[st]) < screen2StatsMaxSamp {
		m.samples[st] = append(m.samples[st], d)
	}
	m.mu.Unlock()
}

func (m *s2Metrics) addCaptured(srcW, srcH, canvasW, canvasH int) {
	m.mu.Lock()
	m.captured++
	m.srcW, m.srcH, m.w, m.h = srcW, srcH, canvasW, canvasH
	m.mu.Unlock()
}

func (m *s2Metrics) addIdle() {
	m.mu.Lock()
	m.idle++
	m.mu.Unlock()
}

func (m *s2Metrics) idleCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.idle
}

func (m *s2Metrics) addSent(bytes int64) {
	m.mu.Lock()
	m.sent++
	m.bytes += bytes
	m.mu.Unlock()
}

func (m *s2Metrics) setMethod(method string) {
	m.mu.Lock()
	m.method = method
	m.mu.Unlock()
}

// stageSummary, orneklerin ortalamasini ve p95'ini (ms) hesaplar. Bos => sifir.
func stageSummary(d []time.Duration) protocol.Screen2StageStat {
	if len(d) == 0 {
		return protocol.Screen2StageStat{}
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum time.Duration
	for _, v := range s {
		sum += v
	}
	// p95: en yakin sira (nearest-rank).
	idx := (len(s)*95 + 99) / 100
	if idx < 1 {
		idx = 1
	}
	ms := func(x time.Duration) float64 { return float64(x.Microseconds()) / 1000 }
	return protocol.Screen2StageStat{
		Avg: ms(sum) / float64(len(s)),
		P95: ms(s[idx-1]),
		N:   len(s),
	}
}

// snapshot, pencereyi ozetler ve sifirlar. dropped yuvanin birlestirme sayaci
// farkidir (cagiran hesaplar).
func (m *s2Metrics) snapshot(now time.Time, merged int) protocol.Screen2Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	win := now.Sub(m.started)
	if win <= 0 {
		win = time.Millisecond
	}
	sec := win.Seconds()
	st := protocol.Screen2Stats{
		Type:       protocol.TypeScreen2Stats,
		WindowMs:   int(win.Milliseconds()),
		FPS:        round1(float64(m.sent) / sec),
		CaptureFPS: round1(float64(m.captured) / sec),
		Merged:     merged,
		Idle:       m.idle,
		KBps:       round1(float64(m.bytes) / 1024 / sec),
		Method:     m.method,
		SrcW:       m.srcW, SrcH: m.srcH, W: m.w, H: m.h,
		Capture: stageSummary(m.samples[stCapture]),
		Scale:   stageSummary(m.samples[stScale]),
		Diff:    stageSummary(m.samples[stDiff]),
		Encode:  stageSummary(m.samples[stEncode]),
		Pack:    stageSummary(m.samples[stPack]),
		Write:   stageSummary(m.samples[stWrite]),
	}
	for i := range m.samples {
		m.samples[i] = m.samples[i][:0]
	}
	m.captured, m.idle, m.sent, m.bytes = 0, 0, 0, 0
	m.started = now
	return st
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// captureMethoder, yakalama yontemini bildiren yakalayicilar (dxgi|gdi|kbinani|darwin).
type captureMethoder interface{ Method() string }

func (s *screen2Session) captureMethod() string {
	if cm, ok := s.cap.(captureMethoder); ok {
		return cm.Method()
	}
	return "bilinmiyor"
}

// statsLoop, her screen2StatsEvery'de pencere ozetini onStats ile yollar.
// onStats nil ise hic calismaz.
func (s *screen2Session) statsLoop(ctx context.Context, slot *s2Slot) {
	if s.onStats == nil {
		return
	}
	t := time.NewTicker(screen2StatsEvery)
	defer t.Stop()
	lastMerged := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.met.setMethod(s.captureMethod())
			cur := slot.droppedCount()
			st := s.met.snapshot(now, cur-lastMerged)
			lastMerged = cur
			st.TargetFPS, st.MaxFPS = s.ctl.FPS(), s.ctl.MaxFPS()
			st.Quality = s.ctl.Quality()
			st.Layers = s.ctl.LayerStats(s.layerInfos(), now)
			if s.vid != nil {
				s.vid.fillStats(&st, now)
			}
			s.onStats(st)
		}
	}
}
