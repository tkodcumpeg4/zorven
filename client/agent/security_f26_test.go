package agent

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestValidLocalPath(t *testing.T) {
	for _, p := range []string{"/", "/a/b", "/a%40b", "//x/y"} {
		if !validLocalPath(p) {
			t.Errorf("%q kabul edilmeliydi", p)
		}
	}
	bad := []string{"", "x", "@evil.com/x", "/@evil.com", "/a\\b", "http://evil.com/", "/a://b", "/a b", "/a\nb", "/a\x00b", "evil.com/x", ".evil.com"}
	for _, p := range bad {
		if validLocalPath(p) {
			t.Errorf("%q reddedilmeliydi", p)
		}
	}
}

func TestStreamDialAllowsConfiguredLocalTarget(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	c, err := streamDialContext(context.Background(), "tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("yapilandirilmis yerel hedefe baglanti bozulmamali: %v", err)
	}
	c.Close()
	// Hassas port (RDP) akis yolunda port kisitina takilmamali.
	if _, err := streamDialContext(context.Background(), "tcp", "127.0.0.1:3389", 200*time.Millisecond); err != nil && strings.Contains(err.Error(), "port korumasi") {
		t.Fatalf("akis yolunda port kisiti uygulanmamali: %v", err)
	}
}

func TestStreamDialBlocksMetadata(t *testing.T) {
	for _, a := range []string{"169.254.169.254:80", "[fe80::1]:80", "[::ffff:169.254.169.254]:80", "metadata.google.internal:80", "[::]:80"} {
		if _, err := streamDialContext(context.Background(), "tcp", a, 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "SSRF") {
			t.Errorf("%s engellenmeliydi, err=%v", a, err)
		}
	}
	if _, err := safeDialContext(context.Background(), "tcp", "127.0.0.1:5432"); err == nil || !strings.Contains(err.Error(), "port korumasi") {
		t.Errorf("HTTP yolunda hassas port engeli korunmali: %v", err)
	}
}
