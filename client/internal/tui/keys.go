// Package tui, zorven'in etkilesimli komutlari (zorven settings) icin
// bagimliliksiz, kucuk bir terminal katmanidir: ham kip, tus cozumleme ve
// satir ici yeniden cizim. Alternatif ekran KULLANILMAZ; arayuz komut
// satirinin altinda acilir ve kapaninca son hali ekranda kalir.
package tui

import "unicode/utf8"

// KeyKind, cozulmus tusun turu.
type KeyKind int

const (
	KeyRune KeyKind = iota // yazilabilir karakter(ler); Text dolu
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyTab
	KeyCtrlC
	KeyCtrlU
	KeyHome
	KeyEnd
	KeyUnknown
)

// Key, tek bir tus olayi. Yapistirmada Text birden cok karakter tasir.
type Key struct {
	Kind KeyKind
	Text string
}

// Decode, terminalden tek okumada gelen bayt obegini tus olaylarina cevirir.
//
// Tek basina gelen ESC (0x1b) Esc tusudur: terminaller kacis dizilerini tek
// yazimda gonderir, bu yuzden obek sinirlari Esc ile ok tusunu ayirmaya yeter.
func Decode(b []byte) []Key {
	var out []Key
	var text []byte
	flush := func() {
		if len(text) > 0 {
			out = append(out, Key{Kind: KeyRune, Text: string(text)})
			text = nil
		}
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			flush()
			if i+1 >= len(b) {
				out = append(out, Key{Kind: KeyEsc})
				i++
				continue
			}
			k, n := decodeEscape(b[i:])
			out = append(out, k)
			i += n
		case c == '\r' || c == '\n':
			flush()
			out = append(out, Key{Kind: KeyEnter})
			i++
			// \r\n tek Enter sayilir.
			if c == '\r' && i < len(b) && b[i] == '\n' {
				i++
			}
		case c == 0x7f || c == 0x08:
			flush()
			out = append(out, Key{Kind: KeyBackspace})
			i++
		case c == '\t':
			flush()
			out = append(out, Key{Kind: KeyTab})
			i++
		case c == 0x03:
			flush()
			out = append(out, Key{Kind: KeyCtrlC})
			i++
		case c == 0x15:
			flush()
			out = append(out, Key{Kind: KeyCtrlU})
			i++
		case c == 0x01:
			flush()
			out = append(out, Key{Kind: KeyHome})
			i++
		case c == 0x05:
			flush()
			out = append(out, Key{Kind: KeyEnd})
			i++
		case c < 0x20:
			flush()
			out = append(out, Key{Kind: KeyUnknown})
			i++
		default:
			r, n := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && n <= 1 {
				i++
				continue
			}
			text = append(text, b[i:i+n]...)
			i += n
		}
	}
	flush()
	return out
}

// decodeEscape, b[0]==ESC olan bir CSI/SS3 dizisini cozer; tuketilen bayt
// sayisini doner. Taninmayan diziler KeyUnknown olur ama tamamen tuketilir.
func decodeEscape(b []byte) (Key, int) {
	if b[1] != '[' && b[1] != 'O' {
		// Alt+tus: ESC + karakter. Esc olarak say, karakteri sonraki tura birak.
		return Key{Kind: KeyEsc}, 1
	}
	// Parametre baytlari (0x30-0x3f) ve ara baytlar (0x20-0x2f), son bayt 0x40-0x7e.
	j := 2
	for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f {
		j++
	}
	if j >= len(b) {
		return Key{Kind: KeyUnknown}, len(b)
	}
	final, params := b[j], string(b[2:j])
	n := j + 1
	switch final {
	case 'A':
		return Key{Kind: KeyUp}, n
	case 'B':
		return Key{Kind: KeyDown}, n
	case 'C':
		return Key{Kind: KeyRight}, n
	case 'D':
		return Key{Kind: KeyLeft}, n
	case 'H':
		return Key{Kind: KeyHome}, n
	case 'F':
		return Key{Kind: KeyEnd}, n
	case '~':
		switch params {
		case "1", "7":
			return Key{Kind: KeyHome}, n
		case "4", "8":
			return Key{Kind: KeyEnd}, n
		}
	}
	return Key{Kind: KeyUnknown}, n
}
