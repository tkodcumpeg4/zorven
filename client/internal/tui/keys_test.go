package tui

import (
	"reflect"
	"testing"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		in   string
		want []Key
	}{
		{"\x1b", []Key{{Kind: KeyEsc}}},
		{"\x1b[A\x1b[B", []Key{{Kind: KeyUp}, {Kind: KeyDown}}},
		{"\x1bOA", []Key{{Kind: KeyUp}}},
		{"\r", []Key{{Kind: KeyEnter}}},
		{"\r\n", []Key{{Kind: KeyEnter}}},
		{"ab\x7f", []Key{{Kind: KeyRune, Text: "ab"}, {Kind: KeyBackspace}}},
		{"zrv_live_çğ", []Key{{Kind: KeyRune, Text: "zrv_live_çğ"}}},
		{"\x03", []Key{{Kind: KeyCtrlC}}},
		{"\x1b[1~\x1b[4~", []Key{{Kind: KeyHome}, {Kind: KeyEnd}}},
		{"\x1b[1;5C", []Key{{Kind: KeyRight}}},
		{"x\x1b[200~", []Key{{Kind: KeyRune, Text: "x"}, {Kind: KeyUnknown}}},
	}
	for _, c := range cases {
		if got := Decode([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Decode(%q) = %+v, beklenen %+v", c.in, got, c.want)
		}
	}
}

func TestTruncateSkipsANSI(t *testing.T) {
	if got := Truncate("\x1b[1mabcdef\x1b[0m", 3); got != "\x1b[1mabc" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("çğüş", 2); got != "çğ" {
		t.Fatalf("Truncate unicode = %q", got)
	}
}
