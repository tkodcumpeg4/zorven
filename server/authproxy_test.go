package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeCache, sessionCache'in kayit tutan sahtesi.
type fakeCache struct {
	mu     sync.Mutex
	events []string
	users  map[string]string
}

func (f *fakeCache) add(e string) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}
func (f *fakeCache) UserIDForToken(_ context.Context, tok string) string { return f.users[tok] }
func (f *fakeCache) InvalidateUser(u string)                             { f.add("user:" + u) }
func (f *fakeCache) InvalidateSession(t string)                          { f.add("tok:" + t) }
func (f *fakeCache) InvalidateAll()                                      { f.add("all") }
func (f *fakeCache) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}
func (f *fakeCache) reset() {
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
}

func newTestAuthProxy(t *testing.T, cache sessionCache, onUpstream func(r *http.Request)) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onUpstream != nil {
			onUpstream(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	front := httptest.NewServer(newAuthProxy(u, cache))
	t.Cleanup(front.Close)
	return front
}

// Istemcinin gonderdigi X-Forwarded-For Better Auth'a ULASMAMALI; yerine tek
// degerli gercek istemci IP'si gitmeli.
func TestAuthProxyRewritesXForwardedFor(t *testing.T) {
	var gotXFF, gotFwdHost string
	front := newTestAuthProxy(t, nil, func(r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotFwdHost = r.Header.Get("X-Forwarded-Host")
	})
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/api/auth/sign-in/email", strings.NewReader(`{}`))
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotXFF != "127.0.0.1" {
		t.Fatalf("XFF tek degerli gercek IP olmali, gelen %q", gotXFF)
	}
	if gotFwdHost != "" {
		t.Fatalf("istemci X-Forwarded-Host iletilmemeli, gelen %q", gotFwdHost)
	}
}

// Hesap degistiren isteklerde onbellek istekten ONCE (upstream gormeden) ve
// yanittan sonra temizlenmeli; okuma istekleri onbellege dokunmamali.
func TestAuthProxyInvalidatesSessionCache(t *testing.T) {
	cache := &fakeCache{users: map[string]string{"tokA": "usrA"}}
	var atUpstream []string
	front := newTestAuthProxy(t, cache, func(r *http.Request) {
		atUpstream = cache.snapshot()
	})
	do := func(method, path string, withCookie bool) []string {
		cache.reset()
		atUpstream = nil
		req, _ := http.NewRequest(method, front.URL+path, nil)
		if withCookie {
			req.AddCookie(&http.Cookie{Name: "__Secure-better-auth.session_token", Value: "tokA.sig"})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return cache.snapshot()
	}

	for _, p := range []string{"/api/auth/two-factor/enable", "/api/auth/two-factor/disable",
		"/api/auth/change-password", "/api/auth/change-email", "/api/auth/update-user",
		"/api/auth/revoke-sessions", "/api/auth/sign-out", "/api/auth/organization/set-active"} {
		ev := do(http.MethodPost, p, true)
		if len(atUpstream) < 2 || atUpstream[0] != "user:usrA" || atUpstream[1] != "tok:tokA" {
			t.Fatalf("%s: upstream'den once temizlenmeli, gelen %v", p, atUpstream)
		}
		if len(ev) != 4 {
			t.Fatalf("%s: once+sonra temizlenmeli, gelen %v", p, ev)
		}
	}
	if ev := do(http.MethodGet, "/api/auth/get-session", true); len(ev) != 0 {
		t.Fatalf("get-session onbellege dokunmamali: %v", ev)
	}
	if ev := do(http.MethodPost, "/api/auth/sign-in/email", false); len(ev) != 0 {
		t.Fatalf("oturumsuz giris onbellege dokunmamali: %v", ev)
	}
	if ev := do(http.MethodGet, "/api/auth/verify-email?token=x", true); len(ev) != 5 || ev[len(ev)-1] != "all" {
		t.Fatalf("e-posta onayi kullaniciyi temizlemeli ve tumden bosaltmali: %v", ev)
	}
	if ev := do(http.MethodGet, "/api/auth/verify-email?token=x", false); len(ev) != 1 || ev[0] != "all" {
		t.Fatalf("cerezsiz e-posta onayi onbellegi tumden temizlemeli: %v", ev)
	}
	ev := do(http.MethodPost, "/api/auth/reset-password", false)
	if len(ev) != 1 || ev[0] != "all" {
		t.Fatalf("sifre sifirlama onbellegi tumden temizlemeli: %v", ev)
	}
}
