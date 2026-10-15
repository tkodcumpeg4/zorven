package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- imlec izleyici -----------------------------------------------------------

type fakeCursorSrc struct {
	st         cursorState
	err        error
	shapeCalls int
	shapes     map[uintptr]*cursorShape
}

func (f *fakeCursorSrc) Poll() (cursorState, error) { return f.st, f.err }
func (f *fakeCursorSrc) Shape(h uintptr) (*cursorShape, bool) {
	f.shapeCalls++
	s, ok := f.shapes[h]
	return s, ok
}
func (f *fakeCursorSrc) Close() {}

type cursorRig struct {
	src    *fakeCursorSrc
	tr     *cursorTracker
	pos    []protocol.ScreenCursor
	shapes []protocol.ScreenCursorShape
}

func newCursorRig() *cursorRig {
	r := &cursorRig{src: &fakeCursorSrc{shapes: map[uintptr]*cursorShape{
		1: {W: 2, H: 2, RGBA: make([]byte, 16)},
		2: {W: 1, H: 1, RGBA: make([]byte, 4)},
	}}}
	r.tr = newCursorTracker(r.src,
		func() displayRect { return displayRect{x: 100, y: 0, w: 2000, h: 1000} },
		func() (int, int) { return 1000, 500 },
		func(c protocol.ScreenCursor) { r.pos = append(r.pos, c) },
		func(c protocol.ScreenCursorShape) { r.shapes = append(r.shapes, c) })
	return r
}

func TestCursorScalesToCanvasAndHidesOutside(t *testing.T) {
	r := newCursorRig()
	t0 := time.Now()
	r.src.st = cursorState{X: 100 + 1000, Y: 500, Visible: true, Handle: 1}
	r.tr.Step(t0)
	if len(r.pos) != 1 || r.pos[0].X != 500 || r.pos[0].Y != 250 || !r.pos[0].Visible || r.pos[0].W != 1000 {
		t.Fatalf("olcekleme: %+v", r.pos)
	}
	// Yakalanan monitorun disi: gizli.
	r.src.st = cursorState{X: 50, Y: 10, Visible: true, Handle: 1}
	r.tr.Step(t0.Add(time.Second))
	if last := r.pos[len(r.pos)-1]; last.Visible {
		t.Fatalf("monitor disinda gizli beklenirdi: %+v", last)
	}
	// Poll hatasi gizli sayilir.
	r.src.err = errors.New("masaustu degisti")
	r.src.st = cursorState{}
	r.tr.Step(t0.Add(2 * time.Second))
}

func TestCursorThrottleAndChangeOnly(t *testing.T) {
	r := newCursorRig()
	t0 := time.Now()
	r.src.st = cursorState{X: 200, Y: 100, Visible: true, Handle: 1}
	r.tr.Step(t0)
	// Degisim yok: gonderme.
	r.tr.Step(t0.Add(100 * time.Millisecond))
	if len(r.pos) != 1 {
		t.Fatalf("degismeyen konum tekrar gonderilmemeli: %d", len(r.pos))
	}
	// Son gonderimden >33 ms sonra degisim hemen gider.
	r.src.st.X = 300
	r.tr.Step(t0.Add(110 * time.Millisecond))
	if len(r.pos) != 2 {
		t.Fatalf("degisim gonderilmeliydi: %d", len(r.pos))
	}
	// 33 ms icinde ikinci degisim bekler, sonraki turda en yeni konum gider.
	r.src.st.X = 400
	r.tr.Step(t0.Add(120 * time.Millisecond))
	if len(r.pos) != 2 {
		t.Fatalf("hiz siniri asildi: %d", len(r.pos))
	}
	r.tr.Step(t0.Add(150 * time.Millisecond))
	if len(r.pos) != 3 || r.pos[2].X != (400-100)*1000/2000 {
		t.Fatalf("bekleyen en yeni konum gitmeli: %+v", r.pos)
	}
}

func TestCursorRateLimitedBurst(t *testing.T) {
	r := newCursorRig()
	t0 := time.Now()
	r.src.st = cursorState{X: 200, Y: 100, Visible: true, Handle: 1}
	r.tr.Step(t0)
	sent := len(r.pos)
	// 1 saniyede her 5 ms'de bir degisen konum: en fazla ~30/sn mesaj.
	for i := 1; i <= 200; i++ {
		r.src.st.X = 200 + i
		r.tr.Step(t0.Add(time.Duration(i) * 5 * time.Millisecond))
	}
	n := len(r.pos) - sent
	if n > 31 || n < 20 {
		t.Fatalf("saniyede ~30 mesaj beklenirdi, %d", n)
	}
	// Son konum eninde sonunda gider (dur-kalk sonrasi).
	r.tr.Step(t0.Add(2 * time.Second))
	if got := r.pos[len(r.pos)-1].X; got != (200+200-100)*1000/2000 {
		t.Fatalf("son konum yanlis: %d", got)
	}
}

