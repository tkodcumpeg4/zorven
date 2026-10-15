package api

import (
	"context"
	"testing"
	"time"
)

func TestChooseScreenMode(t *testing.T) {
	cases := []struct {
		req  string
		sup  bool
		want string
	}{
		{"auto", true, "screen2"},
		{"screen2", true, "screen2"},
		{"auto", false, ""},
		{"screen2", false, ""},
		{"mjpeg", true, "mjpeg"},
		{"h264", true, "h264"},
		{"", true, ""},
	}
	for _, c := range cases {
		if got := chooseScreenMode(c.req, c.sup); got != c.want {
			t.Errorf("chooseScreenMode(%q,%v)=%q, beklenen %q", c.req, c.sup, got, c.want)
		}
	}
}

func TestParseScreenControl(t *testing.T) {
	if c, ok := parseScreenControl([]byte(`{"type":"keyframe"}`)); !ok || c.kind != "keyframe" {
		t.Fatalf("keyframe ayristirilamadi: %+v %v", c, ok)
	}
	c, ok := parseScreenControl([]byte(`{"type":"settings","fps":10,"quality":40,"max_width":1280}`))
	if !ok || c.kind != "settings" || c.opts.FPS != 10 || c.opts.Quality != 40 || c.opts.MaxWidth != 1280 || c.opts.Display != -1 {
		t.Fatalf("settings hatali: %+v %v", c, ok)
	}
	c, _ = parseScreenControl([]byte(`{"type":"settings","display":2,"fps":-5,"quality":900}`))
	if c.opts.Display != 2 || c.opts.FPS != 0 || c.opts.Quality != 0 {
		t.Fatalf("sinirlama hatali: %+v", c)
	}
	if _, ok := parseScreenControl([]byte(`{"kind":"mousemove","x":0.5}`)); ok {
		t.Fatal("girdi olayi kontrol sayilmamali")
	}
	if _, ok := parseScreenControl([]byte(`not json`)); ok {
		t.Fatal("bozuk json kabul edildi")
	}
}

func msgWithFlags(f byte) []byte {
	b := make([]byte, 21)
	b[13] = f
	return b
}

func TestPumpScreen2Backpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan []byte, 16)
	out := make(chan []byte, 1)
	asked := make(chan struct{}, 8)
	go pumpScreen2(ctx, in, out, func() { asked <- struct{}{} })

	delta := msgWithFlags(0x01)
	key := msgWithFlags(0x01 | 0x02 | 0x04)

	in <- delta // out'a girer
	in <- delta // out dolu -> dusurulur, resync
	select {
	case <-asked:
	case <-time.After(time.Second):
		t.Fatal("anahtar kare istenmedi")
	}
	<-out       // yer ac
	in <- delta // resync: dusurulmeli
	in <- key   // kabul edilmeli
	select {
	case b := <-out:
		if k, f := screen2Flags(b); !k || !f {
			t.Fatalf("anahtar kare bekleniyordu, bayrak %x", b[13])
		}
	case <-time.After(time.Second):
		t.Fatal("anahtar kare iletilmedi")
	}
}
