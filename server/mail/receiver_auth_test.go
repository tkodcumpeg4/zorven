package mail

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-smtp"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Bu dosyadaki testler ag kullanmaz: DNS fakeMailDNS, depo memMailStore'dur.

type fakeMailDNS struct {
	txt  map[string][]string
	errs map[string]error
}

func newFakeMailDNS() *fakeMailDNS {
	return &fakeMailDNS{txt: map[string][]string{}, errs: map[string]error{}}
}

func dnsNX(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f *fakeMailDNS) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	if e, ok := f.errs[n]; ok {
		return nil, e
	}
	if v, ok := f.txt[n]; ok {
		return v, nil
	}
	return nil, dnsNX(n)
}
func (f *fakeMailDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, dnsNX(name)
}
func (f *fakeMailDNS) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, dnsNX(host)
}
func (f *fakeMailDNS) LookupAddr(_ context.Context, addr string) ([]string, error) {
	return nil, dnsNX(addr)
}

const (
	rcvSender = "203.0.113.7" // genel bir istemci adresi (TEST-NET-3)
	rcvHelo   = "mx.sender.example"
)

type rcvEnv struct {
	st  *memMailStore
	loc *Local
	dns *fakeMailDNS
	be  *Backend
}

func newRcvEnv(t *testing.T, mode InboundAuthMode) *rcvEnv {
	t.Helper()
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "pro")
	loc := NewLocal(st, "mail.zorven.app", "zorven.app", []string{"info", "postmaster", "abuse"})
	loc.Hub = NewHub()
	dns := newFakeMailDNS()
	return &rcvEnv{st: st, loc: loc, dns: dns, be: &Backend{Local: loc, Auth: NewInboundAuth(loc, mode, dns)}}
}

// msg, example.com'dan gelen basit bir iletidir; extra ek baslik satirlaridir.
func rcvMsg(extra string) string {
	return "From: Alice <alice@example.com>\r\n" +
		"To: acme@mail.zorven.app\r\n" +
		"Subject: merhaba\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"Message-ID: <abc@example.com>\r\n" +
		extra +
		"\r\n" +
		"govde metni\r\n"
}

// send, SMTP oturumunu (MAIL, RCPT, DATA) surer ve DATA sonucunu doner.
func (e *rcvEnv) send(t *testing.T, ip, mailFrom string, rcpts []string, raw string) error {
	t.Helper()
	s := &session{be: e.be, remoteIP: net.ParseIP(ip), helo: rcvHelo}
	if err := s.Mail(mailFrom, nil); err != nil {
		t.Fatalf("MAIL: %v", err)
	}
	for _, r := range rcpts {
		if err := s.Rcpt(r, nil); err != nil {
			t.Fatalf("RCPT %s: %v", r, err)
		}
	}
	return s.Data(strings.NewReader(raw))
}

func junkKey() string { return store.MailUserFolderKey(store.MailJunkFolderName) }

func (e *rcvEnv) inbox() []store.MailMessage {
	return e.st.all(testTenant, testBox, store.MailFolderInbox)
}
func (e *rcvEnv) junk() []store.MailMessage { return e.st.all(testTenant, testBox, junkKey()) }

func rcvSMTPCode(err error) (int, smtp.EnhancedCode) {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return se.Code, se.EnhancedCode
	}
	return 0, smtp.EnhancedCode{}
}