func TestCursorShapeSentOnHandleChangeAndCached(t *testing.T) {
	r := newCursorRig()
	t0 := time.Now()
	step := func(h uintptr, i int) {
		r.src.st = cursorState{X: 200 + i, Y: 100, Visible: true, Handle: h}
		r.tr.Step(t0.Add(time.Duration(i) * time.Second))
	}
	step(1, 0)
	step(1, 1)
	step(2, 2)
	step(1, 3) // onbellekten: Shape tekrar sorulmaz
	step(2, 4)
	if len(r.shapes) != 4 { // 1,2,1,2: her tutac degisiminde bicim gider
		t.Fatalf("bicim mesaj sayisi: %d", len(r.shapes))
	}
	if r.src.shapeCalls != 2 {
		t.Fatalf("kaynaga 2 kez sorulmali (onbellek), %d", r.src.shapeCalls)
	}
	if r.shapes[0].Type != protocol.TypeScreenCursorShape || !r.shapes[0].Valid() {
		t.Fatalf("bicim: %+v", r.shapes[0])
	}
	// Bilinmeyen tutac: bicim gonderilmez ama konum akar.
	step(99, 5)
	if len(r.shapes) != 4 {
		t.Fatal("bilinmeyen tutac icin bicim gonderilmemeli")
	}
}

func TestLimitCursorShape(t *testing.T) {
	big := &cursorShape{W: 128, H: 96, HotX: 64, HotY: 48, RGBA: make([]byte, 128*96*4)}
	got := limitCursorShape(big)
	if got.W != 64 || got.H != 48 || got.HotX != 32 || got.HotY != 24 || len(got.RGBA) != 64*48*4 {
		t.Fatalf("kucultme: %dx%d hot %d,%d", got.W, got.H, got.HotX, got.HotY)
	}
	small := &cursorShape{W: 32, H: 32, RGBA: make([]byte, 32*32*4)}
	if limitCursorShape(small) != small {
		t.Fatal("sinirdaki bicim aynen donmeli")
	}
}

func TestCursorFromBitmaps(t *testing.T) {
	// Renkli, alfasiz: opaklik AND maskesinden (1 => seffaf).
	color := []byte{0, 0, 255, 0, 0, 255, 0, 0} // piksel0 kirmizi(BGRA: b0 g0 r255), piksel1 yesil
	mask := []byte{0, 0, 0, 0, 255, 255, 255, 0}
	s := cursorFromBitmaps(2, 1, color, mask)
	if s == nil || s.RGBA[0] != 255 || s.RGBA[3] != 255 || s.RGBA[7] != 0 {
		t.Fatalf("renkli maskeli: %+v", s)
	}
	// Renkli alfali on-carpimli: r=64,a=128 -> ~127.
	s = cursorFromBitmaps(1, 1, []byte{0, 0, 64, 128}, nil)
	if s.RGBA[0] < 126 || s.RGBA[0] > 128 || s.RGBA[3] != 128 {
		t.Fatalf("alfa duzeltme: %v", s.RGBA)
	}
	// Tek renkli 1x1: AND ust, XOR alt. AND=0,XOR=1 => beyaz opak.
	mono := []byte{0, 0, 0, 0, 255, 255, 255, 0}
	s = cursorFromBitmaps(1, 1, nil, mono)
	if s.RGBA[0] != 255 || s.RGBA[3] != 255 {
		t.Fatalf("tek renkli beyaz: %v", s.RGBA)
	}
	// AND=1,XOR=0 => seffaf.
	s = cursorFromBitmaps(1, 1, nil, []byte{255, 255, 255, 0, 0, 0, 0, 0})
	if s.RGBA[3] != 0 {
		t.Fatalf("seffaf beklenirdi: %v", s.RGBA)
	}
	if cursorFromBitmaps(1, 1, nil, []byte{0}) != nil {
		t.Fatal("kisa maske nil donmeli")
	}
}

