// Package tlscert, lokal gelistirme icin self-signed TLS sertifikasi uretir.
//
// openssl'e bagimlilik YOK: Go'nun crypto/x509 paketi yeterli, boylece
// "once openssl kur" adimi olmadan her platformda calisir.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Validity, uretilen sertifikanin gecerlilik suresi.
// Lokal gelistirme icin 1 yil fazlasiyla yeterli; uretimde ACME kullanilacak (R2).
const Validity = 365 * 24 * time.Hour

// Generate, verilen hostname'ler icin self-signed sertifika ve anahtar uretir.
// certPath ve keyPath'e PEM olarak yazar.
//
// Uretilen sertifika kendi kendini imzalar (leaf = CA). Istemci bunu
// --ca-cert ile guven havuzuna ekleyerek --insecure kullanmadan baglanabilir.
func Generate(hosts []string, certPath, keyPath string) error {
	if len(hosts) == 0 {
		return fmt.Errorf("en az bir hostname gerekli")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("anahtar uretilemedi: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("seri numarasi uretilemedi: %w", err)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Reverse Proxy Shell (lokal gelistirme)"},
			CommonName:   hosts[0],
		},
		NotBefore: now.Add(-1 * time.Hour), // saat kaymasina tolerans
		NotAfter:  now.Add(Validity),

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// Kendi kendini imzaladigi icin CA olarak isaretleniyor; istemci
		// bunu guven havuzuna ekleyebilsin.
		IsCA: true,
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("sertifika olusturulamadi: %w", err)
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("anahtar serilestirilemedi: %w", err)
	}
	// Ozel anahtar yalnizca sahibi tarafindan okunabilir olmali.
	//
	// UYARI: Bu izin yalnizca Unix'te gecerlidir. Windows'ta Go'nun os.Chmod
	// cagrisi POSIX izinlerini uygulamaz (yalnizca salt-okunur bitini degistirir),
	// dolayisiyla dosya orada 0644 gorunur. Windows'ta anahtari korumak icin
	// NTFS ACL gerekir; bu, lokal gelistirme sertifikasi icin kapsam disi.
	return writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600)
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("%s dizini olusturulamadi: %w", dir, err)
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("%s yazilamadi: %w", path, err)
	}
	defer f.Close()

	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return fmt.Errorf("%s PEM kodlanamadi: %w", path, err)
	}
	// OpenFile bazi platformlarda umask nedeniyle perm'i tam uygulamaz.
	// (Windows'ta bu cagri POSIX bitlerini degil salt-okunur bayragini etkiler.)
	return os.Chmod(path, perm)
}
