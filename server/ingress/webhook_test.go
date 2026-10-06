package ingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"
)

var (
	whSecret = []byte("s3cr3t")
	whBody   = []byte(`{"event":"ping"}`)
)

func sign(secret, msg []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// Her saglayici icin: gecerli imza kabul edilir, bozuk imza reddedilir.
func TestVerifyWebhookProviders(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)

	mac := hmac.New(sha256.New, whSecret)
	mac.Write(whBody)
	shopifyB64 := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	tests := []struct {
		provider string
		headers  map[string]string
	}{
		{"github", map[string]string{"X-Hub-Signature-256": "sha256=" + sign(whSecret, whBody)}},
		{"stripe", map[string]string{"Stripe-Signature": "t=" + ts + ",v1=" + sign(whSecret, []byte(ts+"."+string(whBody)))}},
		{"gitlab", map[string]string{"X-Gitlab-Token": string(whSecret)}},
		{"shopify", map[string]string{"X-Shopify-Hmac-Sha256": shopifyB64}},
		{"slack", map[string]string{
			"X-Slack-Signature":         "v0=" + sign(whSecret, []byte("v0:"+ts+":"+string(whBody))),
			"X-Slack-Request-Timestamp": ts,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.provider+"/gecerli", func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			if !verifyWebhook(tt.provider, whSecret, whBody, h, 0, now) {
				t.Error("gecerli imza reddedildi")
			}
		})
		t.Run(tt.provider+"/bozuk", func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v+"00")
			}
			if verifyWebhook(tt.provider, whSecret, whBody, h, 0, now) {
				t.Error("bozuk imza kabul edildi")
			}
		})
		t.Run(tt.provider+"/basliksiz", func(t *testing.T) {
			if verifyWebhook(tt.provider, whSecret, whBody, http.Header{}, 0, now) {
				t.Error("basliksiz istek kabul edildi")
			}
		})
	}
}

// Govde degisirse imza tutmamali (imza govdeye bagli).
func TestVerifyWebhookBodyTampering(t *testing.T) {
	h := http.Header{}
	h.Set("X-Hub-Signature-256", "sha256="+sign(whSecret, whBody))
	if verifyWebhook("github", whSecret, []byte(`{"event":"pong"}`), h, 0, time.Now()) {
		t.Error("degistirilmis govde kabul edildi")
	}
}

// Yanlis secret reddedilmeli.
func TestVerifyWebhookWrongSecret(t *testing.T) {
	h := http.Header{}
	h.Set("X-Hub-Signature-256", "sha256="+sign([]byte("baska"), whBody))
	if verifyWebhook("github", whSecret, whBody, h, 0, time.Now()) {
		t.Error("yanlis secret ile imza kabul edildi")
	}
}

// Eski zaman damgasi (replay) reddedilmeli.
func TestVerifyWebhookStaleTimestamp(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * time.Minute)
	ts := strconv.FormatInt(old.Unix(), 10)
	h := http.Header{}
	h.Set("Stripe-Signature", "t="+ts+",v1="+sign(whSecret, []byte(ts+"."+string(whBody))))
	if verifyWebhook("stripe", whSecret, whBody, h, 5*time.Minute, now) {
		t.Error("tolerans disindaki zaman damgasi kabul edildi")
	}
	// Ayni imza tolerans genisletilince kabul edilmeli (imzanin kendisi gecerli).
	if !verifyWebhook("stripe", whSecret, whBody, h, time.Hour, now) {
		t.Error("tolerans icindeki gecerli imza reddedildi")
	}
}

// Bos secret hicbir zaman dogrulamamali.
func TestVerifyWebhookEmptySecret(t *testing.T) {
	h := http.Header{}
	h.Set("X-Gitlab-Token", "")
	if verifyWebhook("gitlab", nil, whBody, h, 0, time.Now()) {
		t.Error("bos secret ile dogrulama gecti")
	}
}

// Bilinmeyen saglayici reddedilmeli.
func TestVerifyWebhookUnknownProvider(t *testing.T) {
	if isKnownWebhookProvider("discord") {
		t.Error("discord desteklenmiyor olmali (ed25519, kapsam disi)")
	}
	if verifyWebhook("discord", whSecret, whBody, http.Header{}, 0, time.Now()) {
		t.Error("bilinmeyen saglayici dogrulandi")
	}
}
