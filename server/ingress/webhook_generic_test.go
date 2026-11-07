package ingress

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyGenericHMAC(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"a":1}`)
	secret := []byte("k")

	hexSig := hex.EncodeToString(hmacSHA256(secret, body))
	b64Sig := base64.StdEncoding.EncodeToString(hmacSHA256(secret, body))
	tsSig := hex.EncodeToString(hmacSHA256(secret, []byte(ts+"."+string(body))))

	hdr := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name string
		g    genericHMAC
		h    http.Header
		want bool
	}{
		{"hex", genericHMAC{SignatureHeader: "X-Sig"}, hdr("X-Sig", hexSig), true},
		{"hex+onek", genericHMAC{SignatureHeader: "X-Sig", Prefix: "sha256="}, hdr("X-Sig", "sha256="+hexSig), true},
		{"onek yok", genericHMAC{SignatureHeader: "X-Sig", Prefix: "sha256="}, hdr("X-Sig", hexSig), false},
		{"base64", genericHMAC{SignatureHeader: "X-Sig", Encoding: "base64"}, hdr("X-Sig", b64Sig), true},
		{"bozuk", genericHMAC{SignatureHeader: "X-Sig"}, hdr("X-Sig", strings.Repeat("0", 64)), false},
		{"baslik yok", genericHMAC{SignatureHeader: "X-Sig"}, hdr(), false},
		{"baslik adi bos", genericHMAC{}, hdr("X-Sig", hexSig), false},
		{"zaman damgali", genericHMAC{SignatureHeader: "X-Sig", TimestampHeader: "X-Ts"}, hdr("X-Sig", tsSig, "X-Ts", ts), true},
		{"damga eksik", genericHMAC{SignatureHeader: "X-Sig", TimestampHeader: "X-Ts"}, hdr("X-Sig", tsSig), false},
		{"damga bayat", genericHMAC{SignatureHeader: "X-Sig", TimestampHeader: "X-Ts"},
			hdr("X-Sig", hex.EncodeToString(hmacSHA256(secret, []byte("1000."+string(body)))), "X-Ts", "1000"), false},
	}
	for _, c := range cases {
		if got := verifyGenericHMAC(c.g, secret, body, c.h, 0, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if verifyGenericHMAC(genericHMAC{SignatureHeader: "X-Sig"}, nil, body, hdr("X-Sig", hexSig), 0, now) {
		t.Error("bos secret kabul edildi")
	}
}

// Stripe/Slack: ozel tolerans uygulanir; sinirin icinde kabul, disinda red.
func TestWebhookConfigurableTolerance(t *testing.T) {
	now := time.Now()
	old := strconv.FormatInt(now.Add(-2*time.Minute).Unix(), 10)
	h := http.Header{}
	h.Set("Stripe-Signature", "t="+old+",v1="+sign(whSecret, []byte(old+"."+string(whBody))))
	if !verifyWebhook("stripe", whSecret, whBody, h, 5*time.Minute, now) {
		t.Error("5 dk tolerans icinde reddedildi")
	}
	if verifyWebhook("stripe", whSecret, whBody, h, 30*time.Second, now) {
		t.Error("30 sn tolerans disinda kabul edildi")
	}
	// Gelecekteki damga da reddedilir.
	fut := strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10)
	h.Set("Stripe-Signature", "t="+fut+",v1="+sign(whSecret, []byte(fut+"."+string(whBody))))
	if verifyWebhook("stripe", whSecret, whBody, h, 0, now) {
		t.Error("gelecek damga kabul edildi")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("kopuk") }

// Govde okunamazsa dogrulama fail-closed: istek gecmez.
func TestWebhookBodyReadErrorFailsClosed(t *testing.T) {
	cp, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"gh": "s"})
	rt := routerWith(cp)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/x", errReader{})
	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if !out.webhookFailed {
		t.Fatalf("okuma hatasinda istek reddedilmedi: %+v", out)
	}
}

// provider=hmac politika uzerinden: dogru imza gecer, yanlis 401, baslik adi yoksa fail-closed.
func TestGenericHMACPolicy(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/webhooks"},"action":{"type":"verify_webhook","provider":"hmac",
	  "secret_ref":"{{secret:gh}}","signature_header":"X-Sig","signature_prefix":"sha256="}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, map[string]string{"gh": "s"})
	rt := routerWith(cp)
	body := `{"x":1}`
	mk := func(sig string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/x", strings.NewReader(body))
		r.Header.Set("X-Sig", sig)
		return r
	}
	good := "sha256=" + hex.EncodeToString(hmacSHA256([]byte("s"), []byte(body)))
	if out := rt.evaluatePolicies("example.com", mk(good), "1.2.3.4", "tun_1", false); out.webhookFailed {
		t.Errorf("gecerli imza reddedildi: %+v", out)
	}
	if out := rt.evaluatePolicies("example.com", mk("sha256=00"), "1.2.3.4", "tun_1", false); !out.webhookFailed || out.webhookStatus != 401 {
		t.Errorf("bozuk imza 401 vermedi: %+v", out)
	}
	noHdr := strings.Replace(raw, `"signature_header":"X-Sig",`, "", 1)
	cp2, _ := compilePolicy([]byte(noHdr), 10, map[string]string{"gh": "s"})
	assertFailClosed(t, cp2, "hmac basliksiz")
}
