package captcha

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// MaxDomains, bir sitenin alan adi listesi ust siniri.
const MaxDomains = 50

var domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// NormalizeDomain, kullanici girdisini ("https://Ornek.com:8443/yol", "*.ornek.com")
// kucuk harfli, portsuz, semasiz alan adina cevirir. Gecersizse hata doner.
func NormalizeDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(in))
	if d == "" {
		return "", errors.New("bos alan adi")
	}
	if strings.Contains(d, "://") {
		u, err := url.Parse(d)
		if err != nil || u.Hostname() == "" {
			return "", errors.New("gecersiz alan adi: " + in)
		}
		d = u.Hostname()
	} else if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if h, _, err := net.SplitHostPort(d); err == nil {
		d = h
	}
	d = strings.TrimSuffix(strings.Trim(d, "[]"), ".")
	if len(d) > 253 || !domainRe.MatchString(d) {
		// IP adresleri (yerel gelistirme) kabul edilir.
		if ip := net.ParseIP(d); ip != nil {
			return ip.String(), nil
		}
		return "", errors.New("gecersiz alan adi: " + in)
	}
	return d, nil
}

// NormalizeDomains listeyi normallestirir, tekrarlari atar ve siniri uygular.
func NormalizeDomains(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		d, err := NormalizeDomain(raw)
		if err != nil {
			return nil, err
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) > MaxDomains {
		return nil, errors.New("en fazla 50 alan adi tanimlanabilir")
	}
	return out, nil
}

// MatchDomain: liste bossa her alan adi kabul edilir. "ornek.com" girdisi
// ornek.com'u VE alt alanlarini (a.ornek.com) kapsar; "*.ornek.com" yalniz alt
// alanlari (apex haric). host bossa (Origin/Referer yok) liste doluyken red.
func MatchDomain(domains []string, host string) bool {
	if len(domains) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, d := range domains {
		if strings.HasPrefix(d, "*.") {
			if strings.HasSuffix(host, d[1:]) && len(host) > len(d)-1 {
				return true
			}
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// OriginHost, Origin (yoksa Referer) basliklarindan portsuz kucuk harfli host
// cikarir. "null" ya da cozulemeyen deger icin bos doner.
func OriginHost(origin, referer string) string {
	for _, v := range []string{origin, referer} {
		v = strings.TrimSpace(v)
		if v == "" || v == "null" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			continue
		}
		return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	}
	return ""
}
