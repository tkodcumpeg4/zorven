package agent

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestStageSummary(t *testing.T) {
	if s := stageSummary(nil); s.N != 0 || s.Avg != 0 || s.P95 != 0 {
		t.Fatalf("bos: %+v", s)
	}
	var d []time.Duration
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	s := stageSummary(d)
	if s.N != 100 || s.Avg != 50.5 || s.P95 != 95 {
		t.Fatalf("100 ornek: %+v", s)
	}
	one := stageSummary([]time.Duration{3 * time.Millisecond})
	if one.Avg != 3 || one.P95 != 3 {
		t.Fatalf("tek ornek: %+v", one)
	}
}

func TestMetricsSnapshotAndReset(t *testing.T) {
	t0 := time.Unix(100, 0)
	m := newS2Metrics(t0)
	m.setMethod("dxgi")
	for i := 0; i < 20; i++ {
		m.observe(stCapture, 4*time.Millisecond)
		m.addCaptured(1920, 1080, 1280, 720)
	}
	for i := 0; i < 10; i++ {
		m.addSent(10 << 10)
	}
	m.addIdle()
	st := m.snapshot(t0.Add(2*time.Second), 5)
	if st.FPS != 5 || st.CaptureFPS != 10 || st.KBps != 50 || st.Merged != 5 || st.Idle != 1 || st.WindowMs != 2000 {
		t.Fatalf("%+v", st)
	}
	if st.Method != "dxgi" || st.SrcW != 1920 || st.W != 1280 || st.Capture.Avg != 4 || st.Capture.N != 20 {
		t.Fatalf("%+v", st)
	}
	if !st.Valid() {
		t.Fatal("uretilen ozet gecerli olmali")
	}
	// Pencere sifirlandi; anlik degerler (boyut/yontem) kalir.
	st2 := m.snapshot(t0.Add(3*time.Second), 0)
	if st2.FPS != 0 || st2.Capture.N != 0 || st2.Idle != 0 || st2.W != 1280 || st2.Method != "dxgi" {
		t.Fatalf("%+v", st2)
	}
}

func TestMetricsSampleCap(t *testing.T) {
	m := newS2Metrics(time.Now())
	for i := 0; i < screen2StatsMaxSamp*3; i++ {
		m.observe(stWrite, time.Millisecond)
	}
	if n := len(m.samples[stWrite]); n != screen2StatsMaxSamp {
		t.Fatalf("ornek siniri asildi: %d", n)
	}
}

// Oturum calisirken ~her aralikta tam bir ozet gonderilir (hiz siniri) ve
// icerik pipeline'dan beslenir; onStats yoksa hic goroutine/mesaj yok.
func TestSessionEmitsStatsRateLimited(t *testing.T) {
	old := screen2StatsEvery
	screen2StatsEvery = 100 * time.Millisecond
	defer func() { screen2StatsEvery = old }()

	src := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(src, src.Rect, 40, 40, 40)
	fc := &fakeCap{img: src}
	col := &collector{}
	s := newScreen2Session("scr_s", 9, fc, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	var mu sync.Mutex
	var got []protocol.Screen2Stats
	s.onStats = func(st protocol.Screen2Stats) { mu.Lock(); got = append(got, st); mu.Unlock() }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	start := time.Now()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 3 })
	elapsed := time.Since(start)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if elapsed < 250*time.Millisecond {
		t.Fatalf("3 ozet %v icinde geldi; aralik asilmis", elapsed)
	}
	var sentTotal float64
	for _, st := range got {
		if !st.Valid() || st.MaxFPS != 30 || st.Quality <= 0 || st.W != 320 || st.H != 192 {
			t.Fatalf("ozet: %+v", st)
		}
		if st.Method != "bilinmiyor" { // fakeCap yontem bildirmez
			t.Fatalf("yontem %q", st.Method)
		}
		sentTotal += st.FPS
	}
	if sentTotal <= 0 {
		t.Fatal("ilk kare gonderildi: bir pencerede FPS > 0 olmali")
	}
	if got[0].Encode.N == 0 && got[1].Encode.N == 0 {
		t.Fatal("kodlama asamasi olculmedi")
	}
}

func TestSessionWithoutOnStatsStartsNoStatsLoop(t *testing.T) {
	s := newScreen2Session("x", 1, &fakeCap{img: image.NewRGBA(image.Rect(0, 0, 8, 8))}, newGoTileEncoder(),
		func(context.Context, []byte) error { return nil }, screen2Params{})
	done := make(chan struct{})
	go func() { s.statsLoop(context.Background(), newS2Slot()); close(done) }()
	select {
	case <-done: // onStats nil: hemen doner
	case <-time.After(time.Second):
		t.Fatal("onStats nil iken statsLoop hemen donmeli")
	}
}

// Geriye uyum: eski bir Hello (yetenek listesinde screen2_stats yok) sunucuda
// bu ozelligi acmaz; yeni ajan yetenegi ilan eder.
func TestStatsFeatureAdvertised(t *testing.T) {
	a := &Agent{}
	found := false
	for _, f := range a.helloFeatures() {
		if f == protocol.FeatureScreen2Stats {
			found = true
		}
	}
	if !found {
		t.Fatal("ajan screen2_stats yetenegini ilan etmeli")
	}
}
