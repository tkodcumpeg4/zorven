package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// policyStub, yalnizca policy CRUD'unu bellekte tutan sahte depo.
type policyStub struct {
	store.Store
	pol store.Policy
}

func (p *policyStub) GetPolicy(_ context.Context, tenantID, id string) (store.Policy, error) {
	if tenantID != p.pol.TenantID || id != p.pol.ID {
		return store.Policy{}, store.ErrNotFound
	}
	return p.pol, nil
}
func (p *policyStub) UpdatePolicy(_ context.Context, _, _ string, name string, cfg json.RawMessage, enabled bool, prio int) (store.Policy, error) {
	p.pol.Name, p.pol.Config, p.pol.Enabled, p.pol.Priority = name, cfg, enabled, prio
	return p.pol, nil
}

func putPolicy(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policies/pol_1", strings.NewReader(body))
	req.SetPathValue("id", "pol_1")
	req = req.WithContext(withTenant(req.Context(), "ten_a"))
	rec := httptest.NewRecorder()
	s.updatePolicy(rec, req)
	return rec
}

// Kismi PUT ad/config/priority'yi silmemeli; gecersiz config 422 olmali.
func TestUpdatePolicyPartialAndValidation(t *testing.T) {
	cfg := json.RawMessage(`{"rules":[{"match":{},"action":{"type":"deny"}}]}`)
	stub := &policyStub{pol: store.Policy{ID: "pol_1", TenantID: "ten_a", Name: "orijinal",
		Config: cfg, Enabled: true, Priority: 50}}
	s := &Server{Store: stub}

	if rec := putPolicy(t, s, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("kismi PUT %d: %s", rec.Code, rec.Body.String())
	}
	if stub.pol.Name != "orijinal" || string(stub.pol.Config) != string(cfg) || stub.pol.Priority != 50 || stub.pol.Enabled {
		t.Fatalf("kismi PUT alanlari bozdu: %+v", stub.pol)
	}

	if rec := putPolicy(t, s, `{"config":{"rules":[{"action":{"type":"yok"}}]}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("gecersiz config 422 olmali: %d", rec.Code)
	}
	if string(stub.pol.Config) != string(cfg) {
		t.Fatal("gecersiz config kaydedildi")
	}
	if rec := putPolicy(t, s, `{"name":"  "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bos ad 400 olmali: %d", rec.Code)
	}
	if rec := putPolicy(t, s, `{"name":"yeni","priority":7}`); rec.Code != http.StatusOK || stub.pol.Name != "yeni" || stub.pol.Priority != 7 {
		t.Fatalf("ad/priority guncellenmedi: %d %+v", rec.Code, stub.pol)
	}
}
