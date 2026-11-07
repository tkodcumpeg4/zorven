package mail

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	netmail "net/mail"
	"net/smtp"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// splitReceived, ham iletinin EN USTUNDEKI Received basligini (katlanmis devam
// satirlariyla) ve geri kalani ayirir; ileti Received ile baslamiyorsa testi
// bozar.
func splitReceived(t *testing.T, raw string) (received, rest string) {
	t.Helper()
	if !strings.HasPrefix(raw, "Received: ") {
		t.Fatalf("ileti Received ile baslamiyor:\n%s", raw)
	}
	i := 0
	for {
		nl := strings.Index(raw[i:], "\r\n")
		if nl < 0 {
			t.Fatalf("Received sonlanmiyor:\n%s", raw)
		}
		i += nl + 2
		if i >= len(raw) || (raw[i] != ' ' && raw[i] != '\t') {
			return raw[:i], raw[i:]
		}
	}
}

var fixedStamp = time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)

func TestReceivedHeaderFormat(t *testing.T) {
	base := receivedStamp{
		HELO: "mx.sender.example", IP: net.ParseIP("203.0.113.7"), By: "mail.zorven.app",
		ID: "ABC123", For: "acme@mail.zorven.app", At: fixedStamp,
	}
	cases := []struct {
		name string
		mod  func(*receivedStamp)
		want string
	}{
		{"duz ESMTP, tek alici", func(*receivedStamp) {},
			"Received: from mx.sender.example ([203.0.113.7])\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"STARTTLS -> ESMTPS", func(r *receivedStamp) { r.TLS = true },
			"Received: from mx.sender.example ([203.0.113.7])\r\n" +
				"\tby mail.zorven.app with ESMTPS id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"coklu alici: for yok", func(r *receivedStamp) { r.For = "" },
			"Received: from mx.sender.example ([203.0.113.7])\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"IPv6 adres degismezi", func(r *receivedStamp) { r.IP = net.ParseIP("2001:db8::1") },
			"Received: from mx.sender.example ([IPv6:2001:db8::1])\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"IPv4-eslemeli IPv6 IPv4 yazilir", func(r *receivedStamp) { r.IP = net.ParseIP("::ffff:203.0.113.9") },
			"Received: from mx.sender.example ([203.0.113.9])\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"IP yok", func(r *receivedStamp) { r.IP = nil },
			"Received: from mx.sender.example\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"bos HELO, bos by, kimliksiz", func(r *receivedStamp) { r.HELO, r.By, r.ID = "", "", "" },
			"Received: from unknown ([203.0.113.7])\r\n" +
				"\tby localhost with ESMTP\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
		{"HELO adres degismezi", func(r *receivedStamp) { r.HELO = "[203.0.113.7]" },
			"Received: from [203.0.113.7] ([203.0.113.7])\r\n" +
				"\tby mail.zorven.app with ESMTP id ABC123\r\n" +
				"\tfor <acme@mail.zorven.app>; Mon, 02 Jan 2006 15:04:05 +0000\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			c.mod(&r)
			if got := string(r.header()); got != c.want {
				t.Fatalf("baslik:\n%q\nbeklenen:\n%q", got, c.want)
			}
		})
	}
}

// Uretilen baslik RFC 5322 olarak ayristirilir; tarih ayrisir.
func TestReceivedHeaderParsesAsRFC5322(t *testing.T) {
	h := receivedStamp{
		HELO: "mx.sender.example", IP: net.ParseIP("203.0.113.7"), By: "mail.zorven.app",
		TLS: true, ID: newReceivedID(), For: "acme@mail.zorven.app", At: fixedStamp,
	}.header()
	msg, err := netmail.ReadMessage(strings.NewReader(string(h) + "Subject: x\r\n\r\ngovde\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := msg.Header["Received"]
	if len(got) != 1 {
		t.Fatalf("tek Received bekleniyordu: %v", got)
	}
	_, date, ok := strings.Cut(got[0], "; ")
	if !ok {
		t.Fatalf("tarih ayraci yok: %q", got[0])
	}
	if d, err := netmail.ParseDate(date); err != nil || !d.Equal(fixedStamp) {
		t.Fatalf("tarih ayristirilamadi: %q %v %v", date, d, err)
	}
	if msg.Header.Get("Subject") != "x" {
		t.Fatal("sonraki baslik bozuldu")
	}
}

var receivedIDRe = regexp.MustCompile(`^[0-9A-F]{16}$`)

func TestNewReceivedIDShapeAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newReceivedID()
		if !receivedIDRe.MatchString(id) || seen[id] {
			t.Fatalf("kimlik gecersiz ya da tekrar: %q", id)
		}
		seen[id] = true
	}
}

