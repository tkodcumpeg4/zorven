//go:build windows

package agent

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

func newClipboardBackend() clipboardBackend {
	if h := serviceHelper(); h != nil {
		return helperClipboard{h} // servis: pano kullanici oturumundadir
	}
	return winClipboard{}
}

// clipboardSupported, bu platformda pano var mi (Hello'da ilan edilir).
func clipboardSupported() bool { return true }

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