func TestReceiverDeliversToInboxWithAuthenticationResults(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}

	// Ayni authserv-id ile gelmis sahte baslik silinir, baska kimlikliler kalir.
	raw := rcvMsg("Authentication-Results: mail.zorven.app; dkim=pass header.d=bank.example\r\n" +
		"Authentication-Results: upstream.example; spf=pass\r\n")
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, raw); err != nil {
		t.Fatalf("teslim hatasi: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 || len(e.junk()) != 0 {
		t.Fatalf("inbox=%d junk=%d", len(in), len(e.junk()))
	}
	got := string(in[0].RawData())
	// Received (izleme) en ustte; Authentication-Results hemen altinda.
	_, afterReceived := splitReceived(t, got)
	if !strings.HasPrefix(afterReceived, "Authentication-Results: mail.zorven.app;\r\n\tspf=pass (client-ip=203.0.113.7) smtp.mailfrom=bounce@example.com;\r\n") {
		t.Fatalf("Authentication-Results Received'in hemen altinda degil:\n%s", got)
	}
	if !strings.Contains(got, "\tdmarc=pass (p=reject dis=none) header.from=example.com\r\n") {
		t.Fatalf("dmarc sonucu yok:\n%s", got)
	}
	if strings.Contains(got, "bank.example") {
		t.Fatalf("sahte Authentication-Results silinmedi:\n%s", got)
	}
	if !strings.Contains(got, "Authentication-Results: upstream.example; spf=pass\r\n") {
		t.Fatalf("baska kimlikli baslik silindi:\n%s", got)
	}
	if !strings.HasSuffix(got, "\r\n\r\ngovde metni\r\n") {
		t.Fatalf("govde degisti:\n%q", got)
	}
	// Ayristirilan alanlar etkilenmez.
	if in[0].Subject != "merhaba" || !strings.Contains(in[0].From, "alice@example.com") {
		t.Fatalf("alanlar: subject=%q from=%q", in[0].Subject, in[0].From)
	}
}

func TestReceiverRejectsDMARCReject(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 ip4:198.51.100.1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}

	err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg(""))
	code, enh := rcvSMTPCode(err)
	if code != 550 || enh != (smtp.EnhancedCode{5, 7, 1}) {
		t.Fatalf("550 5.7.1 bekleniyordu, hata=%v (kod=%d %v)", err, code, enh)
	}
	if !strings.Contains(err.Error(), "example.com") || !strings.Contains(err.Error(), "DMARC") {
		t.Fatalf("ret metni: %v", err)
	}
	if len(e.inbox()) != 0 || len(e.junk()) != 0 {
		t.Fatal("reddedilen ileti saklandi")
	}
	if l, _ := e.st.ListMailFolders(context.Background(), testTenant, testBox); len(l) != 0 {
		t.Fatalf("ret sirasinda klasor olustu: %+v", l)
	}
}

func TestReceiverRejectLeavesNoPartialDelivery(t *testing.T) {
	// Birden cok alici: ret butun islemi kapsar, hicbir kutuya yazilmaz.
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	err := e.send(t, rcvSender, "bounce@example.com", []string{testBox, "info@zorven.app"}, rcvMsg(""))
	if code, _ := rcvSMTPCode(err); code != 550 {
		t.Fatalf("550 bekleniyordu: %v", err)
	}
	if len(e.inbox()) != 0 || len(e.st.all(store.DefaultTenantID, "info@zorven.app", store.MailFolderInbox)) != 0 {
		t.Fatal("kismi teslim oldu")
	}
}

func TestReceiverQuarantineGoesToJunkAndCreatesFolder(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	if len(e.inbox()) != 0 {
		t.Fatal("karantina iletisi Gelen Kutusu'na yazildi")
	}
	j := e.junk()
	if len(j) != 1 {
		t.Fatalf("junk=%d", len(j))
	}
	if !strings.Contains(string(j[0].RawData()), "dmarc=fail (p=quarantine dis=quarantine) header.from=example.com") {
		t.Fatalf("baslik:\n%s", j[0].RawData())
	}
	if j[0].Seen {
		t.Fatal("Onemsiz iletisi okunmus isaretli")
	}

	// Klasor \Junk ile olusturuldu.
	l, _ := e.st.ListMailFolders(context.Background(), testTenant, testBox)
	if len(l) != 1 || l[0].Name != "Junk" || l[0].SpecialUse != `\Junk` {
		t.Fatalf("klasorler: %+v", l)
	}

	// Ikinci Onemsiz ileti ayni klasoru kullanir, yenisini olusturmaz.
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("ikinci teslim: %v", err)
	}
	l, _ = e.st.ListMailFolders(context.Background(), testTenant, testBox)
	if len(l) != 1 || len(e.junk()) != 2 {
		t.Fatalf("klasorler=%+v junk=%d", l, len(e.junk()))
	}
	if u := e.junk(); u[0].UID != 1 || u[1].UID != 2 {
		t.Fatalf("UID'ler: %d %d", u[0].UID, u[1].UID)
	}
}

