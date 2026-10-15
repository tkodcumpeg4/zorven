package agent

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestMacPixelsToRGBAStride(t *testing.T) {
	// 2x2 piksel, stride 12 (4 bayt dolgu): satir basina 8 bayt veri + 4 dolgu.
	src := []byte{
		1, 2, 3, 9, 4, 5, 6, 9, 0xEE, 0xEE, 0xEE, 0xEE,
		7, 8, 9, 0, 10, 11, 12, 0,
	}
	img, err := macPixelsToRGBA(src, 2, 2, 12, true)
	if err != nil {
		t.Fatal(err)
	}
	// BGRA -> RGBA, alfa 255
	want := []byte{3, 2, 1, 255, 6, 5, 4, 255, 9, 8, 7, 255, 12, 11, 10, 255}
	for y := 0; y < 2; y++ {
		got := img.Pix[y*img.Stride : y*img.Stride+8]
		if string(got) != string(want[y*8:y*8+8]) {
			t.Fatalf("satir %d: %v != %v", y, got, want[y*8:y*8+8])
		}
	}
	rgba, _ := macPixelsToRGBA(src, 2, 2, 12, false)
	if rgba.Pix[0] != 1 || rgba.Pix[2] != 3 || rgba.Pix[3] != 255 {
		t.Fatalf("rgba sirasi yanlis: %v", rgba.Pix[:4])
	}
}

func TestMacPixelsToRGBAErrors(t *testing.T) {
	if _, err := macPixelsToRGBA(make([]byte, 8), 2, 2, 8, true); err == nil {
		t.Fatal("kisa veri hata vermeli")
	}
	if _, err := macPixelsToRGBA(make([]byte, 64), 4, 1, 8, true); err == nil {
		t.Fatal("kucuk stride hata vermeli")
	}
	if _, err := macPixelsToRGBA(nil, 0, 1, 4, true); err == nil {
		t.Fatal("sifir genislik hata vermeli")
	}
}

func TestMacPixelOrder(t *testing.T) {
	// Tipik ekran: kCGBitmapByteOrder32Little | kCGImageAlphaPremultipliedFirst
	if bgra, err := macPixelOrder(0x2002, 32); err != nil || !bgra {
		t.Fatalf("BGRA bekleniyordu: %v %v", bgra, err)
	}
	if bgra, err := macPixelOrder(0x2006, 32); err != nil || !bgra {
		t.Fatalf("BGRX bekleniyordu: %v %v", bgra, err)
	}
	if bgra, err := macPixelOrder(0x0001, 32); err != nil || bgra {
		t.Fatalf("RGBA bekleniyordu: %v %v", bgra, err)
	}
	if _, err := macPixelOrder(0x2002, 64); err == nil {
		t.Fatal("64 bit hata vermeli")
	}
	if _, err := macPixelOrder(0, 32); err == nil {
		t.Fatal("alfasiz bilinmeyen bicim hata vermeli")
	}
}

func TestMacKeycodes(t *testing.T) {
	cases := map[string]uint16{
		"KeyA": 0x00, "KeyC": 0x08, "KeyV": 0x09, "KeyZ": 0x06,
		"Digit1": 0x12, "Digit0": 0x1D, "Enter": 0x24, "Space": 0x31,
		"Escape": 0x35, "Backspace": 0x33, "ArrowLeft": 0x7B, "ArrowUp": 0x7E,
		"F1": 0x7A, "F12": 0x6F, "MetaLeft": 0x37, "ShiftLeft": 0x38,
		"Numpad0": 0x52, "Delete": 0x75,
	}
	for code, want := range cases {
		got, ok := macKeycode(protocol.ScreenInput{Code: code})
		if !ok || got != want {
			t.Errorf("%s: %#x ok=%v, beklenen %#x", code, got, ok, want)
		}
	}
	// Tum harf tuslari tabloda ve benzersiz olmali.
	seen := map[uint16]string{}
	for c := 'A'; c <= 'Z'; c++ {
		code := "Key" + string(c)
		kc, ok := macCodeToKeycode[code]
		if !ok {
			t.Fatalf("%s eksik", code)
		}
		if prev, dup := seen[kc]; dup {
			t.Fatalf("%s ve %s ayni kod %#x", code, prev, kc)
		}
		seen[kc] = code
	}
	// Yedek yol: yalniz Key.
	if kc, ok := macKeycode(protocol.ScreenInput{Key: "a"}); !ok || kc != 0 {
		t.Errorf("key=a: %#x %v", kc, ok)
	}
	if kc, ok := macKeycode(protocol.ScreenInput{Key: "ArrowDown"}); !ok || kc != 0x7D {
		t.Errorf("key=ArrowDown: %#x %v", kc, ok)
	}
	if _, ok := macKeycode(protocol.ScreenInput{Code: "Bilinmeyen"}); ok {
		t.Error("bilinmeyen kod eslenmemeli")
	}
}

func TestMacFlagsAndModifier(t *testing.T) {
	f := macFlags(protocol.ScreenInput{Meta: true, Shift: true})
	if f != macFlagCmd|macFlagShift {
		t.Fatalf("bayrak %#x", f)
	}
	if !macIsModifierCode("MetaRight") || macIsModifierCode("KeyA") {
		t.Fatal("modifier tespiti yanlis")
	}
}

func TestMacScrollPixels(t *testing.T) {
	if v := macScrollPixels(100, 0); v != -100 {
		t.Errorf("asagi: %d", v)
	}
	if v := macScrollPixels(-3, 1); v != 120 {
		t.Errorf("yukari satir: %d", v)
	}
	if v := macScrollPixels(0.2, 0); v != -1 {
		t.Errorf("minimum: %d", v)
	}
	if v := macScrollPixels(0, 0); v != 0 {
		t.Errorf("sifir: %d", v)
	}
}

func TestMacMouseEvent(t *testing.T) {
	if et, b := macMouseEvent("down", 0, -1); et != macEvLeftDown || b != macBtnLeft {
		t.Errorf("sol down: %d %d", et, b)
	}
	if et, b := macMouseEvent("up", 2, -1); et != macEvRightUp || b != macBtnRight {
		t.Errorf("sag up: %d %d", et, b)
	}
	if et, b := macMouseEvent("down", 1, -1); et != macEvOtherDown || b != macBtnCenter {
		t.Errorf("orta down: %d %d", et, b)
	}
	if et, _ := macMouseEvent("move", 0, -1); et != macEvMoved {
		t.Errorf("move: %d", et)
	}
	if et, b := macMouseEvent("move", 0, 2); et != macEvRightDrag || b != macBtnRight {
		t.Errorf("sag drag: %d %d", et, b)
	}
}
