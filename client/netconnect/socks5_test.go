package netconnect

import (
	"bytes"
	"io"
	"testing"
)

// rw, testte okunacak girdiyi ve yazilan ciktiyi ayri tutar.
type rw struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func (r *rw) Read(p []byte) (int, error)  { return r.in.Read(p) }
func (r *rw) Write(p []byte) (int, error) { return r.out.Write(p) }

func newRW(b ...byte) *rw { return &rw{in: bytes.NewReader(b)} }

func TestNegotiateAcceptsNoAuth(t *testing.T) {
	c := newRW(0x05, 0x02, 0x02, 0x00) // kullanici/parola + kimliksiz
	if err := negotiate(c); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.out.Bytes(), []byte{0x05, 0x00}) {
		t.Errorf("yanit = %x, beklenen 0500", c.out.Bytes())
	}
}

func TestNegotiateRejectsWithoutNoAuth(t *testing.T) {
	c := newRW(0x05, 0x01, 0x02) // yalnizca kullanici/parola
	if err := negotiate(c); err != ErrNoAcceptableMethod {
		t.Fatalf("hata = %v", err)
	}
	if !bytes.Equal(c.out.Bytes(), []byte{0x05, 0xFF}) {
		t.Errorf("yanit = %x, beklenen 05ff", c.out.Bytes())
	}
}

func TestNegotiateRejectsSocks4(t *testing.T) {
	if err := negotiate(newRW(0x04, 0x01, 0x00)); err == nil {
		t.Error("SOCKS4 reddedilmeliydi")
	}
}

func TestReadRequestDomain(t *testing.T) {
	name := "db.internal"
	b := []byte{0x05, 0x01, 0x00, 0x03, byte(len(name))}
	b = append(b, name...)
	b = append(b, 0x15, 0x38) // 5432
	got, err := readRequest(newRW(b...))
	if err != nil {
		t.Fatal(err)
	}
	if got != "db.internal:5432" {
		t.Errorf("hedef = %q", got)
	}
}

func TestReadRequestIPv4AndIPv6(t *testing.T) {
	got, err := readRequest(newRW(0x05, 0x01, 0x00, 0x01, 10, 0, 0, 5, 0x00, 0x16))
	if err != nil || got != "10.0.0.5:22" {
		t.Errorf("ipv4 = %q, %v", got, err)
	}
	v6 := append([]byte{0x05, 0x01, 0x00, 0x04}, make([]byte, 15)...)
	v6 = append(v6, 1, 0x00, 0x50) // ::1, port 80
	got, err = readRequest(newRW(v6...))
	if err != nil || got != "[::1]:80" {
		t.Errorf("ipv6 = %q, %v", got, err)
	}
}

// BIND/UDP ASSOCIATE reddedilir ve istemciye 0x07 yazilir.
func TestReadRequestRejectsNonConnect(t *testing.T) {
	c := newRW(0x05, 0x02, 0x00, 0x01, 1, 2, 3, 4, 0, 80)
	if _, err := readRequest(c); err != ErrUnsupportedCommand {
		t.Fatalf("hata = %v", err)
	}
	if c.out.Len() < 2 || c.out.Bytes()[1] != RepCommandNotSupported {
		t.Errorf("yanit = %x, 0x07 beklenirdi", c.out.Bytes())
	}
}

func TestReadRequestRejectsUnknownAddrType(t *testing.T) {
	c := newRW(0x05, 0x01, 0x00, 0x09)
	if _, err := readRequest(c); err == nil {
		t.Fatal("bilinmeyen adres turu kabul edildi")
	}
	if c.out.Len() < 2 || c.out.Bytes()[1] != RepAddrNotSupported {
		t.Errorf("yanit = %x, 0x08 beklenirdi", c.out.Bytes())
	}
}

// Kesik girdi panik yerine hata dondurmeli.
func TestReadRequestTruncated(t *testing.T) {
	if _, err := readRequest(newRW(0x05, 0x01, 0x00, 0x03, 10, 'a')); err != io.ErrUnexpectedEOF {
		t.Errorf("hata = %v, ErrUnexpectedEOF beklenirdi", err)
	}
}

func TestReplyForStatus(t *testing.T) {
	cases := map[int]byte{
		401: RepNotAllowed, 403: RepNotAllowed, 402: RepNotAllowed,
		404: RepHostUnreachable, 400: RepHostUnreachable,
		502: RepConnectionRefused, 503: RepConnectionRefused,
		0: RepGeneralFailure, 500: RepGeneralFailure,
	}
	for code, want := range cases {
		if got := replyForStatus(code); got != want {
			t.Errorf("replyForStatus(%d) = %#x, beklenen %#x", code, got, want)
		}
	}
}