// EHLO adi istemci denetimindedir: CR/LF, bosluk, parantez, noktalivirgul gibi
// karakterler Received'a sizamaz, baslik tek bir mantiksal alan olarak kalir.
func TestReceivedHeaderSanitizesHELO(t *testing.T) {
	attacks := []string{
		"evil.example\r\nX-Injected: yes",
		"evil.example\nX-Injected: yes",
		"evil.example\rX-Injected: yes",
		"a b;c(d)e<f>g",
		"mx.example\x00\x07\x1b[31m",
		"büyük.example",
		"x\r\n\r\nGovde enjekte",
		strings.Repeat("a", 5000),
	}
	for _, helo := range attacks {
		h := string(receivedStamp{HELO: helo, IP: net.ParseIP("203.0.113.7"),
			By: "mail.zorven.app", ID: "ID1", For: "acme@mail.zorven.app", At: fixedStamp}.header())
		// Yalniz CRLF sonlari ve katlama sekmeleri: 3 mantiksal satir.
		lines := strings.Split(strings.TrimSuffix(h, "\r\n"), "\r\n")
		if len(lines) != 3 || strings.ContainsAny(strings.Join(lines, ""), "\r\n") {
			t.Fatalf("baslik 3 satirdan olusmali, CR/LF sizmamali (%q): %q", helo[:min(len(helo), 20)], h)
		}
		if !strings.HasPrefix(lines[0], "Received: from ") || !strings.HasPrefix(lines[1], "\tby ") || !strings.HasPrefix(lines[2], "\tfor <") {
			t.Fatalf("satir bicimi bozuldu: %q", h)
		}
		first := lines[0]
		hv := strings.TrimSuffix(strings.TrimPrefix(first, "Received: from "), " ([203.0.113.7])")
		if len(hv) > maxReceivedToken || strings.ContainsAny(hv, " ;()<>\x00\x1b") || strings.ContainsFunc(hv, func(r rune) bool { return r > 0x7e }) {
			t.Fatalf("HELO temizlenmedi: %q", hv)
		}
		msg, err := netmail.ReadMessage(strings.NewReader(h + "\r\nx"))
		if err != nil {
			t.Fatalf("baslik ayristirilamadi: %v", err)
		}
		if len(msg.Header) != 1 {
			t.Fatalf("yalniz Received olmali, enjekte baslik var: %v", msg.Header)
		}
	}
}

func TestReceivedHeaderSanitizesRecipientAndHost(t *testing.T) {
	h := string(receivedStamp{HELO: "mx.example", By: "mail.zorven.app\r\nBcc: v@v", ID: "A\r\nB",
		For: "a@b.example>\r\nX: y", At: fixedStamp}.header())
	if n := strings.Count(h, "\r\n"); n != 3 || strings.Count(h, "\n") != 3 || strings.Contains(h, "\r\r") {
		t.Fatalf("3 CRLF sonu bekleniyordu, baska CR/LF olmamali: %q", h)
	}
	msg, err := netmail.ReadMessage(strings.NewReader(h + "\r\nx"))
	if err != nil {
		t.Fatalf("baslik ayristirilamadi: %v", err)
	}
	if len(msg.Header) != 1 || len(msg.Header["Received"]) != 1 {
		t.Fatalf("yalniz tek Received olmali: %v", msg.Header)
	}
}

// Kabul edilen iletinin EN USTUNDE Received, hemen altinda Authentication-Results
// durur; govde bayt bayt korunur.
func TestReceiverAddsReceivedAtTopAboveAuthenticationResults(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	raw := rcvMsg("")
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox}, raw); err != nil {
		t.Fatalf("teslim: %v", err)
	}
	in := e.inbox()
	if len(in) != 1 {
		t.Fatalf("inbox=%d", len(in))
	}
	got := string(in[0].RawData())
	rcv, rest := splitReceived(t, got)

	re := regexp.MustCompile(`^Received: from mx\.sender\.example \(\[203\.0\.113\.7\]\)\r\n` +
		`\tby mail\.zorven\.app with ESMTP id [0-9A-F]{16}\r\n` +
		`\tfor <acme@mail\.zorven\.app>; [A-Z][a-z]{2}, \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} \+0000\r\n$`)
	if !re.MatchString(rcv) {
		t.Fatalf("Received bicimi beklenenden farkli:\n%q", rcv)
	}
	if !strings.HasPrefix(rest, "Authentication-Results: mail.zorven.app;\r\n") {
		t.Fatalf("Authentication-Results Received'in hemen altinda olmali:\n%s", got)
	}
	if !strings.HasSuffix(got, "\r\n\r\n"+"govde metni\r\n") || !strings.Contains(got, "Subject: merhaba\r\n") {
		t.Fatalf("orijinal baslik/govde korunmali:\n%s", got)
	}
	// Tarih simdiki zamana yakin.
	_, date, _ := strings.Cut(strings.TrimSuffix(rcv, "\r\n"), "; ")
	if d, err := netmail.ParseDate(date); err != nil || time.Since(d) > time.Minute || time.Until(d) > time.Minute {
		t.Fatalf("tarih simdiki zaman olmali: %q %v", date, err)
	}
}

