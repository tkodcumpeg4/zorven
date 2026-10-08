package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// Attachment, giden bir maile eklenecek dosya.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Sender, giden e-postalari yerel Postfix relay'ine (or. "mail:587") teslim
// eder. Relay ic Docker aginda auth istemez (mailer.ts ile ayni davranis).
type Sender struct {
	relayAddr string // "host:port"
	domain    string // "mail.zorven.app" — Message-ID ve HELO icin
	// Local doluysa yerel alicilar (<slug>@mail.<domain>, <sistem>@<platform>)
	// relay'e GITMEZ (Postfix "loops back to myself" ile geri cevirir); dogrudan
	// INBOX'a teslim edilir.
	Local *Local
}

// NewSender, relay adresi ve mail domaini ile bir gonderici olusturur.
func NewSender(relayAddr, domain string) *Sender {
	return &Sender{
		relayAddr: strings.TrimSpace(relayAddr),
		domain:    strings.ToLower(strings.TrimSpace(domain)),
	}
}

// Send, tek alicili duz-metin bir e-posta gonderir. Yanit ise inReplyTo,
// yanitlanan mesajin Message-ID'sidir (bos gecilebilir). Uretilen Message-ID
// doner (kayit icin).
// htmlBody bos gecilebilir; doluysa e-posta multipart/alternative (text + html)
// olarak gonderilir (istemciler HTML'i gosterir, metin fallback kalir).
func (s *Sender) Send(from, to, subject, body, htmlBody, inReplyTo string, attachments []Attachment) (string, error) {
	// Gonderen ve alici TEK ayristiricidan (ParseAddress) gecer; basliga ve
	// SMTP zarfina ayni yalin adres yazilir (kisit/zarf ayrismasi olmasin).
	var err error
	if from, err = ParseAddress(from); err != nil {
		return "", fmt.Errorf("gonderen adresi gecersiz: %w", err)
	}
	if to, err = ParseAddress(to); err != nil {
		return "", fmt.Errorf("alici adresi gecersiz: %w", err)
	}
	inReplyTo = strings.TrimSpace(inReplyTo)
	if !ValidHeaderValue(inReplyTo) {
		return "", fmt.Errorf("in-reply-to gecersiz")
	}

	messageID := fmt.Sprintf("<%s@%s>", randHex(16), s.domain)
	msg := s.buildMessage(from, to, subject, body, htmlBody, inReplyTo, messageID, attachments)

	if s.Local != nil && s.Local.IsLocalDomain(to) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.Local.Deliver(ctx, from, []string{to}, []byte(msg)); err != nil {
			return "", err
		}
		return messageID, nil
	}
	if s.relayAddr == "" {
		return "", fmt.Errorf("mail relay yapilandirilmadi")
	}
	if err := s.deliver(from, []string{to}, []byte(msg)); err != nil {
		return "", err
	}
	return messageID, nil
}

func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func (s *Sender) buildMessage(from, to, subject, body, htmlBody, inReplyTo, messageID string, attachments []Attachment) string {
	return BuildRFC822(from, to, subject, body, htmlBody, inReplyTo, messageID, time.Now(), attachments)
}

