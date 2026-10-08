package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.zorven.app"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"mail.zorven.app", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

type captureRelay struct {
	mu   sync.Mutex
	from string
	to   []string
	raw  []byte
	n    int
	fail bool
}

func (r *captureRelay) SendRaw(from string, to []string, raw []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("relay down")
	}
	r.from, r.to, r.raw = from, append([]string(nil), to...), append([]byte(nil), raw...)
	r.n++
	return nil
}
func (r *captureRelay) Domain() string { return "mail.zorven.app" }

type subEnv struct {
	st    *memMailStore
	relay *captureRelay
	addr  string
	tlsA  string // implicit TLS adresi
	pw    string
}

func newSubEnv(t *testing.T, plan string) *subEnv {
	t.Helper()
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", plan)
	st.addTenant("ten_diger", "diger", plan)
	st.addTenant("ten_gizli", "gizli", plan)
	pw := GenerateAppPassword()
	st.addAppPassword(testTenant, testBox, pw)
	// Sistem kutusu (platform kiracisi).
	st.addAppPassword(store.DefaultTenantID, "info@zorven.app", pw)
	auth := NewAuthenticator(st, "mail.zorven.app", "zorven.app", []string{"info"}, nil)
	relay := &captureRelay{}
	sub := NewSubmissionServer(auth, st, relay, NewHub(), nil)
	cfg := selfSignedTLS(t)

	srv := sub.NewSMTPServer("", cfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	srv2 := sub.NewSMTPServer("", cfg)
	ln2, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	go srv2.Serve(ln2)
	t.Cleanup(func() { srv2.Close() })

	return &subEnv{st: st, relay: relay, addr: ln.Addr().String(), tlsA: ln2.Addr().String(), pw: pw}
}

func (e *subEnv) client(t *testing.T) *smtp.Client {
	t.Helper()
	c, err := smtp.DialStartTLS(e.addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("starttls: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func smtpCode(err error) int {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

func msgFor(from, to, extra string) string {
	return "From: " + from + "\r\nTo: " + to + "\r\nSubject: Deneme\r\n" + extra +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n\r\ngovde metni\r\n"
}

func TestSubmission_StartTLSRequiredBeforeAuth(t *testing.T) {
	e := newSubEnv(t, "pro")
	c, err := smtp.Dial(e.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err == nil {
		t.Fatal("STARTTLS olmadan AUTH kabul edildi")
	}
	if err := c.Mail(testBox, nil); err == nil || smtpCode(err) != 530 {
		t.Fatalf("kimliksiz MAIL 530 olmali: %v", err)
	}
}

func TestSubmission_AuthMechanismsAndFailures(t *testing.T) {
	e := newSubEnv(t, "pro")

	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, "yanlis")); err == nil || smtpCode(err) != 535 {
		t.Fatalf("yanlis parola 535 olmali: %v", err)
	}
	c2 := e.client(t)
	if err := c2.Auth(sasl.NewLoginClient(testBox, e.pw)); err != nil {
		t.Fatalf("LOGIN mekanizmasi: %v", err)
	}
	c3 := e.client(t)
	if err := c3.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatalf("PLAIN mekanizmasi: %v", err)
	}
	// Implicit TLS (465).
	c4, err := smtp.DialTLS(e.tlsA, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("465: %v", err)
	}
	defer c4.Close()
	if err := c4.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatalf("465 auth: %v", err)
	}
}

func TestSubmission_SenderAndRecipientRules(t *testing.T) {
	e := newSubEnv(t, "pro")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}

	// MAIL FROM kimligi dogrulanan kutuyla ayni olmali.
	if err := c.Mail("baskasi@mail.zorven.app", nil); err == nil || smtpCode(err) != 553 {
		t.Fatalf("farkli MAIL FROM reddedilmeli (553): %v", err)
	}
	if err := c.Mail(testBox, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("diger@mail.zorven.app", nil); err != nil {
		t.Fatalf("ic alici: %v", err)
	}
	// Platform sistem kutusu yerel kutudur (relay'e gitmez), kabul edilir.
	if err := c.Rcpt("info@zorven.app", nil); err != nil {
		t.Fatalf("sistem kutusu: %v", err)
	}
	// Var olmayan yerel adresler RCPT aninda 550.
	if err := c.Rcpt("yok@mail.zorven.app", nil); smtpCode(err) != 550 {
		t.Fatalf("bilinmeyen kiraci 550 olmali: %v", err)
	}
	if err := c.Rcpt("nobody@zorven.app", nil); smtpCode(err) != 550 {
		t.Fatalf("bilinmeyen sistem kutusu 550 olmali: %v", err)
	}
	if err := c.Rcpt("gecersiz adres", nil); err == nil {
		t.Fatal("gecersiz alici kabul edildi")
	}
	if e.relay.n != 0 {
		t.Fatal("henuz bir sey iletilmemeli")
	}
}

func TestSubmission_DeliversAndStoresSentCopy(t *testing.T) {
	e := newSubEnv(t, "pro")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}
	// Bcc iletilen kopyadan cikarilir; Message-ID eklenir.
	body := msgFor(testBox, "diger@mail.zorven.app", "Bcc: gizli@mail.zorven.app\r\n")
	if err := c.SendMail(testBox, []string{"diger@mail.zorven.app", "gizli@mail.zorven.app"}, strings.NewReader(body)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	// Yerel alicilar relay'e GITMEZ; INBOX'a dogrudan teslim edilir.
	if e.relay.n != 0 {
		t.Fatalf("yerel mail relay'e gitmemeli: %+v", e.relay)
	}
	for _, rc := range []struct{ tenant, box string }{{"ten_diger", "diger@mail.zorven.app"}, {"ten_gizli", "gizli@mail.zorven.app"}} {
		in := e.st.all(rc.tenant, rc.box, store.MailFolderInbox)
		if len(in) != 1 || in[0].Direction != "inbound" || in[0].Seen || in[0].Subject != "Deneme" {
			t.Fatalf("%s INBOX: %+v", rc.box, in)
		}
		wire := string(in[0].RawData())
		if strings.Contains(strings.ToLower(wire), "bcc:") {
			t.Fatalf("Bcc teslim edilen mesajda kalmis:\n%s", wire)
		}
		if !strings.Contains(wire, "Message-ID:") && !strings.Contains(wire, "Message-Id:") {
			t.Fatalf("Message-ID eklenmemis:\n%s", wire)
		}
	}
	sent := e.st.all(testTenant, testBox, store.MailFolderSent)
	if len(sent) != 1 {
		t.Fatalf("Sent'te %d mesaj", len(sent))
	}
	s0 := sent[0]
	if s0.Direction != "outbound" || !s0.Seen || s0.Subject != "Deneme" || len(s0.RawData()) == 0 || s0.MessageID == "" {
		t.Fatalf("giden kopya: %+v", s0)
	}
	if !strings.Contains(strings.ToLower(string(s0.RawData())), "bcc:") {
		t.Fatal("Sent kopyasi Bcc'yi korumali")
	}
}

func TestSubmission_FromHeaderMustMatch(t *testing.T) {
	e := newSubEnv(t, "pro")
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}
	err := c.SendMail(testBox, []string{"diger@mail.zorven.app"},
		strings.NewReader(msgFor("ceo@mail.zorven.app", "diger@mail.zorven.app", "")))
	if err == nil || smtpCode(err) != 550 {
		t.Fatalf("From baslik uyusmazligi 550 olmali: %v", err)
	}
	if e.relay.n != 0 {
		t.Fatal("sahte From iletilmemeli")
	}
}

func TestSubmission_RelayFailureIsTemporary(t *testing.T) {
	e := newSubEnv(t, "enterprise")
	e.relay.fail = true
	c := e.client(t)
	if err := c.Auth(sasl.NewPlainClient("", testBox, e.pw)); err != nil {
		t.Fatal(err)
	}
	err := c.SendMail(testBox, []string{"kisi@example.org"}, strings.NewReader(msgFor(testBox, "kisi@example.org", "")))
	if smtpCode(err) != 451 {
		t.Fatalf("relay hatasi 451 olmali: %v", err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderSent)); n != 0 {
		t.Fatal("iletilemeyen mesaj Sent'e yazilmamali")
	}
}
