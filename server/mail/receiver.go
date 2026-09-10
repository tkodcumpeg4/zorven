// Package mail, org-slug@mail.<domain> adreslerine gelen e-postalari kabul edip
// Postgres'e (store.MailMessage, inbound) yazan minimal bir SMTP alicisi saglar.
//
// Yalnizca bizim mail domainimize ve GECERLI bir kiraci slug'ina yonelik
// alicilar kabul edilir; digerleri 550 ile reddedilir (open relay degildir).
// Kimlik dogrulama yoktur — internetten gelen MTA'lar anonim teslim eder; bu
// yuzden yalnizca RCPT'i bilinen adreslere kisitlariz.
package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-smtp"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	maxMessageBytes = 25 * 1024 * 1024 // 25 MB
	maxRecipients   = 50
)

// Backend, gelen SMTP oturumlari icin store + domain baglamini tutar.
type Backend struct {
	Store store.Store
	// Domain, kiraci webmail domaini (or. "mail.zorven.app"): <slug>@Domain.
	Domain string
	// PlatformDomain, sitenin kendi domaini (or. "zorven.app"). Bu domain uzerinde
	// yalnizca SystemLocalParts icindeki adresler kabul edilir (info@, sales@ ...)
	// ve platform kiracisina (ten_default) yonlendirilir — owner gorur/yanitlar.
	PlatformDomain   string
	SystemLocalParts map[string]bool
}

// NewServer, verilen adreste (or. ":25") dinleyecek yapilandirilmis bir SMTP
// sunucusu doner. Cagiran taraf go ile server.ListenAndServe() calistirir.
func NewServer(st store.Store, domain, platformDomain string, systemLocalParts []string, addr string) *smtp.Server {
	sys := make(map[string]bool, len(systemLocalParts))
	for _, lp := range systemLocalParts {
		lp = strings.ToLower(strings.TrimSpace(lp))
		if lp != "" {
			sys[lp] = true
		}
	}
	be := &Backend{
		Store:            st,
		Domain:           strings.ToLower(strings.TrimSpace(domain)),
		PlatformDomain:   strings.ToLower(strings.TrimSpace(platformDomain)),
		SystemLocalParts: sys,
	}
	s := smtp.NewServer(be)
	s.Addr = addr
	s.Domain = be.Domain
	s.ReadTimeout = 60 * time.Second
	s.WriteTimeout = 60 * time.Second
	s.MaxMessageBytes = maxMessageBytes
	s.MaxRecipients = maxRecipients
	s.AllowInsecureAuth = true // 25. portta STARTTLS zorunlu degil; auth zaten yok
	return s
}

func (b *Backend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &session{be: b}, nil
}

// recipient, cozulmus bir alicidir (bize ait, gecerli slug).
type recipient struct {
	tenantID string
	addr     string
}

type session struct {
	be   *Backend
	from string
	rcpt []recipient
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.from = strings.TrimSpace(from)
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	to = strings.TrimSpace(strings.ToLower(to))
	at := strings.LastIndex(to, "@")
	if at < 0 {
		return &smtp.SMTPError{Code: 550, Message: "gecersiz alici adresi"}
	}
	local, domain := to[:at], to[at+1:]
	if local == "" {
		return &smtp.SMTPError{Code: 550, Message: "gecersiz alici"}
	}

	// (1) Sistem adresi: <lp>@<platformDomain> (info@zorven.app gibi). Platform
	// kiracisina (ten_default) yonlendirilir; owner panelden gorur/yanitlar.
	if s.be.PlatformDomain != "" && domain == s.be.PlatformDomain {
		if s.be.SystemLocalParts[local] {
			s.rcpt = append(s.rcpt, recipient{tenantID: store.DefaultTenantID, addr: to})
			return nil
		}
		return &smtp.SMTPError{Code: 550, Message: "boyle bir posta kutusu yok"}
	}

	// (2) Kiraci webmail adresi: <slug>@<mailDomain>.
	if domain != s.be.Domain {
		return &smtp.SMTPError{Code: 550, Message: "bu sunucu bu domain icin mail kabul etmiyor"}
	}
	t, err := s.be.Store.GetTenantBySlug(context.Background(), local)
	if err != nil {
		return &smtp.SMTPError{Code: 550, Message: "boyle bir posta kutusu yok"}
	}
	s.rcpt = append(s.rcpt, recipient{tenantID: t.ID, addr: to})
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

	subject, fromAddr, text, html, messageID, inReplyTo, attachments := parseMessage(raw)
	if fromAddr == "" {
		fromAddr = s.from
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, rc := range s.rcpt {
		saved, err := s.be.Store.InsertMailMessage(ctx, store.MailMessage{
			TenantID:  rc.tenantID,
			Direction: "inbound",
			From:      fromAddr,
			To:        rc.addr,
			Subject:   subject,
			TextBody:  text,
			HTMLBody:  html,
			MessageID: messageID,
			InReplyTo: inReplyTo,
			Seen:      false,
			Raw:       string(raw),
		})
		if err != nil {
			log.Printf("[mail] gelen mail kaydedilemedi (tenant=%s): %v", rc.tenantID, err)
			return &smtp.SMTPError{Code: 451, Message: "gecici depolama hatasi, sonra tekrar deneyin"}
		}
		for _, att := range attachments {
			att.MessageID = saved.ID
			att.TenantID = rc.tenantID
			if err := s.be.Store.InsertMailAttachment(ctx, att); err != nil {
				log.Printf("[mail] ek kaydedilemedi (msg=%s): %v", saved.ID, err)
			}
		}
	}
	log.Printf("[mail] %d aliciya gelen mail teslim edildi (from=%s subject=%q)", len(s.rcpt), fromAddr, subject)
	return nil
}

func (s *session) Reset() {
	s.from = ""
	s.rcpt = nil
}

func (s *session) Logout() error { return nil }

// parseMessage, ham RFC822 mesajindan konu, gonderen, metin/html govde ve
// Message-ID/In-Reply-To basliklarini cikarir. Coklu parcali (multipart)
// mesajlarda ilk text/plain ve text/html parcalari alinir; ekler atlanir.
func parseMessage(raw []byte) (subject, from, text, html, messageID, inReplyTo string, attachments []store.MailAttachment) {
	mr, err := mail.CreateReader(strings.NewReader(string(raw)))
	if err != nil {
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
