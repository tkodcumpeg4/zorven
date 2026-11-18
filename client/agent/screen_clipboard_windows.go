//go:build windows

package agent

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Windows pano erisimi — saf syscall, cgo YOK. Yalniz CF_UNICODETEXT.

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard      = user32.NewProc("OpenClipboard")
	procCloseClipboard     = user32.NewProc("CloseClipboard")
	procEmptyClipboard     = user32.NewProc("EmptyClipboard")
	procGetClipboardData   = user32.NewProc("GetClipboardData")
	procSetClipboardData   = user32.NewProc("SetClipboardData")
	procGetClipboardSeqNum = user32.NewProc("GetClipboardSequenceNumber")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

type winClipboard struct{}

var _ clipboardImageBackend = winClipboard{}

func newClipboardBackend() clipboardBackend {
	if h := serviceHelper(); h != nil {
		return helperClipboard{h} // servis: pano kullanici oturumundadir
	}
	return winClipboard{}
}

// clipboardSupported, bu platformda pano var mi (Hello'da ilan edilir).
func clipboardSupported() bool { return true }

// clipboardImageSupported, gorsel panosu (CF_DIB/"PNG") var mi.
func clipboardImageSupported() bool { return true }

func (winClipboard) Seq() uint32 {
	r, _, _ := procGetClipboardSeqNum.Call()
	return uint32(r)
}

// openClipboard, baska uygulama panoyu tutuyorsa kisa sure yeniden dener.
func openClipboard() error {
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("pano baska bir uygulamada acik")
}

// ptrOf, Global* uintptr adresini vet "unsafe.Pointer" uyarisi olmadan cevirir
// (bellek GlobalLock ile sabitlenmis, GC'ye ait degil).
func ptrOf(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func (winClipboard) Get() (string, error) {
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // metin yok
	}
	size, _, _ := procGlobalSize.Call(h)
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		return "", errors.New("pano kilitlenemedi")
	}
	defer procGlobalUnlock.Call(h)

	n := int(size) / 2
	if n <= 0 || n > 4<<20 { // 8 MiB ustu metin zaten sinirimizi asar
		return "", nil
	}
	words := unsafe.Slice((*uint16)(ptrOf(addr)), n)
	end := 0
	for end < n && words[end] != 0 {
		end++
	}
	return windows.UTF16ToString(words[:end]), nil
}

// --- Gorsel panosu ---------------------------------------------------------
//
// Okuma onceligi: kayitli "PNG" (alfa korunur, donusum yok) > CF_DIBV5 (alfa) >
// CF_DIB. Yazma: CF_DIB (Paint/Word/tarayici) + "PNG" birlikte.

var (
	procRegisterClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
)

const (
	cfDIB   = 8
	cfDIBV5 = 17
	// maxClipRawBytes, panodan okunacak ham bicim (DIB) ust siniri; 24 MP * 4 + baslik.
	maxClipRawBytes = protocol.MaxClipImagePixels*4 + 4096
)

// clipFormat, kayitli pano bicimi kimligini (0 => kaydedilemedi) onbellekler.
func clipFormat(name string) uint32 {
	clipFmtMu.Lock()
	defer clipFmtMu.Unlock()
	if id, ok := clipFmtIDs[name]; ok {
		return id
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(p)))
	clipFmtIDs[name] = uint32(r)
	return uint32(r)
}

var (
	clipFmtMu  sync.Mutex
	clipFmtIDs = map[string]uint32{}
)

func clipFormatAvailable(f uint32) bool {
	if f == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(uintptr(f))
	return r != 0
}

// readClipRaw, panoyu ACIKKEN bir bicimin ham baytlarini kopyalar (nil => yok/okunamadi).
// tooBig: bicim maxClipRawBytes'i asiyor.
func readClipRaw(f uint32) (data []byte, tooBig bool) {
	h, _, _ := procGetClipboardData.Call(uintptr(f))
	if h == 0 {
		return nil, false
	}
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return nil, false
	}
	if size > maxClipRawBytes {
		return nil, true
	}
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	return append([]byte(nil), unsafe.Slice((*byte)(ptrOf(addr)), int(size))...), false
}

