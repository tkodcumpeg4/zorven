package api

// Organizasyon / API & Guvenlik / Secrets bolumlerinin rol ve dogrulama
// regresyon testleri (Postgres gerektirmez; sahte store ile).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type orgAuthzStore struct {
	store.Store
	mu       sync.Mutex
	roles    map[string]string // userID -> rol (ten_a)
	tokens   map[string]store.APIToken
	projects map[string]string // projectID -> tenantID
	rotated  []string
	revoked  []string
	created  []store.APIToken
	invites  map[string]store.TeamInvitation
	accepted []string
	tenants  map[string]bool
}

func newOrgAuthzStore() *orgAuthzStore {
	owner, mem := "u_owner", "u_mem"
	return &orgAuthzStore{
		roles: map[string]string{"u_owner": "owner", "u_admin": "admin", "u_mem": "member", "u_mem2": "member"},
		tokens: map[string]store.APIToken{
			"tok_owner": {ID: "tok_owner", TenantID: "ten_a", UserID: &owner, Name: "owner"},
			"tok_mem":   {ID: "tok_mem", TenantID: "ten_a", UserID: &mem, Name: "mem"},
			"tok_b":     {ID: "tok_b", TenantID: "ten_b", UserID: &mem, Name: "b"},
		},
		projects: map[string]string{"prj_a": "ten_a", "prj_b": "ten_b"},
		invites:  map[string]store.TeamInvitation{},
		tenants:  map[string]bool{"ten_a": true},
	}
}

func (m *orgAuthzStore) GetMemberRole(_ context.Context, tenantID, userID string) (string, error) {
	if tenantID != "ten_a" {
		return "", nil
	}
	return m.roles[userID], nil
}

func (m *orgAuthzStore) GetAPIToken(_ context.Context, tenantID, id string) (store.APIToken, error) {
	t, ok := m.tokens[id]
	if !ok || t.TenantID != tenantID {
		return store.APIToken{}, store.ErrNotFound
	}
	return t, nil
}

func (m *orgAuthzStore) RotateAPIToken(_ context.Context, tenantID, id, _, _ string) (store.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rotated = append(m.rotated, id)
	return m.tokens[id], nil
}

func (m *orgAuthzStore) RevokeAPIToken(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, id)
	return nil
}

func (m *orgAuthzStore) GetProjectByID(_ context.Context, tenantID, id string) (store.Project, error) {
	if m.projects[id] != tenantID {
		return store.Project{}, store.ErrNotFound
	}
	return store.Project{ID: id}, nil
}

func (m *orgAuthzStore) CreateAPIToken(_ context.Context, tenantID string, userID *string, name, tokenID, tokenHash, prefix string, scopes []string, expiresAt *time.Time) (store.APIToken, error) {
	t := store.APIToken{ID: "tok_new", TenantID: tenantID, UserID: userID, Name: name, Scopes: scopes, ExpiresAt: expiresAt}
	m.mu.Lock()
	m.created = append(m.created, t)
	m.mu.Unlock()
	return t, nil
}

func (m *orgAuthzStore) ListSecrets(context.Context, string, string) ([]store.Secret, error) {
	return []store.Secret{}, nil
}
func (m *orgAuthzStore) CreateSecret(_ context.Context, tenantID, _, name, value string) (store.Secret, error) {
	return store.Secret{ID: "sec_1", Name: name}, nil
}
func (m *orgAuthzStore) DeleteSecret(context.Context, string, string) error { return nil }
func (m *orgAuthzStore) GetSubscription(_ context.Context, tenantID string) (store.Subscription, error) {
	return store.Subscription{TenantID: tenantID, Plan: "pro"}, nil
}

func (m *orgAuthzStore) GetInvitation(_ context.Context, id string) (store.TeamInvitation, error) {
	inv, ok := m.invites[id]
	if !ok {
		return store.TeamInvitation{}, store.ErrNotFound
	}
	return inv, nil
}

func (m *orgAuthzStore) AcceptInvitation(_ context.Context, id, userID, _ string) (store.TeamInvitation, error) {
	m.accepted = append(m.accepted, id)
	inv := m.invites[id]
	inv.Status = "accepted"
	return inv, nil
}

