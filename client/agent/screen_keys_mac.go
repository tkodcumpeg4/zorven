package agent

import (
	"strings"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Tarayici tuslarinin macOS sanal tus kodlarina (kVK_*, Carbon HIToolbox
// Events.h) eslenmesi. Saf tablo oldugu icin her platformda derlenir/test edilir;
// kullanimi screen_input_darwin.go'da. Kodlar ANSI fiziksel konumdur.

// macCodeToKeycode, KeyboardEvent.code -> kVK.
var macCodeToKeycode = map[string]uint16{
	"KeyA": 0x00, "KeyS": 0x01, "KeyD": 0x02, "KeyF": 0x03, "KeyH": 0x04,
	"KeyG": 0x05, "KeyZ": 0x06, "KeyX": 0x07, "KeyC": 0x08, "KeyV": 0x09,
	"IntlBackslash": 0x0A, "KeyB": 0x0B, "KeyQ": 0x0C, "KeyW": 0x0D,
	"KeyE": 0x0E, "KeyR": 0x0F, "KeyY": 0x10, "KeyT": 0x11,
	"Digit1": 0x12, "Digit2": 0x13, "Digit3": 0x14, "Digit4": 0x15,
	"Digit6": 0x16, "Digit5": 0x17, "Equal": 0x18, "Digit9": 0x19,
	"Digit7": 0x1A, "Minus": 0x1B, "Digit8": 0x1C, "Digit0": 0x1D,
	"BracketRight": 0x1E, "KeyO": 0x1F, "KeyU": 0x20, "BracketLeft": 0x21,
	"KeyI": 0x22, "KeyP": 0x23, "Enter": 0x24, "KeyL": 0x25, "KeyJ": 0x26,
	"Quote": 0x27, "KeyK": 0x28, "Semicolon": 0x29, "Backslash": 0x2A,
	"Comma": 0x2B, "Slash": 0x2C, "KeyN": 0x2D, "KeyM": 0x2E, "Period": 0x2F,
	"Tab": 0x30, "Space": 0x31, "Backquote": 0x32, "Backspace": 0x33,
	"Escape": 0x35,

	"MetaRight": 0x36, "MetaLeft": 0x37, "ShiftLeft": 0x38, "CapsLock": 0x39,
	"AltLeft": 0x3A, "ControlLeft": 0x3B, "ShiftRight": 0x3C,
	"AltRight": 0x3D, "ControlRight": 0x3E,

	"NumpadDecimal": 0x41, "NumpadMultiply": 0x43, "NumpadAdd": 0x45,
	"NumLock": 0x47, "NumpadDivide": 0x4B, "NumpadEnter": 0x4C,
	"NumpadSubtract": 0x4E, "NumpadEqual": 0x51,
	"Numpad0": 0x52, "Numpad1": 0x53, "Numpad2": 0x54, "Numpad3": 0x55,
	"Numpad4": 0x56, "Numpad5": 0x57, "Numpad6": 0x58, "Numpad7": 0x59,
	"Numpad8": 0x5B, "Numpad9": 0x5C,

	"F1": 0x7A, "F2": 0x78, "F3": 0x63, "F4": 0x76, "F5": 0x60, "F6": 0x61,
	"F7": 0x62, "F8": 0x64, "F9": 0x65, "F10": 0x6D, "F11": 0x67, "F12": 0x6F,
	"F13": 0x69, "F14": 0x6B, "F15": 0x71, "F16": 0x6A, "F17": 0x40,
	"F18": 0x4F, "F19": 0x50, "F20": 0x5A,

	"ContextMenu": 0x6E, "Insert": 0x72, // Insert -> Help
	"Home": 0x73, "PageUp": 0x74, "Delete": 0x75, "End": 0x77, "PageDown": 0x79,
	"ArrowLeft": 0x7B, "ArrowRight": 0x7C, "ArrowDown": 0x7D, "ArrowUp": 0x7E,
}

// CGEventFlags maskeleri.
const (
	macFlagShift uint64 = 0x20000
	macFlagCtrl  uint64 = 0x40000
	macFlagAlt   uint64 = 0x80000
	macFlagCmd   uint64 = 0x100000
)

// macFlags, olayin modifier durumunu CGEventFlags'e cevirir. Tarayicinin Meta
// tusu macOS'ta Command'dir.
func macFlags(msg protocol.ScreenInput) uint64 {
	var f uint64
	if msg.Shift {
		f |= macFlagShift
	}
	if msg.Ctrl {
		f |= macFlagCtrl
	}
	if msg.Alt {
		f |= macFlagAlt
	}
	if msg.Meta {
		f |= macFlagCmd
	}
	return f
}

// macKeycode, bir girdi olayinin fiziksel tusunu bulur: once Code, yoksa Key'den
// tahmin (eski dashboard yedegi).
func macKeycode(msg protocol.ScreenInput) (uint16, bool) {
	if kc, ok := macCodeToKeycode[msg.Code]; ok {
		return kc, true
	}
	kc, ok := macCodeToKeycode[macKeyToCode(msg.Key)]
	return kc, ok
}

// macKeyToCode, KeyboardEvent.key'den makul bir code tahmin eder (yedek yol).
func macKeyToCode(key string) string {
	if key == " " {
		return "Space"
	}
	if _, ok := macCodeToKeycode[key]; ok {
		return key // "ArrowLeft", "Enter", "F5" ...
	}
	if rs := []rune(key); len(rs) == 1 {
		r := rs[0]
		switch {
		case r >= 'a' && r <= 'z':
			return "Key" + strings.ToUpper(string(r))
		case r >= 'A' && r <= 'Z':
			return "Key" + string(r)
		case r >= '0' && r <= '9':
			return "Digit" + string(r)
		}
	}
	return ""
}

// macIsModifierCode, modifier tusunun kendisi mi (Shift/Ctrl/Alt/Meta/CapsLock).
func macIsModifierCode(code string) bool {
	switch code {
	case "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
		"AltLeft", "AltRight", "MetaLeft", "MetaRight", "CapsLock":
		return true
	}
	return false
}

// macSingleRune, tam olarak bir rune iceren stringi doner (yoksa 0).
func macSingleRune(s string) rune {
	if rs := []rune(s); len(rs) == 1 {
		return rs[0]
	}
	return 0
}

// macScrollPixels, tarayici tekerlek deltasini CGEvent piksel birimine cevirir.
// Isaret TERSLENIR: tarayicida asagi/saga pozitif, CGEvent'te yukari/sola
// pozitiftir. Cok kucuk hareket kaybolmasin diye en az 1 birim verilir.
func macScrollPixels(delta float64, deltaMode int) int32 {
	if delta == 0 {
		return 0
	}
	px := delta
	switch deltaMode {
	case 1: // satir
		px = delta * 40
	case 2: // sayfa
		px = delta * 800
	}
	v := int32(-px)
	if v == 0 {
		if delta > 0 {
			return -1
		}
		return 1
	}
	return v
}

// CGEventType (fare) ve CGMouseButton degerleri.
const (
	macEvLeftDown  = 1
	macEvLeftUp    = 2
	macEvRightDown = 3
	macEvRightUp   = 4
	macEvMoved     = 5
	macEvLeftDrag  = 6
	macEvRightDrag = 7
	macEvOtherDown = 25
	macEvOtherUp   = 26
	macEvOtherDrag = 27

	macBtnLeft   = 0
	macBtnRight  = 1
	macBtnCenter = 2
)

// macCGButton, tarayici dugmesini (0 sol, 1 orta, 2 sag) CGMouseButton'a cevirir.
func macCGButton(b int) uint32 {
	switch b {
	case 2:
		return macBtnRight
	case 1:
		return macBtnCenter
	}
	return macBtnLeft
}

// macMouseEvent, CGEventType ve CGMouseButton secer. kind: down|up|move.
// held, move icin basili tarayici dugmesi (-1 yok => surukleme degil).
func macMouseEvent(kind string, button, held int) (evType, cgButton uint32) {
	pick := func(b int, l, r, o uint32) (uint32, uint32) {
		cg := macCGButton(b)
		switch cg {
		case macBtnRight:
			return r, cg
		case macBtnCenter:
			return o, cg
		}
		return l, cg
	}
	switch kind {
	case "down":
		return pick(button, macEvLeftDown, macEvRightDown, macEvOtherDown)
	case "up":
		return pick(button, macEvLeftUp, macEvRightUp, macEvOtherUp)
	}
	if held >= 0 {
		return pick(held, macEvLeftDrag, macEvRightDrag, macEvOtherDrag)
	}
	return macEvMoved, macBtnLeft
}
