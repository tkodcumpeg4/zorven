package visitorauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testManager() *Manager {
	return New([]byte("test-secret-0123456789abcdef"), "app.example.com",
		map[string]*Provider{
			"google": {Name: "google", ClientID: "gid", ClientSecret: "gsec",
				AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "x", UserInfoURL: "y", Scopes: "openid email"},
		}, func(string) (string, []byte, bool, bool) { return "oauth", []byte("{}"), true, true }, nil)
}

func TestEnabled(t *testing.T) {
	if !testManager().Enabled() {
		t.Fatal("creds'li manager Enabled olmalı")
	}
	// creds yok → devre dışı
	m := New([]byte("s"), "h", map[string]*Provider{"google": {Name: "google"}}, nil, nil)
	if m.Enabled() {
		t.Fatal("creds'siz manager devre dışı olmalı")
	}
	// secret yok → devre dışı
	m2 := New(nil, "h", map[string]*Provider{"google": {Name: "google", ClientID: "a", ClientSecret: "b"}}, nil, nil)
	if m2.Enabled() {
		t.Fatal("secret'siz manager devre dışı olmalı")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	m := testManager()
	tok := m.signToken(purposeSession, sessionClaims{Email: "a@b.com", Host: "api.example.com", Exp: time.Now().Add(time.Hour).Unix()})

	r := httptest.NewRequest("GET", "https://api.example.com/x", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	email, ok := m.SessionEmail(r)
	if !ok || email != "a@b.com" {
		t.Fatalf("geçerli oturum kabul edilmeli, got=%q ok=%v", email, ok)
	}
}

func TestSessionRejectsTampered(t *testing.T) {
	m := testManager()
	tok := m.signToken(purposeSession, sessionClaims{Email: "a@b.com", Host: "api.example.com", Exp: time.Now().Add(time.Hour).Unix()})
	// imzayı boz
	bad := tok[:len(tok)-2] + "xy"
	r := httptest.NewRequest("GET", "https://api.example.com/x", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: bad})
	if _, ok := m.SessionEmail(r); ok {
		t.Fatal("kurcalanmış imza reddedilmeli")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	m := testManager()
	tok := m.signToken(purposeSession, sessionClaims{Email: "a@b.com", Host: "api.example.com", Exp: time.Now().Add(-time.Minute).Unix()})
	r := httptest.NewRequest("GET", "https://api.example.com/x", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if _, ok := m.SessionEmail(r); ok {
		t.Fatal("süresi dolmuş oturum reddedilmeli")
	}
}

func TestSessionRejectsWrongHost(t *testing.T) {
	m := testManager()
	tok := m.signToken(purposeSession, sessionClaims{Email: "a@b.com", Host: "baska.example.com", Exp: time.Now().Add(time.Hour).Unix()})
	r := httptest.NewRequest("GET", "https://api.example.com/x", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if _, ok := m.SessionEmail(r); ok {
		t.Fatal("başka host için imzalanmış oturum bu host'ta reddedilmeli")
	}
}

func TestEmailAllowed(t *testing.T) {
	if !EmailAllowed("a@b.com", nil) {
		t.Fatal("boş liste = herkes")
	}
	if !EmailAllowed("A@B.com", []string{"a@b.com"}) {
		t.Fatal("büyük/küçük harf duyarsız eşleşmeli")
	}
	if EmailAllowed("x@y.com", []string{"a@b.com"}) {
		t.Fatal("listede olmayan reddedilmeli")
	}
}

func TestSafeRD(t *testing.T) {
	cases := map[string]string{
		"/dashboard":       "/dashboard",
		"":                 "/",
		"https://evil.com": "/",
		"//evil.com":       "/",
		"/a?b=c":           "/a?b=c",
	}
	for in, want := range cases {
		if got := safeRD(in); got != want {
			t.Errorf("safeRD(%q)=%q, beklenen %q", in, got, want)
		}
	}
}

func TestStartRedirectsToProvider(t *testing.T) {
	m := testManager()
	r := httptest.NewRequest("GET", "https://api.example.com/_zva/start?p=google&rd=/gizli", nil)
	w := httptest.NewRecorder()
	m.handleStart(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("302 beklenir, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Fatalf("Google'a yönlendirmeli, got %q", loc)
	}
	if !strings.Contains(loc, "redirect_uri=https%3A%2F%2Fapp.example.com%2F_zva%2Fcallback") {
		t.Fatalf("redirect_uri kontrol host'una gitmeli, got %q", loc)
	}
	if !strings.Contains(loc, "client_id=gid") {
		t.Fatalf("client_id içermeli, got %q", loc)
	}
}

func TestFinishSetsCookieAndRedirects(t *testing.T) {
	m := testManager()
	grant := m.signToken(purposeGrant, grantClaims{Email: "a@b.com", Host: "api.example.com", RD: "/gizli", Exp: time.Now().Add(time.Minute).Unix()})
	r := httptest.NewRequest("GET", "https://api.example.com/_zva/finish?g="+grant, nil)
	w := httptest.NewRecorder()
	m.handleFinish(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("302 beklenir, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/gizli" {
		t.Fatalf("rd'ye yönlendirmeli, got %q", w.Header().Get("Location"))
	}
	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" && c.HttpOnly && c.Secure {
			found = true
		}
	}
	if !found {
		t.Fatal("HttpOnly+Secure oturum çerezi yazılmalı")
	}
}

func TestFinishRejectsWrongHost(t *testing.T) {
	m := testManager()
	grant := m.signToken(purposeGrant, grantClaims{Email: "a@b.com", Host: "baska.example.com", RD: "/", Exp: time.Now().Add(time.Minute).Unix()})
	r := httptest.NewRequest("GET", "https://api.example.com/_zva/finish?g="+grant, nil)
	w := httptest.NewRecorder()
	m.handleFinish(w, r)
	if w.Code == http.StatusFound {
		t.Fatal("host uyuşmazlığında çerez yazılmamalı")
	}
}

func TestSafeRedirectPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/gizli?a=1", "/gizli?a=1"},
		{"/a/b#x", "/a/b#x"},
		{"//evil.com", "/"},
		{"/\\evil.com", "/"},
		{"/%5Cevil.com", "/"},
		{"/%5cevil.com", "/"},
		{"/\t/evil.com", "/"},
		{"/%09/evil.com", "/"},
		{"/%2Fevil.com", "/"},
		{"/a\\b", "/"},
		{"/a\nb", "/"},
		{"/\x00", "/"},
		{"https://evil.com", "/"},
		{"evil.com", "/"},
		{"javascript:alert(1)", "/"},
		{"/%zz", "/"},
		{"/" + strings.Repeat("a", 3000), "/"},
	}
	for _, c := range cases {
		if got := SafeRedirectPath(c.in); got != c.want {
			t.Errorf("SafeRedirectPath(%q)=%q, beklenen %q", c.in, got, c.want)
		}
		if got := safeRD(c.in); got != c.want {
			t.Errorf("safeRD(%q)=%q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestTokenPurposeSeparation(t *testing.T) {
	m := testManager()
	exp := time.Now().Add(time.Minute).Unix()
	state := m.signToken(purposeState, stateClaims{Host: "api.example.com", Exp: exp})
	var g grantClaims
	if m.verifyToken(purposeGrant, state, &g) {
		t.Fatal("state jetonu grant olarak kabul edilmemeli")
	}
	if m.verifyToken(purposeSession, state, &g) {
		t.Fatal("state jetonu oturum olarak kabul edilmemeli")
	}
	var st stateClaims
	if !m.verifyToken(purposeState, state, &st) {
		t.Fatal("state jetonu kendi amacıyla doğrulanmalı")
	}
	grant := m.signToken(purposeGrant, grantClaims{Email: "a@b.com", Host: "api.example.com", Exp: exp})
	if m.verifyToken(purposeSession, grant, &g) || m.verifyToken(purposeState, grant, &st) {
		t.Fatal("grant jetonu başka amaçla kabul edilmemeli")
	}
}
