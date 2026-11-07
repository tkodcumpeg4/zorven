//go:build windows

package main

import (
	"context"
	"os"
	"sync"
	"unsafe"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW  = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }
type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

const (
	hwndTopmost   = ^uintptr(0) // HWND_TOPMOST (-1)
	hwndNoTopmost = ^uintptr(1) // HWND_NOTOPMOST (-2)
	swpNoActivate = 0x0010
	monNearest    = 0x00000002
)

// mainHWND, bu surece ait "Zorven" basliklI ust pencere. Tum ust pencereler
// taranir (gizli olanlar dahil: --hidden baslatmada pencere gizli yaratilir) ve
// surec kimligi eslesen ilki alinir. Eski FindWindowW yontemi ilk "Zorven"
// basliklI pencereyi alip surec kimligi tutmayinca vazgeciyordu: Gezgin'de
// "Zorven" klasoru (Program Files\Zorven) aciksa tepsi paneli konumlanamazdi.
func mainHWND() uintptr {
	hwndMu.Lock()
	defer hwndMu.Unlock()
	hwndFound = 0
	_ = windows.EnumWindows(hwndEnumCB, nil)
	return hwndFound
}

var (
	hwndMu    sync.Mutex
	hwndFound uintptr
	// Geri cagri bir kez olusturulur (NewCallback sayisi sinirli).
	hwndEnumCB = windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var wpid uint32
		_, _ = windows.GetWindowThreadProcessId(h, &wpid)
		if wpid != uint32(os.Getpid()) {
			return 1
		}
		buf := make([]uint16, 64)
		n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 && windows.UTF16ToString(buf[:n]) == "Zorven" {
			hwndFound = uintptr(h)
			return 0
		}
		return 1
	})
)

func platWindowBounds() (winBounds, bool) {
	h := mainHWND()
	if h == 0 {
		return winBounds{}, false
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return winBounds{}, false
	}
	return winBounds{int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top)}, true
}

func platSetBounds(ctx context.Context, b winBounds) {
	h := mainHWND()
	if h == 0 || b.W <= 0 || b.H <= 0 {
		wailsrt.WindowSetSize(ctx, 1040, 680)
		wailsrt.WindowCenter(ctx)
		return
	}
	procSetWindowPos.Call(h, hwndNoTopmost, uintptr(b.X), uintptr(b.Y), uintptr(b.W), uintptr(b.H), 0)
}

// platPlaceFlyout, paneli imlecin bulundugu ekranin calisma alaninin sag alt
// kosesine (gorev cubugunun hemen ustu) yerlestirir; olcekleme (DPI) hesaba katilir.
func platPlaceFlyout(ctx context.Context, w, hgt int) {
	h := mainHWND()
	if h == 0 {
		wailsrt.WindowSetSize(ctx, w, hgt)
		wailsrt.WindowSetAlwaysOnTop(ctx, true)
		return
	}
	dpi, _, _ := procGetDpiForWindow.Call(h)
	if dpi == 0 {
		dpi = 96
	}
	pw, ph := w*int(dpi)/96, hgt*int(dpi)/96
	margin := 12 * int(dpi) / 96

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	mon, _, _ := procMonitorFromPoint.Call(uintptr(*(*uint64)(unsafe.Pointer(&pt))), monNearest)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	wk := mi.Work
	x := int(wk.Right) - pw - margin
	y := int(wk.Bottom) - ph - margin
	// Gorev cubugu ustte/solda ise calisma alani zaten onu disarida birakir;
	// imlec ekranin sol yarisindaysa paneli imlecin yanina al.
	if int(pt.X) < int(wk.Left+wk.Right)/2 {
		x = int(wk.Left) + margin
	}
	if int(pt.Y) < int(wk.Top+wk.Bottom)/2 {
		y = int(wk.Top) + margin
	}
	// Gostermeden yerlestir (pencere gizli); gosterme OpenFlyout'ta, cizimden sonra.
	procSetWindowPos.Call(h, hwndTopmost, uintptr(x), uintptr(y), uintptr(pw), uintptr(ph), swpNoActivate)
}

// platFocus, pencereyi on plana alir.
func platFocus() {
	if h := mainHWND(); h != 0 {
		procSetForegroundWin.Call(h)
	}
}
