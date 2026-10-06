package pgstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F06): Secret Vault sifreleme. Sirlar uygulama seviyesinde AES-256-GCM ile
// sifrelenir; anahtar env'den (ZORVEN_SECRET_KEY, base64 kodlu 32 bayt) BIR KEZ okunur.
// value_enc = nonce (12 bayt) || gcm.Seal(...). key_version ile ileride birden cok
// aktif anahtar (rotation) desteklenebilir; su an tek anahtar (surum 1) vardir.

const currentSecretKeyVersion = 1

var (
	secretKeyOnce sync.Once
	secretKey     []byte // 32 bayt; bos ise anahtar yapilandirilmamis
)

// loadSecretKey, ZORVEN_SECRET_KEY'i BIR KEZ okur. Gecersiz/eksikse secretKey bos kalir.
func loadSecretKey() {
	secretKeyOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("ZORVEN_SECRET_KEY"))
		if raw == "" {
			return
		}
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return
		}
		secretKey = key
	})
}

// keyForVersion, verilen surum icin sifreleme anahtarini doner. Bilinmeyen surum
// veya anahtar yoksa hata.
func keyForVersion(version int) ([]byte, error) {
	loadSecretKey()
	if len(secretKey) != 32 {
		return nil, store.ErrSecretKeyMissing
	}
	switch version {
	case currentSecretKeyVersion:
		return secretKey, nil
	default:
		return nil, store.ErrUnknownKeyVersion
	}
}

// encryptSecret, plaintext'i AES-256-GCM ile sifreler. nonce||ciphertext ve kullanilan
// anahtar surumunu doner.
func encryptSecret(plain []byte) ([]byte, int, error) {
	key, err := keyForVersion(currentSecretKeyVersion)
	if err != nil {
		return nil, 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, 0, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, 0, err
	}
	enc := gcm.Seal(nonce, nonce, plain, nil)
	return enc, currentSecretKeyVersion, nil
}

// decryptSecret, encryptSecret ciktisini (nonce||ciphertext) verilen anahtar surumuyle cozer.
func decryptSecret(enc []byte, version int) ([]byte, error) {
	key, err := keyForVersion(version)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(enc) < ns {
		return nil, store.ErrNotFound
	}
	nonce, ciphertext := enc[:ns], enc[ns:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