func (m *orgAuthzStore) GetTenant(_ context.Context, id string) (store.Tenant, error) {
	if !m.tenants[id] {
		return store.Tenant{}, store.ErrTenantNotFound
	}
	return store.Tenant{ID: id}, nil
}

// fakeEnt, yalnizca test edilen entitlement cagrilarini uygular.
type fakeEnt struct {
	entitlements.EntitlementService
	acceptErr error
}

func (f *fakeEnt) CheckFeature(context.Context, string, entitlements.Feature) error { return nil }
func (f *fakeEnt) CanAcceptMember(context.Context, string) error                    { return f.acceptErr }

func orgReq(method, path, body, user string, email ...string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := withTenant(r.Context(), "ten_a")
	if user != "" {
		u := &AuthUser{ID: user, TenantID: "ten_a", Role: map[string]string{
			"u_owner": "owner", "u_admin": "admin", "u_mem": "member", "u_mem2": "member",
		}[user]}
		if len(email) > 0 {
			u.Email = email[0]
		}
		ctx = withUser(ctx, u)
	}
	return r.WithContext(ctx)
}

func TestAPITokenRotateRevokeOwnership(t *testing.T) {
	st := newOrgAuthzStore()
	s := &Server{Store: st}

	cases := []struct {
		user, tok string
		want      int
	}{
		{"u_mem", "tok_owner", http.StatusForbidden}, // baskasinin token'i
		{"u_mem2", "tok_mem", http.StatusForbidden},
		{"u_mem", "tok_mem", http.StatusOK},       // kendi token'i
		{"u_admin", "tok_owner", http.StatusOK},   // admin herkesinkini
		{"u_owner", "tok_b", http.StatusNotFound}, // baska kiraci
		{"u_owner", "tok_none", http.StatusNotFound},
	}
	for _, c := range cases {
		for _, op := range []string{"rotate", "revoke"} {
			var w *httptest.ResponseRecorder
			if op == "rotate" {
				r := orgReq("POST", "/api/v1/api-tokens/"+c.tok+"/rotate", "", c.user)
				r.SetPathValue("id", c.tok)
				w = httptest.NewRecorder()
				s.rotateAPIToken(w, r)
			} else {
				r := orgReq("DELETE", "/api/v1/api-tokens/"+c.tok, "", c.user)
				r.SetPathValue("id", c.tok)
				w = httptest.NewRecorder()
				s.revokeAPIToken(w, r)
			}
			want := c.want
			if op == "revoke" && want == http.StatusOK {
				want = http.StatusNoContent
			}
			if w.Code != want {
				t.Errorf("%s %s by %s: kod %d, beklenen %d (%s)", op, c.tok, c.user, w.Code, want, w.Body.String())
			}
		}
	}
	for _, id := range append(st.rotated, st.revoked...) {
		if id == "tok_b" || id == "tok_none" {
			t.Fatalf("yetkisiz/olmayan token degistirildi: %s", id)
		}
	}
	if len(st.rotated) != 2 || len(st.revoked) != 2 {
		t.Fatalf("beklenmeyen islem sayisi: rotate=%v revoke=%v", st.rotated, st.revoked)
	}
}

