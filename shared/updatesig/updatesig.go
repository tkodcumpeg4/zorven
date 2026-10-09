// Package updatesig, istemci (CLI + masaustu) otomatik guncellemesinin imza
// dogrulamasini ve surum karsilastirmasini icerir. Manifest'in BAYT-BAYT
// icerigi, ayri bir ".sig" dosyasindaki base64 ed25519 imzasiyla dogrulanir.
package updatesig

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoKeys, gomulu acik anahtar listesi bos oldugunda doner.
var ErrNoKeys = errors.New("guncelleme imza anahtari yapilandirilmamis")

// Verify, manifest baytlarini sigB64 (base64 ed25519 imza, bosluk/satir sonu
// toleransli) ile keys listesindeki anahtarlarla dener. keys nil ise
// PublicKeys kullanilir. Hicbiri dogrulamazsa hata doner.
func Verify(manifest, sigB64 []byte, keys []string) error {
	if keys == nil {
		keys = PublicKeys
	}
	var pubs []ed25519.PublicKey
	for _, k := range keys {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		pubs = append(pubs, ed25519.PublicKey(raw))
	}
	if len(pubs) == 0 {
		return ErrNoKeys
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("imza dosyasi gecersiz")
	}
	for _, p := range pubs {
		if ed25519.Verify(p, manifest, sig) {
			return nil
		}
	}
	return errors.New("imza dogrulanamadi")
}

// Newer, a > b ise true ("v" oneki ve -/+ eki yok sayilir; sayisal olmayan
// parca 0). Esit surum yeni sayilmaz: downgrade ve ayni surum reddedilir.
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// Describe, hata mesajlarini kullaniciya gosterilebilir kilar.
func Describe(err error) string {
	if errors.Is(err, ErrNoKeys) {
		return ErrNoKeys.Error()
	}
	return fmt.Sprintf("guncelleme imzasi dogrulanamadi (%v)", err)
}
