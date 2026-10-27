package auth

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestSessionCookieValueTossing(t *testing.T) {
	req := func(tlsOn bool, cookies ...string) string {
		r := httptest.NewRequest("GET", "https://panel.example/api/v1/me", nil)
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		} else {
			r.TLS = nil
		}
		for _, c := range cookies {
			r.Header.Add("Cookie", c)
		}
		return SessionCookieValue(r)
	}
	if got := req(true, "__Host-zorven.session_token=kurban"); got != "kurban" {
		t.Fatalf("tek cerez: %q", got)
	}
	if got := req(true, "__Host-zorven.session_token=saldirgan; __Host-zorven.session_token=kurban"); got != "" {
		t.Fatalf("farkli degerli cift cerez reddedilmeli: %q", got)
	}
	if got := req(true, "better-auth.session_token=saldirgan"); got != "" {
		t.Fatalf("HTTPS'te duz ad kabul edilmemeli: %q", got)
	}
	if got := req(true, "__Secure-better-auth.session_token=eski"); got != "" {
		t.Fatalf("eski __Secure- cerezi artik kabul edilmemeli: %q", got)
	}
	if got := req(false, "better-auth.session_token=yerel"); got != "yerel" {
		t.Fatalf("TLS'siz yerel gelistirmede duz ad: %q", got)
	}
	if got := req(true, "__Host-zorven.session_token=a; __Host-zorven.session_token=a"); got != "a" {
		t.Fatalf("ayni degerli tekrar sorun degil: %q", got)
	}
}
