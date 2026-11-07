// Package mail, org-slug@mail.<domain> adreslerine gelen e-postalari kabul edip
// Postgres'e (store.MailMessage, inbound) yazan minimal bir SMTP alicisi saglar.
//
// Yalnizca bizim mail domainimize ve GECERLI bir kiraci slug'ina yonelik
// alicilar kabul edilir; digerleri 550 ile reddedilir (open relay degildir).
// SMTP kimlik dogrulamasi (AUTH) yoktur — internetten gelen MTA'lar anonim
// teslim eder; bu yuzden yalnizca RCPT'i bilinen adreslere kisitlariz.
//
// Gelen her ileti DATA sonunda SPF / DKIM / DMARC ile dogrulanir (bkz.
// mailauth ve InboundAuth): sonuc iletinin en ustune Authentication-Results
// (RFC 8601) olarak yazilir; DMARC p=reject basarisizligi 550 5.7.1 ile
// reddedilir, p=quarantine ya da (DMARC kaydi yokken) SPF fail + gecerli DKIM
// yoklugu Onemsiz (\Junk) klasorune gider; gecici DNS hatalarinda ileti normal
// teslim edilir.
//
// Kabul edilen her iletiye RFC 5321 madde 4.4 izleme basligi (Received) eklenir
// ve EN USTE durur; Authentication-Results onun altindadir (bkz. received.go).
package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // ISO-8859-x, windows-125x vb. cozumleme
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-smtp"
	"github.com/tkodcumpeg4/zorven/server/mail/mailauth"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	maxMessageBytes = 25 * 1024 * 1024 // 25 MB
	maxRecipients   = 50
)

// Backend, gelen SMTP oturumlari icin yerel teslim baglamini tutar.
type Backend struct {
	// Local, adres cozumleme + INBOX teslimi (SMTP gonderim ve panel ile ortak).
	Local *Local
	// Auth, gelen iletilerin SPF/DKIM/DMARC dogrulamasidir; nil ise dogrulama
	// yapilmaz (eski davranis).
	Auth *InboundAuth
}

// NewServer, verilen adreste (or. ":25") dinleyecek yapilandirilmis bir SMTP
// sunucusu doner. Cagiran taraf go ile server.ListenAndServe() calistirir.
//
// domain: kiraci webmail domaini (or. "mail.zorven.app"): <slug>@domain.
// platformDomain: sitenin kendi domaini (or. "zorven.app"); bu domain uzerinde
// yalnizca systemLocalParts (info@, sales@ ...) kabul edilir ve platform
// kiracisina (ten_default) yonlendirilir — owner gorur/yanitlar.
func NewServer(st store.Store, domain, platformDomain string, systemLocalParts []string, addr string) *smtp.Server {
	return NewServerLocal(NewLocal(st, domain, platformDomain, systemLocalParts), addr)
}

// NewServerLocal, hazir bir Local ile gelen SMTP sunucusu kurar; kimlik
// dogrulama varsayilan kipte (AuthEnforce) ve sistem DNS cozumleyicisiyle acik gelir.
func NewServerLocal(l *Local, addr string) *smtp.Server {
	return NewServerLocalAuth(l, addr, NewInboundAuth(l, AuthEnforce, nil))
}

// NewServerLocalAuth, NewServerLocal gibidir; kimlik dogrulama ayarini disaridan
// alir (nil: dogrulama kapali).
func NewServerLocalAuth(l *Local, addr string, auth *InboundAuth) *smtp.Server {
	be := &Backend{Local: l, Auth: auth}
	s := smtp.NewServer(be)
	s.Addr = addr
	s.Domain = l.MailDomain
	s.ReadTimeout = 60 * time.Second
	s.WriteTimeout = 60 * time.Second
	s.MaxMessageBytes = maxMessageBytes
	s.MaxRecipients = maxRecipients
	s.AllowInsecureAuth = true // 25. portta STARTTLS zorunlu degil; auth zaten yok
	return s
}

