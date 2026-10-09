package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/session"
)

// Cerezle kimliklenen yazma isteklerinde Origin ve Content-Type denetimi (Y2).
func TestMiddlewareCSRFCookieWrites(t *testing.T) {
	sm := session.NewManager([]byte("0123456789abcdef0123456789abcdef"))
	val, err := sm.Issue(session.Session{UserID: "u1", TenantID: "ten_x", Login: "u1", Method: "github"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fk := newLegacyFake()
	fk.roles["ten_x:u1"] = "owner"
	m := &Middleware{
		Sessions:     sm,
		Store:        fk,
		TrustedHosts: TrustedOriginHosts("zorven.app", []string{"zorven.app", "panel.zorven.app"}),
		Next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	run := func(method, origin, referer, ctype, body, authz string) int {
		var rd *strings.Reader
		if body != "" {
			rd = strings.NewReader(body)
		} else {
			rd = strings.NewReader("")
		}
		r := httptest.NewRequest(method, "https://panel.zorven.app/api/v1/tunnels", rd)
		r.Host = "panel.zorven.app"
		r.AddCookie(&http.Cookie{Name: session.CookieName, Value: val})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if referer != "" {
			r.Header.Set("Referer", referer)
		}
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if authz != "" {
			r.Header.Set("Authorization", authz)
		}
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Code
	}
	cases := []struct {
		name                                 string
		method, origin, referer, ctype, body string
		authz                                string
		want                                 int
	}{
		{"get-no-origin", http.MethodGet, "", "", "", "", "", 204},
		{"panel-json", http.MethodPost, "https://panel.zorven.app", "", "application/json", `{}`, "", 204},
		{"panel-json-charset", http.MethodPatch, "https://panel.zorven.app", "", "application/json; charset=utf-8", `{}`, "", 204},
		{"apex-json", http.MethodPost, "https://zorven.app", "", "application/json", `{}`, "", 204},
		{"panel-delete-nobody", http.MethodDelete, "https://panel.zorven.app", "", "", "", "", 204},
		{"referer-fallback", http.MethodPost, "", "https://panel.zorven.app/tunnels", "application/json", `{}`, "", 204},
		{"localhost-dev", http.MethodPost, "http://localhost:3000", "", "application/json", `{}`, "", 204},
		{"tunnel-origin", http.MethodPost, "https://evil--acme.zorven.app", "", "application/json", `{}`, "", 403},
		{"foreign-origin", http.MethodPost, "https://evil.example", "", "application/json", `{}`, "", 403},
		{"null-origin", http.MethodPost, "null", "", "application/json", `{}`, "", 403},
		{"no-origin", http.MethodPost, "", "", "application/json", `{}`, "", 403},
		{"suffix-trick", http.MethodPost, "https://panel.zorven.app.evil.example", "", "application/json", `{}`, "", 403},
		{"text-plain", http.MethodPost, "https://panel.zorven.app", "", "text/plain", `{}`, "", 415},
		{"form", http.MethodPost, "https://panel.zorven.app", "", "application/x-www-form-urlencoded", `a=b`, "", 415},
		{"no-ctype", http.MethodPost, "https://panel.zorven.app", "", "", `{}`, "", 415},
		// Authorization basligi olan istek capraz sitede preflight'siz uretilemez:
		// CSRF denetimi uygulanmaz (asagidaki dallar kimligi kendisi dogrular).
		{"bearer-exempt", http.MethodPost, "https://evil.example", "", "text/plain", `{}`, "Bearer x", 204},
	}
	for _, c := range cases {
		if got := run(c.method, c.origin, c.referer, c.ctype, c.body, c.authz); got != c.want {
			t.Errorf("%s: durum %d, beklenen %d", c.name, got, c.want)
		}
	}
}

func TestTrustedOriginHostsExcludesTunnels(t *testing.T) {
	hosts := TrustedOriginHosts("Zorven.App.", []string{"panel.zorven.app:443", "[::1]"})
	want := map[string]bool{"zorven.app": true, "www.zorven.app": true, "panel.zorven.app": true,
		"app.zorven.app": true, "localhost": true, "127.0.0.1": true, "::1": true}
	if len(hosts) != len(want) {
		t.Fatalf("beklenmeyen liste: %v", hosts)
	}
	for _, h := range hosts {
		if !want[h] {
			t.Fatalf("beklenmeyen host %q (%v)", h, hosts)
		}
	}
}
