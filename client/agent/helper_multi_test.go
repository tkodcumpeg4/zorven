package agent

import (
	"bytes"
	"image"
	"testing"
)

func paint(base *image.RGBA, r image.Rectangle, v byte) *image.RGBA {
	out := &image.RGBA{Pix: append([]byte(nil), base.Pix...), Stride: base.Stride, Rect: base.Rect}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			copy(out.Pix[y*out.Stride+x*4:], []byte{v, v, v, 255})
		}
	}
	return out
}

func coversRects(hint []image.Rectangle, want image.Rectangle) bool {
	for _, h := range hint {
		if want.In(h) {
			return true
		}
	}
	return false
}

// Iki oturum ayni yardimciyi paylasir: bir oturumun tukettigi DXGI ipucu
// digerinden KAYBOLMAMALI.
func TestHelperMultiSessionKeepsHints(t *testing.T) {
	r1, r2 := image.Rect(1, 1, 3, 3), image.Rect(5, 2, 7, 4)
	f0 := solid(8, 6, 10)
	f1 := paint(f0, r1, 100)
	f2 := paint(f1, r2, 200)
	be := &fakeBackend{frames: []fakeFrame{
		{img: f0},
		{img: f1, hint: []image.Rectangle{r1}, hinted: true},
		{img: f2, hint: []image.Rectangle{r2}, hinted: true},
	}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	a := helperCapturer{c: cl, s: cl.newSession()}
	b := helperCapturer{c: cl, s: cl.newSession()}

	if _, err := a.Capture(0); err != nil { // backend f0
		t.Fatal(err)
	}
	imgB, _, _, err := b.CaptureHinted(0) // backend f1 (B yeni: tam kare)
	if err != nil || !bytes.Equal(imgB.Pix, f1.Pix) {
		t.Fatalf("B tam kare f1 olmali: %v", err)
	}
	imgA, hintA, hinted, err := a.CaptureHinted(0) // backend f2
	if err != nil || !hinted {
		t.Fatalf("A: %v %v", err, hinted)
	}
	if !bytes.Equal(imgA.Pix, f2.Pix) {
		t.Fatal("A tuvali f2 olmali (B'nin tukettigi f1 degisimi kaybolmamali)")
	}
	if !coversRects(hintA, r1) || !coversRects(hintA, r2) {
		t.Fatalf("A ipucu iki degisimi de kapsamali: %v", hintA)
	}
	if fc.connects != 1 {
		t.Fatalf("connects=%d", fc.connects)
	}
}

// Bir oturumun senkron bozulmasi yardimciyi OLDURMEMELI.
func TestHelperDesyncResyncsOnlyThatSession(t *testing.T) {
	f0 := solid(8, 6, 10)
	be := &fakeBackend{frames: []fakeFrame{{img: f0}, {hinted: true}}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	a := helperCapturer{c: cl, s: cl.newSession()}
	if _, err := a.Capture(0); err != nil {
		t.Fatal(err)
	}
	// Tuvali boyut olarak boz (resync bayragi yok: istemci bilmiyor).
	cl.mu.Lock()
	a.s.canvas = image.NewRGBA(image.Rect(0, 0, 1, 1))
	a.s.disp = 0
	a.s.resync = false
	cl.mu.Unlock()
	img, err := a.Capture(0)
	if err != nil || img == nil || !bytes.Equal(img.Pix, f0.Pix) {
		t.Fatalf("yeniden eslesip tam kare gelmeli: %v", err)
	}
	if fc.connects != 1 || fc.shut != 0 {
		t.Fatalf("yardimci yeniden baslatilmamali: connects=%d shut=%d", fc.connects, fc.shut)
	}
}

func TestHelperDegradedPerSession(t *testing.T) {
	f0 := solid(8, 6, 10)
	be := &fakeBackend{frames: []fakeFrame{{img: f0}}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	a := helperCapturer{c: cl, s: cl.newSession()}
	b := helperCapturer{c: cl, s: cl.newSession()}
	_, _ = a.Capture(0)
	if a.Degraded() || b.Degraded() {
		t.Fatal("baslangicta degraded degil")
	}
	be.mu.Lock()
	be.frames = []fakeFrame{{err: image.ErrFormat}}
	be.capCalls = 0
	be.mu.Unlock()
	if _, _, hinted, err := a.CaptureHinted(0); err != nil || !hinted || !a.Degraded() {
		t.Fatalf("a degraded olmali: %v %v", err, hinted)
	}
	if b.Degraded() {
		t.Fatal("b henuz yakalamadi: degraded olmamali")
	}
}
