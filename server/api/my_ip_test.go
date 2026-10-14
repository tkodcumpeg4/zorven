package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMyIP(t *testing.T) {
	s := &Server{}
	for remote, want := range map[string]string{
		"203.0.113.7:51000":        "203.0.113.7",
		"[2001:db8::1]:443":        "2001:db8::1",
		"[::ffff:198.51.100.2]:80": "198.51.100.2",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/my-ip", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "6.6.6.6") // uydurma baslik yok sayilmali
		w := httptest.NewRecorder()
		s.myIP(w, r)
		var out map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 || out["ip"] != want {
			t.Errorf("%s -> %d %q, beklenen %q", remote, w.Code, out["ip"], want)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("my-ip onbelleklenmemeli")
		}
	}
}
