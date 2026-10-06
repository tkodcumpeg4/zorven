package netconnect

import (
	"net"
	"net/netip"
	"testing"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestSourceAllowedEmptyAllowsAll(t *testing.T) {
	s := &Server{}
	if !s.sourceAllowed(fakeAddr("127.0.0.1:5555")) {
		t.Error("izin listesi bosken loopback reddedildi")
	}
}

func TestSourceAllowedRestricts(t *testing.T) {
	s := &Server{AllowFrom: []netip.Prefix{netip.MustParsePrefix("192.168.2.0/24")}}
	if !s.sourceAllowed(fakeAddr("192.168.2.40:5555")) {
		t.Error("izinli LAN istemcisi reddedildi")
	}
	for _, a := range []string{"192.168.3.1:5555", "10.0.0.1:1", "bozuk", "[::1]:80"} {
		if s.sourceAllowed(fakeAddr(a)) {
			t.Errorf("%q kabul edilmemeliydi", a)
		}
	}
	if !s.sourceAllowed(fakeAddr("[::ffff:192.168.2.9]:80")) {
		t.Error("v4-mapped izinli adres reddedildi")
	}
}

// Izinsiz kaynaga SOCKS selamlasmasi BILE donulmez: proxy varligi sizmaz.
func TestGatewayDropsUnauthorizedSource(t *testing.T) {
	// Istemci 127.0.0.1'den gelir; izin listesinde yalnizca baska bir aralik var.
	s := &Server{AllowFrom: []netip.Prefix{netip.MustParsePrefix("192.168.2.0/24")}}
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		s.handle(t.Context(), &addrConn{Conn: server, remote: fakeAddr("127.0.0.1:4444")})
		close(done)
	}()
	_, _ = client.Write([]byte{0x05, 0x01, 0x00})
	buf := make([]byte, 2)
	if n, err := client.Read(buf); err == nil && n > 0 {
		t.Errorf("izinsiz kaynaga yanit dondu: %x", buf[:n])
	}
	<-done
}

// addrConn, net.Pipe'a sahte bir uzak adres verir.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c *addrConn) RemoteAddr() net.Addr { return c.remote }
