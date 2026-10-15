//go:build linux

package agent

import "testing"

func TestCodeToKeysym(t *testing.T) {
	cases := map[string]uint32{
		"KeyA": 'a', "KeyZ": 'z', "Digit5": '5', "F1": 0xffbe, "F24": 0xffbe + 23,
		"ArrowLeft": xkLeft, "Enter": xkReturn, "ShiftRight": xkShiftR, "MetaLeft": xkSuperL,
		"Numpad7": xkKP0 + 7, "Slash": '/', "CapsLock": xkCapsLock, "Space": ' ',
	}
	for code, want := range cases {
		got, ok := codeToKeysym(code)
		if !ok || got != want {
			t.Errorf("%s: got %#x ok=%v want %#x", code, got, ok, want)
		}
	}
	if _, ok := codeToKeysym("Bogus"); ok {
		t.Error("bilinmeyen code eslesti")
	}
	if k, ok := keyNameKeysym("F12"); !ok || k != xkF1+11 {
		t.Error("key adi F12")
	}
}

func TestRuneKeysym(t *testing.T) {
	if runeKeysym('a') != 'a' || runeKeysym('ü') != 0xfc {
		t.Error("latin1")
	}
	if runeKeysym('ğ') != 0x0100011f {
		t.Errorf("unicode: %#x", runeKeysym('ğ'))
	}
	if runeKeysym('\n') != 0 {
		t.Error("kontrol karakteri")
	}
}

func TestBuildKeymap(t *testing.T) {
	// min=8, 2 sutun: kc8=[a A], kc9=[b B], kc10=[a x]
	syms := []uint32{'a', 'A', 'b', 'B', 'a', 'x'}
	m := buildKeymap(8, 2, syms)
	if l := m['a']; l.code != 8 || l.col != 0 {
		t.Errorf("a: %+v", l)
	}
	if l := m['A']; l.code != 8 || l.col != 1 {
		t.Errorf("A: %+v", l)
	}
	if l := m['x']; l.code != 10 || l.col != 1 {
		t.Errorf("x: %+v", l)
	}
	if _, ok := m['q']; ok {
		t.Error("olmayan keysym")
	}
}

func TestScaleToDisplay(t *testing.T) {
	r := displayRect{x: 1920, y: 0, w: 1920, h: 1080}
	if x, y := scaleToDisplay(0, 0, r, 3840, 1080); x != 1920 || y != 0 {
		t.Errorf("sol ust %d,%d", x, y)
	}
	if x, y := scaleToDisplay(1, 1, r, 3840, 1080); x != 3839 || y != 1079 {
		t.Errorf("sag alt %d,%d", x, y)
	}
	if x, y := scaleToDisplay(2, -1, displayRect{}, 800, 600); x != 799 || y != 0 {
		t.Errorf("kenetleme %d,%d", x, y)
	}
}

func TestWheelClicks(t *testing.T) {
	if wheelClicks(0, 0) != 0 || wheelClicks(100, 0) != 1 || wheelClicks(-300, 0) != 3 ||
		wheelClicks(3, 1) != 1 || wheelClicks(1, 0) != 1 || wheelClicks(1e6, 0) != 20 {
		t.Error("tiklama sayisi")
	}
}

func TestBrowserButton(t *testing.T) {
	if browserButton(0) != 1 || browserButton(1) != 2 || browserButton(2) != 3 {
		t.Error("dugme")
	}
}
