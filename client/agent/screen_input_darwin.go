//go:build darwin

package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// macOS fare/klavye enjeksiyonu: CGEvent, saf Go (purego), cgo YOK.
//
// IZIN: Erisilebilirlik (AXIsProcessTrusted) gerekir. Yoksa olaylar sessizce
// yutulur; bu yuzden once kontrol edilir ve InputStateBlocked bildirilir.
// Sistem Ayarlari > Gizlilik ve Guvenlik > Erisilebilirlik'te istemci (ya da
// onu baslatan uygulama) isaretlenmelidir. Servis/LaunchDaemon olarak
// calisan istemcide etkilesimli oturum olmadigindan girdi calismaz.

const errMacAccessibility = "macOS Erisilebilirlik izni gerekli (Sistem Ayarlari > Gizlilik ve Guvenlik > Erisilebilirlik)"

// displayRect, yakalanan monitorun sanal masaustundeki (nokta) sinirlari.
type displayRect struct{ x, y, w, h int }

const (
	cgHIDEventTap        = 0 // kCGHIDEventTap
	cgScrollUnitPixel    = 0 // kCGScrollEventUnitPixel
	cgFieldClickState    = 1 // kCGMouseEventClickState
	cgFieldKeyAutorepeat = 8 // kCGKeyboardEventAutorepeat
)

var (
	inOnce    sync.Once
	inLoadErr error

	axIsProcessTrusted   func() uint8
	cgEventCreateMouse   func(src uintptr, typ uint32, pt cgPoint, button uint32) uintptr
	cgEventCreateScroll  func(src uintptr, units uint32, count uint32, w1, w2, w3 int32) uintptr
	cgEventCreateKey     func(src uintptr, keycode uint16, down bool) uintptr
	cgEventSetFlags      func(ev uintptr, flags uint64)
	cgEventSetIntField   func(ev uintptr, field uint32, v int64)
	cgEventKeySetUnicode func(ev uintptr, n uint64, s *uint16)
	cgEventPost          func(tap uint32, ev uintptr)
)

func macLoadInput() error {
	inOnce.Do(func() {
		if err := macLoadCapture(); err != nil && cfRelease == nil {
			inLoadErr = err // CoreFoundation (CFRelease) yuklenemedi
			return
		}
		cg, err := purego.Dlopen(macCoreGraphicsPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inLoadErr = fmt.Errorf("CoreGraphics yuklenemedi: %w", err)
			return
		}
		as, err := purego.Dlopen(macAppServicesPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inLoadErr = fmt.Errorf("ApplicationServices yuklenemedi: %w", err)
			return
		}
		for _, b := range []struct {
			lib  uintptr
			name string
			fn   any
		}{
			{as, "AXIsProcessTrusted", &axIsProcessTrusted},
			{cg, "CGEventCreateMouseEvent", &cgEventCreateMouse},
			{cg, "CGEventCreateScrollWheelEvent2", &cgEventCreateScroll},
			{cg, "CGEventCreateKeyboardEvent", &cgEventCreateKey},
			{cg, "CGEventSetFlags", &cgEventSetFlags},
			{cg, "CGEventSetIntegerValueField", &cgEventSetIntField},
			{cg, "CGEventKeyboardSetUnicodeString", &cgEventKeySetUnicode},
			{cg, "CGEventPost", &cgEventPost},
		} {
			if err := macSym(b.lib, b.name, b.fn); err != nil {
				inLoadErr = err
				return
			}
		}
	})
	return inLoadErr
}

// macInput, olaylar arasi durum (basili dugmeler, cift tik sayaci).
var macInput struct {
	mu       sync.Mutex
	held     int // basili tarayici dugmesi, -1 yok
	heldInit bool
	lastDown time.Time
	lastX    float64
	lastY    float64
	lastBtn  int
	clickCnt int64
}

const macDoubleClick = 500 * time.Millisecond

// applyInput, normalize (0..1) koordinatli olayi gercek ekrana uygular.
func applyInput(msg protocol.ScreenInput, rect displayRect) inputResult {
	if err := macLoadInput(); err != nil {
		return inputResult{State: protocol.InputStateError, Detail: err.Error()}
	}
	if axIsProcessTrusted() == 0 {
		return inputResult{State: protocol.InputStateBlocked, Detail: errMacAccessibility}
	}
	switch msg.Kind {
	case "mousemove":
		return macMouse("move", msg, rect)
	case "mousedown":
		return macMouse("down", msg, rect)
	case "mouseup":
		return macMouse("up", msg, rect)
	case "wheel":
		return macWheel(msg)
	case "keydown":
		return macKey(msg, true)
	case "keyup":
		return macKey(msg, false)
	}
	return inputResult{State: protocol.InputStateOK}
}

