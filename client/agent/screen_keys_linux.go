//go:build linux

package agent

import "strings"

// Tarayici KeyboardEvent.code / key -> X keysym eslemesi. Saf veri ve saf
// fonksiyonlar: X sunucusu olmadan test edilir.

// xk sabitleri (X11 keysymdef.h).
const (
	xkBackSpace   = 0xff08
	xkTab         = 0xff09
	xkReturn      = 0xff0d
	xkPause       = 0xff13
	xkScrollLock  = 0xff14
	xkEscape      = 0xff1b
	xkHome        = 0xff50
	xkLeft        = 0xff51
	xkUp          = 0xff52
	xkRight       = 0xff53
	xkDown        = 0xff54
	xkPageUp      = 0xff55
	xkPageDown    = 0xff56
	xkEnd         = 0xff57
	xkPrint       = 0xff61
	xkInsert      = 0xff63
	xkMenu        = 0xff67
	xkNumLock     = 0xff7f
	xkKPEnter     = 0xff8d
	xkKPMultiply  = 0xffaa
	xkKPAdd       = 0xffab
	xkKPSubtract  = 0xffad
	xkKPDecimal   = 0xffae
	xkKPDivide    = 0xffaf
	xkKP0         = 0xffb0
	xkKPEqual     = 0xffbd
	xkF1          = 0xffbe
	xkShiftL      = 0xffe1
	xkShiftR      = 0xffe2
	xkControlL    = 0xffe3
	xkControlR    = 0xffe4
	xkCapsLock    = 0xffe5
	xkAltL        = 0xffe9
	xkAltR        = 0xffea
	xkSuperL      = 0xffeb
	xkSuperR      = 0xffec
	xkDelete      = 0xffff
	xkISOLevel3   = 0xfe03
	xkUnicodeBase = 0x01000000
)

var codeKeysyms = buildCodeKeysyms()

func buildCodeKeysyms() map[string]uint32 {
	m := map[string]uint32{
		"Space": 0x20, "Backspace": xkBackSpace, "Tab": xkTab, "Enter": xkReturn,
		"Escape": xkEscape, "Pause": xkPause, "ScrollLock": xkScrollLock,
		"PrintScreen": xkPrint, "Insert": xkInsert, "Delete": xkDelete,
		"Home": xkHome, "End": xkEnd, "PageUp": xkPageUp, "PageDown": xkPageDown,
		"ArrowLeft": xkLeft, "ArrowUp": xkUp, "ArrowRight": xkRight, "ArrowDown": xkDown,
		"ContextMenu": xkMenu, "CapsLock": xkCapsLock, "NumLock": xkNumLock,
		"ShiftLeft": xkShiftL, "ShiftRight": xkShiftR,
		"ControlLeft": xkControlL, "ControlRight": xkControlR,
		"AltLeft": xkAltL, "AltRight": xkAltR,
		"MetaLeft": xkSuperL, "MetaRight": xkSuperR, "OSLeft": xkSuperL, "OSRight": xkSuperR,
		"Backquote": '`', "Minus": '-', "Equal": '=', "BracketLeft": '[',
		"BracketRight": ']', "Backslash": '\\', "Semicolon": ';', "Quote": '\'',
		"Comma": ',', "Period": '.', "Slash": '/', "IntlBackslash": '<',
		"NumpadEnter": xkKPEnter, "NumpadAdd": xkKPAdd, "NumpadSubtract": xkKPSubtract,
		"NumpadMultiply": xkKPMultiply, "NumpadDivide": xkKPDivide,
		"NumpadDecimal": xkKPDecimal, "NumpadEqual": xkKPEqual,
	}
	for c := 'A'; c <= 'Z'; c++ {
		m["Key"+string(c)] = uint32(c - 'A' + 'a')
	}
	for d := 0; d <= 9; d++ {
		m["Digit"+string(rune('0'+d))] = uint32('0' + d)
		m["Numpad"+string(rune('0'+d))] = uint32(xkKP0 + d)
	}
	for n := 1; n <= 24; n++ {
		m["F"+smallItoa(n)] = uint32(xkF1 + n - 1)
	}
	return m
}

func smallItoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// codeToKeysym, fiziksel tus adini (KeyboardEvent.code) keysym'e cevirir.
func codeToKeysym(code string) (uint32, bool) {
	k, ok := codeKeysyms[code]
	return k, ok
}

// keyNameKeysym, KeyboardEvent.key adlarini (code gelmeyen eski panel) cevirir.
// key adlari code adlariyla ayni olanlar ve F1..F24 desteklenir.
func keyNameKeysym(key string) (uint32, bool) {
	switch key {
	case "Enter", "Backspace", "Tab", "Escape", "Delete", "Insert", "Home", "End",
		"PageUp", "PageDown", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown",
		"CapsLock", "ContextMenu":
		return codeToKeysym(key)
	}
	if strings.HasPrefix(key, "F") && len(key) > 1 {
		return codeToKeysym(key)
	}
	return 0, false
}

// runeKeysym, bir karakterin X keysym'ini verir: Latin-1 dogrudan, digerleri
// Unicode keysym (0x01000000 + kod noktasi). Kontrol karakteri icin 0.
func runeKeysym(r rune) uint32 {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	if r < 0x100 {
		return uint32(r)
	}
	return xkUnicodeBase | uint32(r)
}

// singleRuneKey, key tek bir yazilabilir karakterse onu doner.
func singleRuneKey(key string) rune {
	rs := []rune(key)
	if len(rs) != 1 {
		return 0
	}
	return rs[0]
}

// keyLoc, bir keysym'in klavye tablosundaki yeri.
type keyLoc struct {
	code byte // X keycode
	col  int  // 0=taban, 1=shift, ...
}

// buildKeymap, GetKeyboardMapping yanitindan keysym -> konum tablosu kurar.
// Ayni keysym birden cok yerdeyse DUSUK sutun, sonra DUSUK keycode kazanir.
func buildKeymap(minKeycode byte, perCode int, syms []uint32) map[uint32]keyLoc {
	m := make(map[uint32]keyLoc, len(syms))
	if perCode <= 0 {
		return m
	}
	for i, s := range syms {
		if s == 0 {
			continue
		}
		code := int(minKeycode) + i/perCode
		if code > 255 {
			break
		}
		loc := keyLoc{code: byte(code), col: i % perCode}
		if old, ok := m[s]; ok && (old.col < loc.col || (old.col == loc.col && old.code <= loc.code)) {
			continue
		}
		m[s] = loc
	}
	return m
}

// scaleToDisplay, normalize (0..1) koordinati yakalanan monitorun mutlak
// piksel konumuna cevirir. rect bos ise kok pencere boyutu (rootW x rootH) esas.
func scaleToDisplay(nx, ny float64, rect displayRect, rootW, rootH int) (int, int) {
	rx, ry, rw, rh := rect.x, rect.y, rect.w, rect.h
	if rw <= 0 || rh <= 0 {
		rx, ry, rw, rh = 0, 0, rootW, rootH
	}
	px := rx + int(clampUnit(nx)*float64(max(rw-1, 0))+0.5)
	py := ry + int(clampUnit(ny)*float64(max(rh-1, 0))+0.5)
	return px, py
}

func clampUnit(v float64) float64 {
	if v < 0 || v != v {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// wheelClicks, tarayici tekerlek deltasini X dugme tiklama sayisina cevirir
// (en az 1, en cok 20; yon cagirandadir). delta 0 ise 0.
func wheelClicks(delta float64, deltaMode int) int {
	if delta < 0 {
		delta = -delta
	}
	if delta == 0 {
		return 0
	}
	var notches float64
	switch deltaMode {
	case 1:
		notches = delta / 3
	case 2:
		notches = delta
	default:
		notches = delta / 100
	}
	n := int(notches + 0.5)
	if n < 1 {
		n = 1
	}
	if n > 20 {
		n = 20
	}
	return n
}
