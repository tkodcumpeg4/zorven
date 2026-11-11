package netconnect

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

// flakyListener: ilk fails kez EMFILE doner, sonra gercek dinleyiciye gecer.
type flakyListener struct {
	net.Listener
	mu    sync.Mutex
	fails int
}

func (l *flakyListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.fails > 0 {
		l.fails--
		l.mu.Unlock()
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	l.mu.Unlock()
	return l.Listener.Accept()
}

// Gecici Accept hatasinda dinleyici yasamali; ctx bitince temiz cikmali.
func TestServeSurvivesEMFILE(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &flakyListener{Listener: inner, fails: 6}
	s := &Server{Listen: inner.Addr().String()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, ln) }()

	c, err := net.DialTimeout("tcp", inner.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Dinleyici kabul ettiyse handle SOCKS5 selamlasmasini bekler; baglantinin
	// kapanmadan acik kalmasi kabul edildigini gosterir.
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err == nil || !isTimeout(err) {
		t.Fatalf("baglanti kabul edilip bekletilmeliydi: %v", err)
	}
	c.Close()
	select {
	case err := <-done:
		t.Fatalf("dongu erken cikti: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ctx iptalinde nil bekleniyordu: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx iptalinde dongu cikmadi")
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
