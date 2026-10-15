//go:build windows

package agent

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows imlec kaynagi — saf syscall, cgo YOK.
// Konum/gorunurluk: user32.GetCursorInfo. Bicim: GetIconInfo + GetDIBits.

var (
	procGetCursorInfo = user32.NewProc("GetCursorInfo")
	procGetIconInfo   = user32.NewProc("GetIconInfo")
	procGetDC         = user32.NewProc("GetDC")
	procReleaseDC     = user32.NewProc("ReleaseDC")

	gdi32            = windows.NewLazySystemDLL("gdi32.dll")
	procGetObjectW   = gdi32.NewProc("GetObjectW")
	procGetDIBits    = gdi32.NewProc("GetDIBits")
	procDeleteObject = gdi32.NewProc("DeleteObject")
)

const cursorShowing = 0x00000001 // CURSOR_SHOWING

// cursorInfo, CURSORINFO.
type cursorInfo struct {
	cbSize   uint32
	flags    uint32
	hCursor  uintptr
	ptX, ptY int32
}

// iconInfo, ICONINFO.
type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

// bitmapStruct, BITMAP (64-bit duzeni).
type bitmapStruct struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

// bitmapInfoHeader, BITMAPINFOHEADER (40 bayt).
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type winCursorSource struct{}

func newCursorSource() cursorSource { return winCursorSource{} }

// cursorSupported, bu platformda imlec katmani var mi (Hello'da ilan edilir).
func cursorSupported() bool { return true }

func (winCursorSource) Close() {}

func (winCursorSource) Poll() (cursorState, error) {
	ci := cursorInfo{}
	ci.cbSize = uint32(unsafe.Sizeof(ci))
	r, _, e := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	if r == 0 {
		// Girdi masaustu degismis (kilit ekrani/UAC) olabilir: gizli say.
		return cursorState{}, e
	}
	return cursorState{
		X: int(ci.ptX), Y: int(ci.ptY),
		Visible: ci.flags&cursorShowing != 0 && ci.hCursor != 0,
		Handle:  ci.hCursor,
	}, nil
}

func (winCursorSource) Shape(handle uintptr) (*cursorShape, bool) {
	if handle == 0 {
		return nil, false
	}
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(handle, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil, false
	}
	defer func() {
		if ii.hbmMask != 0 {
			procDeleteObject.Call(ii.hbmMask)
		}
		if ii.hbmColor != 0 {
			procDeleteObject.Call(ii.hbmColor)
		}
	}()

	var bm bitmapStruct
	ref := ii.hbmColor
	if ref == 0 {
		ref = ii.hbmMask
	}
	if r, _, _ := procGetObjectW.Call(ref, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, false
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if ii.hbmColor == 0 {
		h /= 2 // tek renkli: maske iki yari yuksekliktedir (AND + XOR)
	}
	// Asiri buyuk/bozuk bitmap'e karsi savunma (rastgele bellek ayirmayalim).
	if w <= 0 || h <= 0 || w > 512 || h > 512 {
		return nil, false
	}

	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil, false
	}
	defer procReleaseDC.Call(0, dc)

	var color []byte
	if ii.hbmColor != 0 {
		var err error
		if color, err = dibits32(dc, ii.hbmColor, w, h); err != nil {
			return nil, false
		}
	}
	maskH := h
	if ii.hbmColor == 0 {
		maskH = 2 * h
	}
	mask, err := dibits32(dc, ii.hbmMask, w, maskH)
	if err != nil {
		mask = nil
		if color == nil {
			return nil, false
		}
	}
	s := cursorFromBitmaps(w, h, color, mask)
	if s == nil {
		return nil, false
	}
	s.HotX = min(max(int(ii.xHotspot), 0), w-1)
	s.HotY = min(max(int(ii.yHotspot), 0), h-1)
	return s, true
}

// dibits32, bitmap'i ustten asagi 32bpp BGRA olarak okur.
func dibits32(dc, hbm uintptr, w, h int) ([]byte, error) {
	hdr := bitmapInfoHeader{
		biSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), biWidth: int32(w), biHeight: -int32(h),
		biPlanes: 1, biBitCount: 32,
	}
	buf := make([]byte, w*h*4)
	r, _, _ := procGetDIBits.Call(dc, hbm, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&hdr)), 0 /* DIB_RGB_COLORS */)
	if r == 0 {
		return nil, errors.New("GetDIBits basarisiz")
	}
	return buf, nil
}
