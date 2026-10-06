//go:build windows

package agent

import "strings"

// Tarayici tuslarinin Windows sanal tus (VK) kodlarina eslenmesi.
//
// NEDEN VK, Unicode DEGIL:
// Onceki surum KEYEVENTF_UNICODE ile karakter enjekte ediyordu. Unicode
// enjeksiyonu bir KARAKTER uretir; modifier durumuyla BIRLESMEZ. Yani
// Ctrl basiliyken 'c' gondermek "kopyala" yapmaz, yalnizca 'c' harfi yazar.
// Ayrica ok tuslari, F tuslari, Home/End gibi tuslarin Unicode karsiligi
// olmadigi icin bunlar tamamen yutuluyordu.
//
// Cozum: fiziksel tus (KeyboardEvent.code) -> VK eslemesi. Yazilabilir
// karakterler icin Unicode yolu KORUNUR (bkz. screen_input_windows.go):
// boylece Turkce ğ/ş/ı gibi harfler klavye duzeninden bagimsiz dogru yazilir.

// Windows sanal tus kodlari (winuser.h).
const (
	vkBack     = 0x08
	vkTab      = 0x09
	vkReturn   = 0x0D
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12 // Alt
	vkPause    = 0x13
	vkCapital  = 0x14 // CapsLock
	vkEscape   = 0x1B
	vkSpace    = 0x20
	vkPrior    = 0x21 // PageUp
	vkNext     = 0x22 // PageDown
	vkEnd      = 0x23
	vkHome     = 0x24
	vkLeft     = 0x25
	vkUp       = 0x26
	vkRight    = 0x27
	vkDown     = 0x28
	vkSnapshot = 0x2C // PrintScreen
	vkInsert   = 0x2D
	vkDelete   = 0x2E
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkApps     = 0x5D // Menu (context)

	vkNumpad0  = 0x60
	vkMultiply = 0x6A
	vkAdd      = 0x6B
	vkSubtract = 0x6D
	vkDecimal  = 0x6E
	vkDivide   = 0x6F
	vkF1       = 0x70

	vkNumlock = 0x90
	vkScroll  = 0x91

	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5
)

// codeToVK, KeyboardEvent.code (FIZIKSEL tus) -> VK.
//
// Yalnizca duzenden bagimsiz olmasi gereken tuslar burada. Harf/rakam/sembol
// tuslari KASITLI olarak yok: onlar modifier yoksa Unicode ile yazilir
// (duzen dogru calissin diye), modifier varsa asagidaki codeToVKShortcut
// uzerinden VK'ye cevrilir.
var codeToVK = map[string]uint16{
	"Escape":      vkEscape,
	"Backspace":   vkBack,
	"Tab":         vkTab,
	"Enter":       vkReturn,
	"NumpadEnter": vkReturn,
	"Space":       vkSpace,
	"CapsLock":    vkCapital,

	"ArrowLeft":  vkLeft,
	"ArrowUp":    vkUp,
	"ArrowRight": vkRight,
	"ArrowDown":  vkDown,

	"Home":     vkHome,
	"End":      vkEnd,
	"PageUp":   vkPrior,
	"PageDown": vkNext,
	"Insert":   vkInsert,
	"Delete":   vkDelete,

	"PrintScreen": vkSnapshot,
	"ScrollLock":  vkScroll,
	"Pause":       vkPause,
	"NumLock":     vkNumlock,
	"ContextMenu": vkApps,

	"ShiftLeft":    vkLShift,
	"ShiftRight":   vkRShift,
	"ControlLeft":  vkLControl,
	"ControlRight": vkRControl,
	"AltLeft":      vkLMenu,
	"AltRight":     vkRMenu,
	"MetaLeft":     vkLWin,
	"MetaRight":    vkRWin,

	"NumpadDivide":   vkDivide,
	"NumpadMultiply": vkMultiply,
	"NumpadSubtract": vkSubtract,
	"NumpadAdd":      vkAdd,
	"NumpadDecimal":  vkDecimal,
}

// codeToVKShortcut, KISAYOL icin gereken ek eslemeler: harfler ve rakamlar.
//
// Ctrl/Alt/Win basiliyken Unicode yolu ise yaramaz, bu yuzden harf tuslarinin
// da VK karsiligi gerekir. Windows'ta 'A'-'Z' ve '0'-'9' VK kodlari ASCII ile
// AYNIDIR, o yuzden hesaplanabilir — tablo tutmaya gerek yok.
func codeToVKShortcut(code string) (uint16, bool) {
	if vk, ok := codeToVK[code]; ok {
		return vk, true
	}
	// F1..F24
	if n, ok := fnNumber(code); ok {
		return uint16(vkF1 + n - 1), true
	}
	// KeyA..KeyZ -> 'A'..'Z'
	if rest, ok := strings.CutPrefix(code, "Key"); ok && len(rest) == 1 {
		c := rest[0]
		if c >= 'A' && c <= 'Z' {
			return uint16(c), true
		}
	}
	// Digit0..Digit9 -> '0'..'9'
	if rest, ok := strings.CutPrefix(code, "Digit"); ok && len(rest) == 1 {
		c := rest[0]
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	// Numpad0..Numpad9
	if rest, ok := strings.CutPrefix(code, "Numpad"); ok && len(rest) == 1 {
		c := rest[0]
		if c >= '0' && c <= '9' {
			return uint16(vkNumpad0 + int(c-'0')), true
		}
	}
	return 0, false
}

// fnNumber, "F1".."F24" kodundan sayiyi cikarir.
func fnNumber(code string) (int, bool) {
	rest, ok := strings.CutPrefix(code, "F")
	if !ok || rest == "" || len(rest) > 2 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 || n > 24 {
		return 0, false
	}
	return n, true
}

// extendedKeys, KEYEVENTF_EXTENDEDKEY gerektiren tuslar.
//
// NEDEN GEREKLI: ok tuslari, Home/End/PageUp/PageDown/Insert/Delete ve sag
// Ctrl/Alt, numpad'deki ayni isimli tuslarla AYNI tarama kodunu paylasir.
// Genisletilmis bayrak olmadan Windows bunlari numpad tusu sanar; NumLock
// acikken ok tuslari rakam yazar.
var extendedKeys = map[uint16]bool{
	vkLeft: true, vkUp: true, vkRight: true, vkDown: true,
	vkHome: true, vkEnd: true, vkPrior: true, vkNext: true,
	vkInsert: true, vkDelete: true,
	vkRControl: true, vkRMenu: true,
	vkLWin: true, vkRWin: true, vkApps: true,
	vkDivide: true, vkNumlock: true, vkSnapshot: true,
}

// singleRune, tam olarak bir rune iceren stringi doner (yoksa 0).
//
// KeyboardEvent.key yazilabilir tuslarda tek karakterdir ("a", "Ğ", "@");
// ozel tuslarda isimdir ("ArrowLeft"), yani uzunluk kontrolu ikisini ayirir.
func singleRune(s string) rune {
	rs := []rune(s)
	if len(rs) == 1 {
		return rs[0]
	}
	return 0
}
