//go:build windows

package agent

// keyToRune, tarayici KeyboardEvent.key degerini Unicode rune'a cevirir.
//
// Unicode SendInput (keyeventfUnicode) kullandigimiz icin cogu tus dogrudan
// karakter olarak gonderilebilir; ozel tuslar (Enter, Backspace...) ise VK
// yerine karsilik gelen kontrol karakterine/rune'a eslenir. Bu MVP kapsaminda
// yaygin tuslari kapsar.
func keyToRune(key string) rune {
	// Tek karakterlik tuslar (harf, rakam, sembol) dogrudan.
	if r := singleRune(key); r != 0 {
		return r
	}
	switch key {
	case "Enter":
		return '\r'
	case "Tab":
		return '\t'
	case "Backspace":
		return '\b'
	case "Escape":
		return 27
	case " ", "Spacebar":
		return ' '
	}
	// Ok tuslari, fonksiyon tuslari vb. Unicode ile ifade edilemez; MVP'de atlanir.
	return 0
}

func singleRune(s string) rune {
	// Tam olarak bir rune iceren string mi?
	rs := []rune(s)
	if len(rs) == 1 {
		return rs[0]
	}
	return 0
}
