package tunsec

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrustedNormalize(t *testing.T) {
	c, err := Normalize(Config{Trusted: TrustedConfig{CIDRs: []string{
		" 203.0.113.7 ", "198.51.100.77/24", "2001:db8::1", "::ffff:192.0.2.1", "203.0.113.7", ""}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.7/32", "198.51.100.0/24", "2001:db8::1/128", "192.0.2.1/32"}
	if strings.Join(c.Trusted.CIDRs, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", c.Trusted.CIDRs, want)
	}
}

func TestTrustedRejectsBroad(t *testing.T) {
	for _, s := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/7", "2001::/31", "garbage", "1.2.3.4/33"} {
		if _, err := Normalize(Config{Trusted: TrustedConfig{CIDRs: []string{s}}}); err == nil {
			t.Errorf("%q reddedilmeliydi", s)
		}
	}
	for _, s := range []string{"10.0.0.0/8", "2001:db8::/32"} {
		if _, err := Normalize(Config{Trusted: TrustedConfig{CIDRs: []string{s}}}); err != nil {
			t.Errorf("%q kabul edilmeliydi: %v", s, err)
		}
	}
}

func TestTrustedMax(t *testing.T) {
	var l []string
	for i := 0; i < MaxTrustedCIDRs; i++ {
		l = append(l, fmt.Sprintf("10.0.%d.1", i))
	}
	if _, err := Normalize(Config{Trusted: TrustedConfig{CIDRs: l}}); err != nil {
		t.Fatal(err)
	}
	l = append(l, "10.1.0.1")
	if _, err := Normalize(Config{Trusted: TrustedConfig{CIDRs: l}}); err == nil {
		t.Fatal("101 girdi reddedilmeliydi")
	}
}

func TestTrustedMatcher(t *testing.T) {
	c := Compile(Config{WAF: WAFConfig{Mode: WAFBlock}, Trusted: TrustedConfig{
		CIDRs: []string{"203.0.113.7", "198.51.100.0/24", "2001:db8::/48"}}})
	for ip, want := range map[string]bool{
		"203.0.113.7": true, "203.0.113.8": false, "198.51.100.200": true, "198.51.101.1": false,
		"2001:db8:0:5::1": true, "2001:db9::1": false, "::ffff:203.0.113.7": true,
		"fe80::1%eth0": false, "bad": false, "": false,
	} {
		if got := c.IsTrusted(ip); got != want {
			t.Errorf("%q: got %v want %v", ip, got, want)
		}
	}
	var nilc *Compiled
	if nilc.IsTrusted("1.2.3.4") {
		t.Error("nil guvenilir olmamali")
	}
}
