//go:build darwin

package agent

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS ekran yakalama: CGO'suz, saf Go. kbinani/screenshot'in darwin
// arka ucu cgo ister (CGO_ENABLED=0'da 0 ekran bildirir); burada CoreGraphics
// purego ile dlopen/dlsym uzerinden cagrilir.
//
// SINIRLAMA: CGDisplayCreateImage macOS 14'te kullanimdan kalkti, macOS 15+
// (Sequoia) basliklarda "obsoleted" (yerine ScreenCaptureKit). Sembol sistemde
// duruyorsa calisir; yoksa ya da nil donerse acik hata verilir. ScreenCaptureKit
// ObjC/blok/delegate ister ve ayri bir is olarak ele alinmalidir.
//
// Bounds NOKTA (point) cinsindendir (CGEvent koordinatlariyla ayni uzay);
// Capture ise PIKSEL goruntusu doner (Retina'da 2x). Ikisinin orani oturum
// icin onemsizdir: girdi normalize (0..1) koordinatla calisir.

const (
	macCoreGraphicsPath = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	macCoreFoundPath    = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	macAppServicesPath  = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"

	errMacScreenPerm = "macOS Ekran Kaydi izni gerekli (Sistem Ayarlari > Gizlilik ve Guvenlik > Ekran Kaydi)"
)

// cgRect, CGRect (4 x CGFloat=double). Duz struct, C yerlesimiyle ayni.
type cgRect struct{ X, Y, W, H float64 }

// cgPoint, CGPoint.
type cgPoint struct{ X, Y float64 }

var (
	macOnce    sync.Once
	macLoadErr error

	cgGetActiveDisplayList func(max uint32, ids *uint32, count *uint32) int32
	cgDisplayBounds        func(id uint32) cgRect
	cgDisplayCreateImage   func(id uint32) uintptr
	cgImageGetWidth        func(img uintptr) uint64
	cgImageGetHeight       func(img uintptr) uint64
	cgImageGetBytesPerRow  func(img uintptr) uint64
	cgImageGetBitsPerPix   func(img uintptr) uint64
	cgImageGetBitmapInfo   func(img uintptr) uint32
	cgImageGetDataProvider func(img uintptr) uintptr
	cgDataProviderCopyData func(p uintptr) uintptr
	cfDataGetBytePtr       func(d uintptr) unsafe.Pointer
	cfDataGetLength        func(d uintptr) int64
	cfRelease              func(o uintptr)
)

// macSym, sembolu cozer ve fn'e baglar; yoksa hata doner (panik yok).
func macSym(lib uintptr, name string, fn any) error {
	addr, err := purego.Dlsym(lib, name)
	if err != nil || addr == 0 {
		return fmt.Errorf("%s bulunamadi: %v", name, err)
	}
	purego.RegisterFunc(fn, addr)
	return nil
}

func macLoadCapture() error {
	macOnce.Do(func() {
		cg, err := purego.Dlopen(macCoreGraphicsPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			macLoadErr = fmt.Errorf("CoreGraphics yuklenemedi: %w", err)
			return
		}
		cf, err := purego.Dlopen(macCoreFoundPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			macLoadErr = fmt.Errorf("CoreFoundation yuklenemedi: %w", err)
			return
		}
		for _, b := range []struct {
			lib  uintptr
			name string
			fn   any
		}{
			{cg, "CGGetActiveDisplayList", &cgGetActiveDisplayList},
			{cg, "CGDisplayBounds", &cgDisplayBounds},
			{cg, "CGImageGetWidth", &cgImageGetWidth},
			{cg, "CGImageGetHeight", &cgImageGetHeight},
			{cg, "CGImageGetBytesPerRow", &cgImageGetBytesPerRow},
			{cg, "CGImageGetBitsPerPixel", &cgImageGetBitsPerPix},
			{cg, "CGImageGetBitmapInfo", &cgImageGetBitmapInfo},
			{cg, "CGImageGetDataProvider", &cgImageGetDataProvider},
			{cg, "CGDataProviderCopyData", &cgDataProviderCopyData},
			{cf, "CFDataGetBytePtr", &cfDataGetBytePtr},
			{cf, "CFDataGetLength", &cfDataGetLength},
			{cf, "CFRelease", &cfRelease},
		} {
			if err := macSym(b.lib, b.name, b.fn); err != nil {
				macLoadErr = err
				return
			}
		}
		// CGDisplayCreateImage macOS 15+'ta kalkmis olabilir: ayri, anlasilir hata.
		if err := macSym(cg, "CGDisplayCreateImage", &cgDisplayCreateImage); err != nil {
			macLoadErr = errors.New("bu macOS surumunde CGDisplayCreateImage yok (macOS 15+: ScreenCaptureKit gerekir, henuz desteklenmiyor)")
		}
	})
	return macLoadErr
}

// macDisplayIDs, aktif ekranlarin CGDirectDisplayID listesi (ana ekran ilk).
func macDisplayIDs() []uint32 {
	if macLoadCapture() != nil {
		return nil
	}
	var ids [32]uint32
	var n uint32
	if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) != 0 || n == 0 {
		return nil
	}
	return append([]uint32(nil), ids[:n]...)
}

// platMethod, yakalama yontemi adi (canli olcum panelinde gosterilir).
func platMethod() string { return "darwin" }

// newPlatCapturer, oturum icin yakalayici olusturur.
func newPlatCapturer() Capturer { return goCapturer{} }

func platNumDisplays() int {
	// CGDisplayCreateImage yoksa 0 bildir: ust katman acik "desteklenmiyor" hatasi verir.
	if macLoadCapture() != nil {
		return 0
	}
	return len(macDisplayIDs())
}

func platBounds(d int) image.Rectangle {
	ids := macDisplayIDs()
	if d < 0 || d >= len(ids) {
		return image.Rectangle{}
	}
	r := cgDisplayBounds(ids[d])
	return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

func platCapture(d int) (*image.RGBA, error) {
	if err := macLoadCapture(); err != nil {
		return nil, err
	}
	ids := macDisplayIDs()
	if d < 0 || d >= len(ids) {
		return nil, fmt.Errorf("gecersiz ekran: %d", d)
	}
	img := cgDisplayCreateImage(ids[d])
	if img == 0 {
		return nil, errors.New(errMacScreenPerm)
	}
	defer cfRelease(img)

	width, height := int(cgImageGetWidth(img)), int(cgImageGetHeight(img))
	stride := int(cgImageGetBytesPerRow(img))
	bgra, err := macPixelOrder(cgImageGetBitmapInfo(img), int(cgImageGetBitsPerPix(img)))
	if err != nil {
		return nil, err
	}
	prov := cgImageGetDataProvider(img) // "Get" kurali: sahiplik bizde degil
	if prov == 0 {
		return nil, errors.New(errMacScreenPerm)
	}
	data := cgDataProviderCopyData(prov) // "Copy" kurali: CFRelease bizde
	if data == 0 {
		return nil, errors.New(errMacScreenPerm)
	}
	defer cfRelease(data)
	ptr, n := cfDataGetBytePtr(data), int(cfDataGetLength(data))
	if ptr == nil || n <= 0 {
		return nil, errors.New(errMacScreenPerm)
	}
	// Tampon CFData'ya ait; macPixelsToRGBA kopyalar, donus oncesi serbest birakilir.
	return macPixelsToRGBA(unsafe.Slice((*byte)(ptr), n), width, height, stride, bgra)
}
