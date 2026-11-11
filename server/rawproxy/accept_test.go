package rawproxy

import (
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// --- K2: kabul dongusu gecici hatada yasamali --------------------------------

// flakyListener, ilk n Accept'te gecici hata (EMFILE), sonra kuyruktaki
// baglantilari, en sonda ErrClosed doner.
type flakyListener struct {
	mu     sync.Mutex
	fails  int
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	calls  atomic.Int32
}

func newFlaky(fails int) *flakyListener {
	return &flakyListener{fails: fails, conns: make(chan net.Conn, 4), closed: make(chan struct{})}
}

func (l *flakyListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	l.mu.Lock()
	if l.fails > 0 {
		l.fails--
		l.mu.Unlock()
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept4", syscall.EMFILE)}
	}
	l.mu.Unlock()
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *flakyListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *flakyListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }

// Regresyon (K2): EMFILE gibi gecici hatada demux kapanip sunucu sureci
// dusuyordu. Simdi bekleyip devam etmeli, yalniz kapatilinca cikmali.
func TestDemuxListenerSurvivesTemporaryAcceptErrors(t *testing.T) {
	inner := newFlaky(6) // 5+10+20+40+80+160 ms ~ 0.3 sn bekleme
	d := NewDemuxListener(inner, nil, nil, nil, quietLog())
	defer d.Close()
	client, server := net.Pipe()
	defer client.Close()
	inner.conns <- server
	go func() { _, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")) }() // TLS degil: http'ye birakilir

	got := make(chan net.Conn, 1)
	go func() {
		c, err := d.Accept()
		if err == nil {
			got <- c
		}
	}()
	select {
	case c := <-got:
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("gecici hatalardan sonra baglanti kabul edilmeliydi (demux dustu mu?)")
	}
	if n := inner.calls.Load(); n < 7 {
		t.Fatalf("Accept yeniden denenmeliydi: %d", n)
	}
	// Ic dinleyici kapatilinca demux da kapanmali.
	inner.Close()
	if _, err := d.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("kapanista ErrClosed bekleniyordu: %v", err)
	}
}

// Rezerve port dinleyicisi de gecici hatada sagir kalmamali.
func TestPortAcceptLoopSurvivesTemporaryErrors(t *testing.T) {
	m := New(tunnel.NewHub(), nil, quietLog(), nil)
	ln := newFlaky(4)
	done := make(chan struct{})
	go func() { m.acceptLoop(ln, listenKey{port: 1}); close(done) }()
	client, server := net.Pipe()
	defer client.Close()
	ln.conns <- server
	// byPort'ta kayit yok: handleTCP baglantiyi hemen kapatir; kapanma = kabul edildi.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("baglanti kabul edilip kapatilmaliydi: %v", err)
	}
	ln.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dinleyici kapaninca dongu bitmeli")
	}
}

func TestAcceptBackoffBounds(t *testing.T) {
	var b acceptBackoff
	if d := b.next(); d != acceptBackoffMin {
		t.Fatalf("ilk bekleme: %v", d)
	}
	for i := 0; i < 20; i++ {
		b.next()
	}
	if d := b.next(); d != acceptBackoffMax {
		t.Fatalf("tavan: %v", d)
	}
	b.reset()
	if d := b.next(); d != acceptBackoffMin {
		t.Fatalf("sifirlama: %v", d)
	}
	if isClosedErr(&net.OpError{Err: os.NewSyscallError("accept4", syscall.EMFILE)}) || !isClosedErr(net.ErrClosed) {
		t.Fatal("isClosedErr")
	}
}