func TestCreateAPITokenValidation(t *testing.T) {
	st := newOrgAuthzStore()
	s := &Server{Store: st}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name, user, body string
		want             int
	}{
		{"gecmis expires_at", "u_mem", `{"name":"x","expires_at":"` + past + `"}`, http.StatusUnprocessableEntity},
		{"negatif gun", "u_mem", `{"name":"x","expires_in_days":-3}`, http.StatusUnprocessableEntity},
		{"bilinmeyen scope", "u_mem", `{"name":"x","scopes":["tunnels:read","god:mode"]}`, http.StatusUnprocessableEntity},
		{"member kisisel", "u_mem", `{"name":"x","scopes":[" Tunnels:Read ","tunnels:read"],"expires_at":"` + future + `"}`, http.StatusCreated},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		s.createAPIToken(w, orgReq("POST", "/api/v1/api-tokens", c.body, c.user))
		if w.Code != c.want {
			t.Errorf("%s: kod %d, beklenen %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	if len(st.created) != 1 {
		t.Fatalf("1 token olusmaliydi, %d", len(st.created))
	}
	if got := st.created[0].Scopes; len(got) != 1 || got[0] != "tunnels:read" {
		t.Errorf("kapsamlar normalize edilmeli: %v", got)
	}
}

func TestOrgWritesRequireAdmin(t *testing.T) {
	st := newOrgAuthzStore()
	s := &Server{Store: st, Entitlements: &fakeEnt{}}

	type call struct {
		name   string
		h      http.HandlerFunc
		method string
		body   string
		id     string
	}
	writes := []call{
		{"secret create", s.createSecret, "POST", `{"name":"db","value":"x"}`, ""},
		{"secret delete", s.deleteSecret, "DELETE", "", "sec_1"},
	}
	for _, c := range writes {
		r := orgReq(c.method, "/x", c.body, "u_mem")
		if c.id != "" {
			r.SetPathValue("id", c.id)
		}
		w := httptest.NewRecorder()
		c.h(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("member %s: kod %d, 403 beklendi (%s)", c.name, w.Code, w.Body.String())
		}
	}
	// Owner/admin yazabilir.
	for _, c := range writes[:2] {
		r := orgReq(c.method, "/x", c.body, "u_admin")
		if c.id != "" {
			r.SetPathValue("id", c.id)
		}
		w := httptest.NewRecorder()
		c.h(w, r)
		if w.Code >= 400 {
			t.Errorf("admin %s: kod %d (%s)", c.name, w.Code, w.Body.String())
		}
	}
	// Okuma member'a acik.
	for name, h := range map[string]http.HandlerFunc{"secrets": s.listSecrets} {
		w := httptest.NewRecorder()
		h(w, orgReq("GET", "/x", "", "u_mem"))
		if w.Code != http.StatusOK {
			t.Errorf("member %s okuma: kod %d", name, w.Code)
		}
	}
}

func TestAcceptInvitationEnforcesMemberLimit(t *testing.T) {
	st := newOrgAuthzStore()
	st.invites["inv_1"] = store.TeamInvitation{ID: "inv_1", OrganizationID: "ten_a", Email: "new@x.com", Status: "pending", Role: "member", ExpiresAt: time.Now().Add(time.Hour)}
	ent := &fakeEnt{acceptErr: &entitlements.EntitlementError{Code: "member_limit_reached", Message: "limit", Resource: "members", Limit: 1, Current: 1}}
	s := &Server{Store: st, Entitlements: ent}

	accept := func(user string) *httptest.ResponseRecorder {
		r := orgReq("POST", "/api/v1/invitations/inv_1/accept", "", user, "new@x.com")
		r.SetPathValue("id", "inv_1")
		w := httptest.NewRecorder()
		s.acceptInvitation(w, r)
		return w
	}

	w := accept("u_new")
	if w.Code != http.StatusForbidden {
		t.Fatalf("limit doluyken kabul 403 olmali: %d", w.Code)
	}
	// Entitlement hatalari {error:{code,message,...}} bicimindedir (writeJSONError ile ayni).
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "member_limit_reached" {
		t.Fatalf("kod member_limit_reached olmali: %v", body)
	}
	if len(st.accepted) != 0 {
		t.Fatal("limit doluyken uyelik olusmamali")
	}

	// Limit musaitse kabul gecer.
	ent.acceptErr = nil
	if w := accept("u_new"); w.Code != http.StatusOK {
		t.Fatalf("kabul 200 olmali: %d %s", w.Code, w.Body.String())
	}
}

func TestUnknownTenantHeader(t *testing.T) {
	m := &Middleware{Store: newOrgAuthzStore()}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/tunnels", nil)
	if !m.unknownTenant(w, r, "ten_yok") || w.Code != http.StatusNotFound {
		t.Fatalf("olmayan kiraci 404 olmali: %d", w.Code)
	}
	w = httptest.NewRecorder()
	if m.unknownTenant(w, r, "ten_a") {
		t.Fatal("var olan kiraci gecmeli")
	}
	if m.unknownTenant(w, r, store.DefaultTenantID) {
		t.Fatal("varsayilan kiraci her zaman gecmeli")
	}
}
