package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrNotTerminal, girdi ya da cikti bir terminal degilse doner (boru, CI).
var ErrNotTerminal = errors.New("etkilesimli arayuz bir terminal gerektirir")

// Terminal, ham kipteki stdin/stdout cifti.
type Terminal struct {
	in       *os.File
	out      io.Writer
	state    *term.State
	restore  func()
	prev     int // son cizimde yazilan satir sayisi
	keys     chan []Key
	readErrs chan error
}

// Open, stdin'i ham kipe alir, (Windows'ta) VT ciktisini acar ve imleci gizler.
// Kapatmak icin Close cagrilmali; panik durumunda da cagrilsin diye defer edin.
func Open() (*Terminal, error) {
	in, out := os.Stdin, os.Stdout
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil, ErrNotTerminal
	}
	restoreVT, err := enableVT(out)
	if err != nil {
		return nil, err
	}
	st, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		restoreVT()
		return nil, err
	}
	t := &Terminal{in: in, out: out, state: st, restore: restoreVT,
		keys: make(chan []Key, 16), readErrs: make(chan error, 1)}
	fmt.Fprint(out, "\x1b[?25l")
	go t.readLoop()
	return t, nil
}

func (t *Terminal) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := t.in.Read(buf)
		if n > 0 {
			t.keys <- Decode(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			t.readErrs <- err
			return
		}
	}
}

// Events, tus obekleri ve okuma hatasi kanallari (baska olaylarla birlikte
// select edilebilsin diye).
func (t *Terminal) Events() (<-chan []Key, <-chan error) { return t.keys, t.readErrs }

// Width, terminal genisligi (bilinmiyorsa 80).
func (t *Terminal) Width() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// Draw, onceki cizimi silip satirlari yeniden yazar. Satirlar genislige gore
// kirpilir ki sarma satir sayisini bozmasin.
func (t *Terminal) Draw(lines []string) {
	var sb strings.Builder
	if t.prev > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", t.prev)
	}
	sb.WriteString("\r\x1b[J")
	w := t.Width() - 1
	for _, l := range lines {
		sb.WriteString(Truncate(l, w))
		sb.WriteString("\r\n")
	}
	t.prev = len(lines)
	io.WriteString(t.out, sb.String())
}

// Close, terminali eski haline getirir ve imleci gosterir.
func (t *Terminal) Close() {
	fmt.Fprint(t.out, "\x1b[0m\x1b[?25h")
	if t.state != nil {
		_ = term.Restore(int(t.in.Fd()), t.state)
	}
	if t.restore != nil {
		t.restore()
	}
}

// Truncate, ANSI kacis dizilerini sayMAdan gorunur genisligi w ile sinirlar.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var sb strings.Builder
	vis := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				j++
			}
			if j > len(s) {
				j = len(s)
			}
			sb.WriteString(s[i:j])
			i = j
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if vis >= w {
			break
		}
		sb.WriteRune(r)
		vis++
		i += n
	}
	return sb.String()
}
