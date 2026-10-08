package mail

import (
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Platform sistem kutusuna (info@zorven.app -> info@zorven.app) SMTP gonderimi:
// Postfix'e GITMEZ, platform kiracisinin INBOX'una dusar.
func TestSubmission_SystemBoxToSystemBoxIsLocal(t *testing.T) {
	e := newSubEnv(t, "free")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", "info@zorven.app", e.pw)); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMail("info@zorven.app", []string{"info@zorven.app"},
		strings.NewReader(msgFor("info@zorven.app", "info@zorven.app", ""))); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	if e.relay.n != 0 {
		t.Fatal("yerel mail relay'e gitmemeli")
	}
	in := e.st.all(store.DefaultTenantID, "info@zorven.app", store.MailFolderInbox)
	if len(in) != 1 || in[0].Direction != "inbound" {
		t.Fatalf("INBOX: %+v", in)
	}
	if n := len(e.st.all(store.DefaultTenantID, "info@zorven.app", store.MailFolderSent)); n != 1 {
		t.Fatalf("Sent kopyasi bir kez kaydedilmeli: %d", n)
	}
}

// Sistem kutusundan tenant kutusuna: tenant INBOX'ina duser.
func TestSubmission_SystemToTenantBoxIsLocal(t *testing.T) {
	e := newSubEnv(t, "free")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", "info@zorven.app", e.pw)); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMail("info@zorven.app", []string{testBox},
		strings.NewReader(msgFor("info@zorven.app", testBox, ""))); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	if e.relay.n != 0 {
		t.Fatal("relay'e gitmemeli")
	}
	if in := e.st.all(testTenant, testBox, store.MailFolderInbox); len(in) != 1 {
		t.Fatalf("tenant INBOX: %d", len(in))
	}
}

// Karisik liste: yerel alicilar INBOX'a, harici olanlar relay'e; Sent bir kez.
func TestSubmission_MixedRecipientsSplit(t *testing.T) {
	e := newSubEnv(t, "enterprise")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}
	rcpts := []string{"diger@mail.zorven.app", "kisi@example.org", "info@zorven.app"}
	if err := c.SendMail(testBox, rcpts, strings.NewReader(msgFor(testBox, "diger@mail.zorven.app", ""))); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	if e.relay.n != 1 || len(e.relay.to) != 1 || e.relay.to[0] != "kisi@example.org" {
		t.Fatalf("relay yalniz harici aliciyi almali: %+v", e.relay)
	}
	if n := len(e.st.all("ten_diger", "diger@mail.zorven.app", store.MailFolderInbox)); n != 1 {
		t.Fatalf("diger INBOX: %d", n)
	}
	if n := len(e.st.all(store.DefaultTenantID, "info@zorven.app", store.MailFolderInbox)); n != 1 {
		t.Fatalf("info INBOX: %d", n)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderSent)); n != 1 {
		t.Fatalf("Sent bir kez: %d", n)
	}
}

// Postfix DSN'i (bos zarf gonderen, MAILER-DAEMON) gelen :25 alicisinda kabul edilir.
func TestInbound_NullSenderDSNAccepted(t *testing.T) {
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "free")
	l := NewLocal(st, "mail.zorven.app", "zorven.app", []string{"info"})
	s := &session{be: &Backend{Local: l}}
	if err := s.Mail("", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("info@zorven.app", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("baska@zorven.app", nil); smtpCode(err) != 550 {
		t.Fatalf("bilinmeyen sistem kutusu 550: %v", err)
	}
	dsn := "From: MAILER-DAEMON@mail.zorven.app\r\nTo: info@zorven.app\r\nSubject: Undelivered Mail Returned to Sender\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/report; report-type=delivery-status; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nteslim edilemedi\r\n--B\r\nContent-Type: message/delivery-status\r\n\r\nStatus: 5.1.1\r\n--B--\r\n"
	if err := s.Data(strings.NewReader(dsn)); err != nil {
		t.Fatalf("DSN: %v", err)
	}
	in := st.all(store.DefaultTenantID, "info@zorven.app", store.MailFolderInbox)
	if len(in) != 1 || !strings.Contains(in[0].TextBody, "teslim edilemedi") {
		t.Fatalf("DSN INBOX: %+v", in)
	}
}

// 8-bit ISO-8859-9 + NUL iceren ham mesaj birebir saklanir; metin UTF-8'e cevrilir.
func TestInbound_RawBytesPreserved(t *testing.T) {
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "free")
	l := NewLocal(st, "mail.zorven.app", "zorven.app", nil)
	raw := []byte("From: a@example.org\r\nTo: acme@mail.zorven.app\r\nSubject: Selam\r\n" +
		"Content-Type: text/plain; charset=iso-8859-9\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		"\xfd\xfe\xf0 g\xfc\xe7\x00son\r\n")
	if _, err := l.Deliver(t.Context(), "a@example.org", []string{"acme@mail.zorven.app"}, raw); err != nil {
		t.Fatal(err)
	}
	in := st.all(testTenant, testBox, store.MailFolderInbox)
	if len(in) != 1 {
		t.Fatalf("INBOX: %d", len(in))
	}
	if string(in[0].RawData()) != string(raw) {
		t.Fatal("ham mesaj birebir saklanmadi")
	}
	if !strings.Contains(in[0].TextBody, "\u0131\u015f\u011f") || strings.ContainsRune(in[0].TextBody, 0) {
		t.Fatalf("metin govde: %q", in[0].TextBody)
	}
}
