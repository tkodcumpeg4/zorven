package agent

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func fbLayer(id int, fps float64, skipped, queue int) protocol.Screen2FeedbackLayer {
	return protocol.Screen2FeedbackLayer{ID: id, FPS: fps, Skipped: skipped, QueueMs: queue, Viewers: 1}
}

func TestAdaptFeedbackLowersFPSToSlowestViewer(t *testing.T) {
	a := newAdaptCtl(100, 30)
	now := time.Now()
	if a.LayerFPS(1, now) != 30 {
		t.Fatal("geri bildirim yokken tavan")
	}
	// Kullanici olcumu: izleyici 10 fps aliyor, atlama var => 10*1.2+1 = 13.
	a.Feedback(fbLayer(1, 10, 14, 0), 500, now)
	if got := a.LayerFPS(1, now); got != 13 {
		t.Fatalf("katman1 fps=%d, beklenen 13", got)
	}
	// Diger katman etkilenmez.
	if got := a.LayerFPS(0, now); got != 30 {
		t.Fatalf("katman0 fps=%d", got)
	}
	// Katmanin araligi hedef fps'ten gelir.
	if iv := a.LayerInterval(1, now); iv != time.Second/13 {
		t.Fatalf("aralik=%v", iv)
	}
	// Kuyruk yasi tek basina tikanma sayilir.
	b := newAdaptCtl(100, 30)
	b.Feedback(fbLayer(0, 20, 0, 800), 500, now)
	if got := b.LayerFPS(0, now); got != 25 {
		t.Fatalf("kuyruk yasi: fps=%d", got)
	}
}

func TestAdaptFeedbackBoundsAndFloor(t *testing.T) {
	a := newAdaptCtl(100, 20)
	now := time.Now()
	a.Feedback(fbLayer(0, 100, 3, 0), 500, now) // tavanin ustu => tavana kirpilir
	if got := a.LayerFPS(0, now); got != 20 {
		t.Fatalf("tavan: %d", got)
	}
	a.Feedback(fbLayer(0, 0, 9, 0), 500, now) // hic teslim yok => taban 3
	if got := a.LayerFPS(0, now); got != screen2MinFPS {
		t.Fatalf("taban: %d", got)
	}
	// Yerel uyarlama (yazma) daha dusukse o kazanir.
	b := newAdaptCtl(100, 30)
	for i := 0; i < 20; i++ {
		b.Observe(400 * time.Millisecond)
	}
	b.Feedback(fbLayer(0, 20, 1, 0), 500, now)
	if got := b.LayerFPS(0, now); got != screen2MinFPS {
		t.Fatalf("yerel taban: %d", got)
	}
}

func TestAdaptFeedbackRaiseSlowlyAndRelease(t *testing.T) {
	a := newAdaptCtl(100, 24)
	now := time.Now()
	a.Feedback(fbLayer(1, 10, 5, 0), 500, now) // tavan 13
	// Tikanmasiz ilk pencere (500 ms) yukseltmez, ikincisi +2.
	now = now.Add(500 * time.Millisecond)
	a.Feedback(fbLayer(1, 24, 0, 0), 500, now)
	if got := a.LayerFPS(1, now); got != 13 {
		t.Fatalf("erken yukseldi: %d", got)
	}
	now = now.Add(500 * time.Millisecond)
	a.Feedback(fbLayer(1, 24, 0, 0), 500, now)
	if got := a.LayerFPS(1, now); got != 15 {
		t.Fatalf("yukselis: %d", got)
	}
	// Surekli tikanmasiz => en sonunda kisit kalkar ve tavana doner.
	for i := 0; i < 40; i++ {
		now = now.Add(time.Second)
		a.Feedback(fbLayer(1, 24, 0, 0), 1000, now)
	}
	if got := a.LayerFPS(1, now); got != 24 {
		t.Fatalf("tavana donmedi: %d", got)
	}
	// Tikanma yeniden gelirse hemen iner.
	a.Feedback(fbLayer(1, 8, 4, 0), 500, now)
	if got := a.LayerFPS(1, now); got != 10 {
		t.Fatalf("yeniden inis: %d", got)
	}
}

func TestAdaptFeedbackExpires(t *testing.T) {
	a := newAdaptCtl(100, 30)
	now := time.Now()
	a.Feedback(fbLayer(0, 5, 9, 0), 500, now)
	if a.LayerFPS(0, now) != 7 {
		t.Fatal("kisit uygulanmadi")
	}
	later := now.Add(screen2FBTTL + time.Second)
	if got := a.LayerFPS(0, later); got != 30 {
		t.Fatalf("eski geri bildirim yok sayilmali: %d", got)
	}
	// Eski durumdan sonra gelen ilk tikanmasiz bilgi sifirdan baslar (eski tavan canlanmaz).
	a.Feedback(fbLayer(0, 30, 0, 0), 500, later)
	if got := a.LayerFPS(0, later); got != 30 {
		t.Fatalf("eski tavan canlandi: %d", got)
	}
}

