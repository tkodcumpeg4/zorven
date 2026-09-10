// Package auth, istemci token'larini uretir ve dogrular.
//
// Token bicimi:  rpsh_live_<tokenID>_<secret>
//
//	tokenID : 16 hex karakter (8 bayt rastgele) — PUBLIC, DB'de indeksli
//	secret  : 32 bayt rastgele, base64url (padding yok) — yalnizca argon2id hash'i saklanir
//
// Neden iki parca: argon2 tuzlu oldugu icin hash'e gore arama yapilamaz.
// Tek parca token olsaydi her baglantida tum istemciler gezilip argon2
// dogrulamasi yapilirdi — argon2 kasitli olarak yavas oldugundan bu O(n) yavas
// islem demekti. tokenID ile kayit O(1) bulunur, argon2 baglanti basina bir kez calisir.
// Ayni desen GitHub PAT ve Stripe anahtarlarinda kullanilir.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Token onekleri. Ayri onekler, sizan bir anahtarin turunun bakisla
// anlasilmasini saglar (GitHub'in ghp_/ghs_ ayrimi gibi) ve yanlis yerde
// kullanilan bir anahtarin erken yakalanmasina yardim eder.
const (
	PrefixClient       = "zrv_live_"
	PrefixAdmin        = "zrv_admin_"
	PrefixAPI          = "zrv_api_"
	PrefixLegacyClient = "rpsh_live_"
	PrefixLegacyAdmin  = "rpsh_admin_"
)

const (
	tokenIDBytes = 8  // -> 16 hex karakter
	secretBytes  = 32 // -> 43 base64url karakter

	// argon2id parametreleri. Token yuksek entropili (256 bit) rastgele bir deger
	// oldugu icin parola kadar agir maliyet gerekmez; kaba kuvvet zaten olanaksiz.
	// Amac, DB sizarsa hash'in dogrudan kullanilamamasi.
	argonTime    = 1
	argonMemory  = 32 * 1024 // 32 MB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	ErrMalformedToken = errors.New("token bicimi gecersiz")
	ErrBadHash        = errors.New("saklanan hash cozulemedi")
)

// Generate, yeni bir token uretir.
// full  : kullaniciya BIR KEZ gosterilen tam token
// id    : DB'de indekslenecek public arama anahtari
// hash  : DB'de saklanacak argon2id hash'i
// GenerateClient, istemci (tunel ajani) token'i uretir.
func GenerateClient() (full, id, hash string, err error) {
	return generate(PrefixClient)
}

// GenerateAdmin, yonetim API'si icin admin anahtari uretir.
func GenerateAdmin() (full, id, hash string, err error) {
	return generate(PrefixAdmin)
}

// GenerateAPIKey, programatik REST API erisimi icin token uretir.
func GenerateAPIKey() (full, id, hash string, err error) {
	return generate(PrefixAPI)
}

func generate(prefix string) (full, id, hash string, err error) {
	idRaw := make([]byte, tokenIDBytes)
	if _, err = rand.Read(idRaw); err != nil {
		return "", "", "", fmt.Errorf("token id uretilemedi: %w", err)
	}
	secretRaw := make([]byte, secretBytes)
	if _, err = rand.Read(secretRaw); err != nil {
		return "", "", "", fmt.Errorf("token secret uretilemedi: %w", err)
	}

	id = hex.EncodeToString(idRaw)
	secret := base64.RawURLEncoding.EncodeToString(secretRaw)

	hash, err = HashSecret(secret)
	if err != nil {
		return "", "", "", err
	}
	return prefix + id + "_" + secret, id, hash, nil
}

// Split, tam token'i public id ve gizli kisma ayirir.
// Hem istemci hem admin onekini kabul eder.
func Split(full string) (id, secret string, err error) {
	var rest string
	var ok bool
	for _, prefix := range []string{PrefixClient, PrefixAdmin, PrefixAPI, PrefixLegacyClient, PrefixLegacyAdmin, "zorven_live_", "zorven_admin_", "zorven_api_"} {
		if rest, ok = strings.CutPrefix(full, prefix); ok {
			break
		}
	}
	if !ok {
		return "", "", fmt.Errorf("%w: taninan bir onekle baslamiyor", ErrMalformedToken)
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok {
		return "", "", fmt.Errorf("%w: id ve secret ayraci yok", ErrMalformedToken)
	}
	if len(id) != tokenIDBytes*2 {
		return "", "", fmt.Errorf("%w: id uzunlugu %d, beklenen %d", ErrMalformedToken, len(id), tokenIDBytes*2)
	}
	if secret == "" {
		return "", "", fmt.Errorf("%w: secret bos", ErrMalformedToken)
	}
	return id, secret, nil
}

// Verify, gizli kismi saklanan hash ile karsilastirir.
// Karsilastirma sabit zamanlidir.
func Verify(secret, encodedHash string) (bool, error) {
	salt, want, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// --- ic yardimcilar --------------------------------------------------------

// hashSecret, "argon2id$<salt-b64>$<hash-b64>" bicimini uretir.
// HashSecret, verilen gizli kismin argon2id hash'ini uretir.
func HashSecret(secret string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("tuz uretilemedi: %w", err)
	}
	sum := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return "argon2id$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(sum), nil
}

func decodeHash(encoded string) (salt, sum []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return nil, nil, fmt.Errorf("%w: beklenmeyen bicim", ErrBadHash)
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[1]); err != nil {
		return nil, nil, fmt.Errorf("%w: tuz cozulemedi", ErrBadHash)
	}
	if sum, err = base64.RawStdEncoding.DecodeString(parts[2]); err != nil {
		return nil, nil, fmt.Errorf("%w: hash cozulemedi", ErrBadHash)
	}
	return salt, sum, nil
}

// dummyHash, VerifyDummy icin bir kez uretilen gecerli bicimli hash.
// sync.OnceValue ile tembel hesaplanir; paket yuklenirken maliyet olusmaz.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashSecret("gecersiz-token-icin-sabit-deger")
	if err != nil {
		// Bu yalnizca crypto/rand basarisiz olursa olur; o durumda zaten
		// gercek token uretimi de calismaz.
		return "argon2id$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return h
})

// VerifyDummy, tokenID veritabaninda bulunamadiginda cagrilir.
//
// Neden: kayit yoksa hemen donseydik, gecerli bir tokenID ile gecersizi
// yanit suresinden ayirt etmek mumkun olurdu — gecerlide argon2 calisir
// (~10ms), gecersizde hic calismaz. Saldirgan bu farkla gecerli tokenID'leri
// numaralandirabilirdi. Ayni maliyette sahte bir dogrulama yaparak iki yolu
// esitliyoruz. Sonuc bilerek yok sayilir.
func VerifyDummy(secret string) {
	_, _ = Verify(secret, dummyHash())
}
