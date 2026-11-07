package mail

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-smtp"
)

// startRcvServer, e.be ile gercek bir go-smtp sunucusu acar ve adresini doner.
func startRcvServer(t *testing.T, e *rcvEnv) string {
	t.Helper()
	srv := smtp.NewServer(e.be)
	srv.Domain = "mail.zorven.app"
	srv.AllowInsecureAuth = true
	srv.MaxMessageBytes = maxMessageBytes
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// Tel uzerinden gelen (nokta-doldurmali, CRLF'li) imzali ileti, go-smtp'nin
// DATA okuyucusundan gectikten sonra da DKIM ile dogrulanir.
func TestReceiverWireDKIMSignedMessageVerifies(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e.dns.txt["sel._domainkey.example.com"] = []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}

	// Govdede nokta ile baslayan satir ve uzun satir: dot-stuffing / katlama sinanir.
	body := "ilk satir\r\n.nokta ile baslayan satir\r\n" + strings.Repeat("x", 300) + "\r\nson satir\r\n"
	raw := "From: Alice <alice@example.com>\r\nTo: " + testBox + "\r\nSubject: imzali\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <w1@example.com>\r\n\r\n" + body
	for _, c := range []struct {
		name string
		hc   dkim.Canonicalization
		bc   dkim.Canonicalization
	}{
		{"relaxed", dkim.CanonicalizationRelaxed, dkim.CanonicalizationRelaxed},
		{"simple", dkim.CanonicalizationSimple, dkim.CanonicalizationSimple},
	} {
		t.Run(c.name, func(t *testing.T) {
			var signed bytes.Buffer
			if err := dkim.Sign(&signed, strings.NewReader(raw), &dkim.SignOptions{
				Domain: "example.com", Selector: "sel", Signer: priv,
				HeaderCanonicalization: c.hc, BodyCanonicalization: c.bc,
			}); err != nil {
				t.Fatal(err)
			}
			addr := startRcvServer(t, e)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			sc := smtp.NewClient(conn)
			defer sc.Close()
			if err := sc.Hello("client.sender.example"); err != nil {
				t.Fatal(err)
			}
			before := len(e.inbox())
			if err := sc.SendMail("bounce@example.com", []string{testBox}, bytes.NewReader(signed.Bytes())); err != nil {
				t.Fatalf("SendMail: %v", err)
			}
			in := e.inbox()
			if len(in) != before+1 {
				t.Fatalf("inbox=%d (onceki %d)", len(in), before)
			}
			got := string(in[len(in)-1].RawData())
			if !strings.Contains(got, "\tdkim=pass header.d=example.com;\r\n") {
				t.Fatalf("DKIM tel uzerinden dogrulanamadi:\n%s", got)
			}
			if !strings.Contains(got, "dmarc=pass (p=reject dis=none) header.from=example.com") {
				t.Fatalf("dmarc sonucu:\n%s", got)
			}
			// Govde bayt bayt korunur (nokta-doldurma geri alinmis).
			if !strings.HasSuffix(got, "\r\n\r\n"+body) {
				t.Fatalf("govde degisti:\n%q", got)
			}
		})
	}
}
