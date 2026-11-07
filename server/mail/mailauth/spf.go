package mailauth

import (
	"context"
	"strings"

	"blitiri.com.ar/go/spf"
)

// checkSPF, RFC 7208'e gore SPF denetimi yapar. Denetlenen kimlik MAIL FROM
// alanidir; zarf gondericisi bos (bounce) ya da alan adsiz ise HELO adi
// denetlenir (RFC 7208 madde 2.4). Algoritma blitiri.com.ar/go/spf kutuphanesinindir
// (tum mekanizmalar, makrolar, 10 sorgu ve 2 bos sorgu siniri); biz yalnizca
// kimlik secimi, genel olmayan istemci atlamasi ve sonuc eslemesini yapariz.
func (v *Verifier) checkSPF(ctx context.Context, res Resolver, in Input) SPFResult {
	r := SPFResult{}
	if in.ClientIP != nil {
		r.ClientIP = in.ClientIP.String()
	}

	helo := cleanDomain(in.HELO)
	from := strings.TrimSpace(in.MailFrom)

	var sender string
	switch {
	case from != "" && strings.Contains(from, "@"):
		local, dom, ok := splitAddr(from)
		if !ok {
			r.Identity = "mailfrom"
			r.Result, r.Reason = None, "invalid sender domain"
			return r
		}
		r.Identity, r.Domain, r.Sender = "mailfrom", dom, safeMailFrom(from)
		// Alan adi normallestirilmis (kucuk harf, noktasiz) haliyle verilir;
		// makro acilimlari (%{d}, %{s}) ayni degeri gorur.
		sender = local + "@" + dom
	default:
		// Bos gonderici ya da alan adsiz gonderici: HELO kimligi, yerel kisim
		// "postmaster" (RFC 7208 madde 2.4).
		r.Identity, r.Domain, r.Sender = "helo", helo, helo
		sender = "postmaster@" + helo
	}
	if r.Domain == "" {
		r.Result, r.Reason = None, "no usable domain"
		return r
	}
	if !isPublicIP(in.ClientIP) {
		r.Result, r.Skipped, r.Reason = None, true, "skipped: non-public client address"
		return r
	}

	got, _ := spf.CheckHostWithSender(in.ClientIP, helo, sender,
		spf.WithContext(ctx), spf.WithResolver(res))
	r.Result = string(got)
	return r
}
