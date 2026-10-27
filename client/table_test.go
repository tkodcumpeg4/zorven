package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/tkodcumpeg4/zorven/client/internal/tui"
)

func TestTableMatchesTabwriterForASCII(t *testing.T) {
	rows := "NAME\tIP\tSTATUS\nlaptop\t100.64.0.1\tonline\nx\t100.64.0.22\toffline\n"
	var want bytes.Buffer
	tw := tabwriter.NewWriter(&want, 0, 4, 2, ' ', 0)
	fmt.Fprint(tw, rows)
	tw.Flush()
	var got bytes.Buffer
	tb := newTable(&got)
	fmt.Fprint(tb, rows)
	if err := tb.Flush(); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Fatalf("farkli:\n%q\n%q", got.String(), want.String())
	}
}

func TestTableAlignsJapanese(t *testing.T) {
	var got bytes.Buffer
	tb := newTable(&got)
	fmt.Fprint(tb, "\u30ce\u30fc\u30c9\tIP\t\u72b6\u614b\nlaptop\t100.64.0.1\t\u30aa\u30f3\u30e9\u30a4\u30f3\n\u30b5\u30fc\u30d0\u30fc\t100.64.0.2\t\u30aa\u30d5\u30e9\u30a4\u30f3\n")
	tb.Flush()
	lines := strings.Split(strings.TrimSpace(got.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("satir sayisi %d", len(lines))
	}
	// Ikinci sutun her satirda ayni gorunur konumda baslamali.
	var pos []int
	for _, l := range lines {
		i := strings.Index(l, "IP")
		if i < 0 {
			i = strings.Index(l, "100.")
		}
		pos = append(pos, tui.Width(l[:i]))
	}
	if pos[0] != pos[1] || pos[1] != pos[2] {
		t.Fatalf("hizasiz: %v\n%s", pos, got.String())
	}
	if pos[0] != 10 { // "\u30b5\u30fc\u30d0\u30fc" = 8 sutun + 2 bosluk
		t.Fatalf("beklenen 10, gelen %d", pos[0])
	}
}

func TestTableRaggedBlocks(t *testing.T) {
	out := renderTable("a\tb\nlonger\tc\n")
	if out != "a       b\nlonger  c\n" {
		t.Fatalf("%q", out)
	}
	if renderTable("") != "" {
		t.Fatal("bos")
	}
}
