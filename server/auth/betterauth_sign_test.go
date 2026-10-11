package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"
)

func signBA(secret, val string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(val))
	return val + "." + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestBetterAuthSignedCookieToken(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	v := &BetterAuthVerifier{}
	v.SetSecret(secret)
	signed := signBA(secret, "tok_ABC123")

	// Cerez degeri tarayicidan URL kodlu gelir (+ / = karakterleri).
	for _, in := range []string{signed, url.QueryEscape(signed)} {
		if got := v.CookieToken(in); got != "tok_ABC123" {
			t.Fatalf("gecerli imza: %q (girdi %q)", got, in)
		}
		if got := v.BearerToken(in); got != "tok_ABC123" {
			t.Fatalf("gecerli imzali bearer: %q", got)
		}
	}
	bad := map[string]string{
		"ham token":          "tok_ABC123",
		"yanlis imza":        "tok_ABC123." + base64.StdEncoding.EncodeToString([]byte("x")),
		"baska anahtar":      signBA("other-secret-other-secret-other-secret", "tok_ABC123"),
		"degistirilmis":      signBA(secret, "tok_ABC123") + "A",
		"imza kismi bos":     "tok_ABC123.",
		"yalniz nokta":       ".",
		"baska deger imzasi": "tok_EVIL." + signed[len("tok_ABC123."):],
	}
	for name, in := range bad {
		if got := v.CookieToken(in); got != "" {
			t.Errorf("%s: cerez token'i kabul edildi: %q", name, got)
		}
		if got := v.BearerToken(in); got != "" {
			t.Errorf("%s: bearer token'i kabul edildi: %q", name, got)
		}
	}
}

// Anahtar yokken: cerez eski davranisla (gelistirme), Bearer asla.
func TestBetterAuthNoSecretFallback(t *testing.T) {
	v := &BetterAuthVerifier{}
	if v.HasSecret() {
		t.Fatal("anahtar yok")
	}
	if got := v.CookieToken("tok.sig"); got != "tok" {
		t.Fatalf("cerez geri uyumu: %q", got)
	}
	if got := v.BearerToken("tok.sig"); got != "" {
		t.Fatalf("anahtarsiz bearer kabul edilmemeli: %q", got)
	}
	if got := v.BearerToken("tok"); got != "" {
		t.Fatalf("ham bearer kabul edilmemeli: %q", got)
	}
}
