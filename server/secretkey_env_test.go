package main

import (
	"encoding/base64"
	"testing"
)

// Y8: dolu ama gecersiz anahtar baslangici durdurur; URL-safe/dolgusuz anahtar standart
// base64'e cevrilip ortama yazilir (routesnap/nodeauth yalniz standardi cozer).
func TestNormalizeSecretKeyEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	set := func(k, v string) error { env[k] = v; return nil }

	if err := normalizeSecretKeyEnv(get, set); err != nil || env["ZORVEN_SECRET_KEY"] != "" {
		t.Fatalf("bos anahtar dokunulmadan gecmeli: %v", err)
	}
	env["ZORVEN_SECRET_KEY"] = "abc"
	if err := normalizeSecretKeyEnv(get, set); err == nil {
		t.Fatal("gecersiz anahtar hata vermeli")
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(255 - i)
	}
	env["ZORVEN_SECRET_KEY"] = base64.RawURLEncoding.EncodeToString(key)
	if err := normalizeSecretKeyEnv(get, set); err != nil {
		t.Fatalf("URL-safe anahtar kabul edilmeli: %v", err)
	}
	if env["ZORVEN_SECRET_KEY"] != base64.StdEncoding.EncodeToString(key) {
		t.Fatalf("standart kodlamaya cevrilmedi: %q", env["ZORVEN_SECRET_KEY"])
	}
	if captureCodecFromEnv(get) == nil {
		t.Fatal("yakalama kodlayicisi normallesmis anahtarla kurulmali")
	}
}