// BuildRFC822, alanlardan gecerli bir RFC 5322 mesaji uretir (giden gonderim ve
// IMAP icin eski kayitlarin yeniden olusturulmasi ortak kullanir).
func BuildRFC822(from, to, subject, body, htmlBody, inReplyTo, messageID string, date time.Time, attachments []Attachment) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + date.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n")
	if strings.TrimSpace(inReplyTo) != "" {
		b.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
		b.WriteString("References: " + inReplyTo + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	hasHTML := strings.TrimSpace(htmlBody) != ""
	hasAtt := len(attachments) > 0

	// buildBody, govde blogunu (yalniz text; veya text+html multipart/alternative)
	// olusturur ve (Content-Type basligi, ham govde) doner.
	buildBody := func() (string, string) {
		if !hasHTML {
			return "text/plain; charset=\"utf-8\"", normalizeCRLF(body)
		}
		var mp strings.Builder
		w := multipart.NewWriter(&mp)
		th := textproto.MIMEHeader{}
		th.Set("Content-Type", "text/plain; charset=\"utf-8\"")
		th.Set("Content-Transfer-Encoding", "8bit")
		if pw, err := w.CreatePart(th); err == nil {
			_, _ = pw.Write([]byte(normalizeCRLF(body)))
		}
		hh := textproto.MIMEHeader{}
		hh.Set("Content-Type", "text/html; charset=\"utf-8\"")
		hh.Set("Content-Transfer-Encoding", "8bit")
		if pw, err := w.CreatePart(hh); err == nil {
			_, _ = pw.Write([]byte(normalizeCRLF(htmlBody)))
		}
		_ = w.Close()
		return "multipart/alternative; boundary=\"" + w.Boundary() + "\"", mp.String()
	}

	// Ek yok: govde blogu dogrudan mesaj govdesidir.
	if !hasAtt {
		ct, content := buildBody()
		b.WriteString("Content-Type: " + ct + "\r\n")
		if !hasHTML {
			b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
		}
		b.WriteString("\r\n")
		b.WriteString(content)
		return b.String()
	}

	// Ek var: multipart/mixed { govde blogu, ekler... }.
	var mp strings.Builder
	w := multipart.NewWriter(&mp)
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + w.Boundary() + "\"\r\n\r\n")

	// 1) Govde (yalniz text; veya nested multipart/alternative)
	bodyCT, bodyContent := buildBody()
	bodyHdr := textproto.MIMEHeader{}
	bodyHdr.Set("Content-Type", bodyCT)
	if !hasHTML {
		bodyHdr.Set("Content-Transfer-Encoding", "8bit")
	}
	if pw, err := w.CreatePart(bodyHdr); err == nil {
		_, _ = pw.Write([]byte(bodyContent))
	}

	// 2) Ekler (base64)
	for _, att := range attachments {
		// Content-Type kullanicidan gelir: ham yazilirsa baslik enjeksiyonu olur.
		// Gecerli bir medya tipine ayristirilip yeniden bicimlenir.
		ct := "application/octet-stream"
		if mt, params, perr := mime.ParseMediaType(att.ContentType); perr == nil && ValidHeaderValue(att.ContentType) {
			if f := mime.FormatMediaType(mt, params); f != "" {
				ct = f
			}
		}
		fn := strings.NewReplacer("\r", "", "\n", "", "\"", "'", "\\", "_").Replace(strings.TrimSpace(att.Filename))
		if fn == "" {
			fn = "dosya"
		}
		ah := textproto.MIMEHeader{}
		ah.Set("Content-Type", ct)
		ah.Set("Content-Transfer-Encoding", "base64")
		ah.Set("Content-Disposition", "attachment; filename=\""+mime.QEncoding.Encode("utf-8", fn)+"\"")
		if pw, err := w.CreatePart(ah); err == nil {
			enc := base64.StdEncoding.EncodeToString(att.Content)
			// 76 karakterlik satirlara bol (RFC 2045).
			for i := 0; i < len(enc); i += 76 {
				end := i + 76
				if end > len(enc) {
					end = len(enc)
				}
				_, _ = pw.Write([]byte(enc[i:end] + "\r\n"))
			}
		}
	}
	_ = w.Close()

	b.WriteString(mp.String())
	return b.String()
}

// deliver, relay'e baglanir, varsa STARTTLS yapar (self-signed'a izin verir) ve
// mesaji teslim eder. Auth kullanmaz.
func (s *Sender) deliver(from string, to []string, msg []byte) error {
	c, err := smtp.Dial(s.relayAddr)
	if err != nil {
		return fmt.Errorf("relay baglantisi kurulamadi: %w", err)
	}
	defer c.Close()

	helo := s.domain
	if helo == "" {
		helo = "localhost"
	}
	if err := c.Hello(helo); err != nil {
		return fmt.Errorf("HELO basarisiz: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		host, _, splitErr := net.SplitHostPort(s.relayAddr)
		if splitErr != nil {
			host = s.relayAddr
		}
		// Yerel Postfix self-signed sertifika kullanabilir.
		if err := c.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: true}); err != nil {
			return fmt.Errorf("STARTTLS basarisiz: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM reddedildi: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO reddedildi (%s): %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA acilamadi: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("govde yazilamadi: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA kapatilamadi: %w", err)
	}
	return c.Quit()
}

// SendRaw, hazir bir RFC 5322 mesajini (mail istemcisinden gelen) verilen
// zarf alicilarina relay uzerinden teslim eder. Baslik/zarf dogrulamasi
// cagiran tarafin sorumlulugundadir.
func (s *Sender) SendRaw(from string, to []string, raw []byte) error {
	if s.relayAddr == "" {
		return fmt.Errorf("mail relay yapilandirilmadi")
	}
	if len(to) == 0 {
		return fmt.Errorf("alici yok")
	}
	return s.deliver(from, to, raw)
}

// Domain, gondericinin mail domainini (Message-ID uretimi icin) doner.
func (s *Sender) Domain() string { return s.domain }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
