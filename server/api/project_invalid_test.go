package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Bilinmeyen X-Zorven-Project: yazma istegi varsayilan projeye dusmez (400),
// okuma geriye uyumluluk icin gecer.
func TestMiddlewareNextRejectsWriteToUnknownProject(t *testing.T) {
	called := 0
	m := &Middleware{Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ })}
	for method, want := range map[string]int{http.MethodPost: 400, http.MethodDelete: 400, http.MethodGet: 200} {
		r := httptest.NewRequest(method, "/api/v1/tunnels", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyProjectInvalid, "yok"))
		w := httptest.NewRecorder()
		m.next(w, r)
		if w.Code != want {
			t.Errorf("%s: %d, beklenen %d", method, w.Code, want)
		}
	}
	if called != 1 {
		t.Fatalf("yalniz GET isleyiciye ulasmali, ulasan %d", called)
	}
}
