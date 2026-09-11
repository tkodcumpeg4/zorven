package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// mockTokenStore, store.Store'u gomer ve YALNIZCA token dogrulama yolunun
// ihtiyac duydugu iki metodu override eder (digerleri cagrilmaz).
type mockTokenStore struct {
	store.Store
	tok store.APIToken
}

func (m *mockTokenStore) GetAPITokenByTokenID(_ context.Context, tokenID string) (store.APIToken, error) {
	if tokenID == m.tok.TokenID {
		return m.tok, nil
	}
	return store.APIToken{}, errors.New("not found")
}
func (m *mockTokenStore) TouchAPITokenLastUsed(context.Context, string) error { return nil }

// allowEntitlements, CheckFeature'i her zaman gecer (API erisimi acik).
type allowEntitlements struct{ entitlements.EntitlementService }

func (allowEntitlements) CheckFeature(context.Context, string, entitlements.Feature) error {
	return nil
}

// buildTokenMiddleware, verilen scope'lu gercek bir API token'i ve bu token'i
// dogrulayan gercek Middleware zincirini kurar. Next, method'a gore
// requireScope uygulayan bir "handler" taklididir; boylece tam yigin sinanir:
// middleware (auth + yuzey + rate limit) -> handler guard (scope).
func buildTokenMiddleware(t *testing.T, scopes []string) (mw *Middleware, fullToken string, nextCalled *bool) {
	t.Helper()
	full, id, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		needed := ScopeTunnelsRead
		if r.Method != http.MethodGet {
			needed = ScopeTunnelsWrite
		}
		if !requireScope(w, r, needed) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mw = &Middleware{
		Store:        &mockTokenStore{tok: store.APIToken{TokenID: id, TokenHash: hash, TenantID: "ten_x", Scopes: scopes}},
		Entitlements: allowEntitlements{},
		APILimiter:   ratelimit.New(1000, 1000),
		Limiter:      ratelimit.New(1000, 1000),
		Next:         next,
	}
	return mw, full, &called
}

func TestIntegration_TokenScopeAndSurface(t *testing.T) {
	// 1. read token: GET tunnels -> 200; POST tunnels -> 403 insufficient_scope.
	t.Run("read token", func(t *testing.T) {
		mw, tok, called := buildTokenMiddleware(t, []string{ScopeTunnelsRead})

		// GET (read) -> izinli
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/tunnels", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET read: beklenen 200, alinan %d (%s)", w.Code, w.Body.String())
		}
		if !*called {
			t.Error("GET read: handler cagrilmaliydi")
		}
		if w.Header().Get("X-RateLimit-Limit") == "" {
			t.Error("X-RateLimit-Limit basligi eksik")
		}
		if w.Header().Get("X-Request-Id") == "" {
			t.Error("X-Request-Id basligi eksik")
		}

		// POST (write) -> yetersiz scope
		mw2, tok2, _ := buildTokenMiddleware(t, []string{ScopeTunnelsRead})
		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest("POST", "/api/v1/tunnels", nil)
		r2.Header.Set("Authorization", "Bearer "+tok2)
		mw2.ServeHTTP(w2, r2)
		if w2.Code != http.StatusForbidden {
			t.Fatalf("POST write: beklenen 403, alinan %d", w2.Code)
		}
		if body := w2.Body.String(); !contains(body, "insufficient_scope") {
			t.Errorf("POST write: insufficient_scope beklenirdi, alinan %s", body)
		}
	})

	// 2. write token: POST tunnels -> 200.
	t.Run("write token yazabilir", func(t *testing.T) {
		mw, tok, _ := buildTokenMiddleware(t, []string{ScopeTunnelsWrite})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/tunnels", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("beklenen 200, alinan %d (%s)", w.Code, w.Body.String())
		}
	})

	// 3. Yuzey: token yasak bir uca gidince 403 ve handler HIC cagrilmaz.
	t.Run("yuzey disi uc kapali", func(t *testing.T) {
		mw, tok, called := buildTokenMiddleware(t, []string{"read", "write"})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/mail/messages", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("beklenen 403, alinan %d", w.Code)
		}
		if !contains(w.Body.String(), "endpoint_not_available_for_token") {
			t.Errorf("endpoint_not_available_for_token beklenirdi, alinan %s", w.Body.String())
		}
		if *called {
			t.Error("yasak uc: handler ASLA cagrilmamaliydi")
		}
	})

	// 4. Eski read+write token'i write uca -> 200 (geriye uyum).
	t.Run("eski read+write yazabilir", func(t *testing.T) {
		mw, tok, _ := buildTokenMiddleware(t, []string{"read", "write"})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/tunnels", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("beklenen 200, alinan %d (%s)", w.Code, w.Body.String())
		}
	})

	// 5. Auth yok -> 401.
	t.Run("auth yok 401", func(t *testing.T) {
		mw, _, _ := buildTokenMiddleware(t, []string{ScopeTunnelsRead})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/tunnels", nil)
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("beklenen 401, alinan %d", w.Code)
		}
	})
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
