package rawproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
)

// panicStream, ziyaretciden gelen ilk veride panikler (pompa goroutine'i).
type panicStream struct{ *fakeStream }

func (p panicStream) Send(context.Context, []byte) error { panic("pompa bozuk") }

// B2: ham TCP pompasindaki panik sureci dusurmez; yalniz o baglanti ve akis
// kapanir, panik sayilir, dinleyici yeni baglantilari kabul etmeye devam eder.
func TestTCPPumpPanicClosesOnlyConn(t *testing.T) {
	before := obs.Panics("rawproxy.tcp_pump")
	streams := make(chan *fakeStream, 4)
	_, addr := rawTestMgr(t, func() (bridgeStream, bool, error) {
		f := newFakeStream("", false)
		streams <- f
		return panicStream{f}, false, nil
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("merhaba")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("baglanti kapanmali (EOF), hata: %v", err)
	}
	waitFor(t, "panik sayaci", func() bool { return obs.Panics("rawproxy.tcp_pump") == before+1 })
	last := <-streams
	select {
	case <-last.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("akis kapanmadi")
	}

	// Dinleyici hala kabul ediyor.
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dinleyici oldu: %v", err)
	}
	c2.Close()
}
