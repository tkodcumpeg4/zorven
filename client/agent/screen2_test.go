package agent

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func fill(img *image.RGBA, r image.Rectangle, rr, g, b uint8) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			o := y*img.Stride + x*4
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = rr, g, b, 255
		}
	}
}

func noise(img *image.RGBA, r image.Rectangle, rng *rand.Rand) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			o := y*img.Stride + x*4
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255
		}
	}
}

func TestDiffTilesAndMerge(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 300, 200)) // 5x4 karo (kenarda artik)
	b := image.NewRGBA(a.Rect)
	copy(b.Pix, a.Pix)

	dirty, tx, ty := diffTiles(a, b)
	if tx != 5 || ty != 4 {
		t.Fatalf("karo sayisi %dx%d", tx, ty)
	}
	if got := mergeDirty(dirty, tx, ty, 300, 200); len(got) != 0 {
		t.Fatalf("degisiklik yokken rect: %v", got)
	}

	// 2x2 karoluk blok + ayri tek karo + sag kenar kismi karo
	fill(b, image.Rect(70, 10, 130, 100), 9, 9, 9)   // karolar (1,0),(2,0),(1,1),(2,1)... y 10..100 -> satir 0,1
	fill(b, image.Rect(10, 150, 20, 160), 1, 2, 3)   // karo (0,2)
	fill(b, image.Rect(290, 190, 300, 200), 5, 5, 5) // karo (4,3) kismi
	dirty, tx, ty = diffTiles(a, b)
	rects := mergeDirty(dirty, tx, ty, 300, 200)
	want := map[image.Rectangle]bool{
		image.Rect(64, 0, 192, 128):    true, // x 1..2 karo, y 0..1 karo
		image.Rect(0, 128, 64, 192):    true,
		image.Rect(256, 128, 300, 200): true, // y190-191 satir 2 + satir 3, kirpilmis
	}
	if len(rects) != len(want) {
		t.Fatalf("rects=%v", rects)
	}
	for _, r := range rects {
		if !want[r] {
			t.Errorf("beklenmeyen rect %v (hepsi: %v)", r, rects)
		}
	}

	// boyut degisince hepsi kirli
	c := image.NewRGBA(image.Rect(0, 0, 128, 64))
	dirty, tx, ty = diffTiles(a, c)
	if r := mergeDirty(dirty, tx, ty, 128, 64); len(r) != 1 || r[0] != c.Rect {
		t.Fatalf("boyut degisimi: %v", r)
	}
	dirty, tx, ty = diffTiles(nil, c)
	if r := mergeDirty(dirty, tx, ty, 128, 64); len(r) != 1 {
		t.Fatalf("nil prev: %v", r)
	}
}

func TestMergeDoesNotJoinGapped(t *testing.T) {
	// (0,0) ve (0,2) kirli, arada (0,1) temiz: birlesmemeli
	dirty := []bool{true, false, true}
	r := mergeDirty(dirty, 1, 3, 64, 192)
	if len(r) != 2 {
		t.Fatalf("%v", r)
	}
}

func TestSplitRect(t *testing.T) {
	parts := splitRect(image.Rect(0, 0, 1920, 1080), 512)
	area := 0
	for _, p := range parts {
		if p.Dx() > 512 || p.Dy() > 512 {
			t.Errorf("buyuk parca %v", p)
		}
		area += p.Dx() * p.Dy()
	}
	if area != 1920*1080 {
		t.Fatalf("alan %d", area)
	}
}