func TestReceiverSPFFailWithoutDMARCGoesToJunk(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 ip4:198.51.100.1 -all"} // DMARC kaydi yok

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	if len(e.junk()) != 1 || len(e.inbox()) != 0 {
		t.Fatalf("junk=%d inbox=%d", len(e.junk()), len(e.inbox()))
	}
}

func TestReceiverSPFFailWithValidDKIMStaysInInbox(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}

	// Ucuncu taraf (liste/ESP) imzasi gecerli: DMARC yok + SPF fail olsa da Junk degil.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e.dns.txt["sel._domainkey.esp.example.net"] = []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)}
	var b bytes.Buffer
	if err := dkim.Sign(&b, strings.NewReader(rcvMsg("")), &dkim.SignOptions{
		Domain: "esp.example.net", Selector: "sel", Signer: priv,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, b.String()); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 || len(e.junk()) != 0 {
		t.Fatalf("inbox=%d junk=%d", len(in), len(e.junk()))
	}
	if !strings.Contains(string(in[0].RawData()), "\tdkim=pass header.d=esp.example.net;\r\n") {
		t.Fatalf("dkim=pass satiri yok:\n%s", in[0].RawData())
	}
	// Uctan uca: DKIM-Signature basligi korunur (en ust Authentication-Results, altinda imza).
	if !strings.Contains(string(in[0].RawData()), "\r\nDKIM-Signature: ") {
		t.Fatal("DKIM-Signature basligi kayboldu")
	}
}

func TestReceiverTempErrorDeliversNormally(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.errs["_dmarc.example.com"] = &net.DNSError{Err: "server misbehaving", IsTemporary: true}

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 || len(e.junk()) != 0 {
		t.Fatalf("inbox=%d junk=%d", len(in), len(e.junk()))
	}
	if !strings.Contains(string(in[0].RawData()), "dmarc=temperror") {
		t.Fatalf("temperror yazilmadi:\n%s", in[0].RawData())
	}
}

func TestReceiverDNSDeadlineDeliversNormally(t *testing.T) {
	// Tum DNS sorgulari takilir: toplam sure dolunca ileti yine de teslim edilir.
	e := newRcvEnv(t, AuthEnforce)
	e.be.Auth.Verifier.Timeout = 150 * time.Millisecond
	e.be.Auth.Verifier.Resolver = hangDNS{}

	start := time.Now()
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("teslim %v surdu", el)
	}
	in := e.inbox()
	if len(in) != 1 {
		t.Fatalf("inbox=%d", len(in))
	}
	if !strings.Contains(string(in[0].RawData()), "spf=temperror") {
		t.Fatalf("temperror yazilmadi:\n%s", in[0].RawData())
	}
}

type hangDNS struct{}

func (hangDNS) LookupTXT(ctx context.Context, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangDNS) LookupMX(ctx context.Context, _ string) ([]*net.MX, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangDNS) LookupIPAddr(ctx context.Context, _ string) ([]net.IPAddr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (hangDNS) LookupAddr(ctx context.Context, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestReceiverReusesExistingJunkFolder(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}
	ctx := context.Background()

	// (1) Istemcinin \Junk ile olusturdugu baska adli klasor kullanilir.
	if err := e.st.CreateMailFolder(ctx, testTenant, testBox, "Istenmeyen", `\Junk`); err != nil {
		t.Fatal(err)
	}
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatal(err)
	}
	if got := e.st.all(testTenant, testBox, store.MailUserFolderKey("Istenmeyen")); len(got) != 1 {
		t.Fatalf("\\Junk klasorunde %d ileti", len(got))
	}
	if l, _ := e.st.ListMailFolders(ctx, testTenant, testBox); len(l) != 1 {
		t.Fatalf("yeni klasor olustu: %+v", l)
	}

	// (2) Ozel nitelik yoksa "junk" adli klasor (buyuk/kucuk harf farkli) kullanilir.
	e2 := newRcvEnv(t, AuthEnforce)
	e2.dns.txt = e.dns.txt
	if err := e2.st.CreateMailFolder(ctx, testTenant, testBox, "junk", ""); err != nil {
		t.Fatal(err)
	}
	if err := e2.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatal(err)
	}
	if got := e2.st.all(testTenant, testBox, store.MailUserFolderKey("junk")); len(got) != 1 {
		t.Fatalf("junk klasorunde %d ileti", len(got))
	}
	if l, _ := e2.st.ListMailFolders(ctx, testTenant, testBox); len(l) != 1 {
		t.Fatalf("yeni klasor olustu: %+v", l)
	}
}

