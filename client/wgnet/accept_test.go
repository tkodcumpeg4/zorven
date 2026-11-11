package wgnet

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"testing"
	"time"
)

type flakyListener struct {
	net.Listener
	mu    sync.Mutex
	fails int
	err   error // fails bitince donecek kalici hata (nil: gercek Accept)
}

func (l *flakyListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.fails > 0 {
		l.fails--
		l.mu.Unlock()
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	err := l.err
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return l.Listener.Accept()
}

// Gecici Accept hatasinda yerel yonlendirme yasamali; ctx bitince cikmali.
func TestServeTCPSurvivesEMFILE(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &flakyListener{Listener: inner, fails: 6}
	dialed := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ServeTCP(ctx, ln, func(context.Context) (net.Conn, error) {
			dialed <- struct{}{}
			return nil, errors.New("ic ag yok")
		}, nil)
		close(done)
	}()
	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("EMFILE sonrasi baglanti kabul edilmedi")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx iptalinde dongu cikmadi")
	}
}

// Paylasim dinleyicisi: gecici hatada yasar, paylasim durunca (kalici iptal
// hatasi + closeConns) cikar.
func TestShareServeTCPSurvivesAndStops(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &flakyListener{Listener: inner, fails: 5}
	s := &shareServer{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a := &activeShare{conns: map[net.Conn]netip.Addr{}}
	a.allow.Store(allowSet(nil)) // kimseye izin yok: kabul edilen baglanti kapatilir
	done := make(chan struct{})
	go func() { s.serveTCP(a, ln); close(done) }()

	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeoutErr(err) {
		t.Fatalf("izinsiz baglanti kabul edilip kapatilmaliydi: %v", err)
	}
	c.Close()
	// Paylasim durdu: gVisor gibi net.ErrClosed olmayan kalici hata.
	ln.mu.Lock()
	ln.err = errors.New("operation canceled")
	ln.mu.Unlock()
	a.closeConns()
	inner.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("paylasim durunca dongu cikmadi")
	}
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
