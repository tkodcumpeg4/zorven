package mailauth

import (
	"net"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// hostRe, alan adi etiketlerini dogrular (kucuk harf, rakam, tire, alt cizgi;
// etiket en cok 63 karakter). DKIM secicilerinde alt cizgi gectigi icin izinlidir.
var hostRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)*$`)

// cleanDomain, bir alan adini kucuk harfe cevirir, sondaki noktayi atar ve
// gecerli bir ASCII (A-label) ana makine adi degilse "" doner. ASCII disi adlar
// IDNA ile cevrilir.
func cleanDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			a, err := idna.Lookup.ToASCII(s)
			if err != nil {
				return ""
			}
			s = a
			break
		}
	}
	if len(s) > 253 || !hostRe.MatchString(s) {
		return ""
	}
	return s
}

// orgDomain, alan adinin organizasyon alan adini (genel sonek listesine gore
// eTLD+1) doner; turetilemezse alan adinin kendisi.
func orgDomain(d string) string {
	if o, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil && o != "" {
		return o
	}
	return d
}

// aligned, iki alan adinin DMARC hizalamasini denetler: strict ise tam esitlik,
// relaxed ise ayni organizasyon alan adi.
func aligned(a, b string, strict bool) bool {
	if a == "" || b == "" {
		return false
	}
	if strict {
		return a == b
	}
	return orgDomain(a) == orgDomain(b)
}

// localPartRe, tirnaksiz (dot-atom) bir e-posta yerel kismini dogrular (RFC 5322).
var localPartRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$")

// splitAddr, "yerel@alan" adresini ayirir. Alan adi gecerli degilse ok=false.
func splitAddr(addr string) (local, domain string, ok bool) {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return "", "", false
	}
	domain = cleanDomain(addr[at+1:])
	if domain == "" {
		return "", "", false
	}
	return addr[:at], domain, true
}

// safeMailFrom, Authentication-Results basligina yazilacak smtp.mailfrom
// degerini uretir. Deger gonderici denetimindedir (SMTP istemcisi yazar); bu
// yuzden yalnizca guvenli bir dot-atom yerel kismi korunur, digerlerinde
// "@alan" yazilir. Boylece ";", bosluk, parantez, tirnak ya da satir sonu ile
// sahte "dkim=pass" gibi sonuc enjekte edilemez.
func safeMailFrom(addr string) string {
	local, domain, ok := splitAddr(addr)
	if !ok {
		return ""
	}
	if len(local) > 0 && len(local) <= 64 && localPartRe.MatchString(local) {
		return local + "@" + domain
	}
	return "@" + domain
}

// cleanComment, bir Authentication-Results yorumu icin yalniz guvenli
// karakterleri birakir (digerleri "?" olur) ve uzunlugu sinirlar.
func cleanComment(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= max {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == ' ', r == '.', r == '-', r == '_', r == ':', r == ',', r == '/', r == '=', r == '+':
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return strings.TrimSpace(b.String())
}

// cgnat, 100.64.0.0/10 (tasiyici NAT / Tailscale) araligidir.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// isPublicIP, adresin internetten gelen gercek bir istemciye ait olup olmadigini
// soyler. Loopback, ozel (RFC 1918 / ULA), link-local, CGNAT, belirsiz ve
// multicast adresler icin SPF anlamsizdir (ornegin Docker proxy'sinin gorunen
// adresi); bu durumda kontrol atlanir ve enforce edilmez.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.To16() == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && cgnat.Contains(v4) {
		return false
	}
	return true
}
