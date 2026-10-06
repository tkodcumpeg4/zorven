package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type tenantHeaderStore struct {
	store.Store
	tenants map[string]bool
}

func (m *tenantHeaderStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	if !m.tenants[id] {
		return store.Tenant{}, store.ErrTenantNotFound
	}
	return store.Tenant{ID: id, Slug: id}, nil
}

func (m *tenantHeaderStore) GetProjectBySlug(ctx context.Context, tenantID, slug string) (store.Project, error) {
	return store.Project{}, store.ErrNotFound
}

func (m *tenantHeaderStore) GetDefaultProject(ctx context.Context, tenantID string) (store.Project, error) {
	return store.Project{}, store.ErrNotFound
}

// Admin anahtari + X-Tenant-ID: panelin "Yonetime Gec" secimi bu baslikla
// tasinir. Var olan kiraci kapsami degistirir; olmayan kiraci 404 verir.
func TestAdminKey_XTenantID(t *testing.T) {
	adminKey, tokenID, hash, err := auth.GenerateAdmin()
	if err != nil {
		t.Fatal(err)
	}
	var gotTenant string
	var gotAdmin bool
	mw := &Middleware{
		Key:         AdminKey{TokenID: tokenID, Hash: hash},
		Limiter:     ratelimit.New(100, 100),
		Store:       &tenantHeaderStore{tenants: map[string]bool{"ten_b": true}},
		PublicPaths: map[string]bool{},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTenant, _ = tenantFromContext(r.Context())
			gotAdmin = isPlatformAdmin(r.Context())
			w.WriteHeader(http.StatusOK)
		}),
	}
	do := func(tenantHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels", nil)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		if tenantHeader != "" {
			req.Header.Set("X-Tenant-ID", tenantHeader)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(""); rec.Code != http.StatusOK || gotTenant != store.DefaultTenantID || !gotAdmin {
		t.Fatalf("basliksiz: %d tenant=%s admin=%v", rec.Code, gotTenant, gotAdmin)
	}
	if rec := do("ten_b"); rec.Code != http.StatusOK || gotTenant != "ten_b" || !gotAdmin {
		t.Fatalf("ten_b: %d tenant=%s admin=%v", rec.Code, gotTenant, gotAdmin)
	}
	if rec := do("ten_yok"); rec.Code != http.StatusNotFound {
		t.Fatalf("olmayan kiraci 404 olmaliydi: %d %s", rec.Code, rec.Body.String())
	}
}
