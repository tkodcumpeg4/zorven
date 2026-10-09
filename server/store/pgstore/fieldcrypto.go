package pgstore

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// F-17: Secret Vault disindaki sirlar (ornegin oturum imza anahtari) icin metin alan sifreleme yardimcisi. Vault ile ayni AES-256-GCM
// anahtarini (ZORVEN_SECRET_KEY) kullanir. Sifreli deger "enc:v1:" + base64(nonce||ct)
// bicimindedir; onek yoksa deger eski duz metindir ve oldugu gibi okunur. ZORVEN_SECRET_KEY
// yoksa yazma duz metin kalir (uyari logu, tek sefer).

const encFieldPrefix = "enc:v1:"

var warnNoKeyOnce sync.Once

// IsSealed, degerin sifreli bicimde olup olmadigini soyler.
func IsSealed(v string) bool { return strings.HasPrefix(v, encFieldPrefix) }

// SealString, duz metni sifreler. Bos metin, zaten sifreli deger veya anahtarin
// bulunmamasi durumunda girdi aynen doner.
func SealString(plain string) string {
	if plain == "" || IsSealed(plain) {
		return plain
	}
	enc, _, err := encryptSecret([]byte(plain))
	if err != nil {
		if errors.Is(err, store.ErrSecretKeyMissing) {
			warnNoKeyOnce.Do(func() {
				slog.Warn("ZORVEN_SECRET_KEY tanimli degil: oturum sirlari duz metin saklaniyor")
			})
		} else {
			slog.Error("alan sifrelenemedi, duz metin saklaniyor", "err", err)
		}
		return plain
	}
	return encFieldPrefix + base64.StdEncoding.EncodeToString(enc)
}

// OpenString, SealString ciktisini cozer. Onek yoksa (eski duz metin) girdi aynen doner.
// Sifreli deger cozulemezse (anahtar yok/yanlis) hata doner.
func OpenString(v string) (string, error) {
	if !IsSealed(v) {
		return v, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, encFieldPrefix))
	if err != nil {
		return "", err
	}
	plain, err := decryptSecret(raw, currentSecretKeyVersion)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// openStringLenient, cozulemeyen sifreli degeri bos doner (sifreli metin asla
// kullaniciya/giden isteklere sizmaz) ve loglar.
func openStringLenient(v, what string) string {
	out, err := OpenString(v)
	if err != nil {
		slog.Error("sifreli alan cozulemedi (ZORVEN_SECRET_KEY eksik/yanlis?)", "alan", what, "err", err)
		return ""
	}
	return out
}
