//go:build ignore

// release-sign: manifest.json / desktop.json icin ".sig" dosyasi uretir
// (manifest'in bayt-bayt icerigi uzerinden base64 ed25519 imza).
//
//	ZORVEN_RELEASE_KEY_FILE=~/zorven-release.key go run scripts/release-sign.go dist/manifest.json [dist/desktop.json ...]
//	go run scripts/release-sign.go -key ~/zorven-release.key dist/manifest.json
//
// Cikti: <dosya>.sig. Anahtar dosyasi yoksa/okunamazsa hata ile durur.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "release-sign: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	keyPath := flag.String("key", os.Getenv("ZORVEN_RELEASE_KEY_FILE"), "ozel anahtar dosyasi (veya env ZORVEN_RELEASE_KEY_FILE)")
	flag.Parse()
	if *keyPath == "" {
		fail("ozel anahtar yolu verilmedi (-key veya ZORVEN_RELEASE_KEY_FILE)")
	}
	if flag.NArg() == 0 {
		fail("imzalanacak dosya verilmedi")
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		fail("ozel anahtar okunamadi: %v", err)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		fail("ozel anahtar gecersiz (64 baytlik ed25519 anahtarin base64'u bekleniyor)")
	}
	priv := ed25519.PrivateKey(b)
	pub := priv.Public().(ed25519.PublicKey)
	for _, path := range flag.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fail("%s okunamadi: %v", path, err)
		}
		sig := ed25519.Sign(priv, data)
		if !ed25519.Verify(pub, data, sig) {
			fail("%s: kendi kendine dogrulama basarisiz", path)
		}
		out := path + ".sig"
		if err := os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
			fail("%s yazilamadi: %v", out, err)
		}
		fmt.Printf("imzalandi: %s\n", out)
	}
	fmt.Printf("acik anahtar: %s\n", base64.StdEncoding.EncodeToString(pub))
}
