package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// memAddrStore, memDoorStore'a door.AddrStore yuzeyini ekler (sayfa testi icin kuru).
type memAddrStore struct{ *memDoorStore }

func (memAddrStore) AddDoorGrantAddr(context.Context, string, string) (store.DoorGrant, bool, error) {
	return store.DoorGrant{}, false, store.ErrNotFound
}
func (memAddrStore) DoorGrantAddrsByIP(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

// loginCookie, alice/s3cret ile Basic giris yapip oturum cerezini doner.
func loginCookie(t *testing.T, h *Handler) *http.Cookie {
	t.Helper()
	form := url.Values{"user": {"alice"}, "pass": {"s3cret"}, "rd": {"/"}}
	lr := httptest.NewRequest("POST", "https://"+doorHost+"/_zvb/login", strings.NewReader(form.Encode()))
	lr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lr.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, lr)
	c := cookieNamed(w, basicCookie)
	if c == nil {
		t.Fatal("oturum cerezi yok")
	}
	return c
}

func TestDoorPageAddsOtherFamilyProbe(t *testing.T) {
	hash, _ := auth.HashSecret("s3cret")
	h, ms := newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)
	svc := door.New(memAddrStore{ms}, nil, nil)
	svc.TokenKey = []byte("0123456789abcdef0123456789abcdef")
	svc.V4Host, svc.V6Host = "door4.example.com", "door6.example.com"
	h.Door = svc

	cookie := loginCookie(t, h)
	r := doorReq("GET", "/")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != http.StatusOK || !strings.Contains(body, "https://door6.example.com/_door/v1/addr?t=") ||
		!strings.Contains(body, `id="zv-other"`) || !strings.Contains(body, "IPv4 (izinli)") ||
		!strings.Contains(body, "IPv6 (izinli)") {
		t.Fatalf("yan kanal eksik (%d): %s", w.Code, body)
	}
	if !strings.Contains(csp, "connect-src https://door6.example.com") || !strings.Contains(csp, "script-src 'nonce-") {
		t.Fatalf("CSP yan kanala izin vermiyor: %s", csp)
	}
	// Yan kanal kapaliyken (anahtar yok) betik/baglanti izni olmaz.
	svc.TokenKey = nil
	r = doorReq("GET", "/")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src") {
		t.Fatal("yan kanal kapaliyken betik/connect-src olmamali")
	}
}
