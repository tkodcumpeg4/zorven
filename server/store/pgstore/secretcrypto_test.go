package pgstore

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// resetSecretKey, testler arasi paket-seviye anahtar durumunu sifirlar.
func resetSecretKey() {
	secretKeyOnce = sync.Once{}
	secretKey = nil
}

func TestSecretEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	os.Setenv("ZORVEN_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	defer os.Unsetenv("ZORVEN_SECRET_KEY")
	resetSecretKey()

	plain := []byte("github_pat_süpergizli_123")
	enc, ver, err := encryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt hata: %v", err)
	}
	if ver != currentSecretKeyVersion {
		t.Fatalf("beklenen surum %d, gelen %d", currentSecretKeyVersion, ver)
	}
	if bytes.Contains(enc, plain) {
		t.Fatal("sifreli metin duz metni icermemeli")
	}
	got, err := decryptSecret(enc, ver)
	if err != nil {
		t.Fatalf("decrypt hata: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip uyumsuz: %q != %q", got, plain)
	}
}

func TestSecretMissingKey(t *testing.T) {
	os.Unsetenv("ZORVEN_SECRET_KEY")
	resetSecretKey()
	if _, _, err := encryptSecret([]byte("x")); !errors.Is(err, store.ErrSecretKeyMissing) {
		t.Fatalf("ErrSecretKeyMissing bekleniyordu, gelen: %v", err)
	}
}

func TestSecretUnknownKeyVersion(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	os.Setenv("ZORVEN_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	defer os.Unsetenv("ZORVEN_SECRET_KEY")
	resetSecretKey()

	enc, _, err := encryptSecret([]byte("x"))
	if err != nil {
		t.Fatalf("encrypt hata: %v", err)
	}
	if _, err := decryptSecret(enc, 99); !errors.Is(err, store.ErrUnknownKeyVersion) {
		t.Fatalf("ErrUnknownKeyVersion bekleniyordu, gelen: %v", err)
	}
}

func TestSecretInvalidKeyRejected(t *testing.T) {
	os.Setenv("ZORVEN_SECRET_KEY", "kısa") // base64 degil / 32 bayt degil
	defer os.Unsetenv("ZORVEN_SECRET_KEY")
	resetSecretKey()
	if _, _, err := encryptSecret([]byte("x")); !errors.Is(err, store.ErrSecretKeyMissing) {
		t.Fatalf("gecersiz anahtar reddedilmeliydi: %v", err)
	}
}

func TestParseSecretKeyEncodings(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(250 - i*3) // +/ ile -_ kodlamalarinin ayristigi baytlar
	}
	for name, enc := range map[string]*base64.Encoding{
		"std": base64.StdEncoding, "url": base64.URLEncoding,
		"rawstd": base64.RawStdEncoding, "rawurl": base64.RawURLEncoding,
	} {
		got, err := ParseSecretKey(" " + enc.EncodeToString(key) + "\n")
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("%s: kabul edilmeliydi: err=%v", name, err)
		}
	}
	for _, bad := range []string{"abc", "kısa", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := ParseSecretKey(bad); err == nil {
			t.Fatalf("%q reddedilmeliydi", bad)
		}
	}
}

func TestCheckSecretKeyEnv(t *testing.T) {
	get := func(v string) func(string) string { return func(string) string { return v } }
	if err := CheckSecretKeyEnv(get("")); err != nil {
		t.Fatalf("bos deger mevcut davranis (uyari) olmali, hata degil: %v", err)
	}
	if err := CheckSecretKeyEnv(get("abc")); err == nil {
		t.Fatal("dolu ama gecersiz anahtar baslangicta reddedilmeli")
	}
	good := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if err := CheckSecretKeyEnv(get(good)); err != nil {
		t.Fatalf("URL-safe dolgusuz anahtar kabul edilmeli: %v", err)
	}
}
