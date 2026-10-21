package localctl

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type fake struct {
	mu sync.Mutex
	c  Caps
}

func (f *fake) Current() Caps { f.mu.Lock(); defer f.mu.Unlock(); return f.c }
func (f *fake) Set(t, s *bool) Caps {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t != nil {
		f.c.TerminalCap, f.c.TerminalEff = *t, *t
	}
	if s != nil {
		f.c.ScreenCap, f.c.ScreenEff = *s, *s
	}
	return f.c
}

func TestRoundTripAndPerms(t *testing.T) {
	dir, err := os.MkdirTemp("", "zv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ep := Endpoint{Path: filepath.Join(dir, "d", "ctl.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{}
	if err := Serve(ctx, ep, f, "t", nil); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(ep.Path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("soket modu %v, 0600 bekleniyordu", st.Mode().Perm())
		}
		ds, _ := os.Stat(filepath.Dir(ep.Path))
		if ds.Mode().Perm() != 0o700 {
			t.Errorf("dizin modu %v", ds.Mode().Perm())
		}
	}
	yes := true
	r, err := Call(ep, Request{Op: "set", Terminal: &yes})
	if err != nil || !r.Caps.TerminalEff || r.Caps.ScreenEff {
		t.Fatalf("set: %v %+v", err, r)
	}
	r, err = Call(ep, Request{Op: "status"})
	if err != nil || !r.Caps.TerminalCap {
		t.Fatalf("status: %v %+v", err, r)
	}
	no := false
	r, _ = Call(ep, Request{Op: "set", Terminal: &no})
	if r.Caps.TerminalEff {
		t.Error("kapatma calismadi")
	}
	if _, err := Call(ep, Request{Op: "x"}); err == nil {
		t.Error("bilinmeyen islem hata vermeliydi")
	}
	// Ikinci ajan ayni soketi alamaz.
	if err := Serve(ctx, ep, f, "t", nil); err == nil {
		t.Error("ikinci dinleyici reddedilmeliydi")
	}
}
