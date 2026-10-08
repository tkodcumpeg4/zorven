package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// appPwStore, uygulama parolasi uclarinin store bagimliliklarini taklit eder.
type appPwStore struct {
	store.Store
	role string
	pws  []store.MailAppPassword
}

func (m *appPwStore) GetTenant(_ context.Context, id string) (store.Tenant, error) {
	return store.Tenant{ID: id, Slug: "acme"}, nil
}
func (m *appPwStore) GetMemberRole(_ context.Context, _, _ string) (string, error) {
	return m.role, nil
}
func (m *appPwStore) CreateMailAppPassword(_ context.Context, p store.MailAppPassword) (store.MailAppPassword, error) {
	p.ID = "mapw_1"
	m.pws = append(m.pws, p)
	return p, nil
}
func (m *appPwStore) ListMailAppPasswords(_ context.Context, tenantID, mailbox string) ([]store.MailAppPassword, error) {
	var out []store.MailAppPassword
	for _, p := range m.pws {
		if p.TenantID == tenantID && (mailbox == "" || p.Mailbox == mailbox) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (m *appPwStore) RevokeMailAppPassword(_ context.Context, tenantID, id string) error {
	for i := range m.pws {
		if m.pws[i].ID == id && m.pws[i].TenantID == tenantID {
			m.pws = append(m.pws[:i], m.pws[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func newAppPwServer(st *appPwStore) *Server {
	return &Server{
		Store:           st,
		MailDomain:      "mail.zorven.app",
		SystemMailboxes: []string{"info@zorven.app"},
		MailClient: &mail.ClientConfig{MailDomain: "mail.zorven.app", PlatformDomain: "zorven.app",
			Host: "mail.zorven.app", IMAPPort: 993, SubmissionPort: 587, SubmissionsTLS: 465},
	}
}

func doAppPw(s *Server, method, path, body string, ctx func(context.Context) context.Context, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req = req.WithContext(ctx(req.Context()))
	if strings.Contains(path, "/app-passwords/") {
		req.SetPathValue("id", path[strings.LastIndex(path, "/")+1:])
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestAppPasswords_CreateListRevoke(t *testing.T) {
	st := &appPwStore{}
	s := newAppPwServer(st)
	tenantCtx := func(c context.Context) context.Context { return withTenant(c, "ten_acme") }

	rec := doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{"label":"Telefon"}`, tenantCtx, s.createMailAppPassword)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Mailbox  string `json:"mailbox"`
		Password string `json:"password"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if !regexp.MustCompile(`^[a-z2-9]{4}(-[a-z2-9]{4}){3}$`).MatchString(created.Password) || created.Mailbox != "acme@mail.zorven.app" {
		t.Fatalf("yanit: %+v", created)
	}
	// Saklanan sey argon2id hash'idir, parola degil; dogrulanabilir.
	if len(st.pws) != 1 || strings.Contains(st.pws[0].PasswordHash, created.Password) {
		t.Fatalf("kayit: %+v", st.pws)
	}
	if ok, err := auth.Verify(mail.NormalizeAppPassword(created.Password), st.pws[0].PasswordHash); err != nil || !ok {
		t.Fatalf("hash dogrulanamadi: %v %v", ok, err)
	}

	// Liste parolayi ve hash'i ASLA donmez.
	rec = doAppPw(s, "GET", "/api/v1/mail/app-passwords", "", tenantCtx, s.listMailAppPasswords)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), created.Password) || strings.Contains(rec.Body.String(), "argon2") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"label":"Telefon"`) {
		t.Fatalf("liste etiketi yok: %s", rec.Body.String())
	}

	rec = doAppPw(s, "DELETE", "/api/v1/mail/app-passwords/"+created.ID, "", tenantCtx, s.revokeMailAppPassword)
	if rec.Code != 200 || len(st.pws) != 0 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	rec = doAppPw(s, "DELETE", "/api/v1/mail/app-passwords/yok", "", tenantCtx, s.revokeMailAppPassword)
	if rec.Code == 200 {
		t.Fatal("olmayan parola iptali basarili donmemeli")
	}
}

func TestAppPasswords_RolesAndSystemBoxes(t *testing.T) {
	st := &appPwStore{role: "member"}
	s := newAppPwServer(st)
	memberCtx := func(c context.Context) context.Context {
		return withUser(withTenant(c, "ten_acme"), &AuthUser{ID: "u1", Role: "member"})
	}
	// Uye (owner/admin degil) parola olusturamaz.
	rec := doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{"label":"x"}`, memberCtx, s.createMailAppPassword)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("uye icin 403 bekleniyordu: %d %s", rec.Code, rec.Body.String())
	}
	// Sistem kutusu: platform admin disinda kimse (owner dahil) olusturamaz.
	st.role = "owner"
	ownerCtx := func(c context.Context) context.Context {
		return withUser(withTenant(c, "ten_acme"), &AuthUser{ID: "u2", Role: "owner"})
	}
	rec = doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{"label":"x","mailbox":"info@zorven.app"}`, ownerCtx, s.createMailAppPassword)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sistem kutusu 403 olmali: %d %s", rec.Code, rec.Body.String())
	}
	// Baska kiracinin / rastgele bir adres.
	rec = doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{"label":"x","mailbox":"baska@mail.zorven.app"}`, ownerCtx, s.createMailAppPassword)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("baska kutu 403 olmali: %d", rec.Code)
	}
	// Platform admin sistem kutusu icin olusturabilir; parola ten_default'a baglanir.
	adminCtx := func(c context.Context) context.Context {
		return withPlatformAdmin(withTenant(c, store.DefaultTenantID))
	}
	rec = doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{"label":"owner","mailbox":"info@zorven.app"}`, adminCtx, s.createMailAppPassword)
	if rec.Code != http.StatusCreated || len(st.pws) != 1 || st.pws[0].TenantID != store.DefaultTenantID || st.pws[0].Mailbox != "info@zorven.app" {
		t.Fatalf("platform admin sistem kutusu: %d %s %+v", rec.Code, rec.Body.String(), st.pws)
	}
}

func TestAppPasswords_DisabledWithoutClientConfig(t *testing.T) {
	s := newAppPwServer(&appPwStore{})
	s.MailClient = nil
	rec := doAppPw(s, "POST", "/api/v1/mail/app-passwords", `{}`, func(c context.Context) context.Context { return withTenant(c, "ten_acme") }, s.createMailAppPassword)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("kapali ozellik 503 olmali: %d", rec.Code)
	}
	if info := s.mailClientInfo(); info["enabled"] != false {
		t.Fatalf("info: %v", info)
	}
}

func TestMailMobileConfigEndpoint(t *testing.T) {
	s := newAppPwServer(&appPwStore{})
	rec := doAppPw(s, "GET", "/api/v1/mail/mobileconfig", "", func(c context.Context) context.Context { return withTenant(c, "ten_acme") }, s.mailMobileConfig)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "apple-aseprofile") ||
		!strings.Contains(rec.Body.String(), "acme@mail.zorven.app") {
		t.Fatalf("mobileconfig: %d %s %s", rec.Code, rec.Header(), rec.Body.String())
	}
}