// failCreateStore, klasor olusturmayi reddeder (ornegin klasor sayisi siniri).
type failCreateStore struct{ *memMailStore }

func (failCreateStore) CreateMailFolder(context.Context, string, string, string, string) error {
	return store.ErrMailFolderLimit
}

func TestReceiverJunkFallsBackToInboxWhenFolderCannotBeCreated(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.loc.Store = failCreateStore{e.st}
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	if len(e.inbox()) != 1 {
		t.Fatalf("posta kaybolmamali: inbox=%d", len(e.inbox()))
	}
}

func TestReceiverMonitorModeNeverEnforces(t *testing.T) {
	e := newRcvEnv(t, AuthMonitor)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
		t.Fatalf("izleme kipinde ret olmamali: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 || len(e.junk()) != 0 {
		t.Fatalf("inbox=%d junk=%d", len(in), len(e.junk()))
	}
	if !strings.Contains(string(in[0].RawData()), "dmarc=fail (p=reject dis=reject)") {
		t.Fatalf("sonuc yine de yazilmali:\n%s", in[0].RawData())
	}
}

// Dogrulama kapaliyken Authentication-Results eklenmez; iletiye yalniz izleme
// basligi (Received) eklenir, geri kalani degismez.
func TestReceiverOffModeAddsOnlyReceived(t *testing.T) {
	e := newRcvEnv(t, AuthOff)
	if e.be.Auth != nil {
		t.Fatal("AuthOff icin Auth nil olmali")
	}
	raw := rcvMsg("")
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, raw); err != nil {
		t.Fatal(err)
	}
	in := e.inbox()
	if len(in) != 1 {
		t.Fatalf("inbox=%d", len(in))
	}
	got := string(in[0].RawData())
	if _, rest := splitReceived(t, got); rest != raw {
		t.Fatalf("kapali kipta Received disinda ileti degismemeli:\n%s", got)
	}
	if strings.Contains(got, "Authentication-Results") {
		t.Fatalf("kapali kipta Authentication-Results eklenmemeli:\n%s", got)
	}
}

func TestReceiverExemptsPostmasterAndAbuse(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}

	for _, rcpt := range []string{"postmaster@zorven.app", "ABUSE@zorven.app"} {
		if err := e.send(t, rcvSender, "bounce@example.com", []string{rcpt}, rcvMsg("")); err != nil {
			t.Fatalf("%s reddedilmemeli: %v", rcpt, err)
		}
	}
	if got := e.st.all(store.DefaultTenantID, "postmaster@zorven.app", store.MailFolderInbox); len(got) != 1 {
		t.Fatalf("postmaster inbox=%d", len(got))
	}
	if got := e.st.all(store.DefaultTenantID, "abuse@zorven.app", store.MailFolderInbox); len(got) != 1 {
		t.Fatalf("abuse inbox=%d", len(got))
	}
	// Ayni ileti normal bir kutuya reddedilir.
	err := e.send(t, rcvSender, "bounce@example.com", []string{"info@zorven.app"}, rcvMsg(""))
	if code, _ := rcvSMTPCode(err); code != 550 {
		t.Fatalf("info@ icin 550 bekleniyordu: %v", err)
	}
}

func TestReceiverNonPublicClientIsNotEnforced(t *testing.T) {
	// Docker proxy / yerel istemci: SPF anlamsiz, DMARC reddi uygulanmaz.
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	for _, ip := range []string{"127.0.0.1", "172.17.0.1", "10.2.3.4", "::1"} {
		if err := e.send(t, ip, "bounce@example.com", []string{testBox}, rcvMsg("")); err != nil {
			t.Fatalf("%s reddedilmemeli: %v", ip, err)
		}
	}
	in := e.inbox()
	if len(in) != 4 || len(e.junk()) != 0 {
		t.Fatalf("inbox=%d junk=%d", len(in), len(e.junk()))
	}
	if !strings.Contains(string(in[0].RawData()), "spf=none (client-ip=127.0.0.1, skipped: non-public client address)") {
		t.Fatalf("baslik:\n%s", in[0].RawData())
	}
}

