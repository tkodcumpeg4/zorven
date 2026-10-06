//go:build windows

package agent

import (
	"testing"
	"unsafe"
)

// REGRESYON: ok tuslari, F tuslari ve Home/End/Delete onceden keyToRune'dan 0
// donuyordu ve SESSIZCE YUTULUYORDU. Bu testin amaci o davranisin geri
// gelmemesi.
func TestSpecialKeysMapToVK(t *testing.T) {
	cases := map[string]uint16{
		"ArrowLeft":  vkLeft,
		"ArrowRight": vkRight,
		"ArrowUp":    vkUp,
		"ArrowDown":  vkDown,
		"Home":       vkHome,
		"End":        vkEnd,
		"PageUp":     vkPrior,
		"PageDown":   vkNext,
		"Delete":     vkDelete,
		"Insert":     vkInsert,
		"Escape":     vkEscape,
		"Tab":        vkTab,
		"Enter":      vkReturn,
		"Backspace":  vkBack,
		"Space":      vkSpace,
	}
	for code, want := range cases {
		got, ok := codeToVKShortcut(code)
		if !ok {
			t.Errorf("%q eslenemedi (onceki hata: sessizce yutuluyordu)", code)
			continue
		}
		if got != want {
			t.Errorf("codeToVKShortcut(%q) = 0x%02X, beklenen 0x%02X", code, got, want)
		}
	}
}

// Kisayollarin calismasi icin harf ve rakam tuslarinin da VK karsiligi olmali:
// Ctrl basiliyken Unicode yolu ise yaramaz.
func TestLetterAndDigitVK(t *testing.T) {
	if vk, ok := codeToVKShortcut("KeyC"); !ok || vk != 'C' {
		t.Errorf("KeyC = 0x%02X ok=%v, beklenen 0x43", vk, ok)
	}
	if vk, ok := codeToVKShortcut("KeyA"); !ok || vk != 'A' {
		t.Errorf("KeyA = 0x%02X ok=%v", vk, ok)
	}
	if vk, ok := codeToVKShortcut("Digit5"); !ok || vk != '5' {
		t.Errorf("Digit5 = 0x%02X ok=%v", vk, ok)
	}
	if vk, ok := codeToVKShortcut("Numpad7"); !ok || vk != vkNumpad0+7 {
		t.Errorf("Numpad7 = 0x%02X ok=%v", vk, ok)
	}
	// Gecersiz bicimler eslenmemelidir.
	for _, bad := range []string{"Key", "KeyAB", "Keya", "Digit", "Digit12", "", "Bilinmeyen"} {
		if _, ok := codeToVKShortcut(bad); ok {
			t.Errorf("codeToVKShortcut(%q) eslememeliydi", bad)
		}
	}
}

func TestFunctionKeys(t *testing.T) {
	if vk, ok := codeToVKShortcut("F1"); !ok || vk != vkF1 {
		t.Errorf("F1 = 0x%02X ok=%v", vk, ok)
	}
	if vk, ok := codeToVKShortcut("F5"); !ok || vk != vkF1+4 {
		t.Errorf("F5 = 0x%02X ok=%v", vk, ok)
	}
	if vk, ok := codeToVKShortcut("F12"); !ok || vk != vkF1+11 {
		t.Errorf("F12 = 0x%02X ok=%v", vk, ok)
	}
	// F24 son gecerli tus; F0 ve F25 gecersiz.
	if _, ok := codeToVKShortcut("F24"); !ok {
		t.Error("F24 gecerli olmaliydi")
	}
	for _, bad := range []string{"F0", "F25", "F99", "Fx"} {
		if _, ok := codeToVKShortcut(bad); ok {
			t.Errorf("%q gecersiz olmaliydi", bad)
		}
	}
}

// Ok tuslari ve Home/End numpad'deki esleriyle AYNI tarama kodunu paylasir;
// genisletilmis bayrak olmadan NumLock acikken rakam yazarlar.
func TestExtendedFlagOnNavigationKeys(t *testing.T) {
	for _, vk := range []uint16{vkLeft, vkRight, vkUp, vkDown, vkHome, vkEnd,
		vkPrior, vkNext, vkInsert, vkDelete} {
		if !extendedKeys[vk] {
			t.Errorf("0x%02X genisletilmis (extended) isaretlenmeliydi", vk)
		}
	}
	// Harf tuslari genisletilmis DEGIL.
	if extendedKeys['A'] {
		t.Error("harf tusu genisletilmis olmamali")
	}
}