func TestEncoderChoice(t *testing.T) {
	enc := newGoTileEncoder()
	img := image.NewRGBA(image.Rect(0, 0, 256, 128))
	fill(img, img.Rect, 10, 20, 30)

	e, p, err := enc.Encode(img, image.Rect(0, 0, 64, 64), 70)
	if err != nil || e != protocol.Screen2EncSolid || !bytes.Equal(p, []byte{10, 20, 30, 255}) {
		t.Fatalf("solid: %d %v %v", e, p, err)
	}

	// az renk: dama tahtasi iki renk
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if (x/4+y/4)%2 == 0 {
				fill(img, image.Rect(x, y, x+1, y+1), 255, 255, 255)
			}
		}
	}
	e, p, err = enc.Encode(img, image.Rect(0, 0, 64, 64), 70)
	if err != nil || e != protocol.Screen2EncPNG {
		t.Fatalf("png bekleniyordu: %d %v", e, err)
	}
	dec, err := png.Decode(bytes.NewReader(p))
	if err != nil || dec.Bounds().Dx() != 64 {
		t.Fatalf("png cozulemedi: %v", err)
	}
	if _, _, _, a := dec.At(1, 1).RGBA(); a != 0xffff {
		t.Error("png opak olmali")
	}

	// foto benzeri: gurultu
	noise(img, image.Rect(128, 0, 256, 128), rand.New(rand.NewSource(2)))
	e, p, err = enc.Encode(img, image.Rect(128, 0, 256, 128), 70)
	if err != nil || e != protocol.Screen2EncJPEG {
		t.Fatalf("jpeg bekleniyordu: %d %v", e, err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(p)); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptCtl(t *testing.T) {
	a := newAdaptCtl(80, 20)
	if a.Quality() != 80 || a.FPS() != 20 {
		t.Fatal("baslangic tavanda olmali")
	}
	// tikanma: dusus, tabana kadar
	for i := 0; i < 20; i++ {
		a.Observe(400 * time.Millisecond)
	}
	if a.Quality() != screen2MinQuality || a.FPS() != screen2MinFPS {
		t.Fatalf("taban: q=%d fps=%d", a.Quality(), a.FPS())
	}
	// rahat: yavas yukselis, 8 kareden once degismez
	for i := 0; i < screen2RaiseAfter-1; i++ {
		if a.Observe(time.Millisecond) {
			t.Fatal("erken yukseldi")
		}
	}
	if !a.Observe(time.Millisecond) || a.Quality() != screen2MinQuality+5 || a.FPS() != screen2MinFPS+2 {
		t.Fatalf("yukselis: q=%d fps=%d", a.Quality(), a.FPS())
	}
	// orta bolge sayaci sifirlar
	for i := 0; i < screen2RaiseAfter-1; i++ {
		a.Observe(time.Millisecond)
	}
	a.Observe(a.Interval() / 4)
	for i := 0; i < screen2RaiseAfter-1; i++ {
		if a.Observe(time.Millisecond) {
			t.Fatal("sayac sifirlanmaliydi")
		}
	}
	// tavan asilmaz
	for i := 0; i < 1000; i++ {
		a.Observe(time.Microsecond)
	}
	if a.Quality() != 80 || a.FPS() != 20 {
		t.Fatalf("tavan: q=%d fps=%d", a.Quality(), a.FPS())
	}
	// tavan dusurulunce kirpilir; tavan tabandan dusukse taban tavana iner
	a.SetMax(50, 10)
	if a.Quality() != 50 || a.FPS() != 10 {
		t.Fatal("kirpma")
	}
	b := newAdaptCtl(20, 2)
	b.Observe(time.Second)
	if b.Quality() != 20 || b.FPS() != 2 {
		t.Fatalf("dusuk tavan: %d %d", b.Quality(), b.FPS())
	}
}

// --- oturum dongusu (sahte yakalayici) -------------------------------------

type fakeCap struct {
	mu   sync.Mutex
	img  *image.RGBA
	disp int
	n    int
}

func (f *fakeCap) NumDisplays() int { return 2 }
func (f *fakeCap) Bounds(d int) image.Rectangle {
	return image.Rect(d*1000, 0, d*1000+f.img.Rect.Dx(), f.img.Rect.Dy())
}
func (f *fakeCap) Capture(d int) (*image.RGBA, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disp = d
	f.n++
	c := image.NewRGBA(f.img.Rect)
	copy(c.Pix, f.img.Pix)
	return c, nil
}

type collector struct {
	mu   sync.Mutex
	msgs []protocol.Screen2Message
}

func (c *collector) send(_ context.Context, b []byte) error {
	m, err := protocol.DecodeScreen2(b)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	return nil
}

func (c *collector) snapshot() []protocol.Screen2Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.Screen2Message(nil), c.msgs...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("zaman asimi")
}

