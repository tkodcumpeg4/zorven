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
	procGetSystemMet = user32.NewProc("GetSystemMetrics")
)

const (
	// Birincil ekran (eski, tek monitor icin).
	smCXSCREEN = 0
	smCYSCREEN = 1
	// SANAL ekran: tum monitorleri kapsayan dikdortgen.
	smXVIRTUALSCREEN  = 76
	smYVIRTUALSCREEN  = 77
	smCXVIRTUALSCREEN = 78
	smCYVIRTUALSCREEN = 79

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfAbsolute    uint32 = 0x8000
	mouseeventfVirtualDesk uint32 = 0x4000
	mouseeventfMove        uint32 = 0x0001
	mouseeventfLeftDown    uint32 = 0x0002
	mouseeventfLeftUp      uint32 = 0x0004
	mouseeventfRightDown   uint32 = 0x0008
	mouseeventfRightUp     uint32 = 0x0010
	mouseeventfMiddleDown  uint32 = 0x0020
	mouseeventfMiddleUp    uint32 = 0x0040
	mouseeventfWheel       uint32 = 0x0800
	mouseeventfHWheel      uint32 = 0x01000

	keyeventfExtended uint32 = 0x0001
	keyeventfKeyup    uint32 = 0x0002
	keyeventfUnicode  uint32 = 0x0004

	// wheelDelta, Windows'un bir tekerlek centigi icin bekledigi birim.
	wheelDelta = 120
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

// inputSize, SendInput'a bildirilen INPUT boyutu. Iki varyant da ayni
// boyutta olmali; degilse SendInput sessizce hicbir sey yapmaz.
const inputSize = unsafe.Sizeof(mouseInput{})

func metric(i int) int {
	v, _, _ := procGetSystemMet.Call(uintptr(i))
	return int(int32(v))
}

// virtualScreen, TUM monitorleri kapsayan dikdortgeni doner.
//
// NEDEN: MOUSEEVENTF_ABSOLUTE tek basina koordinati BIRINCIL monitore esler.
// Ikincil ekran yakalanip tiklandiginda imlec yanlis ekrana giderdi.
// MOUSEEVENTF_VIRTUALDESK ile birlikte 0..65535 araligi sanal masaustunun
// tamamina eslenir.
func virtualScreen() (x, y, w, h int) {
	x = metric(smXVIRTUALSCREEN)
	y = metric(smYVIRTUALSCREEN)
	w = metric(smCXVIRTUALSCREEN)
	h = metric(smCYVIRTUALSCREEN)
	if w <= 0 || h <= 0 {
		// Sanal masaustu metrikleri yoksa birincil ekrana dus.
		return 0, 0, metric(smCXSCREEN), metric(smCYSCREEN)
	}
	return x, y, w, h
}

// displayRect, uygulanacak olayin ait oldugu monitorun sanal masaustundeki
// mutlak dikdortgeni. inputBounds, yakalanan monitorun sinirlarini tutar.
type displayRect struct{ x, y, w, h int }

// applyInput, normalize (0..1) koordinatli olayi gercek ekrana uygular.
//
// rect, YAKALANAN monitorun sanal masaustundeki konumudur: normalize koordinat
// o monitorun icinde yorumlanir, sonra sanal masaustune cevrilir. Aksi halde
// ikinci monitoru izlerken tiklamalar birinciye giderdi.
func applyInput(msg protocol.ScreenInput, rect displayRect) {
	switch msg.Kind {
	case "mousemove":
		moveMouse(msg.X, msg.Y, rect)
	case "mousedown":
		moveMouse(msg.X, msg.Y, rect)
		mouseButton(msg.Button, true)
	case "mouseup":
		moveMouse(msg.X, msg.Y, rect)
		mouseButton(msg.Button, false)
	case "wheel":
		wheel(msg.DeltaX, msg.DeltaY, msg.DeltaMode)
	case "keydown":
		keyEvent(msg, true)
	case "keyup":
		keyEvent(msg, false)
	}
}

func moveMouse(nx, ny float64, rect displayRect) {
	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		return
	}

	// Normalize koordinati once YAKALANAN monitorun piksel uzayina, sonra
	// sanal masaustunun 0..65535 uzayina cevir.
	rx, ry, rw, rh := rect.x, rect.y, rect.w, rect.h
	if rw <= 0 || rh <= 0 {
		rx, ry, rw, rh = vx, vy, vw, vh
	}
	px := float64(rx) + clamp01(nx)*float64(rw)
	py := float64(ry) + clamp01(ny)*float64(rh)

	// (px,py) sanal masaustu koordinati -> 0..65535.
	// 65535/(boyut-1) olceginin nedeni: Windows bu araligi kapsayici sayar;
	// (boyut) ile bolmek son piksel sutununa hic ulasamamaya yol acar.
	absX := int32((px - float64(vx)) * 65535 / float64(max(vw-1, 1)))
	absY := int32((py - float64(vy)) * 65535 / float64(max(vh-1, 1)))

	in := mouseInput{
		typ:     inputMouse,
		dx:      absX,
		dy:      absY,
		dwFlags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk,
	}
	sendInputs(in)
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
	sendInputs(mouseInput{typ: inputMouse, dwFlags: flag})
}