// Birden cok alicida for yazilmaz (diger alicilar sizmasin); ayni adres tekrari tek sayilir.
func TestReceiverReceivedForOnlyWithSingleRecipient(t *testing.T) {
	e := newRcvEnv(t, AuthOff)
	if err := e.send(t, rcvSender, "bounce@example.com", []string{testBox, "info@zorven.app"}, rcvMsg("")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tenant, box string
	}{{testTenant, testBox}, {store.DefaultTenantID, "info@zorven.app"}} {
		msgs := e.st.all(c.tenant, c.box, store.MailFolderInbox)
		if len(msgs) != 1 {
			t.Fatalf("%s: %d ileti", c.box, len(msgs))
		}
		rcv, _ := splitReceived(t, string(msgs[0].RawData()))
		if strings.Contains(rcv, "\tfor ") || strings.Contains(rcv, "info@") || strings.Contains(rcv, "acme@") {
			t.Fatalf("coklu aliciya for yazilmamali:\n%s", rcv)
		}
	}

	e2 := newRcvEnv(t, AuthOff)
	if err := e2.send(t, rcvSender, "bounce@example.com", []string{testBox, strings.ToUpper(testBox)}, rcvMsg("")); err != nil {
		t.Fatal(err)
	}
	msgs := e2.inbox()
	if len(msgs) != 1 {
		t.Fatalf("ayni adres tek teslim: %d", len(msgs))
	}
	if rcv, _ := splitReceived(t, string(msgs[0].RawData())); !strings.Contains(rcv, "\tfor <acme@mail.zorven.app>;") {
		t.Fatalf("ayni adres tekrari tek alici sayilmali:\n%s", rcv)
	}
}

// HELO ile baslik enjeksiyonu: saklanan iletide sahte baslik olusmaz.
func TestReceiverHELOInjectionDoesNotCreateHeader(t *testing.T) {
	e := newRcvEnv(t, AuthOff)
	s := &session{be: e.be, remoteIP: net.ParseIP(rcvSender),
		helo: "evil.example\r\nX-Injected: yes\rBcc: victim@example.com"}
	if err := s.Mail("bounce@example.com", nil); err != nil {
		t.Fatal(err)
	}
	// Mail() baglantisiz oturumda helo'yu degistirmez (testler dogrudan doldurur).
	if err := s.Rcpt(testBox, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Data(strings.NewReader(rcvMsg(""))); err != nil {
		t.Fatal(err)
	}
	in := e.inbox()
	if len(in) != 1 {
		t.Fatalf("inbox=%d", len(in))
	}
	msg, err := netmail.ReadMessage(strings.NewReader(string(in[0].RawData())))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"X-Injected", "Bcc"} {
		if v := msg.Header.Get(name); v != "" {
			t.Fatalf("%s basligi enjekte edildi: %q", name, v)
		}
	}
	if got := msg.Header["Received"]; len(got) != 1 {
		t.Fatalf("tek Received bekleniyordu: %v", got)
	}
}

