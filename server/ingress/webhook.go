package ingress

// Webhook imza dogrulama (FAZ 1 Part 2 / F05).
//
// Saglayicilar gelen istegin govdesi uzerinden bir HMAC uretir ve basliga koyar.
// Burasi ayni HMAC'i paylasilan secret ile yeniden hesaplar ve SABIT ZAMANLI
// karsilastirir. Imza tutmazsa istek ajana hic gonderilmez.
//
// Govde: imza govdenin TAMAMI uzerinden hesaplandigi icin, dogrulama yapilacak
// isteklerde govde once bellege alinir (bkz. ingress.go, WebhookMaxBody). Bu
// tamponlama YALNIZCA verify_webhook kurali eslesen isteklerde olur; diger tum
// trafik eskisi gibi akar.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultWebhookTolerance, zaman damgali saglayicilarda (stripe, slack) kabul
// edilen azami saat farki. Tekrar saldirisini (replay) sinirlar.
const defaultWebhookTolerance = 5 * time.Minute

// webhookProviders, desteklenen saglayici adlari. compileAction bilinmeyen bir
// ad gorurse kurali devre disi birakir (fail-closed).
var webhookProviders = map[string]struct{}{
	"github":  {},
	"stripe":  {},
	"gitlab":  {},
	"shopify": {},
	"slack":   {},
}

func isKnownWebhookProvider(p string) bool {
	_, ok := webhookProviders[p]
	return ok
}

// verifyWebhook, istegin imzasini dogrular. Gecerliyse true doner.
// tolerance yalnizca zaman damgali saglayicilar icin anlamlidir.
func verifyWebhook(provider string, secret, body []byte, h http.Header, tolerance time.Duration, now time.Time) bool {
	if len(secret) == 0 {
		return false
	}
	if tolerance <= 0 {
		tolerance = defaultWebhookTolerance
	}
	switch provider {
	case "github":
		return verifyGitHub(secret, body, h)
	case "stripe":
		return verifyStripe(secret, body, h, tolerance, now)
	case "gitlab":
		return verifyGitLab(secret, h)
	case "shopify":
		return verifyShopify(secret, body, h)
	case "slack":
		return verifySlack(secret, body, h, tolerance, now)
	}
	return false
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// equalHex, beklenen HMAC ile hex kodlu imzayi sabit zamanda karsilastirir.
func equalHex(expected []byte, got string) bool {
	raw, err := hex.DecodeString(strings.TrimSpace(got))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, raw)
}

// --- github: X-Hub-Signature-256: sha256=<hex> ---
func verifyGitHub(secret, body []byte, h http.Header) bool {
	sig := h.Get("X-Hub-Signature-256")
	rest, ok := strings.CutPrefix(sig, "sha256=")
	if !ok {
		return false
	}
	return equalHex(hmacSHA256(secret, body), rest)
}

// --- stripe: Stripe-Signature: t=<unix>,v1=<hex>[,v1=<hex>...] ---
// Imza "<t>.<govde>" uzerinden hesaplanir. Birden cok v1 olabilir (anahtar
// rotasyonu sirasinda); herhangi biri tutarsa gecerlidir.
func verifyStripe(secret, body []byte, h http.Header, tolerance time.Duration, now time.Time) bool {
	header := h.Get("Stripe-Signature")
	if header == "" {
		return false
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return false
	}
	if !timestampFresh(ts, tolerance, now) {
		return false
	}
	expected := hmacSHA256(secret, []byte(ts+"."+string(body)))
	for _, s := range sigs {
		if equalHex(expected, s) {
			return true
		}
	}
	return false
}

// --- gitlab: X-Gitlab-Token: <token> (HMAC yok, duz token) ---
func verifyGitLab(secret []byte, h http.Header) bool {
	got := h.Get("X-Gitlab-Token")
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare(secret, []byte(got)) == 1
}

// --- shopify: X-Shopify-Hmac-Sha256: <base64> ---
func verifyShopify(secret, body []byte, h http.Header) bool {
	got := strings.TrimSpace(h.Get("X-Shopify-Hmac-Sha256"))
	if got == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		return false
	}
	return hmac.Equal(hmacSHA256(secret, body), raw)
}

// --- slack: X-Slack-Signature: v0=<hex>, X-Slack-Request-Timestamp: <unix> ---
// Imza "v0:<ts>:<govde>" uzerinden hesaplanir.
func verifySlack(secret, body []byte, h http.Header, tolerance time.Duration, now time.Time) bool {
	sig := h.Get("X-Slack-Signature")
	rest, ok := strings.CutPrefix(sig, "v0=")
	if !ok {
		return false
	}
	ts := h.Get("X-Slack-Request-Timestamp")
	if ts == "" || !timestampFresh(ts, tolerance, now) {
		return false
	}
	expected := hmacSHA256(secret, []byte("v0:"+ts+":"+string(body)))
	return equalHex(expected, rest)
}

// timestampFresh, unix saniye damgasinin tolerans penceresi icinde olup
// olmadigini soyler. Ileri ve geri sapmanin ikisi de sinirlanir.
func timestampFresh(ts string, tolerance time.Duration, now time.Time) bool {
	sec, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return false
	}
	d := now.Sub(time.Unix(sec, 0))
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}
