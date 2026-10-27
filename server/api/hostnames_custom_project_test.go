package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// customProjStore, createCustomHostname icin bellek ici depo.
type customProjStore struct {
	store.Store
	rows []store.Hostname
}

func (f *customProjStore) ListHostnames(context.Context, string) ([]store.Hostname, error) {
	return f.rows, nil
}
func (f *customProjStore) GetProjectByID(_ context.Context, _, id string) (store.Project, error) {
	return store.Project{ID: id, Name: map[string]string{"prj_def": "Default", "prj_web": "Web"}[id]}, nil
}
func (f *customProjStore) AddCustomHostnameWithProject(_ context.Context, tenantID, tunnelID, fqdn, tok, projectID string) (store.Hostname, error) {
	h := store.Hostname{ID: "hst_x", TenantID: tenantID, FQDN: fqdn, Type: store.HostTypeCustom, VerifyToken: tok, ProjectID: projectID}
	f.rows = append(f.rows, h)
	return h, nil
}

func postCustom(s *Server, project, fqdn string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"fqdn": fqdn})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/hostnames/custom", bytes.NewReader(body))
	ctx := withTenant(r.Context(), "ten_1")
	if project != "" {
		ctx = withProject(ctx, project)
	}
	w := httptest.NewRecorder()
	s.createCustomHostname(w, r.WithContext(ctx))
	return w
}

// Secili projede eklenen ozel alan adi o projeye yazilir (Default'a dusmez);
// ayni ad baska projede varsa acik mesajli 409 doner.
func TestCustomHostnameRespectsProject(t *testing.T) {
	st := &customProjStore{}
	s := &Server{Store: st, PlatformDomain: "zorven.app"}
	w := postCustom(s, "prj_web", "ornek-site.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("olusturma: %d %s", w.Code, w.Body.String())
	}
	if len(st.rows) != 1 || st.rows[0].ProjectID != "prj_web" {
		t.Fatalf("proje yanlis: %+v", st.rows)
	}
	w = postCustom(s, "prj_def", "ornek-site.com")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "hostname_in_other_project") || !strings.Contains(w.Body.String(), "Web") {
		t.Fatalf("baska projede kayitli: %d %s", w.Code, w.Body.String())
	}
}

// Ayni projede ayni ad tekrar eklenirse "baska projede" mesaji VERILMEZ
// (depo benzersizligiyle genel hostname_taken doner).
func TestCustomHostnameSameProjectNotOtherProject(t *testing.T) {
	st := &customProjStore{rows: []store.Hostname{{ID: "hst_1", TenantID: "ten_1", FQDN: "ornek-site.com", Type: store.HostTypeCustom, ProjectID: "prj_web"}}}
	s := &Server{Store: st, PlatformDomain: "zorven.app"}
	w := postCustom(s, "prj_web", "ornek-site.com")
	if strings.Contains(w.Body.String(), "hostname_in_other_project") {
		t.Fatalf("ayni projede 'baska proje' denmemeli: %d %s", w.Code, w.Body.String())
	}
}
