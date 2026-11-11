package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A4 #12: trafik politikasi PUT'u gecersiz baslik adi/degeri, CRLF Location ve
// izinsiz durum kodunu depoya ulasmadan 422 ile reddeder.
func TestSetTunnelTrafficRejectsInvalidConfig(t *testing.T) {
	s := &Server{Store: roleMatrixStore{newOrgAuthzStore()}}
	req := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/tunnels/x/traffic", strings.NewReader(body))
		r.SetPathValue("id", "x")
		ctx := withTenant(r.Context(), "ten_a")
		return r.WithContext(withUser(ctx, &AuthUser{ID: "u_owner", TenantID: "ten_a", Role: "owner"}))
	}
	for _, cfg := range []string{
		`{"request_headers":{"set":{"":"x"}}}`,
		`{"response_headers":{"set":{"X-A":"a\r\nSet-Cookie: x=1"}}}`,
		`{"redirects":[{"location":"/a\r\nX: y","status":301}]}`,
		`{"redirects":[{"location":"/a","status":999}]}`,
	} {
		w := httptest.NewRecorder()
		s.setTunnelTraffic(w, req(`{"enabled":true,"config":`+cfg+`}`))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid_config") {
			t.Errorf("%s: kod %d govde %s, beklenen 422 invalid_config", cfg, w.Code, w.Body.String())
		}
	}
	// Gecerli config dogrulamadan gecip depoya ulasir (sahte depo panikler -> 0).
	if c := callHandler(s.setTunnelTraffic, req(`{"enabled":true,"config":{"redirects":[{"match_prefix":"/a","location":"/b","status":308}]}}`)); c == http.StatusUnprocessableEntity {
		t.Fatalf("gecerli config 422 aldi")
	}
}
