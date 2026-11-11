package netctl

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/stun"
)

// flakyPC, ilk n okumada gecici hata (ENOBUFS) doner, sonra tek bir Binding
// istegi verir, sonra kapanir.
type flakyPC struct {
	fails  int
	req    []byte
	served bool
	wrote  chan []byte
}

func (p *flakyPC) ReadFrom(b []byte) (int, net.Addr, error) {
	if p.fails > 0 {
		p.fails--
		return 0, nil, &net.OpError{Op: "read", Err: syscall.ENOBUFS}
	}
	if !p.served {
		p.served = true
		return copy(b, p.req), &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 4000}, nil
	}
	return 0, nil, net.ErrClosed
}
func (p *flakyPC) WriteTo(b []byte, _ net.Addr) (int, error) {
	p.wrote <- append([]byte(nil), b...)
	return len(b), nil
}
func (p *flakyPC) Close() error                     { return nil }
func (p *flakyPC) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (p *flakyPC) SetDeadline(time.Time) error      { return nil }
func (p *flakyPC) SetReadDeadline(time.Time) error  { return nil }
func (p *flakyPC) SetWriteDeadline(time.Time) error { return nil }

// Eskiden zaman asimi disindaki ilk okuma hatasi STUN'u kalici durduruyordu.
func TestStunServeSurvivesTransientReadErrors(t *testing.T) {
	s := NewStunServer(nil)
	pc := &flakyPC{fails: 4, req: stun.NewBindingRequest(stun.NewTxID()), wrote: make(chan []byte, 1)}
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), pc) }()
	select {
	case <-pc.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("gecici hatalardan sonra istek yanitlanmadi")
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve hatasi: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kapanan sokette Serve bitmedi")
	}
}
