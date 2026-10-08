package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const doorHost = "door--ssh--acme.example.com"

type memDoorStore struct {
	mu     sync.Mutex
	grants []store.DoorGrant
	revs   map[string]time.Time
}

func (m *memDoorStore) RecordDoorRevocation(_ context.Context, tid, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revs == nil {
		m.revs = map[string]time.Time{}
	}
	m.revs[tid+"|"+id] = at
	return nil
}
func (m *memDoorStore) ListDoorRevocations(_ context.Context, tid string) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range m.revs {
		if strings.HasPrefix(k, tid+"|") {
			out[strings.TrimPrefix(k, tid+"|")] = v
		}
	}
	return out, nil
}
func (m *memDoorStore) CloseDoorsForTenant(_ context.Context, tenant string, at time.Time) ([]string, []store.DoorGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rv []store.DoorGrant
	for i := range m.grants {
		if m.grants[i].RevokedAt == nil {
			t := at
			m.grants[i].RevokedAt = &t
			rv = append(rv, m.grants[i])
		}
	}
	return []string{"tun_1"}, rv, nil
}

func (m *memDoorStore) ListEnabledDoors(context.Context) ([]store.TunnelDoor, error) { return nil, nil }
func (m *memDoorStore) ListActiveDoorGrantsByTunnel(_ context.Context, tid string) ([]store.DoorGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.DoorGrant
	for _, g := range m.grants {
		if g.TunnelID == tid && g.RevokedAt == nil && g.ExpiresAt.After(time.Now()) {
			out = append(out, g)
		}
	}
	return out, nil
}
func (m *memDoorStore) OpenDoorGrant(_ context.Context, g store.DoorGrant) (store.DoorGrant, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.grants {
		if c.TunnelID == g.TunnelID && c.IP == g.IP && c.RevokedAt == nil && c.ExpiresAt.After(time.Now()) {
			return c, false, nil
		}
	}
	g.ID = "dg_test"
	m.grants = append(m.grants, g)
	return g, true, nil
}
func (m *memDoorStore) RevokeDoorGrant(_ context.Context, _, tid, id string) (store.DoorGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.grants {
		if m.grants[i].ID == id && m.grants[i].TunnelID == tid && m.grants[i].RevokedAt == nil {
			now := time.Now()
			m.grants[i].RevokedAt = &now
			return m.grants[i], nil
		}
	}
	return store.DoorGrant{}, store.ErrNotFound
}
func (m *memDoorStore) RevokeDoorGrantsByIP(_ context.Context, _, tid, ip string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for i := range m.grants {
		if m.grants[i].TunnelID == tid && m.grants[i].IP == ip && m.grants[i].RevokedAt == nil {
			now := time.Now()
			m.grants[i].RevokedAt = &now
			n++
		}
	}
	return n, nil
}

func newDoorHandler(t *testing.T, mode, cfg string, accessEnabled bool) (*Handler, *memDoorStore) {
	t.Helper()
	rt := store.HostRoute{
		FQDN: doorHost, TenantID: "ten_1", TunnelID: "tun_1", ClientID: "cli", Enabled: true,
		Proto: "tcp", Exposure: "port",
		AccessEnabled: accessEnabled, AccessMode: mode, AccessConfig: []byte(cfg),
		Door: &store.DoorRoute{DurationSec: 3600, PublicPort: 10007, Proto: "tcp", Exposure: "port", TunnelHost: "ssh--acme.example.com"},
	}
	router := NewRouter(&fakeStore{doors: []store.HostRoute{rt}})
	if err := router.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ms := &memDoorStore{}
	return &Handler{Router: router, Door: door.New(ms, nil, nil), PlatformDomain: "example.com",
		BasicSecret: []byte("basic-secret-0123456789"), BasicLimiter: ratelimit.New(10.0/600, 10),
		Log: quietLog()}, ms
}

func doorReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, "https://"+doorHost+target, nil)
	r.Header.Set("Accept", "text/html")
	r.RemoteAddr = "203.0.113.7:4444"
	return r
}

