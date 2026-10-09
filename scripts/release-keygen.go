//go:build ignore

// release-keygen: istemci guncelleme imzasi icin ed25519 anahtar cifti uretir.
//
//	go run scripts/release-keygen.go <ozel-anahtar-yolu>
//
// Ozel anahtar verilen yola 0600 ile yazilir (var olan dosyanin uzerine YAZMAZ);
// acik anahtar base64 olarak stdout'a basilir ve shared/updatesig/keys.go
// icindeki PublicKeys listesine eklenir. Ozel anahtari repoya/sunucuya KOYMAYIN.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "kullanim: go run scripts/release-keygen.go <ozel-anahtar-yolu>")
		os.Exit(2)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anahtar uretilemedi:", err)
		os.Exit(1)
	}
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ozel anahtar dosyasi olusturulamadi (var olan uzerine yazilmaz):", err)
		os.Exit(1)
	}
	// Dosya icerigi: 64 baytlik ed25519 ozel anahtarin base64'u.
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n"); err != nil {
		f.Close()
		fmt.Fprintln(os.Stderr, "yazma hatasi:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "yazma hatasi:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Ozel anahtar yazildi: %s (YEDEKLEYIN, kimseyle paylasmayin)\n", os.Args[1])
	fmt.Fprintln(os.Stderr, "Acik anahtar (shared/updatesig/keys.go PublicKeys listesine ekleyin):")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
}
