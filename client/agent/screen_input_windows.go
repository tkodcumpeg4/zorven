//go:build windows

package agent

import (
	"unsafe"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/sys/windows"
)

// Windows fare/klavye enjeksiyonu — SAF SYSCALL, cgo YOK.
// user32.SendInput ile calisir; boylece istemci Windows'ta gcc olmadan derlenir.

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
	procSetCursorPos = user32.NewProc("SetCursorPos")
	procGetSystemMet = user32.NewProc("GetSystemMetrics")
)

const (
	smCXSCREEN = 0
	smCYSCREEN = 1

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfAbsolute   uint32 = 0x8000
	mouseeventfMove       uint32 = 0x0001
	mouseeventfLeftDown   uint32 = 0x0002
	mouseeventfLeftUp     uint32 = 0x0004
	mouseeventfRightDown  uint32 = 0x0008
	mouseeventfRightUp    uint32 = 0x0010
	mouseeventfMiddleDown uint32 = 0x0020
	mouseeventfMiddleUp   uint32 = 0x0040
	mouseeventfWheel      uint32 = 0x0800

	keyeventfKeyup   uint32 = 0x0002
	keyeventfUnicode uint32 = 0x0004
)

// mouseInput, 64-bit Windows INPUT (type + MOUSEINPUT union) yapisi = 40 bayt.
//
// KRITIK: union, type'tan sonra 4 bayt DOLGU ile offset 8'de baslar (union
// bir ULONG_PTR icerdigi icin 8 hizalidir). Bu dolgu olmadan Go alanlari 4
// bayt kaydirir, dwFlags yanlis offset'e duser ve SendInput hicbir sey yapmaz.
type mouseInput struct {
	typ       uint32
	_         uint32 // union'i offset 8'e hizala
	dx        int32
	dy        int32
	mouseData uint32
	dwFlags   uint32
	time      uint32
	_         uint32 // dwExtra'yi offset 32'ye (8 hiza) getir
	dwExtra   uintptr
}

// keybdInput, ayni INPUT yapisinin KEYBDINPUT varyanti; toplam 40 bayt olmali
// (INPUT boyutu en buyuk union uyesine, yani mouse'a gore 40'tir).
type keybdInput struct {
	typ     uint32
	_       uint32 // union hizalama dolgusu
	wVk     uint16
	wScan   uint16
	dwFlags uint32
	time    uint32
	_       uint32 // dwExtra 8 hizasi
	dwExtra uintptr
	_       uint64 // 40 bayta tamamla (INPUT boyutu = 40)
}

func screenMetrics() (int, int) {
	w, _, _ := procGetSystemMet.Call(uintptr(smCXSCREEN))
	h, _, _ := procGetSystemMet.Call(uintptr(smCYSCREEN))
	return int(w), int(h)
}

// applyInput, normalize (0..1) koordinatli olayi gercek ekrana uygular.
func applyInput(msg protocol.ScreenInput) {
	switch msg.Kind {
	case "mousemove":
		moveMouse(msg.X, msg.Y)
	case "mousedown":
		moveMouse(msg.X, msg.Y)
		mouseButton(msg.Button, true)
	case "mouseup":
		moveMouse(msg.X, msg.Y)
		mouseButton(msg.Button, false)
	case "wheel":
		wheel(msg.DeltaY)
	case "keydown":
		key(msg.Key, true)
	case "keyup":
		key(msg.Key, false)
	}
}

func moveMouse(nx, ny float64) {
	sw, sh := screenMetrics()
	if sw == 0 || sh == 0 {
		return
	}
	// SendInput mutlak koordinatlari 0..65535 normalize bekler.
	absX := int32(clamp01(nx) * 65535)
	absY := int32(clamp01(ny) * 65535)
	in := mouseInput{
		typ:     inputMouse,
		dx:      absX,
		dy:      absY,
		dwFlags: mouseeventfMove | mouseeventfAbsolute,
	}
	sendInput(unsafe.Pointer(&in))
}

func mouseButton(button int, down bool) {
	var flag uint32
	switch button {
	case 2: // sag (tarayici standardi: 2=sag)
		flag = boolPick(down, mouseeventfRightDown, mouseeventfRightUp)
	case 1: // orta
		flag = boolPick(down, mouseeventfMiddleDown, mouseeventfMiddleUp)
	default: // sol
		flag = boolPick(down, mouseeventfLeftDown, mouseeventfLeftUp)
	}
	in := mouseInput{typ: inputMouse, dwFlags: flag}
	sendInput(unsafe.Pointer(&in))
}

func wheel(deltaY float64) {
	// Tarayici wheel deltaY: asagi pozitif. Windows: yukari pozitif → isaret ters.
	amount := int32(-deltaY)
	if amount == 0 {
		if deltaY > 0 {
			amount = -120
		} else {
			amount = 120
		}
	}
	in := mouseInput{
		typ:       inputMouse,
		mouseData: uint32(amount),
		dwFlags:   mouseeventfWheel,
	}
	sendInput(unsafe.Pointer(&in))
}

func key(browserKey string, down bool) {
	r := keyToRune(browserKey)
	if r == 0 {
		return
	}
	flags := uint32(keyeventfUnicode)
	if !down {
		flags |= keyeventfKeyup
	}
	in := keybdInput{typ: inputKeyboard, wScan: uint16(r), dwFlags: flags}
	sendInput(unsafe.Pointer(&in))
}

func sendInput(p unsafe.Pointer) {
	// INPUT yapisi boyutu 64-bit'te 40 bayt.
	procSendInput.Call(1, uintptr(p), unsafe.Sizeof(mouseInput{}))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func boolPick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