// wheel, tarayici tekerlek olayini Windows centik birimine cevirir.
//
// deltaMode: 0=piksel, 1=satir, 2=sayfa. Ham deltaY gondermek yanlisti:
// deltaMode=1'de deltaY=3 gelir ve 3/120 centik ≈ hic kaydirma demektir.
func wheel(dx, dy float64, deltaMode int) {
	if amount := wheelAmount(dy, deltaMode); amount != 0 {
		// Tarayici: asagi POZITIF. Windows: yukari pozitif → isaret ters.
		sendInputs(mouseInput{
			typ:       inputMouse,
			mouseData: uint32(int32(-amount)),
			dwFlags:   mouseeventfWheel,
		})
	}
	if amount := wheelAmount(dx, deltaMode); amount != 0 {
		// Yatay tekerlekte iki taraf da saga pozitif; ters cevirme YOK.
		sendInputs(mouseInput{
			typ:       inputMouse,
			mouseData: uint32(int32(amount)),
			dwFlags:   mouseeventfHWheel,
		})
	}
}

// wheelAmount, tarayici deltasini WHEEL_DELTA (120) birimine cevirir.
func wheelAmount(delta float64, deltaMode int) int {
	if delta == 0 {
		return 0
	}
	var notches float64
	switch deltaMode {
	case 1: // satir: tarayicilar centik basina ~3 satir bildirir
		notches = delta / 3
	case 2: // sayfa: bir sayfa bir centik sayilir
		notches = delta
	default: // piksel: tarayicilar centik basina ~100 piksel bildirir
		notches = delta / 100
	}
	amount := int(notches * wheelDelta)
	// Cok kucuk hareketler sifira yuvarlanip kaybolmasin: en az bir birim ver.
	if amount == 0 {
		if delta > 0 {
			return 1
		}
		return -1
	}
	return amount
}

// keyEvent, bir tus olayini uygular.
//
// Iki yol var ve secim MODIFIER durumuna baglidir:
//
//   - Ctrl/Alt/Win basili DEGIL ve tus yazilabilir bir karakter ise:
//     UNICODE yolu. Boylece Turkce ğ/ş/ı gibi harfler uzak makinenin klavye
//     duzeninden BAGIMSIZ dogru yazilir.
//   - Aksi halde (kisayol veya ozel tus): VK yolu. Unicode enjeksiyonu
//     modifier durumuyla birlesmedigi icin Ctrl+C'yi ancak boyle yapabiliriz.
func keyEvent(msg protocol.ScreenInput, down bool) {
	shortcut := msg.Ctrl || msg.Alt || msg.Meta

	if !shortcut {
		if r := singleRune(msg.Key); r != 0 && r != ' ' {
			// Bosluk VK yoluna birakilir: Unicode bosluk bazi uygulamalarda
			// tus olarak algilanmaz (or. oyunlarda ziplama).
			typeUnicode(r, down)
			return
		}
	}

	vk, ok := codeToVKShortcut(msg.Code)
	if !ok {
		// Code gelmediyse (cok eski dashboard) Key uzerinden son bir deneme.
		if vk, ok = codeToVKShortcut(keyToCode(msg.Key)); !ok {
			return
		}
	}

	// Modifier tuslarinin KENDISI geldiginde sarmalama yapma: sonsuz dongu
	// olurdu (Ctrl'yi basmak icin Ctrl'yi basmak).
	if isModifierVK(vk) {
		sendKey(vk, down)
		return
	}

	if !down {
		sendKey(vk, false)
		return
	}

	// Kisayol: modifier'lari bas, tusa bas, modifier'lari birak.
	//
	// Neden tek SendInput cagrisinda: araya baska bir uygulamanin girdisi
	// karismasin diye Windows dizinin tamamini atomik isler.
	var batch []keybdInput
	mods := heldModifiers(msg)
	for _, m := range mods {
		batch = append(batch, keyStroke(m, true))
	}
	batch = append(batch, keyStroke(vk, true), keyStroke(vk, false))
	for i := len(mods) - 1; i >= 0; i-- {
		batch = append(batch, keyStroke(mods[i], false))
	}
	sendKeyBatch(batch)
}