// GetImage, panodaki gorseli PNG olarak doner (yoksa nil, nil). Pano YALNIZ ham
// baytlar kopyalanirken acik tutulur; donusum (CPU) kapaliyken yapilir.
func (winClipboard) GetImage() ([]byte, error) {
	pngIDs := []uint32{clipFormat("PNG"), clipFormat("image/png")}
	havePNG := false
	for _, id := range pngIDs {
		havePNG = havePNG || clipFormatAvailable(id)
	}
	if !havePNG && !clipFormatAvailable(cfDIBV5) && !clipFormatAvailable(cfDIB) {
		return nil, nil
	}

	type raw struct {
		isPNG bool
		data  []byte
	}
	var got *raw
	tooBig := false
	err := func() error {
		if err := openClipboard(); err != nil {
			return err
		}
		defer procCloseClipboard.Call()
		for _, id := range pngIDs {
			if !clipFormatAvailable(id) {
				continue
			}
			d, big := readClipRaw(id)
			tooBig = tooBig || big
			if len(d) > 0 {
				got = &raw{true, d}
				return nil
			}
		}
		for _, f := range []uint32{cfDIBV5, cfDIB} {
			if !clipFormatAvailable(f) {
				continue
			}
			d, big := readClipRaw(f)
			tooBig = tooBig || big
			if len(d) > 0 {
				got = &raw{false, d}
				return nil
			}
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if got == nil {
		if tooBig {
			return nil, errClipImageTooLarge
		}
		return nil, nil
	}
	if got.isPNG {
		if len(got.data) > protocol.MaxScreenClipboardImage {
			return nil, errClipImageTooLarge
		}
		if !protocol.ClipImageOK(got.data) {
			// Bozuk/asiri buyuk PNG: DIB'e geri dusme sansi yok (pano kapandi); kalici hata.
			return nil, errClipImageInvalid
		}
		return got.data, nil
	}
	return dibToPNG(got.data)
}

// newGlobal, verinin kopyasini GMEM_MOVEABLE bellege koyar (SetClipboardData'ya verilecek).
func newGlobal(data []byte) (uintptr, error) {
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, errors.New("bellek ayrilamadi")
	}
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		procGlobalFree.Call(h)
		return 0, errors.New("bellek kilitlenemedi")
	}
	copy(unsafe.Slice((*byte)(ptrOf(addr)), len(data)), data)
	procGlobalUnlock.Call(h)
	return h, nil
}

// SetImage, PNG'yi panoya CF_DIB + "PNG" olarak yazar. GlobalFree YALNIZ ilgili
// SetClipboardData basarisizsa (basarida bellegin sahibi sistemdir).
func (winClipboard) SetImage(pngData []byte) error {
	dib, err := pngToDIB(pngData)
	if err != nil {
		return err
	}
	hDIB, err := newGlobal(dib)
	if err != nil {
		return err
	}
	var hPNG uintptr
	pngFmt := clipFormat("PNG")
	if pngFmt != 0 {
		if hPNG, err = newGlobal(pngData); err != nil {
			hPNG = 0 // PNG bicimi opsiyonel: yalniz DIB yazilir
		}
	}
	if err := openClipboard(); err != nil {
		procGlobalFree.Call(hDIB)
		if hPNG != 0 {
			procGlobalFree.Call(hPNG)
		}
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(cfDIB, hDIB); r == 0 {
		procGlobalFree.Call(hDIB)
		if hPNG != 0 {
			procGlobalFree.Call(hPNG)
		}
		return errors.New("pano yazilamadi")
	}
	if hPNG != 0 {
		if r, _, _ := procSetClipboardData.Call(uintptr(pngFmt), hPNG); r == 0 {
			procGlobalFree.Call(hPNG) // DIB yazildi: gorsel yine de panoda (alfasiz)
		}
	}
	return nil
}

func (winClipboard) Set(text string) error {
	// Windows satir sonu CRLF'dir.
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
	u, err := windows.UTF16FromString(text) // NUL sonlu
	if err != nil {
		return err
	}
	bytes := uintptr(len(u) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, bytes)
	if h == 0 {
		return errors.New("bellek ayrilamadi")
	}
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		procGlobalFree.Call(h)
		return errors.New("bellek kilitlenemedi")
	}
	copy(unsafe.Slice((*uint16)(ptrOf(addr)), len(u)), u)
	procGlobalUnlock.Call(h)

	if err := openClipboard(); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("pano yazilamadi")
	}
	return nil // sahiplik sisteme gecti
}
