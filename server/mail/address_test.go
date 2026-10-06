package mail

import (
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	ok := map[string]string{
		"bob@mail.zorven.app":             "bob@mail.zorven.app",
		"  Bob@Mail.Zorven.App ":          "Bob@mail.zorven.app",
		"Bob Smith <bob@mail.zorven.app>": "bob@mail.zorven.app",
		// Gorunen ad ic adres gibi gorunse de gercek adres harici -> harici doner.
		`"x@mail.zorven.app" <victim@example.org>`: "victim@example.org",
	}
	for in, want := range ok {
		got, err := ParseAddress(in)
		if err != nil || got != want {
			t.Errorf("ParseAddress(%q) = %q, %v; beklenen %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"bob",
		"victim@example.org, bob@mail.zorven.app",
		"bob@mail.zorven.app, victim@example.org",
		`"victim@example.org"@mail.zorven.app`,
		"bob@mail.zorven.app\r\nBcc: victim@example.org",
		"bob@mail.zorven.app\nX: y",
		"victim@example.org <bob@mail.zorven.app",
		"grp: a@b.com, c@d.com;",
		"bob@[127.0.0.1]",
		"bob@localhost",
		"bob@@mail.zorven.app",
	}
	for _, in := range bad {
		if got, err := ParseAddress(in); err == nil {
			t.Errorf("ParseAddress(%q) kabul edildi (%q); reddedilmeliydi", in, got)
		}
	}
}

func TestAddressDomain(t *testing.T) {
	if d := AddressDomain("a@Mail.Zorven.App"); d != "mail.zorven.app" {
		t.Fatalf("AddressDomain = %q", d)
	}
}

func TestSend_RejectsHeaderInjection(t *testing.T) {
	s := NewSender("127.0.0.1:1", "mail.zorven.app")
	cases := []struct{ from, to, irt string }{
		{"a@mail.zorven.app", "b@mail.zorven.app\r\nBcc: x@gmail.com", ""},
		{"a@mail.zorven.app\r\nBcc: x@gmail.com", "b@mail.zorven.app", ""},
		{"a@mail.zorven.app", "b@mail.zorven.app", "<id@x>\r\nBcc: x@gmail.com"},
	}
	for _, c := range cases {
		_, err := s.Send(c.from, c.to, "s", "b", "", c.irt, nil)
		if err == nil || strings.Contains(err.Error(), "relay baglantisi") {
			t.Errorf("Send(%q,%q,%q) dogrulama hatasi vermeliydi, alinan: %v", c.from, c.to, c.irt, err)
		}
	}
}

func TestBuildMessage_AttachmentHeadersSanitized(t *testing.T) {
	s := NewSender("relay:25", "mail.zorven.app")
	msg := s.buildMessage("a@mail.zorven.app", "b@mail.zorven.app", "konu", "govde", "", "", "<m@x>", []Attachment{{
		Filename:    "a\r\nX-Evil: 1\".txt",
		ContentType: "text/plain\r\nX-Evil: 2",
		Content:     []byte("hi"),
	}})
	if strings.Contains(msg, "\r\nX-Evil:") {
		t.Fatalf("ek basliginda enjeksiyon kaldi:\n%s", msg)
	}
	if !strings.Contains(msg, "Content-Type: application/octet-stream") {
		t.Fatalf("gecersiz content-type octet-stream'e dusmeliydi:\n%s", msg)
	}
}
