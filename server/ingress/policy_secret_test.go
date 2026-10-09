package ingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const whPolicy = `{"rules":[{"match":{"path_prefix":"/webhooks"},
  "action":{"type":"verify_webhook","provider":"github","secret_ref":"{{secret:gh}}"}}]}`

// {{secret:ad}} derleme zamaninda cozulmeli.
func TestSecretRefResolvedAtCompileTime(t *testing.T) {
	cp, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"gh": "s3cr3t"})
	if cp == nil || len(cp.rules) != 1 {
		t.Fatal("kural derlenmedi")
	}
	if got := string(cp.rules[0].action.secret); got != "s3cr3t" {
		t.Errorf("secret cozulmedi: %q", got)
	}
}

// assertFailClosed, derlenemeyen guvenlik kuralinin 503 reddine donustugunu
// ve istegi gecirmedigini dogrular (F-02).
func assertFailClosed(t *testing.T, cp *compiledPolicy, what string) {
	t.Helper()
	if cp == nil || len(cp.rules) != 1 {
		t.Fatalf("%s: kural fail-closed olarak korunmadi (sessizce dustu)", what)
	}
	act := cp.rules[0].action
	if act.kind != actDeny || act.status != 503 {
		t.Fatalf("%s: beklenen deny 503, gelen kind=%v status=%d", what, act.kind, act.status)
	}
	rt := routerWith(cp)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/x", strings.NewReader("{}"))
	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if out.denyStatus != 503 {
		t.Fatalf("%s: imzasiz istek reddedilmedi: %+v", what, out)
	}
}

// Secret bulunamazsa kural fail-closed (503) olmali — imzasiz istek gecmemeli.
func TestSecretRefMissingFailsClosed(t *testing.T) {
	cp, _ := compilePolicy([]byte(whPolicy), 10, nil)
	assertFailClosed(t, cp, "secret yok")
	cp2, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"baska": "x"})
	assertFailClosed(t, cp2, "yanlis isimli secret")
}

// Bilinmeyen saglayici da fail-closed olmali.
func TestUnknownProviderFailsClosed(t *testing.T) {
	raw := strings.Replace(whPolicy, `"provider":"github"`, `"provider":"myprovider"`, 1)
	cp, _ := compilePolicy([]byte(raw), 10, map[string]string{"gh": "s"})
	assertFailClosed(t, cp, "bilinmeyen saglayici")
}

// policySecretRefs benzersiz adlari toplamali.
func TestPolicySecretRefs(t *testing.T) {
	raw := []byte(`{"a":"{{secret:one}}","b":"{{secret:two}}","c":"{{secret:one}}","d":"duz"}`)
	got := policySecretRefs(raw)
	if len(got) != 2 {
		t.Fatalf("2 benzersiz ad bekleniyordu, alinan: %v", got)
	}
}

// Duz deger (referans olmayan) oldugu gibi gecmeli.
func TestResolveSecretRefPlainValue(t *testing.T) {
	v, ok := resolveSecretRef("duz-token", nil)
	if !ok || v != "duz-token" {
		t.Errorf("duz deger bozuldu: %q %v", v, ok)
	}
}

func ghSign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// EN KRITIK REGRESYON: dogrulama govdeyi okur; govde ajana BOZULMADAN ulasmali.
func TestWebhookBodyPreservedForDownstream(t *testing.T) {
	body := `{"event":"ping","n":42}`
	cp, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"gh": "s3cr3t"})
	rt := routerWith(cp)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/gh", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", ghSign([]byte("s3cr3t"), []byte(body)))

	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if out.terminal() {
		t.Fatalf("gecerli imza reddedildi: %+v", out)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("govde yeniden okunamadi: %v", err)
	}
	if string(got) != body {
		t.Errorf("govde bozuldu:\n aldim   = %q\n beklenen= %q", got, body)
	}
	if req.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength bozuldu: %d != %d", req.ContentLength, len(body))
	}
}

// Gecersiz imza 401 ile sonlandirmali.
func TestWebhookInvalidSignatureRejected(t *testing.T) {
	cp, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"gh": "s3cr3t"})
	rt := routerWith(cp)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/gh", strings.NewReader(`{}`))
	req.Header.Set("X-Hub-Signature-256", ghSign([]byte("yanlis"), []byte(`{}`)))

	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if !out.webhookFailed || out.webhookStatus != http.StatusUnauthorized {
		t.Errorf("gecersiz imza 401 vermedi: %+v", out)
	}
}

// Sinir asan govde 413 ile reddedilmeli (dogrulanamayan istek gecirilmez).
func TestWebhookOversizedBodyRejected(t *testing.T) {
	cp, _ := compilePolicy([]byte(whPolicy), 10, map[string]string{"gh": "s3cr3t"})
	rt := routerWith(cp)
	big := strings.Repeat("a", WebhookMaxBody+10)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/webhooks/gh", strings.NewReader(big))
	req.Header.Set("X-Hub-Signature-256", ghSign([]byte("s3cr3t"), []byte(big)))

	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if !out.webhookFailed || out.webhookStatus != http.StatusRequestEntityTooLarge {
		t.Errorf("buyuk govde 413 vermedi: %+v", out)
	}
}

// REGRESYON: verify_webhook kurali YOKKEN govde tamponlanmamali.
func TestNoWebhookRuleLeavesBodyUntouched(t *testing.T) {
	plain := `{"rules":[{"match":{"path_prefix":"/x"},"action":{"type":"set_header",
	  "request":{"set":{"X-A":"1"}}}}]}`
	cp, _ := compilePolicy([]byte(plain), 10, nil)
	rt := routerWith(cp)

	body := "akan-govde"
	req := httptest.NewRequest(http.MethodPost, "http://example.com/x", strings.NewReader(body))
	orig := req.Body

	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if out.terminal() {
		t.Fatalf("beklenmeyen sonlandirma: %+v", out)
	}
	if req.Body != orig {
		t.Error("webhook kurali yokken govde tamponlandi — akis bozuldu")
	}
}
