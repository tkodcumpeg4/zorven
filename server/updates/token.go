package updates

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"
)

// Abonelikten cikma jetonu:
//
//	yeni:  base64url(userID) "." base64url(unixExp) "." base64url(HMAC(key, "announce-unsub|"+userID+"|"+exp))
//	eski:  base64url(userID) "." base64url(HMAC(key, "announce-unsub|"+userID))     (sureli degil)
//
// Eski (suresiz) jetonlar gecis penceresinde kabul edilir: LegacyCutoff'a kadar.
// Sonrasinda gecersizdir (kullanici panelden tercihini degistirebilir).
//
// Ortam:
//
//	ZORVEN_UNSUB_TOKEN_DAYS    yeni jetonun omru, gun (vars. 365)
//	ZORVEN_UNSUB_LEGACY_UNTIL  eski jetonlarin kabul son tarihi: YYYY-MM-DD veya RFC3339
//	                           (vars. DefaultLegacyCutoff = yayindan ~90 gun sonra)
const (
	DefaultUnsubTokenDays = 365
	maxUnsubTokenDays     = 3650
)

// DefaultLegacyCutoff: ZORVEN_UNSUB_LEGACY_UNTIL tanimsizsa eski jeton kabulunun son ani.
var DefaultLegacyCutoff = time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)

// TokenStatus, ParseUnsubTokenStatus sonucudur.
type TokenStatus int

const (
	TokenInvalid TokenStatus = iota
	TokenOK
	TokenExpired
)

// UnsubTokenTTL: ZORVEN_UNSUB_TOKEN_DAYS (gecersizse 365 gun).
func UnsubTokenTTL() time.Duration {
	days := DefaultUnsubTokenDays
	if v := strings.TrimSpace(os.Getenv("ZORVEN_UNSUB_TOKEN_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxUnsubTokenDays {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}

// LegacyCutoff: eski (suresiz) jetonlarin kabul son ani.
func LegacyCutoff() time.Time {
	v := strings.TrimSpace(os.Getenv("ZORVEN_UNSUB_LEGACY_UNTIL"))
	if v == "" {
		return DefaultLegacyCutoff
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t
	}
	return DefaultLegacyCutoff
}

// UnsubToken, kullanici icin DURUMSUZ ve SURELI abonelikten cikma jetonu uretir
// (omur: UnsubTokenTTL). Satir tutmaz; anahtar donerse eski jetonlar gecersiz olur.
func UnsubToken(key []byte, userID string) string {
	return UnsubTokenAt(key, userID, time.Now(), UnsubTokenTTL())
}

// UnsubTokenAt: now + ttl'de sona eren jeton (testler icin acik zaman).
func UnsubTokenAt(key []byte, userID string, now time.Time, ttl time.Duration) string {
	enc := base64.RawURLEncoding
	exp := strconv.FormatInt(now.Add(ttl).Unix(), 10)
	return enc.EncodeToString([]byte(userID)) + "." + enc.EncodeToString([]byte(exp)) + "." + enc.EncodeToString(unsubMAC(key, userID, exp))
}

// legacyUnsubToken: eski bicim (yalniz gecis testleri icin).
func legacyUnsubToken(key []byte, userID string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(userID)) + "." + enc.EncodeToString(unsubMAC(key, userID, ""))
}

// ParseUnsubToken, jetonu dogrular ve kullanici kimligini doner (suresi dolmus = gecersiz).
func ParseUnsubToken(key []byte, token string) (string, bool) {
	id, st := ParseUnsubTokenStatus(key, token, time.Now())
	return id, st == TokenOK
}

// ParseUnsubTokenStatus: imza gecerli ama sure dolmussa (ya da eski jeton
// kesim tarihini asmissa) TokenExpired; kullanici kimligi yine doner.
func ParseUnsubTokenStatus(key []byte, token string, now time.Time) (string, TokenStatus) {
	if len(key) == 0 {
		return "", TokenInvalid
	}
	token = strings.TrimSpace(token)
	if len(token) > 512 {
		return "", TokenInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return "", TokenInvalid
	}
	enc := base64.RawURLEncoding
	idRaw, err := enc.DecodeString(parts[0])
	if err != nil || len(idRaw) == 0 {
		return "", TokenInvalid
	}
	userID := string(idRaw)
	got, err := enc.DecodeString(parts[len(parts)-1])
	if err != nil {
		return "", TokenInvalid
	}
	if len(parts) == 2 { // eski, suresiz
		if !hmac.Equal(got, unsubMAC(key, userID, "")) {
			return "", TokenInvalid
		}
		if !now.Before(LegacyCutoff()) {
			return userID, TokenExpired
		}
		return userID, TokenOK
	}
	expRaw, err := enc.DecodeString(parts[1])
	if err != nil {
		return "", TokenInvalid
	}
	exp, err := strconv.ParseInt(string(expRaw), 10, 64)
	if err != nil || exp <= 0 {
		return "", TokenInvalid
	}
	if !hmac.Equal(got, unsubMAC(key, userID, string(expRaw))) {
		return "", TokenInvalid
	}
	if now.Unix() > exp {
		return userID, TokenExpired
	}
	return userID, TokenOK
}

// unsubMAC: exp bos ise eski bicim.
func unsubMAC(key []byte, userID, exp string) []byte {
	h := hmac.New(sha256.New, key)
	msg := "announce-unsub|" + userID
	if exp != "" {
		msg += "|" + exp
	}
	h.Write([]byte(msg))
	return h.Sum(nil)
}
