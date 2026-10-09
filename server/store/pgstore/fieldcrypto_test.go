package pgstore

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func setTestKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	os.Setenv("ZORVEN_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	t.Cleanup(func() { os.Unsetenv("ZORVEN_SECRET_KEY"); resetSecretKey() })
	resetSecretKey()
}

func TestSealOpenStringRoundTrip(t *testing.T) {
	setTestKey(t)
	sealed := SealString("sso-client-secret")
	if !IsSealed(sealed) || strings.Contains(sealed, "sso-client-secret") {
		t.Fatalf("sifreli bicim bekleniyordu: %q", sealed)
	}
	if again := SealString(sealed); again != sealed {
		t.Fatal("zaten sifreli deger tekrar sifrelenmemeli")
	}
	got, err := OpenString(sealed)
	if err != nil || got != "sso-client-secret" {
		t.Fatalf("gidis-donus basarisiz: %q %v", got, err)
	}
}

func TestOpenStringLegacyPlain(t *testing.T) {
	setTestKey(t)
	got, err := OpenString("eski-duz-metin")
	if err != nil || got != "eski-duz-metin" {
		t.Fatalf("eski duz metin okunabilmeli: %q %v", got, err)
	}
	if SealString("") != "" {
		t.Fatal("bos deger bos kalmali")
	}
}

func TestSealStringNoKeyKeepsPlain(t *testing.T) {
	os.Unsetenv("ZORVEN_SECRET_KEY")
	resetSecretKey()
	if got := SealString("x"); got != "x" {
		t.Fatalf("anahtar yokken duz metin beklenir: %q", got)
	}
	// Anahtar yokken sifreli deger cozulemez ama sizmaz.
	if _, err := OpenString(encFieldPrefix + "AAAA"); err == nil {
		t.Fatal("anahtarsiz sifreli deger hata vermeli")
	}
	if openStringLenient(encFieldPrefix+"AAAA", "t") != "" {
		t.Fatal("cozulemeyen deger bos donmeli")
	}
}
