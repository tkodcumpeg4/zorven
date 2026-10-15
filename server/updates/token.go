package updates

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// UnsubToken, kullanici icin DURUMSUZ abonelikten cikma jetonu uretir:
// base64url(userID) + "." + base64url(HMAC-SHA256(key, "announce-unsub|"+userID)).
// Satir tutmaz: tercih satiri olmayan (varsayilan abone) kullanicilar da
// jeton alabilir. Anahtar donerse eski jetonlar gecersiz olur.
func UnsubToken(key []byte, userID string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(userID)) + "." + enc.EncodeToString(unsubMAC(key, userID))
}

// ParseUnsubToken, jetonu dogrular ve kullanici kimligini doner.
func ParseUnsubToken(key []byte, token string) (string, bool) {
	if len(key) == 0 {
		return "", false
	}
	idPart, macPart, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || idPart == "" || macPart == "" || len(token) > 512 {
		return "", false
	}
	enc := base64.RawURLEncoding
	idRaw, err := enc.DecodeString(idPart)
	if err != nil || len(idRaw) == 0 {
		return "", false
	}
	got, err := enc.DecodeString(macPart)
	if err != nil {
		return "", false
	}
	userID := string(idRaw)
	if !hmac.Equal(got, unsubMAC(key, userID)) {
		return "", false
	}
	return userID, true
}

func unsubMAC(key []byte, userID string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("announce-unsub|" + userID))
	return h.Sum(nil)
}
