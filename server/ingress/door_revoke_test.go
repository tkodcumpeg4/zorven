package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// activeGrants, iptal edilmemis grant sayisini doner.
func activeGrants(ms *memDoorStore) int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	n := 0
	for _, g := range ms.grants {
		if g.RevokedAt == nil {
			n++
		}
	}
	return n
}

func basicLogin(t *testing.T, h *Handler, user, pass string) *http.Cookie {
	t.Helper()
	form := url.Values{"user": {user}, "pass": {pass}, "rd": {"/"}}
	lr := httptest.NewRequest("POST", "https://"+doorHost+"/_zvb/login", strings.NewReader(form.Encode()))
	lr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lr.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, lr)
	c := cookieNamed(w, basicCookie)
	if w.Code != http.StatusSeeOther || c == nil {
		t.Fatalf("giris basarisiz: %d", w.Code)
	}
	return c
}

func doorGetWith(h *Handler, c *http.Cookie) *httptest.ResponseRecorder {
	r := doorReq("GET", "/")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func setup(t *testing.T) (*Handler, *memDoorStore) {
	hash, _ := auth.HashSecret("s3cret")
	return newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)
}

func TestDoorRevokeKillsBasicSession(t *testing.T) {
	h, ms := setup(t)
	cookie := basicLogin(t, h, "alice", "s3cret")
	if w := doorGetWith(h, cookie); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("giris sonrasi kapi acilmali: %d", w.Code)
	}

	// Sahip grant'i iptal eder.
	if _, err := h.Door.Revoke(context.Background(), "ten_1", "tun_1", "dg_test"); err != nil {
		t.Fatal(err)
	}
	// Eski cerez kapiyi YENIDEN ACMAZ: giris formu + cerez silinir.
	w := doorGetWith(h, cookie)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "/_zvb/login") {
		t.Fatalf("iptal sonrasi giris formu bekleniyordu: %d", w.Code)
	}
	if c := cookieNamed(w, basicCookie); c == nil || c.MaxAge >= 0 {
		t.Fatal("iptal edilen oturum cerezi silinmeli")
	}
	if activeGrants(ms) != 0 {
		t.Fatal("eski oturum yeni grant acmamali")
	}

	// Taze giris normal calisir ve yeni grant olusturur.
	time.Sleep(5 * time.Millisecond)
	fresh := basicLogin(t, h, "alice", "s3cret")
	if w := doorGetWith(h, fresh); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("taze giris kapiyi acmali: %d grants=%d", w.Code, activeGrants(ms))
	}
	// Eski cerez hala olu.
	if w := doorGetWith(h, cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("eski cerez hala gecersiz olmali: %d", w.Code)
	}
}

func TestDoorVisitorCloseKillsSessionCopies(t *testing.T) {
	h, ms := setup(t)
	cookie := basicLogin(t, h, "alice", "s3cret")
	doorGetWith(h, cookie)

	cr := httptest.NewRequest("POST", "https://"+doorHost+doorClosePath, nil)
	cr.Header.Set("Origin", "https://"+doorHost)
	cr.AddCookie(cookie)
	cr.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, cr)
	if w.Code != http.StatusOK || activeGrants(ms) != 0 {
		t.Fatalf("kapatma grant'i iptal etmeli: %d", w.Code)
	}
	// Cerez baska yere kopyalanmis olsa bile (silinmemis sayilir) kapiyi acamaz.
	if w := doorGetWith(h, cookie); w.Code != http.StatusUnauthorized || activeGrants(ms) != 0 {
		t.Fatalf("kapatan kisinin eski cerezi kapiyi acmamali: %d", w.Code)
	}
	time.Sleep(5 * time.Millisecond)
	fresh := basicLogin(t, h, "alice", "s3cret")
	if w := doorGetWith(h, fresh); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("yeniden giris kapiyi acmali: %d", w.Code)
	}
}

