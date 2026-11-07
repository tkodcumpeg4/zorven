package ingress

import (
	"context"
	"net/http"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

func trustedJSON(extra string) string {
	return `"trusted":{"cidrs":["203.0.113.0/24","2001:db8:1::/48"]` + extra + `}`
}

func TestTrustedBypassesWAF(t *testing.T) {
	h := secHandler(t, `{"waf":{"mode":"block"},`+trustedJSON(``)+`}`, "pro", nil, nil)
	bad := "/x?q=%3Cscript%3Ealert(1)%3C/script%3E"
	if w := serve(h, reqFrom("198.51.100.4", bad)); w.Code != 403 {
		t.Fatalf("guvenilmeyen IP icin WAF 403 bekleniyordu: %d", w.Code)
	}
	if w := serve(h, reqFrom("203.0.113.9", bad)); w.Code != http.StatusBadGateway {
		t.Fatalf("guvenilir IP WAF'tan gecmeli (502 offline): %d", w.Code)
	}
	if w := serve(h, reqFrom("2001:db8:1::7", bad)); w.Code != http.StatusBadGateway {
		t.Fatalf("guvenilir IPv6 gecmeli: %d", w.Code)
	}
}

func TestTrustedBypassesBotAndRate(t *testing.T) {
	h := botHandler(t, `{"bot":{"mode":"always","block_empty_ua":true},"rate":{"enabled":true,"requests":2,"window_seconds":60},`+trustedJSON(``)+`}`, nil)
	if w := serve(h, browserGet("/", "198.51.100.4")); w.Code == http.StatusBadGateway {
		t.Fatalf("guvenilmeyen IP sorguya girmeliydi: %d", w.Code)
	}
	for i := 0; i < 6; i++ {
		if w := serve(h, reqFrom("203.0.113.7", "/")); w.Code != http.StatusBadGateway {
			t.Fatalf("istek %d guvenilir IP gecmeliydi: %d", i, w.Code)
		}
	}
}

func TestTrustedGeoSkipOnlyWhenEnabled(t *testing.T) {
	geo := `"geo":{"mode":"allow","countries":["TR"]},`
	h := secHandler(t, `{`+geo+trustedJSON(``)+`}`, "pro", nil, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/x")); w.Code != 403 {
		t.Fatalf("skip_geo=false iken Geo hala engellemeli: %d", w.Code)
	}
	h = secHandler(t, `{`+geo+trustedJSON(`,"skip_geo":true`)+`}`, "pro", nil, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/x")); w.Code != http.StatusBadGateway {
		t.Fatalf("skip_geo=true iken gecmeli: %d", w.Code)
	}
}

func TestTrustedStillRequiresAccessLogin(t *testing.T) {
	hash, err := auth.HashSecret("pw")
	if err != nil {
		t.Fatal(err)
	}
	tun := routeFor(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`)
	tun.SecurityConfig = []byte(`{"waf":{"mode":"block"},` + trustedJSON(``) + `}`)
	tun.Plan = "pro"
	h := newAccessHandler(t, tun, nil)
	if err := h.Router.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := serve(h, reqFrom("203.0.113.9", "/x"))
	if w.Code == http.StatusBadGateway || w.Code == http.StatusOK {
		t.Fatalf("guvenilir IP de giris yapmak zorunda: %d", w.Code)
	}
}
