package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// legacySessStore, imzali cerez dogrulamasinin ihtiyac duydugu store alt kumesi.
type legacySessStore struct {
	store.Store
	roles  map[string]string // tenant+":"+user -> rol
	epochs map[string]int64
}

func (f *legacySessStore) GetLegacyMemberRole(_ context.Context, t, u string) (string, error) {
	return f.roles[t+":"+u], nil
}
func (f *legacySessStore) GetSessionEpoch(_ context.Context, s string) (int64, error) {
	return f.epochs[s], nil
}
func (f *legacySessStore) BumpSessionEpoch(_ context.Context, s string) error {
	f.epochs[s]++
	return nil
}
func (f *legacySessStore) GetProjectBySlug(context.Context, string, string) (store.Project, error) {
	return store.Project{}, store.ErrNotFound
}
func (f *legacySessStore) GetDefaultProject(context.Context, string) (store.Project, error) {
	return store.Project{}, store.ErrNotFound
}

func newLegacyFake() *legacySessStore {
	return &legacySessStore{roles: map[string]string{}, epochs: map[string]int64{}}
}

var legacyTestKey = []byte("0123456789abcdef0123456789abcdef")

// legacyCall, cerezle bir istek yapar; kod ve baglamdaki kullaniciyi doner.
func legacyCall(t *testing.T, st store.Store, sm *session.Manager, s session.Session) (int, *AuthUser) {
	t.Helper()
	val, err := sm.Issue(s, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return legacyCallVal(st, sm, val)
}

func legacyCallVal(st store.Store, sm *session.Manager, val string) (int, *AuthUser) {
	var got *AuthUser
	m := &Middleware{Sessions: sm, Store: st, Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = userFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels", nil)
	r.AddCookie(&http.Cookie{Name: session.CookieName, Value: val})
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return w.Code, got
}

// method=github cerezi gercek rolu tasimali: member owner islemi yapamaz.
func TestLegacyGithubCookieCarriesRealRole(t *testing.T) {
	sm := session.NewManager(legacyTestKey)
	st := newLegacyFake()
	st.roles["ten_x:usr_m"] = store.RoleMember
	st.roles["ten_x:usr_o"] = store.RoleOwner

	code, u := legacyCall(t, st, sm, session.Session{UserID: "usr_m", TenantID: "ten_x", Login: "m", Method: "github"})
	if code != 200 || u == nil || u.Role != store.RoleMember || u.TenantID != "ten_x" {
		t.Fatalf("member rolu beklenir: kod=%d user=%+v", code, u)
	}
	// member rolu owner/admin kapisindan gecmemeli (403).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/team/members/invite", nil)
	req = req.WithContext(withUser(req.Context(), u))
	if (&Server{}).requirePrivileged(rec, req, "ten_x") || rec.Code != http.StatusForbidden {
		t.Fatalf("member owner/admin kapisindan gecmemeli: %d", rec.Code)
	}
	code, u = legacyCall(t, st, sm, session.Session{UserID: "usr_o", TenantID: "ten_x", Login: "o", Method: "github"})
	if code != 200 || u == nil || u.Role != store.RoleOwner {
		t.Fatalf("owner rolu beklenir: kod=%d user=%+v", code, u)
	}
}

// Silinmis kullanici (uyelik yok) -> 401.
func TestLegacyDeletedUserRejected(t *testing.T) {
	sm := session.NewManager(legacyTestKey)
	st := newLegacyFake()
	code, _ := legacyCall(t, st, sm, session.Session{UserID: "usr_gone", TenantID: "ten_x", Login: "g", Method: "github"})
	if code != http.StatusUnauthorized {
		t.Fatalf("silinmis kullanici 401 almali, %d", code)
	}
}

// Logout sonrasi eski cerez 401; yeni giris (yeni epoch) gecerli.
func TestLegacyLogoutRevokes(t *testing.T) {
	sm := session.NewManager(legacyTestKey)
	st := newLegacyFake()
	st.roles["ten_x:usr_m"] = store.RoleMember
	s := session.Session{UserID: "usr_m", TenantID: "ten_x", Login: "m", Method: "github"}
	val, _ := sm.Issue(s, time.Hour)
	if code, _ := legacyCallVal(st, sm, val); code != 200 {
		t.Fatalf("logout oncesi 200 beklenir, %d", code)
	}
	srv := &Server{Store: st, Sessions: sm}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: session.CookieName, Value: val})
	srv.logout(httptest.NewRecorder(), r)
	if code, _ := legacyCallVal(st, sm, val); code != http.StatusUnauthorized {
		t.Fatalf("logout sonrasi eski cerez 401 almali, %d", code)
	}
	s.Epoch = st.epochs[sessionSubject("ten_x", "usr_m")]
	if code, _ := legacyCall(t, st, sm, s); code != 200 {
		t.Fatalf("yeni epoch'lu cerez gecerli olmali, %d", code)
	}
}