func TestAdaptFeedbackQualityDropAndRecover(t *testing.T) {
	a := newAdaptCtl(100, 30)
	now := time.Now()
	// Kalite: spec 40, yerel 100 => 40. Tabanda degilken indirim yok.
	a.Feedback(fbLayer(1, 10, 5, 0), 500, now)
	if q := a.LayerQuality(1, 40, now); q != 40 {
		t.Fatalf("fps tabanda degilken kalite inmemeli: %d", q)
	}
	// fps tabanda ve atlama suruyor: kalite spec'in altina iner, 30'da durur.
	for i := 0; i < 14; i++ {
		now = now.Add(500 * time.Millisecond)
		a.Feedback(fbLayer(1, 0, 6, 900), 500, now)
	}
	if got := a.LayerFPS(1, now); got != screen2MinFPS {
		t.Fatalf("fps taban: %d", got)
	}
	if q := a.LayerQuality(1, 40, now); q != screen2FBMinQuality {
		t.Fatalf("kalite 30'a inmeli: %d", q)
	}
	if q := a.LayerQuality(1, 90, now); q != 90-screen2FBQDropMax {
		t.Fatalf("yuksek katman kalite=%d", q)
	}
	// Spec zaten 30'un altindaysa dokunulmaz.
	if q := a.LayerQuality(1, 20, now); q != 20 {
		t.Fatalf("taban altinda dokunulmamali: %d", q)
	}
	// Toparlanma: kalite adim adim geri cikar.
	prev := a.LayerQuality(1, 90, now)
	for i := 0; i < 40; i++ {
		now = now.Add(time.Second)
		a.Feedback(fbLayer(1, 30, 0, 0), 1000, now)
	}
	if q := a.LayerQuality(1, 90, now); q != 90 || q <= prev {
		t.Fatalf("kalite geri cikmadi: %d (once %d)", q, prev)
	}
	// Katmansiz akis (-1): spec yok, yerel kalite uzerinden.
	b := newAdaptCtl(70, 30)
	for i := 0; i < 5; i++ {
		b.Feedback(fbLayer(-1, 0, 3, 0), 500, now)
	}
	if q := b.LayerQuality(-1, 0, now); q != 45 {
		t.Fatalf("katmansiz kalite=%d", q)
	}
}

func TestScreen2SessionFeedbackAbsentLayerCountsClean(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 64))
	s := newScreen2Session("x", 1, &fakeCap{img: src}, newGoTileEncoder(), (&collector{}).send, screen2Params{FPS: 20})
	now := time.Now()
	s.Feedback(protocol.Screen2Feedback{WindowMs: 500, Layers: []protocol.Screen2FeedbackLayer{fbLayer(-1, 5, 2, 0)}}, now)
	if got := s.ctl.LayerFPS(-1, now); got != 7 {
		t.Fatalf("fps=%d", got)
	}
	// Bos ozet => katman tikanmasiz; 2 pencere sonra tavan yukselir.
	for i := 0; i < 2; i++ {
		now = now.Add(500 * time.Millisecond)
		s.Feedback(protocol.Screen2Feedback{WindowMs: 500}, now)
	}
	if got := s.ctl.LayerFPS(-1, now); got != 9 {
		t.Fatalf("yukselis: %d", got)
	}
	// Gecersiz mesaj yok sayilir.
	s.Feedback(protocol.Screen2Feedback{WindowMs: -1, Layers: []protocol.Screen2FeedbackLayer{fbLayer(-1, 1, 9, 0)}}, now)
	if got := s.ctl.LayerFPS(-1, now); got != 9 {
		t.Fatalf("gecersiz mesaj etkiledi: %d", got)
	}
}

func TestLayerStatsReportsEffectiveQuality(t *testing.T) {
	a := newAdaptCtl(100, 24)
	now := time.Now()
	a.Feedback(fbLayer(1, 10, 5, 0), 500, now)
	ls := a.LayerStats([]layerInfo{{0, 75}, {1, 40}}, now)
	if len(ls) != 2 || ls[0].Quality != 75 || ls[0].TargetFPS != 24 || ls[1].Quality != 40 || ls[1].TargetFPS != 13 || ls[1].SpecQuality != 40 {
		t.Fatalf("%+v", ls)
	}
	// Yerel uyarlama kaliteyi dusururse efektif kalite onu yansitir.
	for i := 0; i < 20; i++ {
		a.Observe(400 * time.Millisecond)
	}
	ls = a.LayerStats([]layerInfo{{0, 75}}, now)
	if ls[0].Quality != screen2MinQuality {
		t.Fatalf("efektif kalite=%d", ls[0].Quality)
	}
}

// Katman basina fps: yavas katman daha seyrek gonderilir, hizli katman etkilenmez.
func TestScreen2LayeredPerLayerPacing(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(src, src.Rect, 40, 40, 40)
	fc := &fakeCap{img: src}
	col := &collector{}
	specs := []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 4000, Quality: 80}, {ID: 1, MaxWidth: 160, Quality: 40}}
	s := newScreen2Session("scr_p", 9, fc, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Layers: specs})
	s.Feedback(protocol.Screen2Feedback{WindowMs: 500, Layers: []protocol.Screen2FeedbackLayer{fbLayer(1, 2, 5, 0)}}, time.Now())
	if got := s.ctl.LayerFPS(1, time.Now()); got != screen2MinFPS {
		t.Fatalf("katman1 fps=%d", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	// Yakalamayi surekli degistir: her tur her katmanda degisim olsun.
	stop := make(chan struct{})
	go func() {
		c := uint8(0)
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			c += 7
			fc.mu.Lock()
			fill(fc.img, image.Rect(0, 0, 60, 60), c, 0, 0)
			fc.mu.Unlock()
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	n0, n1 := len(col.byLayer(0)), len(col.byLayer(1))
	cancel()
	<-done
	if n0 < 10 {
		t.Fatalf("hizli katman cok seyrek: %d", n0)
	}
	// 3 fps * 1.5 sn ~ 5 (+ilk keyframe); hizli katmanin cok altinda olmali.
	if n1 < 2 || n1 > 9 || n1*2 > n0 {
		t.Fatalf("yavas katman=%d hizli=%d", n1, n0)
	}
}
