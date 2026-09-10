package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateAndVerify(t *testing.T) {
	full, id, hash, err := GenerateClient()
	if err != nil {
		t.Fatalf("Generate hata verdi: %v", err)
	}
	if !strings.HasPrefix(full, PrefixClient) {
		t.Errorf("token %q onegiyle baslamali, alinan: %q", PrefixClient, full)
	}
	if len(id) != tokenIDBytes*2 {
		t.Errorf("id uzunlugu %d, beklenen %d", len(id), tokenIDBytes*2)
	}

	gotID, secret, err := Split(full)
	if err != nil {
		t.Fatalf("Split hata verdi: %v", err)
	}
	if gotID != id {
		t.Errorf("Split id = %q, beklenen %q", gotID, id)
	}

	ok, err := Verify(secret, hash)
	if err != nil {
		t.Fatalf("Verify hata verdi: %v", err)
	}
	if !ok {
		t.Error("dogru secret dogrulanmadi")
	}
}

func TestLegacyPrefixes(t *testing.T) {
	legacy := "rpsh_live_0123456789abcdef_someSecretPayload12345"
	id, secret, err := Split(legacy)
	if err != nil {
		t.Fatalf("Legacy token split edilemedi: %v", err)
	}
	if id != "0123456789abcdef" || secret != "someSecretPayload12345" {
		t.Errorf("Beklenmeyen legacy token ayrisimi: id=%s secret=%s", id, secret)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	_, _, hash, err := GenerateClient()
	if err != nil {
		t.Fatalf("Generate hata verdi: %v", err)
	}
	ok, err := Verify("yanlis-secret", hash)
	if err != nil {
		t.Fatalf("Verify hata verdi: %v", err)
	}
	if ok {
		t.Error("yanlis secret kabul edildi")
	}
}

// Iki token asla ayni olmamali; ayni secret bile farkli tuzla farkli hash uretmeli.
func TestGenerateIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		full, id, _, err := GenerateClient()
		if err != nil {
			t.Fatalf("Generate hata verdi: %v", err)
		}
		if seen[full] || seen[id] {
			t.Fatal("tekrar eden token veya id uretildi")
		}
		seen[full], seen[id] = true, true
	}
}

func TestSplitRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"yanlis_onek_abc_def",
		PrefixClient + "kisa_secret",       // id uzunlugu yanlis
		PrefixClient + "0011223344556677",  // ayrac ve secret yok
		PrefixClient + "0011223344556677_", // secret bos
	}
	for _, b := range bad {
		if _, _, err := Split(b); !errors.Is(err, ErrMalformedToken) {
			t.Errorf("Split(%q) ErrMalformedToken vermeliydi, alinan: %v", b, err)
		}
	}
}

func TestVerifyRejectsCorruptHash(t *testing.T) {
	for _, h := range []string{"", "duz-metin", "bcrypt$aaa$bbb", "argon2id$!!!$bbb"} {
		if _, err := Verify("x", h); !errors.Is(err, ErrBadHash) {
			t.Errorf("Verify(_, %q) ErrBadHash vermeliydi, alinan: %v", h, err)
		}
	}
}

func TestGenerateAPIKey(t *testing.T) {
	full, id, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey hata verdi: %v", err)
	}
	if !strings.HasPrefix(full, PrefixAPI) {
		t.Errorf("token %q onegiyle baslamali, alinan: %q", PrefixAPI, full)
	}
	if len(id) != tokenIDBytes*2 {
		t.Errorf("id uzunlugu %d, beklenen %d", len(id), tokenIDBytes*2)
	}

	gotID, secret, err := Split(full)
	if err != nil {
		t.Fatalf("Split hata verdi: %v", err)
	}
	if gotID != id {
		t.Errorf("Split id = %q, beklenen %q", gotID, id)
	}

	ok, err := Verify(secret, hash)
	if err != nil {
		t.Fatalf("Verify hata verdi: %v", err)
	}
	if !ok {
		t.Error("dogru secret dogrulanmadi")
	}
}