func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &session{be: b, conn: c}, nil
}

type session struct {
	be   *Backend
	conn *smtp.Conn
	from string
	rcpt []string

	// Istemci bilgisi: Mail() aninda baglantidan alinir (testler dogrudan doldurur).
	remoteIP net.IP
	helo     string
	// tls, islem STARTTLS ile sifreli bir baglantida mi basladi (Received: with ESMTPS).
	tls bool
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.from = strings.TrimSpace(from)
	if s.conn != nil {
		// Mail() EHLO/HELO'dan sonra cagrilir; SPF icin istemci adresi ve HELO adi gerekir.
		s.helo = s.conn.Hostname()
		s.remoteIP = connIP(s.conn.Conn())
		// STARTTLS sonrasi istemci EHLO + MAIL'i yeniden verir; durum islem basinda okunur.
		_, s.tls = s.conn.TLSConnectionState()
	}
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	to = strings.TrimSpace(strings.ToLower(to))
	// RFC 5321 madde 4.5.1: "postmaster" alan adsiz da gecerli bir alicidir ve
	// desteklenmesi ZORUNLUDUR; platformun postmaster@<platform> sistem kutusuna gider.
	// Platform alan adi yoksa (gelistirme) eski davranis: alan adsiz adres reddedilir.
	//
	// Tel uzerinde: go-smtp yerel kopyasi (server/third_party/go-smtp, YAMA.md)
	// RCPT'te alan adsiz postmaster'i oturuma iletir.
	if to == "postmaster" && s.be.Local.PlatformDomain != "" {
		to += "@" + s.be.Local.PlatformDomain
	}
	at := strings.LastIndex(to, "@")
	if at < 0 {
		return &smtp.SMTPError{Code: 550, Message: "gecersiz alici adresi"}
	}
	if to[:at] == "" {
		return &smtp.SMTPError{Code: 550, Message: "gecersiz alici"}
	}
	// Yalnizca bizim alan adlarimiz ve var olan kutular (open relay degil).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, local, err := s.be.Local.Resolve(ctx, to)
	switch {
	case !local:
		return &smtp.SMTPError{Code: 550, Message: "bu sunucu bu domain icin mail kabul etmiyor"}
	case errors.Is(err, ErrNoSuchMailbox):
		return &smtp.SMTPError{Code: 550, Message: "boyle bir posta kutusu yok"}
	case err != nil:
		return &smtp.SMTPError{Code: 451, Message: "gecici hata, sonra tekrar deneyin"}
	}
	s.rcpt = append(s.rcpt, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if len(s.rcpt) == 0 {
		return &smtp.SMTPError{Code: 550, Message: "gecerli alici yok"}
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxMessageBytes))
	if err != nil {
		return fmt.Errorf("mail govdesi okunamadi: %w", err)
	}

	// Kimlik dogrulama (SPF / DKIM / DMARC): kendi DNS zaman siniri vardir
	// (mailauth.DefaultTimeout); teslim baglami bundan ayridir.
	var opts DeliverOpts
	if a := s.be.Auth; a != nil && a.Verifier != nil {
		res := a.Verifier.Verify(context.Background(), mailauth.Input{
			ClientIP: s.remoteIP, HELO: s.helo, MailFrom: s.from, Message: raw,
		})
		// Sahte Authentication-Results basliklari silinir, sonuc en uste eklenir.
		raw = mailauth.Prepend(raw, a.AuthservID, res, a.StripIDs...)
		act := a.action(res, s.rcpt)
		log.Printf("[mail] gelen ileti dogrulamasi: %s", authSummary(res, s.remoteIP, act))
		switch act {
		case mailauth.ActionReject:
			return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: rejectText(res)}
		case mailauth.ActionJunk:
			opts.Junk = true
		}
	}

	// RFC 5321 madde 4.4: izleme basligi EN USTE (Authentication-Results altinda kalir).
	raw = append(s.receivedHeader(), raw...)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	n, err := s.be.Local.DeliverWith(ctx, s.from, s.rcpt, raw, opts)
	if err != nil {
		return &smtp.SMTPError{Code: 451, Message: "gecici depolama hatasi, sonra tekrar deneyin"}
	}
	log.Printf("[mail] %d aliciya gelen mail teslim edildi (from=%s)", n, s.from)
	return nil
}

