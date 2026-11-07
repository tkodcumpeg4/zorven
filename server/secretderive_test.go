package main

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestDeriveKeyAnnounceVektoru: duyuru abonelik anahtari Node tarafinda da (web/server/lib/
// unsub-token.ts announceKey: HKDF-SHA256, tuz yok, bilgi "zorven-announce-unsub-v1")
// turetilir; deneme teklifi e-postasi jetonu web servisinde uretilir. Cikti sabit kalmali.
func TestDeriveKeyAnnounceVektoru(t *testing.T) {
	got := hex.EncodeToString(deriveKey([]byte("zorven-test-master-secret-0123456789"), keyPurposeAnnounce))
	const want = "9ecb9492c6225e73cb7f9900636c84acef04a36b469910417d5c6bf5e2869910"
	if got != want {
		t.Fatalf("anahtar turetme vektoru sapti:\n got %s\nwant %s", got, want)
	}
	if keyPurposeAnnounce != "zorven-announce-unsub-v1" {
		t.Fatal("amac etiketi degisti: web/server/lib/unsub-token.ts ANNOUNCE_KEY_PURPOSE ile birlikte guncelleyin")
	}
}

// TestDeriveKeyAccountVektoru: hesap silme ic ucu anahtari Node tarafinda da (web/server/lib/
// account-internal.ts accountKey: HKDF-SHA256, tuz yok, bilgi "zorven-account-internal-v1")
// turetilir. Cikti sabit kalmali; account-internal.test.ts ayni vektoru dogrular.
func TestDeriveKeyAccountVektoru(t *testing.T) {
	got := hex.EncodeToString(deriveKey([]byte("zorven-test-master-secret-0123456789"), keyPurposeAccount))
	const want = "fb8ccf24670e3f11f573f12307f799fc3d4ddffb95509cfc5a66b8c6d79b6891"
	if got != want {
		t.Fatalf("anahtar turetme vektoru sapti:\n got %s\nwant %s", got, want)
	}
	if keyPurposeAccount != "zorven-account-internal-v1" {
		t.Fatal("amac etiketi degisti: web/server/lib/account-internal.ts ACCOUNT_KEY_PURPOSE ile birlikte guncelleyin")
	}
}

func TestDeriveKey(t *testing.T) {
	m := []byte("ana-secret")
	a := deriveKey(m, keyPurposeVisitor)
	if len(a) != 32 || !bytes.Equal(a, deriveKey(m, keyPurposeVisitor)) {
		t.Fatal("turetme deterministik ve 32 bayt olmali")
	}
	if bytes.Equal(a, deriveKey(m, keyPurposeBasicAuth)) {
		t.Fatal("farkli amac farkli anahtar vermeli")
	}
	if bytes.Equal(a, m) || bytes.Equal(a, deriveKey([]byte("baska"), keyPurposeVisitor)) {
		t.Fatal("anahtar ana secret'a bagli ama ona esit olmamali")
	}
}

func TestResolveVisitorSecret(t *testing.T) {
	if s, w, err := resolveVisitorSecret("x", "zorven.app"); err != nil || w || s != "x" {
		t.Fatal("verilen secret aynen kullanilmali")
	}
	if _, _, err := resolveVisitorSecret("", "zorven.app"); err == nil {
		t.Fatal("uretimde secret yoksa hata beklenir")
	}
	for _, pd := range []string{"", "localhost", "dev.localhost", "x.test"} {
		s, w, err := resolveVisitorSecret("", pd)
		if err != nil || !w || s == "" {
			t.Fatalf("gelistirmede (%q) uyari ile varsayilan beklenir", pd)
		}
	}
}
