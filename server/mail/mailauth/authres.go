package mailauth

import "strings"

// HeaderField, RFC 8601 Authentication-Results basligini uretir (sonunda CRLF ile;
// her yontem sonucu girintili ayri satirda). authservID, bu sunucunun posta ana
// makine adidir. Basliga yazilan tum degerler gonderici denetimindeki veriden
// turedigi icin dogrulanmis ya da sabit sozlukten secilmistir (bkz. cleanDomain,
// safeMailFrom, cleanComment): sonuc enjeksiyonu (ornegin "; dkim=pass") yapilamaz.
//
// Ornek:
//
//	Authentication-Results: mail.zorven.app;
//		spf=pass (client-ip=203.0.113.7) smtp.mailfrom=bounce@example.com;
//		dkim=pass header.d=example.com;
//		dmarc=pass (p=reject dis=none) header.from=example.com
func (r *Result) HeaderField(authservID string) string {
	id := cleanDomain(authservID)
	if id == "" {
		id = "localhost"
	}
	var b strings.Builder
	b.WriteString(arHeaderName + ": " + id)
	b.WriteString(";\r\n\t" + r.SPF.line())
	for _, d := range r.DKIM {
		b.WriteString(";\r\n\t" + d.line())
	}
	b.WriteString(";\r\n\t" + r.DMARC.line())
	b.WriteString("\r\n")
	return b.String()
}

func (s SPFResult) line() string {
	var b strings.Builder
	b.WriteString("spf=" + s.Result)
	var notes []string
	if s.ClientIP != "" {
		notes = append(notes, "client-ip="+s.ClientIP)
	}
	if s.Reason != "" {
		notes = append(notes, s.Reason)
	}
	if len(notes) > 0 {
		b.WriteString(" (" + cleanComment(strings.Join(notes, ", "), 120) + ")")
	}
	if s.Sender != "" {
		if s.Identity == "helo" {
			b.WriteString(" smtp.helo=" + s.Sender)
		} else {
			b.WriteString(" smtp.mailfrom=" + s.Sender)
		}
	}
	return b.String()
}

func (d DKIMResult) line() string {
	s := "dkim=" + d.Result
	if d.Reason != "" {
		s += " (" + cleanComment(d.Reason, 80) + ")"
	}
	if d.Domain != "" {
		s += " header.d=" + d.Domain
	}
	return s
}

func (d DMARCResult) line() string {
	s := "dmarc=" + d.Result
	var notes []string
	if d.Policy != "" {
		notes = append(notes, "p="+d.Policy)
		if d.Disposition != "" {
			notes = append(notes, "dis="+d.Disposition)
		}
	}
	if d.Note != "" {
		notes = append(notes, d.Note)
	}
	if len(notes) > 0 {
		s += " (" + cleanComment(strings.Join(notes, " "), 120) + ")"
	}
	if d.FromDomain != "" {
		s += " header.from=" + d.FromDomain
	}
	return s
}