// receivedHeader, bu islem icin Received satirlarini uretir. "for" yalniz tek
// (tekil) alici varsa yazilir: coklu aliciya yazmak diger alicilari sizdirirdi.
func (s *session) receivedHeader() []byte {
	forRcpt := ""
	if len(s.rcpt) > 0 {
		forRcpt = s.rcpt[0]
		for _, rc := range s.rcpt[1:] {
			if rc != forRcpt {
				forRcpt = ""
				break
			}
		}
	}
	return receivedStamp{
		HELO: s.helo, IP: s.remoteIP, By: s.be.Local.MailDomain, TLS: s.tls,
		ID: newReceivedID(), For: forRcpt, At: time.Now(),
	}.header()
}

func (s *session) Reset() {
	s.from = ""
	s.rcpt = nil
	s.tls = false
}

func (s *session) Logout() error { return nil }

// parseMessage, ham RFC822 mesajindan konu, gonderen, metin/html govde ve
// Message-ID/In-Reply-To basliklarini cikarir. Coklu parcali (multipart)
// mesajlarda ilk text/plain ve text/html parcalari alinir; ekler atlanir.
//
// Donen metinler Postgres TEXT'e guvenle yazilabilir (NUL temiz, gecerli UTF-8);
// birebir bayt korumasi ham mesajda (raw_bytes) yapilir.
func parseMessage(raw []byte) (subject, from, text, html, messageID, inReplyTo string, attachments []store.MailAttachment) {
	subject, from, text, html, messageID, inReplyTo, attachments = parseMessageRaw(raw)
	return cleanText(subject), from, cleanText(text), cleanText(html), messageID, inReplyTo, attachments
}

// cleanText, TEXT sutununa yazilamayan NUL baytlarini ve gecersiz UTF-8'i temizler.
func cleanText(s string) string {
	if s == "" {
		return s
	}
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "?")
	}
	return s
}

func parseMessageRaw(raw []byte) (subject, from, text, html, messageID, inReplyTo string, attachments []store.MailAttachment) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		// Parse edilemezse en azindan ham govdeyi metin olarak sakla.
		return "", "", string(raw), "", "", "", nil
	}
	h := mr.Header
	subject, _ = h.Subject()
	messageID = strings.TrimSpace(h.Get("Message-Id"))
	inReplyTo = strings.TrimSpace(h.Get("In-Reply-To"))
	if addrs, e := h.AddressList("From"); e == nil && len(addrs) > 0 {
		from = addrs[0].String()
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		switch ph := part.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := ph.ContentType()
			body, _ := io.ReadAll(part.Body)
			if strings.HasPrefix(ct, "text/html") {
				if html == "" {
					html = string(body)
				}
			} else if strings.HasPrefix(ct, "text/") {
				if text == "" {
					text = string(body)
				}
			}
		case *mail.AttachmentHeader:
			body, err := io.ReadAll(io.LimitReader(part.Body, maxMessageBytes))
			if err != nil || len(body) == 0 {
				continue
			}
			filename, _ := ph.Filename()
			if strings.TrimSpace(filename) == "" {
				filename = "dosya"
			}
			ct, _, _ := ph.ContentType()
			if ct == "" {
				ct = "application/octet-stream"
			}
			attachments = append(attachments, store.MailAttachment{
				Filename:    filename,
				ContentType: ct,
				SizeBytes:   int64(len(body)),
				Content:     body,
			})
		}
	}
	return subject, from, text, html, messageID, inReplyTo, attachments
}
