package tui

import "testing"

func TestTruncateDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 3, "hel"},
		{"日本語テキスト", 6, "日本語"},                 // 3 genis karakter = 6 sutun
		{"日本語テキスト", 5, "日本"},                  // 3. karakter sigmaz, yarim birakilmaz
		{"a日b", 2, "a"},                       // 日 icin yer yok
		{"\x1b[31m日本\x1b[0m", 3, "\x1b[31m日"}, // ANSI sayilmaz
		{"abc", 0, ""},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.w)
		if got != c.want {
			t.Errorf("Truncate(%q,%d)=%q want %q", c.in, c.w, got, c.want)
		}
		if Width(got) > c.w && c.w > 0 {
			t.Errorf("Truncate(%q,%d) genislik %d asildi", c.in, c.w, Width(got))
		}
	}
}
