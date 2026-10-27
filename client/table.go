package main

import (
	"bytes"
	"io"
	"strings"

	"github.com/tkodcumpeg4/zorven/client/internal/tui"
)

// table, text/tabwriter'in yerini alan kucuk tablo yazicisidir: sutunlari
// gorunur genislige gore hizalar (Japonca/CJK karakterler iki sutun sayilir).
// Hucreler "\t" ile ayrilir; her satirin son hucresi doldurulmaz. Sutun
// araligi 2 bosluktur; ASCII icin cikti tabwriter(0, 4, 2, ' ', 0) ile aynidir.
type table struct {
	w   io.Writer
	buf bytes.Buffer
}

func newTable(w io.Writer) *table { return &table{w: w} }

func (t *table) Write(p []byte) (int, error) { return t.buf.Write(p) }

// Flush, biriken satirlari hizalayip yazar.
func (t *table) Flush() error {
	text := t.buf.String()
	t.buf.Reset()
	_, err := io.WriteString(t.w, renderTable(text))
	return err
}

const tableGap = 2

// renderTable, sekmeyle ayrilmis satirlari hizalar. Bir sutunun genisligi, o
// sutunu sekmeyle sonlandirilmis hucre olarak iceren ardisik satirlardan
// hesaplanir (tabwriter ile ayni kural).
func renderTable(text string) string {
	if text == "" {
		return ""
	}
	trailingNL := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	cells := make([][]string, len(lines))
	widths := make([][]int, len(lines))
	for i, l := range lines {
		cells[i] = strings.Split(l, "\t") // son oge sekmesiz kuyruk
		widths[i] = make([]int, len(cells[i])-1)
	}
	for col := 0; ; col++ {
		found := false
		for i := 0; i < len(cells); {
			if col >= len(cells[i])-1 {
				i++
				continue
			}
			found = true
			j, widest := i, 0
			for j < len(cells) && col < len(cells[j])-1 {
				if w := tui.Width(cells[j][col]); w > widest {
					widest = w
				}
				j++
			}
			for k := i; k < j; k++ {
				widths[k][col] = widest + tableGap
			}
			i = j
		}
		if !found {
			break
		}
	}
	var sb strings.Builder
	for i, row := range cells {
		for c, cell := range row {
			if c < len(row)-1 {
				sb.WriteString(tui.PadRight(cell, widths[i][c]))
			} else {
				sb.WriteString(cell)
			}
		}
		if i < len(cells)-1 || trailingNL {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
