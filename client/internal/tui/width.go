package tui

import (
	"strings"
	"unicode/utf8"
)

// IsWide, dogu asyasi genis/tam genislikli (CJK) karakterler icin true doner;
// bunlar terminalde iki sutun kaplar.
func IsWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115f) || (r >= 0x2e80 && r <= 0xa4cf) ||
		(r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6)
}

// Width, metnin terminaldeki gorunur sutun sayisidir: ANSI renk dizileri
// (ESC ... m) sayilmaz, genis karakterler 2 sayilir.
func Width(s string) int {
	vis := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := strings.IndexByte(s[i:], 'm')
			if j < 0 {
				break
			}
			i += j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		vis++
		if IsWide(r) {
			vis++
		}
		i += n
	}
	return vis
}

// PadRight, metni w gorunur sutuna bosluklarla tamamlar; tasarsa oldugu gibi birakir.
func PadRight(s string, w int) string {
	if vis := Width(s); vis < w {
		return s + strings.Repeat(" ", w-vis)
	}
	return s
}
