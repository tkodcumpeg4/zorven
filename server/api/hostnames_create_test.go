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

// hostCreateStore, createHostname'in ihtiyac duydugu depo yuzeyini bellekte taklit eder.
// Acik surumde plan/kota yoktur: kisa adlar sinirsizdir.
type hostCreateStore struct {
	store.Store
	taken    map[string]bool
	reserved map[string]bool
	added    []store.Hostname
}

func (f *hostCreateStore) GetTenant(context.Context, string) (store.Tenant, error) {
	return store.Tenant{ID: "ten_1", Slug: "acme"}, nil
}
func (f *hostCreateStore) IsReservedName(_ context.Context, n string) (bool, error) {
	return f.reserved[n], nil
}
func (f *hostCreateStore) AddHostnameWithProject(_ context.Context, tenantID, tunnelID, fqdn, typ, proj string) (store.Hostname, error) {
	if f.taken[fqdn] {
		return store.Hostname{}, store.ErrHostnameTaken
	}
	f.taken[fqdn] = true
	h := store.Hostname{ID: "hst_1", TenantID: tenantID, FQDN: fqdn, Type: typ}
	f.added = append(f.added, h)
	return h, nil
}

func hostCreate(s *Server, body string, platformAdmin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/hostnames", strings.NewReader(body))
	ctx := withTenant(r.Context(), "ten_1")
	if platformAdmin {
		ctx = withPlatformAdmin(ctx)
	}
	w := httptest.NewRecorder()
	s.createHostname(w, r.WithContext(ctx))
	return w
}

func hostServer() (*Server, *hostCreateStore) {
	st := &hostCreateStore{taken: map[string]bool{}, reserved: map[string]bool{"admin": true, "support": true}}
	return &Server{Store: st, PlatformDomain: "zorven.app"}, st
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Error.Code
}

func TestCreateHostnameDefaultsToScoped(t *testing.T) {
	s, st := hostServer()
	for _, body := range []string{`{"name":"api"}`, `{"name":"api","kind":"scoped"}`} {
		st.taken = map[string]bool{}
		w := hostCreate(s, body, false)
		if w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		got := st.added[len(st.added)-1]
		if got.FQDN != "api--acme.zorven.app" || got.Type != store.HostTypeScoped {
			t.Fatalf("%s: kapsamli ad bekleniyordu: %+v", body, got)
		}
	}
}

// Acik surumde kisa ad sayisi sinirsizdir ve her kiraci kisa ad alabilir.
func TestCreateHostnameShortUnlimited(t *testing.T) {
	s, st := hostServer()
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		w := hostCreate(s, `{"name":"`+n+`","kind":"short"}`, false)
		if w.Code != http.StatusCreated {
			t.Fatalf("kisa ad %s: %d %s", n, w.Code, w.Body.String())
		}
	}
	if got := st.added[0]; got.FQDN != "a1.zorven.app" || got.Type != store.HostTypeGlobal {
		t.Fatalf("kisa ad bekleniyordu: %+v", got)
	}
}

func TestCreateHostnameConflicts(t *testing.T) {
	s, st := hostServer()
	// Alinmis kapsamli ad
	st.taken["api--acme.zorven.app"] = true
	if w := hostCreate(s, `{"name":"api"}`, false); w.Code != http.StatusConflict || errCode(t, w) != "hostname_taken" {
		t.Fatalf("kapsamli alinmis: %d %s", w.Code, w.Body.String())
	}
	// Alinmis kisa ad
	st.taken["web.zorven.app"] = true
	if w := hostCreate(s, `{"name":"web","kind":"short"}`, false); w.Code != http.StatusConflict || errCode(t, w) != "hostname_taken" {
		t.Fatalf("kisa alinmis: %d %s", w.Code, w.Body.String())
	}
	// Ayrilmis ad (kisa); platform yoneticisi icin de gecerli (altyapi adlari)
	for _, admin := range []bool{false, true} {
		if w := hostCreate(s, `{"name":"admin","kind":"short"}`, admin); w.Code != http.StatusConflict || errCode(t, w) != "name_reserved" {
			t.Fatalf("ayrilmis (admin=%v): %d %s", admin, w.Code, w.Body.String())
		}
	}
	// Kapsamli adda tam eslesmeli ayrilmis kelime sorun degil: "--" ile cakismaz.
	if w := hostCreate(s, `{"name":"admin"}`, false); w.Code != http.StatusCreated {
		t.Fatalf("admin--acme serbest olmali: %d %s", w.Code, w.Body.String())
	}
}

// Marka deseni: zorven/rpshell iceren etiket kisa adda (platform yoneticisi haric)
// VE kapsamli adin <ad> kisminda (yonetici dahil) reddedilir.
func TestCreateHostnameBrandPattern(t *testing.T) {
	s, st := hostServer()
	for _, n := range []string{"zorven-login", "myzorven", "zorvenpay", "rpshell-x", "ZorVen", "zor-ven"} {
		body := `{"name":"` + n + `","kind":"short"}`
		if w := hostCreate(s, body, false); w.Code != http.StatusConflict || errCode(t, w) != "name_reserved" {
			t.Fatalf("kisa %q reddedilmeli: %d %s", n, w.Code, w.Body.String())
		}
		scoped := `{"name":"` + n + `"}`
		for _, admin := range []bool{false, true} {
			if w := hostCreate(s, scoped, admin); w.Code != http.StatusConflict || errCode(t, w) != "name_reserved" {
				t.Fatalf("kapsamli %q (admin=%v) reddedilmeli: %d %s", n, admin, w.Code, w.Body.String())
			}
		}
	}
	if len(st.added) != 0 {
		t.Fatalf("hicbiri kaydedilmemeli: %+v", st.added)
	}
	// Platform yoneticisi kisa adda marka deseninden muaf.
	if w := hostCreate(s, `{"name":"zorven-status","kind":"short"}`, true); w.Code != http.StatusCreated {
		t.Fatalf("yonetici marka deseninde kisa ad alabilmeli: %d %s", w.Code, w.Body.String())
	}
}

func TestCreateHostnameRejectsDoubleDashAndBadKind(t *testing.T) {
	s, _ := hostServer()
	for _, body := range []string{`{"name":"api--x"}`, `{"name":"api--x","kind":"short"}`} {
		if w := hostCreate(s, body, false); w.Code != http.StatusUnprocessableEntity || errCode(t, w) != "invalid_name" {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := hostCreate(s, `{"name":"api","kind":"x"}`, false); w.Code != http.StatusUnprocessableEntity || errCode(t, w) != "invalid_kind" {
		t.Fatalf("gecersiz kind: %d %s", w.Code, w.Body.String())
	}
}