func TestArgbToRGBA(t *testing.T) {
	out := argbToRGBA([]uint32{0x80400000, 0xFF102030, 0})
	if out[0] < 126 || out[0] > 128 || out[3] != 0x80 {
		t.Fatalf("on-carpim cozme: %v", out[:4])
	}
	if out[4] != 0x10 || out[5] != 0x20 || out[6] != 0x30 || out[7] != 0xFF {
		t.Fatalf("opak: %v", out[4:8])
	}
}

// --- pano izleyici ------------------------------------------------------------

type fakeClip struct {
	seq     uint32
	text    string
	setErr  error
	getErrs int
}

func (f *fakeClip) Seq() uint32 { return f.seq }
func (f *fakeClip) Get() (string, error) {
	return f.text, nil
}
func (f *fakeClip) Set(t string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.text = t
	f.seq++
	return nil
}
func (f *fakeClip) userCopy(t string) { f.text = t; f.seq++ }

func TestClipboardWatcherOnlyWhenEnabledAndNoOldContent(t *testing.T) {
	fc := &fakeClip{text: "eski parola", seq: 5}
	var sent []string
	w := newClipboardWatcher(fc, func(s string) { sent = append(sent, s) })

	fc.userCopy("kapaliyken")
	w.Step()
	if len(sent) != 0 {
		t.Fatal("izleme kapaliyken gonderilmemeli")
	}
	w.SetEnabled(true)
	w.Step()
	if len(sent) != 0 {
		t.Fatal("acilista mevcut pano icerigi sizmamali")
	}
	fc.userCopy("yeni\r\nmetin")
	w.Step()
	if len(sent) != 1 || sent[0] != "yeni\nmetin" {
		t.Fatalf("sent=%q", sent)
	}
	w.Step()
	if len(sent) != 1 {
		t.Fatal("degismeyen pano tekrar gonderilmemeli")
	}
	// Ayni metni yeniden kopyalamak da tekrar gonderilmez (kopya bastirma).
	fc.userCopy("yeni\nmetin")
	w.Step()
	if len(sent) != 1 {
		t.Fatal("ayni metin tekrar gonderilmemeli")
	}
}

func TestClipboardWatcherEchoSuppressionAndLimits(t *testing.T) {
	fc := &fakeClip{seq: 1}
	var sent []string
	w := newClipboardWatcher(fc, func(s string) { sent = append(sent, s) })
	w.SetEnabled(true)

	// Panelden gelen metin panoya yazilir ve GERI YANSIMAZ.
	if err := w.Apply("panelden"); err != nil {
		t.Fatal(err)
	}
	w.Step()
	if fc.text != "panelden" || len(sent) != 0 {
		t.Fatalf("yanki: text=%q sent=%q", fc.text, sent)
	}
	// Sinir: 64 KiB tamam, +1 red; gecersiz UTF-8 red.
	if err := w.Apply(strings.Repeat("a", protocol.MaxScreenClipboardSize)); err != nil {
		t.Fatalf("tam sinir: %v", err)
	}
	if err := w.Apply(strings.Repeat("a", protocol.MaxScreenClipboardSize+1)); err == nil {
		t.Fatal("sinir asimi reddedilmeli")
	}
	if err := w.Apply("bozuk\xff"); err == nil {
		t.Fatal("gecersiz utf-8 reddedilmeli")
	}
	// Kullanici cok buyuk metin kopyalarsa gonderilmez.
	fc.userCopy(strings.Repeat("b", protocol.MaxScreenClipboardSize+10))
	w.Step()
	if len(sent) != 0 {
		t.Fatal("buyuk pano gonderilmemeli")
	}
	// Set hatasi yayilir.
	fc.setErr = errors.New("kilitli")
	if err := w.Apply("x"); err == nil {
		t.Fatal("hata yayilmali")
	}
}

func TestHelloFeaturesNoClipboard(t *testing.T) {
	has := func(fs []string, f string) bool {
		for _, x := range fs {
			if x == f {
				return true
			}
		}
		return false
	}
	a := &Agent{NoClipboard: true}
	if has(a.helloFeatures(), protocol.FeatureScreenClipboard) {
		t.Fatal("--no-clipboard ile pano ilan edilmemeli")
	}
	// Eski ozellikler korunur.
	for _, f := range []string{protocol.FeatureFlowControl, protocol.FeatureScreenInputStatus, protocol.FeatureScreen2} {
		if !has(a.helloFeatures(), f) {
			t.Fatalf("%s ilan edilmeli", f)
		}
	}
	b := &Agent{}
	if has(b.helloFeatures(), protocol.FeatureScreenClipboard) != clipboardSupported() {
		t.Fatal("pano ilani platform destegini izlemeli")
	}
}
