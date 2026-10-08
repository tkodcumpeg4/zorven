package mail

import (
	"bufio"
	"bytes"
	"fmt"
	stdmail "net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message/textproto"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// cleanHeader, baslik degerinden satir sonlarini (enjeksiyon) temizler.
func cleanHeader(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(s))
}

// ReconstructRaw, saklanan alanlardan (ve eklerden) gecerli bir RFC 5322 mesaji
// uretir. Ham mesaji (raw) olmayan eski kayitlar icin IMAP'te kullanilir.
func ReconstructRaw(m store.MailMessage, atts []Attachment) string {
	from := cleanHeader(m.From)
	if from == "" {
		from = "unknown@localhost"
	}
	to := cleanHeader(m.To)
	if to == "" {
		to = "undisclosed-recipients:;"
	}
	mid := cleanHeader(m.MessageID)
	if mid == "" {
		domain := "localhost"
		if a, err := stdmail.ParseAddress(from); err == nil {
			if d := AddressDomain(a.Address); d != "" {
				domain = d
			}
		}
		mid = fmt.Sprintf("<%s@%s>", m.ID, domain)
	}
	date := m.ReceivedAt
	if date.IsZero() {
		date = time.Now()
	}
	body := m.TextBody
	if strings.TrimSpace(body) == "" && strings.TrimSpace(m.HTMLBody) == "" {
		body = "\r\n"
	}
	return BuildRFC822(from, to, m.Subject, body, m.HTMLBody, cleanHeader(m.InReplyTo), mid, date, atts)
}

// headerBlock, ham mesajin baslik bolumunu (bos satira kadar) ve govdenin
// baslangic ofsetini doner.
func headerBlock(raw []byte) (hdr []byte, bodyStart int) {
	if bytes.HasPrefix(raw, []byte("\r\n")) {
		return nil, 2
	}
	if bytes.HasPrefix(raw, []byte("\n")) {
		return nil, 1
	}
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i+2], i + 4
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i+1], i + 2
	}
	return raw, len(raw)
}

// StripHeader, ham mesajdan verilen basligi (katlanmis satirlar dahil) siler.
func StripHeader(raw []byte, name string) []byte {
	hdr, bodyStart := headerBlock(raw)
	if len(hdr) == 0 {
		return raw
	}
	prefix := strings.ToLower(name) + ":"
	var out bytes.Buffer
	skipping := false
	changed := false
	for _, line := range bytes.SplitAfter(hdr, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if skipping {
				continue
			}
		} else {
			skipping = strings.HasPrefix(strings.ToLower(string(line)), prefix)
			if skipping {
				changed = true
				continue
			}
		}
		out.Write(line)
	}
	if !changed {
		return raw
	}
	out.Write(raw[len(hdr):bodyStart])
	out.Write(raw[bodyStart:])
	return out.Bytes()
}

// HeaderValue, ham mesajdaki ilk basligin (cozulmemis) degerini doner.
func HeaderValue(raw []byte, name string) string {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h.Get(name))
}

// HeaderAddresses, Basligi adres listesi olarak cozer (From/To/Cc/Bcc).
func HeaderAddresses(raw []byte, name string) []string {
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil
	}
	v := h.Get(name)
	if strings.TrimSpace(v) == "" {
		return nil
	}
	addrs, err := stdmail.ParseAddressList(v)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

// EnsureEnvelopeHeaders, Message-ID ve Date eksikse ham mesajin basina ekler.
// Kullanilan (veya uretilen) Message-ID'yi doner.
func EnsureEnvelopeHeaders(raw []byte, domain string) ([]byte, string) {
	mid := HeaderValue(raw, "Message-Id")
	var prefix strings.Builder
	if mid == "" {
		mid = fmt.Sprintf("<%s@%s>", randHex(16), firstNonEmptyStr(domain, "localhost"))
		prefix.WriteString("Message-ID: " + mid + "\r\n")
	}
	if HeaderValue(raw, "Date") == "" {
		prefix.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	}
	if prefix.Len() == 0 {
		return raw, mid
	}
	return append([]byte(prefix.String()), raw...), mid
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
