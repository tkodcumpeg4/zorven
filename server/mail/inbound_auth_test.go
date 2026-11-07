package mail

import (
	"net"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/mail/mailauth"
)

func TestParseInboundAuthMode(t *testing.T) {
	cases := []struct {
		in   string
		want InboundAuthMode
		ok   bool
	}{
		{"", AuthEnforce, true},
		{"enforce", AuthEnforce, true},
		{" ENFORCE ", AuthEnforce, true},
		{"on", AuthEnforce, true},
		{"monitor", AuthMonitor, true},
		{"Report", AuthMonitor, true},
		{"off", AuthOff, true},
		{"0", AuthOff, true},
		{"false", AuthOff, true},
		{"monitr", AuthEnforce, false}, // yazim hatasi: varsayilan politika, cagiran uyarir
	}
	for _, tc := range cases {
		got, ok := ParseInboundAuthMode(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseInboundAuthMode(%q) = %q,%v; beklenen %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNewInboundAuthIdentities(t *testing.T) {
	loc := NewLocal(newMemMailStore(), "Mail.Zorven.App", "zorven.app", nil)
	a := NewInboundAuth(loc, AuthEnforce, nil)
	if a == nil || a.AuthservID != "mail.zorven.app" || len(a.StripIDs) != 1 || a.StripIDs[0] != "zorven.app" {
		t.Fatalf("kimlikler: %+v", a)
	}
	if NewInboundAuth(loc, AuthOff, nil) != nil {
		t.Fatal("AuthOff icin nil bekleniyordu")
	}
	// Posta alan adi ile platform alan adi ayniysa tekrar eklenmez.
	same := NewLocal(newMemMailStore(), "zorven.app", "zorven.app", nil)
	if a := NewInboundAuth(same, AuthEnforce, nil); len(a.StripIDs) != 0 {
		t.Fatalf("StripIDs: %v", a.StripIDs)
	}
}

func TestInboundAuthAction(t *testing.T) {
	reject := &mailauth.Result{Action: mailauth.ActionReject}
	junk := &mailauth.Result{Action: mailauth.ActionJunk}
	inbox := &mailauth.Result{Action: mailauth.ActionInbox}
	enforce := &InboundAuth{Mode: AuthEnforce}
	monitor := &InboundAuth{Mode: AuthMonitor}

	cases := []struct {
		name  string
		a     *InboundAuth
		res   *mailauth.Result
		rcpts []string
		want  mailauth.Action
	}{
		{"enforce_reject", enforce, reject, []string{"acme@mail.zorven.app"}, mailauth.ActionReject},
		{"enforce_junk", enforce, junk, []string{"acme@mail.zorven.app"}, mailauth.ActionJunk},
		{"enforce_inbox", enforce, inbox, []string{"acme@mail.zorven.app"}, mailauth.ActionInbox},
		{"monitor_reject_uygulanmaz", monitor, reject, []string{"acme@mail.zorven.app"}, mailauth.ActionInbox},
		{"monitor_junk_uygulanmaz", monitor, junk, []string{"acme@mail.zorven.app"}, mailauth.ActionInbox},
		{"postmaster_muaf", enforce, reject, []string{"postmaster@zorven.app"}, mailauth.ActionInbox},
		{"abuse_buyuk_harf_muaf", enforce, junk, []string{"Abuse@zorven.app"}, mailauth.ActionInbox},
		{"alicilardan_biri_muaf", enforce, reject, []string{"acme@mail.zorven.app", "postmaster@zorven.app"}, mailauth.ActionInbox},
		{"benzer_ad_muaf_degil", enforce, reject, []string{"postmaster2@zorven.app"}, mailauth.ActionReject},
	}
	for _, tc := range cases {
		if got := tc.a.action(tc.res, tc.rcpts); got != tc.want {
			t.Errorf("%s: action = %v, beklenen %v", tc.name, got, tc.want)
		}
	}
}

func TestRejectTextAndSummary(t *testing.T) {
	res := &mailauth.Result{
		SPF:    mailauth.SPFResult{Result: mailauth.Fail},
		DKIM:   []mailauth.DKIMResult{{Result: mailauth.None}},
		DMARC:  mailauth.DMARCResult{Result: mailauth.Fail, FromDomain: "example.com"},
		Action: mailauth.ActionReject,
		Why:    "dmarc fail, p=reject",
	}
	txt := rejectText(res)
	if !strings.Contains(txt, "example.com") || strings.ContainsAny(txt, "\r\n") {
		t.Fatalf("ret metni: %q", txt)
	}
	if got := rejectText(&mailauth.Result{}); !strings.Contains(got, "the sender domain") {
		t.Fatalf("alan adi yokken ret metni: %q", got)
	}

	s := authSummary(res, net.ParseIP("203.0.113.7"), mailauth.ActionInbox)
	for _, want := range []string{"ip=203.0.113.7", "spf=fail", "dkim=none", "dmarc=fail", "karar=inbox", "politika=reject", "neden=dmarc fail, p=reject"} {
		if !strings.Contains(s, want) {
			t.Errorf("ozet %q icinde %q yok", s, want)
		}
	}
	if s := authSummary(res, nil, mailauth.ActionReject); !strings.Contains(s, "ip=-") || strings.Contains(s, "politika=") {
		t.Errorf("ozet: %q", s)
	}
}

func TestConnIP(t *testing.T) {
	if connIP(nil) != nil {
		t.Fatal("nil baglanti nil donmeli")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	// net.Pipe adresi TCP degildir ("pipe"): cozulemez -> nil.
	if ip := connIP(a); ip != nil {
		t.Fatalf("pipe adresi IP olmamali: %v", ip)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ip := connIP(c); ip == nil || !ip.IsLoopback() {
		t.Fatalf("loopback bekleniyordu: %v", ip)
	}
}
