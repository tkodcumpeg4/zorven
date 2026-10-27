package agent

import (
	"errors"
	"image"
	"sync"
	"time"
)

// DXGI Desktop Duplication yakalayicisinin platformdan bagimsiz kismi:
// geri dusus durum makinesi, kirli dikdortgen hesaplari ve ortam degiskeni.
// Gercek COM cagrilari capture_dxgi_windows.go'dadir ve dxgiSource arayuzunu
// uygular; boylece durum makinesi sahte kaynakla her platformda test edilir.

// ZORVEN_SCREEN_CAPTURE degerleri.
const (
	// envScreenCapture: bos/"auto" => DXGI (Windows 8+; cokme korumasi ve kbinani
	// yedegiyle, bkz. capture_guard.go); "dxgi" DXGI'yi KORUMAYI yok sayarak zorlar;
	// "gdi" kalici DC/DIB yolunu, "kbinani" eski kbinani yolunu zorlar.
	envScreenCapture = "ZORVEN_SCREEN_CAPTURE"

	dxgiFailLimit   = 3                // art arda bu kadar basarisizlikta yedege dus
	dxgiRetryBase   = 30 * time.Second // yedekte ilk bekleme
	dxgiRetryMax    = 4 * time.Minute  // ussel artisin tavani
	dxgiNextTimeout = 10 * time.Millisecond
	dxgiMaxRects    = 32 // bundan fazla dikdortgen bir sinirlayici kutuya birlestirilir
)

// Hata siniflari: kaynak bunlari sarar (errors.Is).
var (
	// errDXGILost: ACCESS_LOST / cihaz kaybi gibi gecici durum; yeniden kurulur.
	errDXGILost = errors.New("dxgi: erisim kaybedildi (yeniden kurulacak)")
	// errDXGIUnsupported: bu oturum/surucu DXGI duplication desteklemiyor (RDP, eski surucu).
	errDXGIUnsupported = errors.New("dxgi: desteklenmiyor")
)

// dxgiResult, kaynaktan tek tur sonucu.
type dxgiResult struct {
	// Changed=false: zaman asimi ya da yalniz imlec guncellemesi; ekran degismedi.
	Changed bool
	// Img, kaynagin KALICI RGBA tamponu (sahibi kaynak; capturer kopyalar).
	Img *image.RGBA
	// Dirty, degisen bolgeler (goruntu koordinatlari, kirpilmis, birlestirilmis).
	// Ilk kare ve yeniden kurulumdan sonraki ilk kare TUM ekrandir.
	Dirty []image.Rectangle
}

// dxgiSource, DXGI duplication kaynagi (Windows'ta gercek, testte sahte).
type dxgiSource interface {
	// Open, display'in bounds sinirlariyla eslesen cikis icin duplication kurar.
	Open(display int, bounds image.Rectangle) error
	// Next, bir sonraki kareyi bekler (en fazla timeout).
	Next(timeout time.Duration) (dxgiResult, error)
	// Close, tum COM kaynaklarini birakir (birden fazla kez cagrilabilir).
	Close()
}

// dxgiCapturer, DXGI birincil, fb (GDI/kbinani) yedek olan Capturer ve
// hintCapturer. Tek yakalama goroutine'i kullanir; Method baska goroutine'den
// okunabilir.
type dxgiCapturer struct {
	fb  Capturer
	src dxgiSource
	now func() time.Time

	opened  bool
	display int
	primed  bool // acildiktan sonra en az bir gercek kare alindi
	fails   int
	fbUntil time.Time
	delay   time.Duration

	// guard, cokme korumasi (nil olabilir; bkz. capture_guard.go).
	guard captureGuard

	mu      sync.Mutex
	usedFB  bool // son kare yedekten mi geldi
	lastErr error
}

// captureGuard, dxgiGuard'in dxgiCapturer tarafindan kullanilan yuzu.
type captureGuard interface {
	Begin() error
	FrameOK()
	Clear()
}