func TestScreen2SessionLoop(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(src, src.Rect, 40, 40, 40)
	fc := &fakeCap{img: src}
	col := &collector{}
	s := newScreen2Session("scr_x", 7, fc, newGoTileEncoder(), col.send,
		screen2Params{FPS: 20, Quality: 70, MaxWidth: 4000})
	var gotDisp []int
	var dmu sync.Mutex
	s.onDisplay = func(d int, b image.Rectangle) { dmu.Lock(); gotDisp = append(gotDisp, d); dmu.Unlock() }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()

	// 1) ilk kare keyframe
	waitFor(t, func() bool { return len(col.snapshot()) >= 1 })
	m := col.snapshot()[0]
	if !m.Keyframe || !m.First || !m.EOF || m.W != 320 || m.H != 192 || m.StreamID != 7 {
		t.Fatalf("ilk kare: %+v", m)
	}

	// 2) degisiklik yok => yeni mesaj yok
	time.Sleep(200 * time.Millisecond)
	if n := len(col.snapshot()); n != 1 {
		t.Fatalf("degismeyen ekranda %d mesaj", n)
	}

	// 3) kucuk degisiklik => tek rect, keyframe degil
	fc.mu.Lock()
	fill(fc.img, image.Rect(70, 70, 80, 80), 200, 0, 0)
	fc.mu.Unlock()
	waitFor(t, func() bool { return len(col.snapshot()) >= 2 })
	m = col.snapshot()[1]
	if m.Keyframe || len(m.Rects) != 1 || m.Rects[0].X != 64 || m.Rects[0].Y != 64 || m.Rects[0].W != 64 {
		t.Fatalf("diff kare: %+v", m)
	}
	if m.Seq != 1 {
		t.Errorf("seq=%d", m.Seq)
	}

	// 4) keyframe istegi
	s.Keyframe()
	waitFor(t, func() bool { return len(col.snapshot()) >= 3 })
	if m = col.snapshot()[2]; !m.Keyframe {
		t.Fatalf("keyframe bekleniyordu: %+v", m)
	}

	// 5) monitor degisimi => keyframe + onDisplay
	s.Update(screen2Params{Display: 1})
	waitFor(t, func() bool { return len(col.snapshot()) >= 4 })
	if m = col.snapshot()[3]; !m.Keyframe {
		t.Fatalf("monitor degisiminde keyframe: %+v", m)
	}
	fc.mu.Lock()
	d := fc.disp
	fc.mu.Unlock()
	dmu.Lock()
	if d != 1 || len(gotDisp) != 2 || gotDisp[1] != 1 {
		t.Errorf("display=%d bildirim=%v", d, gotDisp)
	}
	dmu.Unlock()

	// 6) canli kalite/fps tavani
	s.Update(screen2Params{FPS: 5, Quality: 40, Display: -1})
	waitFor(t, func() bool { return s.ctl.maxFPS == 5 }) // yarisli okuma kabul: yalniz test
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type noDisp struct{ fakeCap }

func (*noDisp) NumDisplays() int { return 0 }

func TestScreen2NoDisplayFailsClearly(t *testing.T) {
	fc := &noDisp{fakeCap{img: image.NewRGBA(image.Rect(0, 0, 8, 8))}}
	s := newScreen2Session("x", 1, fc, newGoTileEncoder(), func(context.Context, []byte) error { return nil }, screen2Params{})
	if err := s.run(context.Background()); err == nil {
		t.Fatal("hata bekleniyordu")
	}
}

// --- benchmark: 1080p -------------------------------------------------------

func benchFrame(rng *rand.Rand) (*image.RGBA, *image.RGBA) {
	a := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	// ofis benzeri: duz zemin + metin benzeri iki renkli bloklar + foto bolge
	fill(a, a.Rect, 240, 240, 240)
	for y := 100; y < 900; y += 20 {
		for x := 100; x < 900; x += 14 {
			if rng.Intn(3) == 0 {
				fill(a, image.Rect(x, y, x+8, y+12), 20, 20, 20)
			}
		}
	}
	noise(a, image.Rect(1000, 100, 1800, 700), rng)
	b := image.NewRGBA(a.Rect)
	copy(b.Pix, a.Pix)
	return a, b
}

func BenchmarkDiffTiles1080p(bb *testing.B) {
	a, b := benchFrame(rand.New(rand.NewSource(1)))
	fill(b, image.Rect(500, 500, 600, 540), 1, 2, 3)
	bb.ResetTimer()
	for i := 0; i < bb.N; i++ {
		diffTiles(a, b)
	}
}

func BenchmarkKeyframe1080p(bb *testing.B) {
	a, _ := benchFrame(rand.New(rand.NewSource(1)))
	enc := newGoTileEncoder()
	rects := splitRect(a.Rect, screen2MaxRectDim)
	bb.ResetTimer()
	var total int
	for i := 0; i < bb.N; i++ {
		total = 0
		for _, r := range rects {
			_, p, _ := enc.Encode(a, r, 70)
			total += len(p)
		}
	}
	bb.ReportMetric(float64(total), "bytes/frame")
}

func BenchmarkTypingUpdate1080p(bb *testing.B) {
	a, b := benchFrame(rand.New(rand.NewSource(1)))
	fill(b, image.Rect(500, 500, 640, 530), 1, 2, 3) // kucuk metin guncellemesi
	enc := newGoTileEncoder()
	bb.ResetTimer()
	var total int
	for i := 0; i < bb.N; i++ {
		d, tx, ty := diffTiles(a, b)
		total = 0
		for _, r := range mergeDirty(d, tx, ty, 1920, 1080) {
			_, p, _ := enc.Encode(b, r, 70)
			total += len(p)
		}
	}
	bb.ReportMetric(float64(total), "bytes/frame")
}
