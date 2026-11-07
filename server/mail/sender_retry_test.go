package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSMTP, her baglantida behave(conn, n) cagiran basit sahte sunucu.
func fakeSMTP(t *testing.T, behave func(c net.Conn, n int32)) (addr string, conns *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	conns = &atomic.Int32{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n := conns.Add(1)
			go func() {
				defer c.Close()
				behave(c, n)
			}()
		}
	}()
	return ln.Addr().String(), conns
}

// okSession, tam basarili bir SMTP oturumu yurutur; mailReply MAIL FROM yanitidir.
func okSession(c net.Conn, mailReply string) {
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case strings.HasPrefix(cmd, "MAIL"):
			w(mailReply)
		case strings.HasPrefix(cmd, "RCPT"):
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
			}
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func testSender(addr string) (*Sender, *[]time.Duration) {
	waits := &[]time.Duration{}
	s := NewSender(addr, "mail.test")
	s.sleeper = func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return nil
	}
	return s, waits
}

func TestDeliverRetriesOn4xxThenSucceeds(t *testing.T) {
	addr, conns := fakeSMTP(t, func(c net.Conn, n int32) {
		if n < 3 {
			_, _ = c.Write([]byte("421 try later\r\n"))
			return
		}
		okSession(c, "250 ok")
	})
	s, waits := testSender(addr)
	if err := s.SendRaw("a@x.test", []string{"b@y.test"}, []byte("Subject: t\r\n\r\nhi\r\n")); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if conns.Load() != 3 || len(*waits) != 2 {
		t.Fatalf("conns=%d waits=%v", conns.Load(), *waits)
	}
}

func TestDeliverRetries4xxExhausted(t *testing.T) {
	addr, conns := fakeSMTP(t, func(c net.Conn, n int32) { okSession(c, "451 greylisted") })
	s, _ := testSender(addr)
	if err := s.SendRaw("a@x.test", []string{"b@y.test"}, []byte("x")); err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if conns.Load() != int32(smtpRetry.Attempts) {
		t.Fatalf("conns=%d", conns.Load())
	}
}

func TestDeliverNoRetryOn5xx(t *testing.T) {
	addr, conns := fakeSMTP(t, func(c net.Conn, n int32) { okSession(c, "550 no such user") })
	s, waits := testSender(addr)
	if err := s.SendRaw("a@x.test", []string{"b@y.test"}, []byte("x")); err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if conns.Load() != 1 || len(*waits) != 0 {
		t.Fatalf("5xx tekrar denendi: conns=%d", conns.Load())
	}
}

func TestDeliverHangingRelayTimesOut(t *testing.T) {
	// Sunucu baglanir ama hic selamlamaz: istemci sonsuza dek beklememeli.
	addr, _ := fakeSMTP(t, func(c net.Conn, n int32) { time.Sleep(5 * time.Second) })
	s := NewSender(addr, "mail.test")
	s.msgTimeout = 300 * time.Millisecond
	s.sleeper = func(context.Context, time.Duration) error { return nil }
	start := time.Now()
	err := s.SendRaw("a@x.test", []string{"b@y.test"}, []byte("x"))
	if err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("takilan relay yeterince hizli kesilmedi: %v", d)
	}
}

func TestDeliverDialFailureRetried(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // port kapali: baglanti reddedilir
	s, waits := testSender(addr)
	if err := s.SendRaw("a@x.test", []string{"b@y.test"}, []byte("x")); err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if len(*waits) != smtpRetry.Attempts-1 {
		t.Fatalf("waits=%v", *waits)
	}
}
