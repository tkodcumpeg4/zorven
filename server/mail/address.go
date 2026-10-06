package mail

import (
	"fmt"
	netmail "net/mail"
	"strings"
)

// ParseAddress, kullanicidan gelen TEK bir alici/gonderen adresini RFC 5322
// ayristiricisiyla (net/mail) cozer ve yalin "local@domain" doner (domain
// kucuk harfe indirilir). Plan kisiti (ic/harici alan adi) ve SMTP zarfi
// (RCPT TO) AYNI bu degerden turetilir; boylece kisit ile gercek alici
// ayrisamaz.
//
// Reddedilenler: bos girdi, CR/LF (baslik enjeksiyonu), birden fazla adres
// (liste / grup), tirnakli ya da '@' iceren yerel kisim, kaynak-rota gibi
// zarf ile basligin farkli yorumlanabilecegi her bicim.
func ParseAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("adres bos")
	}
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", fmt.Errorf("adres gecersiz karakter iceriyor")
	}
	a, err := netmail.ParseAddress(s)
	if err != nil {
		return "", fmt.Errorf("gecersiz adres: %w", err)
	}
	addr := a.Address
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 || strings.Count(addr, "@") != 1 {
		return "", fmt.Errorf("gecersiz adres")
	}
	local, domain := addr[:at], strings.ToLower(addr[at+1:])
	if !isSafeLocal(local) || !isSafeDomain(domain) {
		return "", fmt.Errorf("gecersiz adres")
	}
	return local + "@" + domain, nil
}

// AddressDomain, ParseAddress ciktisinin alan adini doner.
func AddressDomain(addr string) string {
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		return strings.ToLower(addr[at+1:])
	}
	return ""
}

// isSafeLocal, yerel kismi dot-atom karakterleriyle sinirlar (tirnak, bosluk,
// '<', '>', ',' vb. YOK) — SMTP zarfina oldugu gibi yazilabilir.
func isSafeLocal(s string) bool {
	if s == "" || len(s) > 64 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune(".!#$%&'*+/=?^_`{|}~-", c):
		default:
			return false
		}
	}
	return true
}

// isSafeDomain, alan adini harf/rakam/'-'/'.' ile sinirlar (IP literali YOK).
func isSafeDomain(s string) bool {
	if s == "" || len(s) > 253 || !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// ValidHeaderValue, ham yazilacak baslik degerinde (In-Reply-To, References)
// CR/LF/NUL olmadigini ve makul uzunlukta oldugunu denetler.
func ValidHeaderValue(s string) bool {
	return len(s) <= 998 && !strings.ContainsAny(s, "\r\n\x00")
}
