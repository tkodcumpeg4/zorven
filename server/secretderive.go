package main

import (
	"crypto/sha256"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// Amac etiketleri: ayni ana secret'tan her kullanim icin AYRI anahtar turetilir;
// bir amac icin uretilen jeton baska amacta (ya da Better Auth oturumunda)
// dogrulanamaz. Etiketi degistirmek o amactaki tum cerezleri gecersiz kilar.
const (
	keyPurposeVisitor   = "zorven-visitor-v1"
	keyPurposeBasicAuth = "zorven-basic-auth-v1"
)

// devVisitorSecret yalniz yerel gelistirmede (platform domain yok/localhost) ve
// hicbir secret verilmediginde kullanilir.
const devVisitorSecret = "zorven-dev-visitor-secret-not-for-production"

// deriveKey, ana secret'tan amac etiketli 32 baytlik anahtar turetir (HKDF-SHA256).
func deriveKey(master []byte, purpose string) []byte {
	out := make([]byte, 32)
	r := hkdf.New(sha256.New, master, nil, []byte(purpose))
	if _, err := io.ReadFull(r, out); err != nil {
		panic("hkdf: " + err.Error()) // 32 bayt icin ulasilamaz
	}
	return out
}

// isProductionDomain: platform domain tanimli ve localhost/.localhost/.test degilse
// uretim sayilir.
func isProductionDomain(pd string) bool {
	pd = strings.ToLower(strings.TrimSpace(pd))
	if pd == "" || pd == "localhost" || strings.HasSuffix(pd, ".localhost") ||
		strings.HasSuffix(pd, ".test") || pd == "127.0.0.1" || pd == "::1" {
		return false
	}
	return true
}

// resolveVisitorSecret ziyaretci/Basic-auth anahtarlarinin ana secret'ini dogrular.
// Uretimde secret yoksa hata doner (sessiz zayif varsayilan yok); gelistirmede
// sabit gelistirme secret'ina dusup warn=true doner.
func resolveVisitorSecret(secret, platformDomain string) (resolved string, warn bool, err error) {
	if secret != "" {
		return secret, false, nil
	}
	if isProductionDomain(platformDomain) {
		return "", false, errors.New("BETTER_AUTH_SECRET (veya --cluster-secret / ZORVEN_CLUSTER_SECRET) tanimli degil: uretimde ziyaretci ve Basic-auth jeton anahtari icin gizli anahtar zorunlu; baslatma reddedildi")
	}
	return devVisitorSecret, true, nil
}
