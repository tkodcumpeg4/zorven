package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type nilListStore struct {
	store.Store
}

func (m *nilListStore) ListClients(ctx context.Context, tenantID string) ([]store.Client, error) {
	return nil, nil
}

func (m *nilListStore) ListTunnels(ctx context.Context, tenantID string) ([]store.Tunnel, error) {
	return nil, nil
}

func (m *nilListStore) ListHostnames(ctx context.Context, tenantID string) ([]store.Hostname, error) {
	return nil, nil
}

func (m *nilListStore) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	return nil, nil
}

func (m *nilListStore) AdminListTenantsWithCounts(ctx context.Context) ([]store.TenantWithCounts, error) {
	return nil, nil
}

func (m *nilListStore) AdminListAllClients(ctx context.Context) ([]store.ClientWithTenant, error) {
	return nil, nil
}

func (m *nilListStore) AdminListAllHostnames(ctx context.Context) ([]store.HostnameWithTenant, error) {
	return nil, nil
}

func TestEmptyListsSerializeAsEmptyArray(t *testing.T) {
	srv := &Server{
		Store: &nilListStore{},
		Hub:   tunnel.NewHub(),
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		admin   bool
	}{
		{name: "listClients", handler: srv.listClients},
		{name: "listTunnels", handler: srv.listTunnels},
		{name: "listHostnames", handler: srv.listHostnames},
		{name: "adminListTenants", handler: srv.adminListTenants, admin: true},
		{name: "adminListClients", handler: srv.adminListClients, admin: true},
		{name: "adminListHostnames", handler: srv.adminListHostnames, admin: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			ctx := withTenant(req.Context(), "ten_default")
			if tc.admin {
				ctx = withPlatformAdmin(ctx)
			}
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			tc.handler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}
			body := strings.TrimSpace(rec.Body.String())
			if body != "[]" {
				t.Errorf("expected empty JSON array '[]', got %q", body)
			}
		})
	}
}