func TestDoorBasicLoginOpensGrant(t *testing.T) {
	hash, _ := auth.HashSecret("s3cret")
	h, ms := newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)

	// Giris yokken kapi acilmaz, form gorunur.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, doorReq("GET", "/"))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "/_zvb/login") {
		t.Fatalf("giris formu bekleniyordu: %d", w.Code)
	}
	if len(ms.grants) != 0 {
		t.Fatal("girissiz grant olusmamali")
	}

	// Yanlis parola: grant yok.
	form := url.Values{"user": {"alice"}, "pass": {"wrong"}, "rd": {"/"}}
	lr := httptest.NewRequest("POST", "https://"+doorHost+"/_zvb/login", strings.NewReader(form.Encode()))
	lr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lr.RemoteAddr = "203.0.113.7:4444"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, lr)
	if w.Code != http.StatusUnauthorized || len(ms.grants) != 0 {
		t.Fatalf("yanlis parola reddedilmeli: %d grants=%d", w.Code, len(ms.grants))
	}

	// Dogru parola -> cerez -> GET / grant acar ve kapi acildi sayfasini gosterir.
	form.Set("pass", "s3cret")
	lr = httptest.NewRequest("POST", "https://"+doorHost+"/_zvb/login", strings.NewReader(form.Encode()))
	lr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lr.RemoteAddr = "203.0.113.7:4444"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, lr)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("giris sonrasi yonlendirme bekleniyordu: %d", w.Code)
	}
	cookie := cookieNamed(w, basicCookie)
	if cookie == nil {
		t.Fatal("oturum cerezi yok")
	}
	r := doorReq("GET", "/")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "203.0.113.7") ||
		!strings.Contains(body, "example.com:10007") || !strings.Contains(body, "/_zvd/close") {
		t.Fatalf("kapi acildi sayfasi eksik (%d): %s", w.Code, body)
	}
	if len(ms.grants) != 1 || ms.grants[0].IP != "203.0.113.7" || ms.grants[0].Identity != "alice" ||
		ms.grants[0].Method != "basic" || time.Until(ms.grants[0].ExpiresAt) < 59*time.Minute {
		t.Fatalf("grant beklenmedik: %+v", ms.grants)
	}
	// Yeniden yukleme grant'i cogaltmaz.
	r = doorReq("GET", "/")
	r.AddCookie(cookie)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if len(ms.grants) != 1 {
		t.Fatal("ikinci ziyaret yeni grant acmamali")
	}

	// Kapat: grant iptal, cerez silinir.
	cr := httptest.NewRequest("POST", "https://"+doorHost+doorClosePath, nil)
	cr.Header.Set("Origin", "https://"+doorHost)
	cr.AddCookie(cookie)
	cr.RemoteAddr = "203.0.113.7:4444"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, cr)
	if w.Code != http.StatusOK || ms.grants[0].RevokedAt == nil {
		t.Fatalf("kapatma grant'i iptal etmeli: %d", w.Code)
	}
	if c := cookieNamed(w, basicCookie); c == nil || c.MaxAge >= 0 {
		t.Fatal("oturum cerezi silinmeli")
	}

	// Baska siteden kapatma istegi reddedilir.
	cr = httptest.NewRequest("POST", "https://"+doorHost+doorClosePath, nil)
	cr.Header.Set("Origin", "https://evil.example.org")
	cr.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, cr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin kapatma 403 olmali: %d", w.Code)
	}
}

func TestDoorUnconfiguredAccessFailsClosed(t *testing.T) {
	h, ms := newDoorHandler(t, "none", `{}`, false)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, doorReq("GET", "/"))
	if w.Code != http.StatusForbidden || len(ms.grants) != 0 {
		t.Fatalf("giris yontemi yokken kapi acilmamali: %d", w.Code)
	}
}

func TestDoorPlanGateBlocksOpen(t *testing.T) {
	hash, _ := auth.HashSecret("pw")
	h, ms := newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)
	h.Door.Gate = func(context.Context, string) error { return context.Canceled }
	r := doorReq("GET", "/")
	r.SetBasicAuth("alice", "pw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(ms.grants) != 0 {
		t.Fatalf("plan kapisi grant'i engellemeli: %d", w.Code)
	}
}