func macPost(ev uintptr) inputResult {
	if ev == 0 {
		return inputResult{State: protocol.InputStateError, Detail: "CGEvent olusturulamadi"}
	}
	cgEventPost(cgHIDEventTap, ev)
	cfRelease(ev)
	return inputResult{State: protocol.InputStateOK}
}

func macMouse(kind string, msg protocol.ScreenInput, rect displayRect) inputResult {
	// Normalize koordinat -> yakalanan monitorun global nokta uzayi.
	// rect bos ise (bilinmiyor) ana ekran bounds'u kullanilir.
	rx, ry, rw, rh := rect.x, rect.y, rect.w, rect.h
	if rw <= 0 || rh <= 0 {
		b := platBounds(0)
		rx, ry, rw, rh = b.Min.X, b.Min.Y, b.Dx(), b.Dy()
		if rw <= 0 || rh <= 0 {
			return inputResult{State: protocol.InputStateOK}
		}
	}
	pt := cgPoint{
		X: float64(rx) + clamp01Mac(msg.X)*float64(rw-1),
		Y: float64(ry) + clamp01Mac(msg.Y)*float64(rh-1),
	}

	macInput.mu.Lock()
	defer macInput.mu.Unlock()
	if !macInput.heldInit {
		macInput.held, macInput.heldInit = -1, true
	}
	held := macInput.held
	evType, cgBtn := macMouseEvent(kind, msg.Button, held)
	ev := cgEventCreateMouse(0, evType, pt, cgBtn)
	if ev != 0 && kind != "move" {
		cgEventSetFlags(ev, macFlags(msg))
	}
	switch kind {
	case "down":
		now := time.Now()
		near := absf(pt.X-macInput.lastX) < 5 && absf(pt.Y-macInput.lastY) < 5
		if macInput.lastBtn == msg.Button && near && now.Sub(macInput.lastDown) < macDoubleClick {
			macInput.clickCnt++
		} else {
			macInput.clickCnt = 1
		}
		macInput.lastDown, macInput.lastX, macInput.lastY, macInput.lastBtn = now, pt.X, pt.Y, msg.Button
		macInput.held = msg.Button
		if ev != 0 {
			cgEventSetIntField(ev, cgFieldClickState, macInput.clickCnt)
		}
	case "up":
		macInput.held = -1
		if ev != 0 {
			cgEventSetIntField(ev, cgFieldClickState, max(macInput.clickCnt, 1))
		}
	}
	return macPost(ev)
}

func macWheel(msg protocol.ScreenInput) inputResult {
	dy := macScrollPixels(msg.DeltaY, msg.DeltaMode)
	dx := macScrollPixels(msg.DeltaX, msg.DeltaMode)
	if dx == 0 && dy == 0 {
		return inputResult{State: protocol.InputStateOK}
	}
	return macPost(cgEventCreateScroll(0, cgScrollUnitPixel, 2, dy, dx, 0))
}

// macKey, tus olayini uygular.
//
// Modifier (Ctrl/Alt/Cmd) basili degilse ve tus yazilabilir tek karakterse
// olaya Unicode dizgisi eklenir: klavye duzeninden bagimsiz dogru karakter
// (Turkce g/s/i vb.) yazilir. Kisayollarda fiziksel tus kodu + modifier
// bayraklari kullanilir (Cmd+C gibi).
func macKey(msg protocol.ScreenInput, down bool) inputResult {
	kc, known := macKeycode(msg)
	shortcut := msg.Ctrl || msg.Alt || msg.Meta
	r := macSingleRune(msg.Key)
	typed := !shortcut && r != 0 && r != ' ' && r <= 0xFFFF
	if !known && !typed {
		return inputResult{State: protocol.InputStateOK}
	}
	ev := cgEventCreateKey(0, kc, down)
	if ev == 0 {
		return macPost(0)
	}
	cgEventSetFlags(ev, macFlags(msg))
	if typed {
		u := uint16(r)
		cgEventKeySetUnicode(ev, 1, &u)
	}
	if msg.Repeat && down {
		cgEventSetIntField(ev, cgFieldKeyAutorepeat, 1)
	}
	return macPost(ev)
}

func clamp01Mac(v float64) float64 {
	return min(max(v, 0), 1)
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
