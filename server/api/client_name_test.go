package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Istemci adi "--" iceremez: ad--<slug> ayracini taklit etmeyi engeller (F-15).
func TestCreateClientRejectsDoubleDash(t *testing.T) {
	s := &Server{}
	for _, name := range []string{"x--v1", "--a", "a--"} {
		r := httptest.NewRequest("POST", "/api/v1/clients", strings.NewReader(`{"name":"`+name+`"}`))
		w := httptest.NewRecorder()
		s.createClient(w, r.WithContext(withTenant(r.Context(), "ten_1")))
		if w.Code != http.StatusUnprocessableEntity || errCode(t, w) != "invalid_name" {
			t.Fatalf("%q: %d %s", name, w.Code, w.Body.String())
		}
	}
}
