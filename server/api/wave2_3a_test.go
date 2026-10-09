package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// F-30: NUL / gecersiz metin gosterimi Postgres hatalari 500 degil 404 olur.
func TestFailMapsPostgresTextErrorsTo404(t *testing.T) {
	s := &Server{}
	for _, code := range []string{"22021", "22P02"} {
		rec := httptest.NewRecorder()
		s.fail(rec, fmt.Errorf("sorgu: %w", &pgconn.PgError{Code: code}))
		if rec.Code != http.StatusNotFound {
			t.Errorf("SQLSTATE %s -> %d, 404 beklenirdi", code, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.fail(rec, errors.New("baska hata"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("bilinmeyen hata -> %d, 500 beklenirdi", rec.Code)
	}
}

// F-34: secret adi bicimi.
func TestSecretNameRe(t *testing.T) {
	for _, n := range []string{"api_key", "A.b-c", "x", strings.Repeat("a", 64)} {
		if !secretNameRe.MatchString(n) {
			t.Errorf("%q kabul edilmeliydi", n)
		}
	}
	for _, n := range []string{"", "a b", "a}b", "a{{b", "../x", "a\x00b", "ad\n", "çay", strings.Repeat("a", 65)} {
		if secretNameRe.MatchString(n) {
			t.Errorf("%q reddedilmeliydi", n)
		}
	}
}

// F-41: kimliksiz health yalniz status; admin anahtariyla surum+istemci sayisi.
func TestHealthDetailsOnlyWhenAuthenticated(t *testing.T) {
	full, id, hash, err := auth.GenerateAdmin()
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Hub: tunnel.NewHub(), Version: "9.9.9"}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", srv.Routes())
	mw := &Middleware{
		Next:        mux,
		Key:         AdminKey{TokenID: id, Hash: hash},
		PublicPaths: map[string]bool{healthPath: true},
	}
	get := func(authz string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, healthPath, nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("health %q -> %d", authz, rec.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	anon := get("")
	if anon["status"] != "ok" || len(anon) != 1 {
		t.Errorf("kimliksiz health = %v, yalniz status beklenirdi", anon)
	}
	bad := get("Bearer zrv_admin_gecersiz.deger")
	if len(bad) != 1 {
		t.Errorf("gecersiz kimlikli health = %v, yalniz status beklenirdi", bad)
	}
	authed := get("Bearer " + full)
	if authed["version"] != "9.9.9" {
		t.Errorf("kimlikli health = %v, version beklenirdi", authed)
	}
	if _, ok := authed["connected_clients"]; !ok {
		t.Errorf("kimlikli health connected_clients icermeli: %v", authed)
	}
}

// F-29: basarisiz user_code denemeleri sinirlanir (429).
func TestDeviceApproveFailedAttemptsRateLimited(t *testing.T) {
	s := &Server{}
	try := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/device/approve", strings.NewReader(`{"user_code":"ABCD1234"}`))
		req.RemoteAddr = "203.0.113.7:5555"
		req = req.WithContext(withTenant(context.Background(), "ten_x"))
		rec := httptest.NewRecorder()
		s.deviceApprove(rec, req)
		return rec.Code
	}
	for i := 0; i < deviceApproveMaxFails; i++ {
		if c := try(); c != http.StatusNotFound {
			t.Fatalf("deneme %d -> %d, 404 beklenirdi", i, c)
		}
	}
	if c := try(); c != http.StatusTooManyRequests {
		t.Fatalf("limit asiminda %d, 429 beklenirdi", c)
	}
}
