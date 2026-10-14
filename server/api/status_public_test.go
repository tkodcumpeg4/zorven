package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// T2: kimliksiz /status surum ve istemci sayisini vermez; kimlikliyse verir.
func TestStatusDetailsOnlyWhenAuthenticated(t *testing.T) {
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
		PublicPaths: map[string]bool{statusPath: true},
	}
	get := func(authz string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, statusPath, nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %q -> %d", authz, rec.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, authz := range []string{"", "Bearer zrv_admin_gecersiz.deger"} {
		anon := get(authz)
		if _, ok := anon["version"]; ok {
			t.Errorf("kimliksiz status version icermemeli: %v", anon)
		}
		if _, ok := anon["connected_clients"]; ok {
			t.Errorf("kimliksiz status connected_clients icermemeli: %v", anon)
		}
		if anon["status"] == nil || anon["components"] == nil {
			t.Errorf("genel durum alanlari eksik: %v", anon)
		}
	}
	authed := get("Bearer " + full)
	if authed["version"] != "9.9.9" {
		t.Errorf("kimlikli status version beklenirdi: %v", authed)
	}
	if _, ok := authed["connected_clients"]; !ok {
		t.Errorf("kimlikli status connected_clients icermeli: %v", authed)
	}
}