func TestDoorRevokeLegacyBasicCookieWithoutIat(t *testing.T) {
	hash, _ := auth.HashSecret("s3cret")
	cfgJSON := `{"username":"alice","password_hash":"` + hash + `"}`
	h, ms := newDoorHandler(t, "basic", cfgJSON, true)
	cfg := parseBasicConfig([]byte(cfgJSON))
	// iat'siz (eski bicim) cerez: verilis = exp - basicTTL.
	issued := time.Now().Add(-time.Hour)
	legacy := h.signBasic(basicClaims{Tunnel: "tun_1", Host: doorHost, FP: passFingerprint(cfg), Exp: issued.Add(basicTTL).Unix()})
	c := &http.Cookie{Name: basicCookie, Value: legacy}
	if w := doorGetWith(h, c); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("eski bicim cerez iptal yokken calismali: %d", w.Code)
	}
	if _, err := h.Door.Revoke(context.Background(), "ten_1", "tun_1", "dg_test"); err != nil {
		t.Fatal(err)
	}
	if w := doorGetWith(h, c); w.Code != http.StatusUnauthorized || activeGrants(ms) != 0 {
		t.Fatalf("eski bicim cerez iptalden sonra gecersiz olmali: %d", w.Code)
	}
}

func TestDoorRevokeKillsOAuthSession(t *testing.T) {
	h, ms := newDoorHandler(t, "oauth", `{}`, true)
	vis := newVisitor("google")
	h.Visitor = vis

	cookie := &http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValueAt(doorHost, "Bob@Example.com", time.Now().Add(-time.Minute))}
	if w := doorGetWith(h, cookie); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("oauth oturumu kapiyi acmali: %d", w.Code)
	}
	if _, err := h.Door.Revoke(context.Background(), "ten_1", "tun_1", "dg_test"); err != nil {
		t.Fatal(err)
	}
	w := doorGetWith(h, cookie)
	if activeGrants(ms) != 0 || w.Code == http.StatusOK {
		t.Fatalf("iptalden sonra eski oauth oturumu kapiyi acmamali: %d", w.Code)
	}
	if c := cookieNamed(w, "_zva_session"); c == nil || c.MaxAge >= 0 {
		t.Fatal("iptal edilen oauth cerezi silinmeli")
	}
	// Taze giris (verilis iptalden sonra) calisir.
	time.Sleep(5 * time.Millisecond)
	fresh := &http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValue(doorHost, "bob@example.com")}
	if w := doorGetWith(h, fresh); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("taze oauth girisi kapiyi acmali: %d", w.Code)
	}
}

func TestDoorPlanPage(t *testing.T) {
	h, ms := setup(t)
	h.Door.Gate = func(context.Context, string) error { return door.ErrNotAvailable }
	r := doorReq("GET", "/")
	r.SetBasicAuth("alice", "s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "planı web ile kapı açmayı içermiyor") {
		t.Fatalf("plan sayfasi bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	if activeGrants(ms) != 0 {
		t.Fatal("plan yokken grant olusmamali")
	}
}

func TestDoorDisabledRoute(t *testing.T) {
	hash, _ := auth.HashSecret("s3cret")
	rt := store.HostRoute{
		FQDN: doorHost, TenantID: "ten_1", TunnelID: "tun_1", ClientID: "cli", Enabled: true,
		Proto: "tcp", Exposure: "port", AccessEnabled: true, AccessMode: "basic",
		AccessConfig: []byte(`{"username":"alice","password_hash":"` + hash + `"}`),
		Door:         &store.DoorRoute{DurationSec: 3600, PublicPort: 10007, Proto: "tcp", Exposure: "port", Disabled: true},
	}
	router := NewRouter(&fakeStore{doors: []store.HostRoute{rt}})
	if err := router.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ms := &memDoorStore{}
	h := &Handler{Router: router, Door: door.New(ms, nil, nil), BasicSecret: []byte("basic-secret-0123456789"), Log: quietLog()}

	// Plan yeterli ama kapi kalici kapali: kapi yok (404), giris/grant yok.
	r := doorReq("GET", "/")
	r.SetBasicAuth("alice", "s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || activeGrants(ms) != 0 {
		t.Fatalf("kapali kapi 404 olmali: %d", w.Code)
	}
	// Plan yetersiz: bilgilendirme sayfasi.
	h.Door.Gate = func(context.Context, string) error { return door.ErrNotAvailable }
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "planı web ile kapı açmayı içermiyor") {
		t.Fatalf("plan sayfasi bekleniyordu: %d", w.Code)
	}
}