func newDXGICapturer(fb Capturer, src dxgiSource) *dxgiCapturer {
	return &dxgiCapturer{fb: fb, src: src, now: time.Now, delay: dxgiRetryBase}
}

func (c *dxgiCapturer) NumDisplays() int                   { return c.fb.NumDisplays() }
func (c *dxgiCapturer) Bounds(d int) image.Rectangle       { return c.fb.Bounds(d) }
func (c *dxgiCapturer) Capture(d int) (*image.RGBA, error) { return c.fb.Capture(d) }

// Method, son karenin yakalama yontemi.
func (c *dxgiCapturer) Method() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.usedFB {
		return "dxgi"
	}
	if m, ok := c.fb.(captureMethoder); ok {
		return m.Method()
	}
	return "gdi"
}

// LastError, DXGI'den en son yedege dusme nedeni (log/teshis).
func (c *dxgiCapturer) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Close, DXGI kaynaklarini serbest birakir.
func (c *dxgiCapturer) Close() error {
	c.closeSrc()
	if c.guard != nil {
		c.guard.Clear()
	}
	return nil
}

func (c *dxgiCapturer) closeSrc() {
	if c.opened {
		c.src.Close()
	}
	c.opened, c.primed = false, false
}

func (c *dxgiCapturer) setUsedFB(v bool) {
	c.mu.Lock()
	c.usedFB = v
	c.mu.Unlock()
}

// viaFallback, tam goruntuyu yedek yakalayiciyla alir (ipucu yok).
func (c *dxgiCapturer) viaFallback(display int) (*image.RGBA, []image.Rectangle, bool, error) {
	c.setUsedFB(true)
	img, err := c.fb.Capture(display)
	return img, nil, false, err
}

// noteFail, DXGI hatasini sayar; esik asilirsa ya da kaynak desteklenmiyorsa
// yedege duser ve ussel artan bir sure sonra DXGI'yi yeniden dener.
func (c *dxgiCapturer) noteFail(err error) {
	c.mu.Lock()
	c.lastErr = err
	c.mu.Unlock()
	c.fails++
	if c.fails >= dxgiFailLimit || errors.Is(err, errDXGIUnsupported) {
		c.fails = 0
		c.fbUntil = c.now().Add(c.delay)
		c.delay = min(c.delay*2, dxgiRetryMax)
	}
}

// CaptureHinted, hintCapturer sozlesmesini uygular (bkz. screen2_hint.go).
func (c *dxgiCapturer) CaptureHinted(display int) (*image.RGBA, []image.Rectangle, bool, error) {
	if c.now().Before(c.fbUntil) {
		return c.viaFallback(display)
	}
	if c.opened && c.display != display {
		c.closeSrc()
	}
	if !c.opened {
		if c.guard != nil {
			_ = c.guard.Begin() // surec ilk DXGI cagrisinda olurse sonraki acilis atlar
		}
		if err := c.src.Open(display, c.fb.Bounds(display)); err != nil {
			c.src.Close()
			c.noteFail(err)
			return c.viaFallback(display)
		}
		c.opened, c.display, c.primed = true, display, false
	}
	res, err := c.src.Next(dxgiNextTimeout)
	if err != nil {
		// Her hata kaynagi sifirlar: sonraki tur yeniden kurar (ilk kare tam ekran).
		c.closeSrc()
		c.noteFail(err)
		return c.viaFallback(display)
	}
	if !res.Changed {
		if !c.primed {
			// Henuz DXGI karesi yok (ilk kare gecikti): bu turu yedekle doldur.
			return c.viaFallback(display)
		}
		c.setUsedFB(false)
		return nil, nil, true, nil
	}
	if res.Img == nil || len(res.Dirty) == 0 {
		c.closeSrc()
		c.noteFail(errors.New("dxgi: bos kare sonucu"))
		return c.viaFallback(display)
	}
	// Basari: sayaclari sifirla.
	c.fails, c.delay, c.primed = 0, dxgiRetryBase, true
	if c.guard != nil {
		c.guard.FrameOK()
	}
	c.setUsedFB(false)
	snap := &image.RGBA{Pix: make([]byte, len(res.Img.Pix)), Stride: res.Img.Stride, Rect: res.Img.Rect}
	copy(snap.Pix, res.Img.Pix)
	return snap, res.Dirty, true, nil
}