// Tarayici delta birimi platformun centik birimine cevrilmeli; ham deger
// gondermek deltaMode=1'de (deltaY=3) neredeyse hic kaydirma yapmiyordu.
func TestWheelAmountConvertsToNotches(t *testing.T) {
	cases := []struct {
		delta float64
		mode  int
		want  int
		desc  string
	}{
		{100, 0, wheelDelta, "piksel modunda bir centik ~100px"},
		{-100, 0, -wheelDelta, "yukari yonde isaret korunur"},
		{200, 0, 2 * wheelDelta, "iki centik"},
		{3, 1, wheelDelta, "satir modunda bir centik 3 satir (ONCEDEN BOZUKTU)"},
		{-3, 1, -wheelDelta, "satir modu yukari"},
		{1, 2, wheelDelta, "sayfa modunda bir sayfa bir centik"},
	}
	for _, c := range cases {
		if got := wheelAmount(c.delta, c.mode); got != c.want {
			t.Errorf("wheelAmount(%v, %d) = %d, beklenen %d — %s",
				c.delta, c.mode, got, c.want, c.desc)
		}
	}

	// Sifir delta hicbir sey yapmamali.
	if got := wheelAmount(0, 0); got != 0 {
		t.Errorf("wheelAmount(0,0) = %d, beklenen 0", got)
	}
	// Cok kucuk hareketler sifira yuvarlanip KAYBOLMAMALI.
	if got := wheelAmount(0.1, 0); got == 0 {
		t.Error("kucuk pozitif delta kaybolmamaliydi")
	}
	if got := wheelAmount(-0.1, 0); got == 0 {
		t.Error("kucuk negatif delta kaybolmamaliydi")
	}
}

func TestSingleRune(t *testing.T) {
	// Yazilabilir tuslar tek rune'dur; ozel tuslar isimdir.
	if r := singleRune("a"); r != 'a' {
		t.Errorf("singleRune(\"a\") = %q", r)
	}
	if r := singleRune("ğ"); r != 'ğ' {
		t.Errorf("cok baytli UTF-8 tek rune sayilmaliydi: %q", r)
	}
	for _, s := range []string{"", "ArrowLeft", "ab", "F5"} {
		if r := singleRune(s); r != 0 {
			t.Errorf("singleRune(%q) = %q, beklenen 0", s, r)
		}
	}
}

// Eski dashboard code gondermez; en azindan ozel tuslar Key uzerinden
// calismaya devam etmeli.
func TestKeyToCodeFallback(t *testing.T) {
	cases := map[string]string{
		"ArrowLeft": "ArrowLeft",
		"Enter":     "Enter",
		"F5":        "F5",
		" ":         "Space",
		"a":         "KeyA",
		"Z":         "KeyZ",
		"7":         "Digit7",
	}
	for key, want := range cases {
		if got := keyToCode(key); got != want {
			t.Errorf("keyToCode(%q) = %q, beklenen %q", key, got, want)
		}
	}
	if got := keyToCode("ğ"); got != "" {
		t.Errorf("duzen disi harf icin code tahmin edilmemeli: %q", got)
	}
}

// Modifier tuslarinin KENDISI sarmalanmamali; aksi halde Ctrl'yi basmak icin
// Ctrl'yi basmak gerekirdi.
func TestModifierDetection(t *testing.T) {
	for _, vk := range []uint16{vkControl, vkShift, vkMenu, vkLControl,
		vkRControl, vkLShift, vkRShift, vkLMenu, vkRMenu, vkLWin, vkRWin} {
		if !isModifierVK(vk) {
			t.Errorf("0x%02X modifier sayilmaliydi", vk)
		}
	}
	for _, vk := range []uint16{'A', vkLeft, vkReturn, vkF1} {
		if isModifierVK(vk) {
			t.Errorf("0x%02X modifier SAYILMAMALIYDI", vk)
		}
	}
}

// INPUT yapisinin iki varyanti da ayni boyutta olmali; degilse SendInput
// sessizce hicbir sey yapmaz (en sinsi hata bicimi).
func TestInputStructSizesMatch(t *testing.T) {
	if got := inputSize; got != 40 {
		t.Fatalf("INPUT boyutu %d, 64-bit Windows'ta 40 olmali", got)
	}
	if got := unsafe.Sizeof(keybdInput{}); got != inputSize {
		t.Fatalf("keybdInput %d bayt, mouseInput %d bayt — esit olmali",
			got, inputSize)
	}
	// dwFlags offset'i kritik: yanlis olursa SendInput sessizce hicbir sey yapmaz.
	if off := unsafe.Offsetof(mouseInput{}.dwFlags); off != 20 {
		t.Fatalf("mouseInput.dwFlags offset %d, beklenen 20", off)
	}
	if off := unsafe.Offsetof(keybdInput{}.wVk); off != 8 {
		t.Fatalf("keybdInput.wVk offset %d, beklenen 8 (union 8 hizali)", off)
	}
}