// STARTTLS kurulmus oturum ESMTPS, duz oturum ESMTP yazar (gercek SMTP uzerinden).
func TestReceiverReceivedProtocolOverTheWire(t *testing.T) {
	e := newRcvEnv(t, AuthOff)
	cert := testMailCert(t)
	srv := NewServerLocalAuth(e.loc, "", e.be.Auth)
	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	send := func(startTLS bool) {
		t.Helper()
		c, err := smtp.Dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Hello("client.sender.example"); err != nil {
			t.Fatal(err)
		}
		if startTLS {
			if err := c.StartTLS(&tls.Config{ServerName: "mail.zorven.app", RootCAs: pool}); err != nil {
				t.Fatalf("STARTTLS: %v", err)
			}
		}
		if err := c.Mail("bounce@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := c.Rcpt(testBox); err != nil {
			t.Fatal(err)
		}
		w, err := c.Data()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(rcvMsg(""))); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		_ = c.Quit()
	}
	send(false)
	send(true)

	in := e.inbox()
	if len(in) != 2 {
		t.Fatalf("2 ileti bekleniyordu: %d", len(in))
	}
	plain, _ := splitReceived(t, string(in[0].RawData()))
	secure, _ := splitReceived(t, string(in[1].RawData()))
	if !strings.Contains(plain, " with ESMTP id ") || strings.Contains(plain, "ESMTPS") {
		t.Fatalf("duz oturum ESMTP yazmali:\n%s", plain)
	}
	if !strings.Contains(secure, " with ESMTPS id ") {
		t.Fatalf("STARTTLS oturumu ESMTPS yazmali:\n%s", secure)
	}
	for _, r := range []string{plain, secure} {
		if !strings.Contains(r, "from client.sender.example ([127.0.0.1])") || !strings.Contains(r, "\tby mail.zorven.app with ") {
			t.Fatalf("from/by bekleniyordu:\n%s", r)
		}
	}
}

// RFC 5321 madde 4.5.1: alan adsiz "postmaster" platformun postmaster@<platform>
// sistem kutusuna gider (buyuk/kucuk harf duyarsiz); Received for satiri
// cozulmus adresi yazar. Bu, oturum (Rcpt) katmanidir; tel testi:
// receiver_postmaster_wire_test.go.
func TestReceiverAcceptsBarePostmasterRecipient(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"} // postmaster muaf

	for _, rcpt := range []string{"postmaster", "Postmaster", "  POSTMASTER "} {
		s := &session{be: e.be, remoteIP: net.ParseIP(rcvSender), helo: rcvHelo}
		_ = s.Mail("bounce@example.com", nil)
		if err := s.Rcpt(rcpt, nil); err != nil {
			t.Fatalf("%q kabul edilmeli: %v", rcpt, err)
		}
		if len(s.rcpt) != 1 || s.rcpt[0] != "postmaster@zorven.app" {
			t.Fatalf("%q postmaster@zorven.app'e cozulmeli: %v", rcpt, s.rcpt)
		}
		if err := s.Data(strings.NewReader(rcvMsg(""))); err != nil {
			t.Fatalf("%q teslim: %v", rcpt, err)
		}
	}
	got := e.st.all(store.DefaultTenantID, "postmaster@zorven.app", store.MailFolderInbox)
	if len(got) != 3 {
		t.Fatalf("postmaster sistem kutusunda 3 ileti bekleniyordu: %d", len(got))
	}
	if rcv, _ := splitReceived(t, string(got[0].RawData())); !strings.Contains(rcv, "\tfor <postmaster@zorven.app>;") {
		t.Fatalf("for cozulmus adresi yazmali:\n%s", rcv)
	}
}

// Yalniz "postmaster" ozeldir; diger alan adsiz adresler eskisi gibi reddedilir.
// Platform alan adi yoksa ya da postmaster sistem kutusu degilse de ret.
func TestReceiverBareRecipientRejections(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	for _, rcpt := range []string{"abuse", "info", "acme", "root"} {
		s := &session{be: e.be, remoteIP: net.ParseIP(rcvSender), helo: rcvHelo}
		_ = s.Mail("bounce@example.com", nil)
		err := s.Rcpt(rcpt, nil)
		if code, _ := rcvSMTPCode(err); code != 550 {
			t.Fatalf("%q icin 550 bekleniyordu: %v", rcpt, err)
		}
	}

	// Platform alan adi yok: alan adsiz postmaster cozulemez.
	noPlat := &Backend{Local: NewLocal(newMemMailStore(), "mail.zorven.app", "", []string{"postmaster"})}
	s := &session{be: noPlat}
	if code, _ := rcvSMTPCode(s.Rcpt("postmaster", nil)); code != 550 {
		t.Fatalf("platform alan adi yokken 550 bekleniyordu")
	}

	// postmaster sistem kutusu olarak yapilandirilmamis.
	st := newMemMailStore()
	noPM := &Backend{Local: NewLocal(st, "mail.zorven.app", "zorven.app", []string{"info"})}
	s = &session{be: noPM}
	err := s.Rcpt("postmaster", nil)
	if code, _ := rcvSMTPCode(err); code != 550 || !strings.Contains(err.Error(), "posta kutusu yok") {
		t.Fatalf("yapilandirilmamis postmaster icin 550 'kutu yok' bekleniyordu: %v", err)
	}
}