// --- kirli dikdortgen hesaplari (platformdan bagimsiz) ----------------------

// dxgiMove, DXGI_OUTDUPL_MOVE_RECT: Src noktasindaki piksel bloku Dst'ye tasindi.
type dxgiMove struct {
	Src image.Point
	Dst image.Rectangle
}

// dxgiDirtyUnion, kirli dikdortgenler ile tasima HEDEF dikdortgenlerini w x h
// goruntuye kirpip birlestirir. Kaynak (Src) bolgesi eklenmez: kare dokusu zaten
// tum masaustunun guncel halidir ve kaynakta degisen kisim ayrica kirli listesinde
// bulunur. Sonuc cakisan/bitisik dikdortgenleri birlestirir; cok fazlaysa tek
// sinirlayici kutuya indirger (dogruluk korunur: kirli alan yalniz buyur).
func dxgiDirtyUnion(dirty []image.Rectangle, moves []dxgiMove, w, h int) []image.Rectangle {
	bounds := image.Rect(0, 0, w, h)
	rs := make([]image.Rectangle, 0, len(dirty)+len(moves))
	for _, r := range dirty {
		if r = r.Canon().Intersect(bounds); !r.Empty() {
			rs = append(rs, r)
		}
	}
	for _, m := range moves {
		if r := m.Dst.Canon().Intersect(bounds); !r.Empty() {
			rs = append(rs, r)
		}
	}
	return coalesceRects(rs, dxgiMaxRects)
}

// coalesceRects, cakisan ya da kenar kenara dikdortgenleri birlestirir. Birlesme
// sonucu her zaman girdilerin ustkumesidir (kirli alan kuculmez). maxN asilirsa
// tek sinirlayici kutu doner.
func coalesceRects(rs []image.Rectangle, maxN int) []image.Rectangle {
	if len(rs) <= 1 {
		return rs
	}
	out := append([]image.Rectangle(nil), rs...)
	for merged := true; merged && len(out) > 1; {
		merged = false
		for i := 0; i < len(out) && !merged; i++ {
			for j := i + 1; j < len(out); j++ {
				if rectsTouch(out[i], out[j]) {
					out[i] = out[i].Union(out[j])
					out = append(out[:j], out[j+1:]...)
					merged = true
					break
				}
			}
		}
	}
	if len(out) > maxN {
		bb := out[0]
		for _, r := range out[1:] {
			bb = bb.Union(r)
		}
		return []image.Rectangle{bb}
	}
	return out
}

// rectsTouch: dikdortgenler cakisiyor ya da kenar kenara mi.
func rectsTouch(a, b image.Rectangle) bool {
	return a.Min.X <= b.Max.X && b.Min.X <= a.Max.X && a.Min.Y <= b.Max.Y && b.Min.Y <= a.Max.Y
}

// rectsArea, ayrik varsayilan dikdortgenlerin toplam alani.
func rectsArea(rs []image.Rectangle) int {
	n := 0
	for _, r := range rs {
		n += r.Dx() * r.Dy()
	}
	return n
}

// matchOutputRect, bounds (kbinani monitor siniri) ile eslesen DXGI cikis
// dizinini bulur; yoksa -1. Oncelik: tam eslesme; sonra ayni sol-ust kose;
// sonra ayni boyut (yalniz tek aday varsa).
func matchOutputRect(bounds image.Rectangle, outs []image.Rectangle) int {
	for i, o := range outs {
		if o == bounds {
			return i
		}
	}
	for i, o := range outs {
		if o.Min == bounds.Min {
			return i
		}
	}
	idx, n := -1, 0
	for i, o := range outs {
		if o.Dx() == bounds.Dx() && o.Dy() == bounds.Dy() {
			idx = i
			n++
		}
	}
	if n == 1 {
		return idx
	}
	return -1
}
