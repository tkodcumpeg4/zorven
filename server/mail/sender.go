package mail

import (
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
func (s *Sender) Send(from, to, subject, body, inReplyTo string, attachments []Attachment) (string, error) {
	if s.relayAddr == "" {
		return "", fmt.Errorf("mail relay yapilandirilmadi")
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return "", fmt.Errorf("gonderen ve alici zorunlu")
	}

	messageID := fmt.Sprintf("<%s@%s>", randHex(16), s.domain)
	msg := s.buildMessage(from, to, subject, body, inReplyTo, messageID, attachments)

	if err := s.deliver(from, to, []byte(msg)); err != nil {
		return "", err
	}
	return messageID, nil
}

func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func (s *Sender) buildMessage(from, to, subject, body, inReplyTo, messageID string, attachments []Attachment) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n")
	if strings.TrimSpace(inReplyTo) != "" {
		b.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
		b.WriteString("References: " + inReplyTo + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	// Ek yoksa duz text/plain; varsa multipart/mixed (metin + ekler).
	if len(attachments) == 0 {
		b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(normalizeCRLF(body))
		return b.String()
	}

	var mp strings.Builder
	w := multipart.NewWriter(&mp)
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + w.Boundary() + "\"\r\n\r\n")

	// 1) Metin govdesi
	textHdr := textproto.MIMEHeader{}
	textHdr.Set("Content-Type", "text/plain; charset=\"utf-8\"")
	textHdr.Set("Content-Transfer-Encoding", "8bit")
	if pw, err := w.CreatePart(textHdr); err == nil {
		_, _ = pw.Write([]byte(normalizeCRLF(body)))
	}

	// 2) Ekler (base64)
	for _, att := range attachments {
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		fn := att.Filename
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
func (s *Sender) deliver(from, to string, msg []byte) error {
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

	if err := c.Mail(addrOnly(from)); err != nil {
		return fmt.Errorf("MAIL FROM reddedildi: %w", err)
	}
	if err := c.Rcpt(addrOnly(to)); err != nil {
		return fmt.Errorf("RCPT TO reddedildi: %w", err)
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

// addrOnly, "Ad <a@b.com>" -> "a@b.com". Zarf (envelope) icin saf adres gerekir.
func addrOnly(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j >= 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