func TestReceiverMultiRecipientJunkPerMailbox(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}

	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox, "info@zorven.app"}, rcvMsg("")); err != nil {
		t.Fatal(err)
	}
	if len(e.junk()) != 1 {
		t.Fatalf("acme junk=%d", len(e.junk()))
	}
	if got := e.st.all(store.DefaultTenantID, "info@zorven.app", junkKey()); len(got) != 1 {
		t.Fatalf("info junk=%d", len(got))
	}
	for _, mb := range []struct{ tenant, box string }{{testTenant, testBox}, {store.DefaultTenantID, "info@zorven.app"}} {
		l, _ := e.st.ListMailFolders(context.Background(), mb.tenant, mb.box)
		if len(l) != 1 || l[0].SpecialUse != `\Junk` {
			t.Fatalf("%s klasorleri: %+v", mb.box, l)
		}
	}
}

func TestReceiverSessionCapturesClientInfoFromConn(t *testing.T) {
	// Gercek bir SMTP istemcisiyle: IP ve HELO oturumdan alinir (loopback -> SPF atlanir).
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	srv := smtp.NewServer(e.be)
	srv.Domain = "mail.zorven.app"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sc := smtp.NewClient(c)
	defer sc.Close()
	if err := sc.Hello("client.sender.example"); err != nil {
		t.Fatal(err)
	}
	if err := sc.SendMail("bounce@example.com", []string{testBox}, strings.NewReader(rcvMsg(""))); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 {
		t.Fatalf("inbox=%d", len(in))
	}
	if !strings.Contains(string(in[0].RawData()), "client-ip=127.0.0.1") {
		t.Fatalf("istemci adresi alinmadi:\n%s", in[0].RawData())
	}
}

// Onemsiz klasoru IMAP'te \Junk ile sunulur; ileti SELECT/FETCH ile okunur.
func TestIMAPJunkFolderAfterInboundQuarantine(t *testing.T) {
	ie := newIMAPEnv(t)
	loc := NewLocal(ie.st, "mail.zorven.app", "zorven.app", []string{"info"})
	loc.Hub = ie.hub
	dns := newFakeMailDNS()
	dns.txt["example.com"] = []string{"v=spf1 -all"}
	dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}
	be := &Backend{Local: loc, Auth: NewInboundAuth(loc, AuthEnforce, dns)}

	s := &session{be: be, remoteIP: net.ParseIP(rcvSender), helo: rcvHelo}
	_ = s.Mail("bounce@example.com", nil)
	if err := s.Rcpt(testBox, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Data(strings.NewReader(rcvMsg(""))); err != nil {
		t.Fatal(err)
	}

	c := ie.login(t)
	all := listAll(t, c, "*", &imap.ListOptions{ReturnSpecialUse: true})
	if !hasAttr(all["Junk"], imap.MailboxAttrJunk) {
		t.Fatalf("Junk \\Junk ile listelenmedi: %+v", all)
	}
	sel := listAll(t, c, "*", &imap.ListOptions{SelectSpecialUse: true})
	if sel["Junk"] == nil {
		t.Fatalf("SPECIAL-USE secimi Junk'i icermiyor: %v", sel)
	}
	if st, err := c.Select("INBOX", nil).Wait(); err != nil || st.NumMessages != 0 {
		t.Fatalf("INBOX bos olmali: %+v %v", st, err)
	}
	st, err := c.Select("Junk", nil).Wait()
	if err != nil || st.NumMessages != 1 {
		t.Fatalf("Junk: %+v %v", st, err)
	}
	sec := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true,
		BodySection: []*imap.FetchItemBodySection{sec}}).Collect()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("fetch: %v %d", err, len(msgs))
	}
	body := string(msgs[0].FindBodySection(sec))
	_, afterReceived := splitReceived(t, body)
	if !strings.HasPrefix(afterReceived, "Authentication-Results: mail.zorven.app;") || !strings.Contains(body, "dmarc=fail") {
		t.Fatalf("IMAP govdesi:\n%s", body)
	}
}
