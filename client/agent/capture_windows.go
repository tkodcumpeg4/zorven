//go:build windows

package agent

import (
	"errors"
	"image"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
)

// Windows ekran yakalama: kalici bellek DC'si + DIB section (GDI BitBlt).
//
// kbinani/screenshot her karede CreateCompatibleBitmap + GlobalAlloc +
// BitBlt + GetDIBits yapar ve sonra her pikseli unsafe ile tek tek BGRA->RGBA
// cevirir. Burada DC ve DIB bir kez olusturulup yeniden kullanilir; BitBlt
// dogrudan DIB belegine yazar (GetDIBits kopyasi yok); BGRA->RGBA donusumu
// hizli 64 bit yolla (bgraToRGBA) yapilir.
//
// Her turlu hatada (DC/DIB olusturulamadi, BitBlt basarisiz, boyut gecersiz)
// kbinani yoluna dusulur; yani davranis hicbir zaman eskisinden kotu olmaz.
// Monitor sinirlari kbinani'den gelir (DPI'dan bagimsiz gercek cozunurluk).

var (
	modGDI32Cap  = syscall.NewLazyDLL("gdi32.dll")
	modUser32Cap = syscall.NewLazyDLL("user32.dll")

	procGetDCCap         = modUser32Cap.NewProc("GetDC")
	procReleaseDCCap     = modUser32Cap.NewProc("ReleaseDC")
	procCreateCompatDC   = modGDI32Cap.NewProc("CreateCompatibleDC")
	procCreateDIBSection = modGDI32Cap.NewProc("CreateDIBSection")
	procSelectObjectCap  = modGDI32Cap.NewProc("SelectObject")
	procBitBltCap        = modGDI32Cap.NewProc("BitBlt")
	procDeleteObjectCap  = modGDI32Cap.NewProc("DeleteObject")
	procDeleteDCCap      = modGDI32Cap.NewProc("DeleteDC")
)

const (
	gdiSRCCOPY       = 0x00CC0020
	gdiDIBRGBColors  = 0
	gdiBIRGB         = 0
	gdiMaxPixelBytes = 1 << 30 // 1 GiB: aberan boyutlara karsi koruma
)

type gdiBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// gdiCapture, bir boyut icin kalici bellek DC'si ve DIB bolumu.
type gdiCapture struct {
	w, h   int
	memDC  uintptr
	bitmap uintptr
	old    uintptr
	bits   unsafe.Pointer // DIB belegi (BGRA, ust-alt, stride = w*4)
}

func (g *gdiCapture) close() {
	if g.memDC != 0 {
		if g.old != 0 {
			procSelectObjectCap.Call(g.memDC, g.old)
		}
		if g.bitmap != 0 {
			procDeleteObjectCap.Call(g.bitmap)
		}
		procDeleteDCCap.Call(g.memDC)
	}
	*g = gdiCapture{}
}

func newGDICapture(screenDC uintptr, w, h int) (*gdiCapture, error) {
	if w <= 0 || h <= 0 || w*h*4 > gdiMaxPixelBytes {
		return nil, errors.New("gecersiz yakalama boyutu")
	}
	g := &gdiCapture{w: w, h: h}
	memDC, _, _ := procCreateCompatDC.Call(screenDC)
	if memDC == 0 {
		return nil, errors.New("CreateCompatibleDC basarisiz")
	}
	g.memDC = memDC
	hdr := gdiBitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(gdiBitmapInfoHeader{})), Width: int32(w), Height: -int32(h),
		Planes: 1, BitCount: 32, Compression: gdiBIRGB,
	}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(screenDC, uintptr(unsafe.Pointer(&hdr)), gdiDIBRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		g.close()
		return nil, errors.New("CreateDIBSection basarisiz")
	}
	g.bitmap, g.bits = bmp, bits
	old, _, _ := procSelectObjectCap.Call(memDC, bmp)
	if old == 0 {
		g.close()
		return nil, errors.New("SelectObject basarisiz")
	}
	g.old = old
	return g, nil
}