// heldModifiers, olayda basili bildirilen modifier'larin VK listesi.
//
// Shift BILEREK dahil: Ctrl+Shift+T gibi kisayollar icin gerekli. Shift'in tek
// basina yazilabilir karakteri etkilemesi ise Unicode yolunda zaten cozulmus
// durumda (tarayici bize buyuk harfi verir).
func heldModifiers(msg protocol.ScreenInput) []uint16 {
	var out []uint16
	if msg.Ctrl {
		out = append(out, vkControl)
	}
	if msg.Alt {
		out = append(out, vkMenu)
	}
	if msg.Shift {
		out = append(out, vkShift)
	}
	if msg.Meta {
		out = append(out, vkLWin)
	}
	return out
}

func isModifierVK(vk uint16) bool {
	switch vk {
	case vkShift, vkControl, vkMenu,
		vkLShift, vkRShift, vkLControl, vkRControl, vkLMenu, vkRMenu,
		vkLWin, vkRWin:
		return true
	}
	return false
}

// typeUnicode, tek bir karakteri Unicode olarak yazar (duzenden bagimsiz).
func typeUnicode(r rune, down bool) {
	// BMP disi karakterler (emoji vb.) iki UTF-16 birimi ister; yaygin
	// kullanimda gerekmedigi icin atlaniyor.
	if r > 0xFFFF {
		return
	}
	flags := keyeventfUnicode
	if !down {
		flags |= keyeventfKeyup
	}
	sendKeyBatch([]keybdInput{{typ: inputKeyboard, wScan: uint16(r), dwFlags: flags}})
}

func keyStroke(vk uint16, down bool) keybdInput {
	flags := uint32(0)
	if extendedKeys[vk] {
		flags |= keyeventfExtended
	}
	if !down {
		flags |= keyeventfKeyup
	}
	return keybdInput{typ: inputKeyboard, wVk: vk, dwFlags: flags}
}

func sendKey(vk uint16, down bool) {
	sendKeyBatch([]keybdInput{keyStroke(vk, down)})
}

func sendKeyBatch(in []keybdInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), inputSize)
}

func sendInputs(in ...mouseInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), inputSize)
}

// keyToCode, KeyboardEvent.key'den makul bir code tahmin eder.
//
// Yalnizca YEDEK yoldur: yeni dashboard her olayda code gonderir. Eski bir
// dashboard baglanirsa en azindan ozel tuslar calissin diye duruyor.
func keyToCode(key string) string {
	switch key {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown",
		"Home", "End", "PageUp", "PageDown", "Insert", "Delete",
		"Enter", "Tab", "Escape", "Backspace", "CapsLock",
		"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		return key
	case " ":
		return "Space"
	}
	if r := singleRune(key); r != 0 {
		switch {
		case r >= 'a' && r <= 'z':
			return "Key" + string(r-32)
		case r >= 'A' && r <= 'Z':
			return "Key" + string(r)
		case r >= '0' && r <= '9':
			return "Digit" + string(r)
		}
	}
	return ""
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