var (
	gdiMu    sync.Mutex
	gdiState *gdiCapture
	// GDI yolu art arda gdiFailLimit kez basarisiz olursa gdiRetryAfter kadar
	// kbinani'ye sabitlenir (her karede iki kez denemek yerine); sonra yeniden denenir.
	gdiFails   int
	gdiRetryAt time.Time
)

const (
	gdiFailLimit  = 5
	gdiRetryAfter = 30 * time.Second
)

func platNumDisplays() int {
	if h := serviceHelper(); h != nil { // servis (oturum 0): yardimci kullanici oturumunda
		return h.NumDisplays()
	}
	return screenshot.NumActiveDisplays()
}

func platBounds(d int) image.Rectangle {
	if h := serviceHelper(); h != nil {
		return h.Bounds(d)
	}
	return screenshot.GetDisplayBounds(d)
}

// platMethodName, son karenin GDI mi kbinani mi ile alindigini tutar.
var platMethodName atomic.Value

// platMethod, yakalama yontemi adi (canli olcum panelinde gosterilir).
func platMethod() string {
	if h := serviceHelper(); h != nil {
		return h.Method()
	}
	if v, ok := platMethodName.Load().(string); ok {
		return v
	}
	return "gdi"
}

func platCapture(d int) (*image.RGBA, error) {
	if h := serviceHelper(); h != nil {
		return h.Capture(d)
	}
	b := screenshot.GetDisplayBounds(d)
	// Kalici DC/DIB yolu (0.2.22) Windows'ta bir kullanicida ajan kilitlenmesiyle
	// iliskilendirildi; dogrulanana kadar yalniz ZORVEN_SCREEN_CAPTURE=gdi ile
	// acilir. Varsayilan: 0.2.21'e kadar sorunsuz calisan kbinani yolu.
	if strings.EqualFold(strings.TrimSpace(os.Getenv(envScreenCapture)), "gdi") {
		if img, err := gdiCaptureRect(b); err == nil {
			platMethodName.Store("gdi")
			return img, nil
		}
	}
	platMethodName.Store("kbinani")
	return screenshot.CaptureRect(b)
}

// gdiCaptureRect, b dikdortgenini kalici DC/DIB ile yakalar; sonuc cagirana
// aittir (yeni RGBA).
func gdiCaptureRect(b image.Rectangle) (*image.RGBA, error) {
	gdiMu.Lock()
	defer gdiMu.Unlock()
	if gdiFails >= gdiFailLimit {
		if time.Now().Before(gdiRetryAt) {
			return nil, errors.New("GDI yolu gecici devre disi")
		}
		gdiFails = 0
	}
	w, h := b.Dx(), b.Dy()
	fail := func(err error) (*image.RGBA, error) {
		gdiFails++
		gdiRetryAt = time.Now().Add(gdiRetryAfter)
		if gdiState != nil {
			gdiState.close()
			gdiState = nil
		}
		return nil, err
	}
	// Ekran DC'si her karede alinir/birakilir: masaustu degisince (UAC, kilit
	// ekrani) eski DC gecersiz olur.
	screenDC, _, _ := procGetDCCap.Call(0)
	if screenDC == 0 {
		return fail(errors.New("GetDC basarisiz"))
	}
	defer procReleaseDCCap.Call(0, screenDC)

	if gdiState == nil || gdiState.w != w || gdiState.h != h {
		if gdiState != nil {
			gdiState.close()
			gdiState = nil
		}
		g, err := newGDICapture(screenDC, w, h)
		if err != nil {
			return fail(err)
		}
		gdiState = g
	}
	ok, _, _ := procBitBltCap.Call(gdiState.memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(int32(b.Min.X)), uintptr(int32(b.Min.Y)), gdiSRCCOPY)
	if ok == 0 {
		return fail(errors.New("BitBlt basarisiz"))
	}
	gdiFails = 0
	src := unsafe.Slice((*byte)(gdiState.bits), w*h*4)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bgraToRGBA(img.Pix, src, w, h, w*4, img.Stride)
	return img, nil
}
