package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
)

const testHost = "app.example.com"

func newVisitor(providers ...string) *visitorauth.Manager {
	ps := map[string]*visitorauth.Provider{}
	for _, p := range providers {
		ps[p] = &visitorauth.Provider{Name: p, ClientID: "id", ClientSecret: "sec",
			AuthURL: "https://idp.example/auth", TokenURL: "x", UserInfoURL: "y", Scopes: "email"}
	}
	return visitorauth.New([]byte("visitor-secret-0123456789"), "ctl.example.com", ps,
		func(string) (string, []byte, bool, bool) { return "oauth", []byte("{}"), true, true }, nil)
}

func routeFor(t *testing.T, mode, cfg string) store.HostRoute {
	t.Helper()
	return store.HostRoute{
		FQDN: testHost, TenantID: "ten_1", TunnelID: "tun_1", ClientID: "cli",
		Target: "http://localhost:1", Enabled: true,
		AccessEnabled: true, AccessMode: mode, AccessConfig: []byte(cfg),
	}
}

func newAccessHandler(t *testing.T, tun store.HostRoute, vis *visitorauth.Manager) *Handler {
	t.Helper()
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Handler{Router: rt, Visitor: vis, BasicSecret: []byte("basic-secret-0123456789"),
		BasicLimiter: ratelimit.New(10.0/600, 10)}
}

func browserReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, "https://"+testHost+target, nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.RemoteAddr = "203.0.113.7:4444"
	return r
}

func basicRoute(t *testing.T, pass string) store.HostRoute {
	t.Helper()
	hash, err := auth.HashSecret(pass)
	if err != nil {
		t.Fatal(err)
	}
	return routeFor(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`)
}

func postLogin(h *Handler, user, pass, rd string) *httptest.ResponseRecorder {
	form := url.Values{"user": {user}, "pass": {pass}, "rd": {rd}}
	r := httptest.NewRequest("POST", "https://"+testHost+"/_zvb/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html")
	r.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// --- OAuth sayfasi ---------------------------------------------------------

func TestOAuthPage_ProvidersFilteredByTunnelAndServer(t *testing.T) {
	// Sunucuda google+github var, tunel yalniz github'a izin veriyor.
	h := newAccessHandler(t, routeFor(t, "oauth", `{"providers":["github"]}`), newVisitor("google", "github"))
	w := httptest.NewRecorder()
	if h.enforceAccess(w, browserReq("GET", "/gizli?x=1"), h.mustRoute(t)) {
		t.Fatal("oturumsuz istek gecmemeli")
	}
	if w.Code != 401 {
		t.Fatalf("status = %d, istenen 401", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/_zva/start?p=github&amp;rd=%2Fgizli%3Fx%3D1") {
		t.Errorf("github butonu/rd yok:\n%s", body)
	}
	if strings.Contains(body, "p=google") {
		t.Error("tunelin izin vermedigi google butonu gosterilmemeli")
	}
	if !strings.Contains(body, "<svg") || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("svg ikon / html bekleniyordu")
	}
	if w.Header().Get("WWW-Authenticate") != "" {
		t.Error("oauth sayfasinda WWW-Authenticate olmamali")
	}

	// Tunel hicbir kisit koymamis: sunucudaki ikisi de gorunur.
	h2 := newAccessHandler(t, routeFor(t, "oauth", `{}`), newVisitor("google", "github"))
	w2 := httptest.NewRecorder()
	h2.enforceAccess(w2, browserReq("GET", "/"), h2.mustRoute(t))
	if b := w2.Body.String(); !strings.Contains(b, "p=google") || !strings.Contains(b, "p=github") {
		t.Errorf("iki saglayici da beklenirdi:\n%s", b)
	}
}

func (h *Handler) mustRoute(t *testing.T) store.HostRoute {
	t.Helper()
	tun, ok := h.Router.Lookup(testHost)
	if !ok {
		t.Fatal("route yok")
	}
	return tun
}

func TestOAuthPage_NoUsableProvider(t *testing.T) {
	// Tunel github istiyor ama sunucuda yalniz google var.
	h := newAccessHandler(t, routeFor(t, "oauth", `{"providers":["github"]}`), newVisitor("google"))
	w := httptest.NewRecorder()
	h.enforceAccess(w, browserReq("GET", "/"), h.mustRoute(t))
	if w.Code != 403 {
		t.Fatalf("status = %d, istenen 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "henüz tamamlamamış") {
		t.Errorf("ziyaretci mesaji yok:\n%s", w.Body.String())
	}
	// Ziyaretci OAuth hic yok + Accept: en -> Ingilizce.
	h2 := newAccessHandler(t, routeFor(t, "oauth", `{}`), nil)
	r := browserReq("GET", "/")
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	w2 := httptest.NewRecorder()
	h2.enforceAccess(w2, r, h2.mustRoute(t))
	if w2.Code != 403 || !strings.Contains(w2.Body.String(), "has not finished") {
		t.Errorf("EN 403 bekleniyordu: %d\n%s", w2.Code, w2.Body.String())
	}
	// Tarayici olmayan istemci eski JSON 403'u alir.
	r3 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r3.Header.Set("Accept", "application/json")
	w3 := httptest.NewRecorder()
	h2.enforceAccess(w3, r3, h2.mustRoute(t))
	if w3.Code != 403 || !strings.Contains(w3.Body.String(), "yapilandirilmamis") {
		t.Errorf("JSON 403 bekleniyordu: %d %s", w3.Code, w3.Body.String())
	}
}

func TestOAuthPage_DeniedSessionShowsSwitchAccount(t *testing.T) {
	vis := newVisitor("google")
	h := newAccessHandler(t, routeFor(t, "oauth", `{"allowed_emails":["ok@example.com"]}`), vis)
	r := browserReq("GET", "/panel")
	r.AddCookie(&http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValue(testHost, "evil@example.com")})
	w := httptest.NewRecorder()
	if h.enforceAccess(w, r, h.mustRoute(t)) {
		t.Fatal("izinsiz e-posta gecmemeli")
	}
	body := w.Body.String()
	if w.Code != 403 || !strings.Contains(body, "evil@example.com") ||
		!strings.Contains(body, "/_zva/logout?rd=%2Fpanel") {
		t.Errorf("403 + farkli hesap baglantisi bekleniyordu: %d\n%s", w.Code, body)
	}
	// Izinli e-posta gecer.
	r2 := browserReq("GET", "/panel")
	r2.AddCookie(&http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValue(testHost, "OK@example.com")})
	if !h.enforceAccess(httptest.NewRecorder(), r2, h.mustRoute(t)) {
		t.Error("izinli e-posta gecmeliydi")
	}
	// Logout cerezi siler.
	lw := httptest.NewRecorder()
	vis.ServeTunnelEndpoint(lw, httptest.NewRequest("GET", "https://"+testHost+"/_zva/logout?rd=/panel", nil))
	if lw.Code != 303 || lw.Header().Get("Location") != "/panel" {
		t.Errorf("logout yonlendirmesi: %d %q", lw.Code, lw.Header().Get("Location"))
	}
	if c := cookieNamed(lw, "_zva_session"); c == nil || c.MaxAge >= 0 {
		t.Error("logout oturum cerezini silmeli")
	}
}

func TestOAuth_NonBrowserKeepsOldBehaviour(t *testing.T) {
	h := newAccessHandler(t, routeFor(t, "oauth", `{}`), newVisitor("google"))
	// POST -> 401 JSON/duz.
	r := httptest.NewRequest("POST", "https://"+testHost+"/x", nil)
	w := httptest.NewRecorder()
	h.enforceAccess(w, r, h.mustRoute(t))
	if w.Code != 401 || strings.Contains(w.Body.String(), "<svg") {
		t.Errorf("POST 401 bekleniyordu: %d", w.Code)
	}
	// curl GET (Accept */*) -> eski 302.
	r2 := httptest.NewRequest("GET", "https://"+testHost+"/x", nil)
	r2.Header.Set("Accept", "*/*")
	w2 := httptest.NewRecorder()
	h.enforceAccess(w2, r2, h.mustRoute(t))
	if w2.Code != 302 || !strings.HasPrefix(w2.Header().Get("Location"), "/_zva/start?p=google") {
		t.Errorf("302 bekleniyordu: %d %q", w2.Code, w2.Header().Get("Location"))
	}
}

// --- Basic Auth formu ------------------------------------------------------

func TestBasicForm_FullFlow(t *testing.T) {
	route := basicRoute(t, "s3cret")
	h := newAccessHandler(t, route, nil)

	// 1) Tarayici GET -> 401 form, WWW-Authenticate YOK.
	w := httptest.NewRecorder()
	if h.enforceAccess(w, browserReq("GET", "/dash?a=b"), route) {
		t.Fatal("kimliksiz istek gecmemeli")
	}
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("form 401 ve basliksiz olmali: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	body := w.Body.String()
	for _, want := range []string{`action="/_zvb/login"`, `name="user"`, `name="pass"`,
		`autocomplete="username"`, `autocomplete="current-password"`, `value="/dash?a=b"`,
		`<label for="zv-user">`, `name="viewport"`, "prefers-color-scheme"} {
		if !strings.Contains(body, want) {
			t.Errorf("form %q icermiyor", want)
		}
	}
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Error("harici kaynak olmamali")
	}

	// 2) Yanlis parola -> 401 form + hata.
	bad := postLogin(h, "alice", "wrong", "/dash")
	if bad.Code != 401 || !strings.Contains(bad.Body.String(), "hatalı") || cookieNamed(bad, basicCookie) != nil {
		t.Errorf("hatali giris 401+hata olmali: %d", bad.Code)
	}
	if bad.Header().Get("WWW-Authenticate") != "" {
		t.Error("form hatasinda WWW-Authenticate olmamali")
	}

	// 3) Dogru parola -> cerez + 303.
	ok := postLogin(h, "alice", "s3cret", "/dash?a=b")
	if ok.Code != 303 || ok.Header().Get("Location") != "/dash?a=b" {
		t.Fatalf("303 bekleniyordu: %d %q", ok.Code, ok.Header().Get("Location"))
	}
	c := cookieNamed(ok, basicCookie)
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" {
		t.Fatalf("cerez ozellikleri hatali: %+v", c)
	}

	// 4) Cerez erisim verir.
	r := browserReq("GET", "/dash")
	r.AddCookie(c)
	if !h.enforceAccess(httptest.NewRecorder(), r, route) {
		t.Error("gecerli cerez erisim vermeli")
	}
	// Baska tunelin cerezi / bozuk cerez gecmez.
	other := route
	other.TunnelID = "tun_other"
	if h.enforceAccess(httptest.NewRecorder(), r, other) {
		t.Error("baska tunelde cerez gecmemeli")
	}
	r2 := browserReq("GET", "/dash")
	r2.AddCookie(&http.Cookie{Name: basicCookie, Value: c.Value + "x"})
	if h.enforceAccess(httptest.NewRecorder(), r2, route) {
		t.Error("bozuk imza gecmemeli")
	}

	// 5) Parola degisince cerez gecersiz.
	changed := basicRoute(t, "yeni-parola")
	if h.enforceAccess(httptest.NewRecorder(), r, changed) {
		t.Error("parola degisince eski cerez gecersiz olmali")
	}

	// 6) Logout.
	lr := httptest.NewRequest("GET", "https://"+testHost+"/_zvb/logout?rd=/dash", nil)
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, lr)
	if lw.Code != 303 || cookieNamed(lw, basicCookie) == nil || cookieNamed(lw, basicCookie).MaxAge >= 0 {
		t.Errorf("logout cerezi silmeli: %d", lw.Code)
	}
}

func TestBasicForm_OpenRedirectRejected(t *testing.T) {
	route := basicRoute(t, "pw")
	h := newAccessHandler(t, route, nil)
	for _, rd := range []string{"https://evil.com/", "//evil.com", "/\\evil.com", "evil.com", "/_zvb/login", "/a\r\nSet-Cookie: x=1"} {
		w := postLogin(h, "alice", "pw", rd)
		if w.Code != 303 || w.Header().Get("Location") != "/" {
			t.Errorf("rd=%q -> %d %q, '/' bekleniyordu", rd, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestBasicForm_HeaderAuthAndChallengeForAPIClients(t *testing.T) {
	route := basicRoute(t, "pw")
	h := newAccessHandler(t, route, nil)

	// curl: Accept yok -> WWW-Authenticate'li 401.
	r := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r.RemoteAddr = "198.51.100.1:1"
	w := httptest.NewRecorder()
	h.enforceAccess(w, r, route)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("API istemcisi challenge almali: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if strings.Contains(w.Body.String(), "<form") {
		t.Error("API istemcisine form gonderilmemeli")
	}
	// Dogru header gecer, yanlis header gecmez.
	r2 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r2.SetBasicAuth("alice", "pw")
	if !h.enforceAccess(httptest.NewRecorder(), r2, route) {
		t.Error("gecerli Basic header gecmeli")
	}
	r3 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r3.SetBasicAuth("alice", "nope")
	r3.RemoteAddr = "198.51.100.1:1"
	w3 := httptest.NewRecorder()
	if h.enforceAccess(w3, r3, route) || w3.Code != 401 {
		t.Errorf("yanlis header reddedilmeli: %d", w3.Code)
	}
	r4 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r4.SetBasicAuth("mallory", "pw")
	r4.RemoteAddr = "198.51.100.1:1"
	if h.enforceAccess(httptest.NewRecorder(), r4, route) {
		t.Error("yanlis kullanici adi reddedilmeli")
	}
}

func TestBasicForm_NoSecretFallsBackToNativePopup(t *testing.T) {
	route := basicRoute(t, "pw")
	h := newAccessHandler(t, route, nil)
	h.BasicSecret = nil
	w := httptest.NewRecorder()
	h.enforceAccess(w, browserReq("GET", "/"), route)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("sir yokken eski davranis bekleniyordu: %d", w.Code)
	}
}

func TestBasicForm_RateLimit(t *testing.T) {
	route := basicRoute(t, "pw")
	h := newAccessHandler(t, route, nil)
	for i := 0; i < 10; i++ {
		if w := postLogin(h, "alice", "bad", "/"); w.Code != 401 {
			t.Fatalf("deneme %d: %d, 401 bekleniyordu", i, w.Code)
		}
	}
	w := postLogin(h, "alice", "pw", "/") // dogru parola bile engelli
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || cookieNamed(w, basicCookie) != nil {
		t.Errorf("429 bekleniyordu: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Çok fazla") {
		t.Error("429 sayfasi bekleniyordu")
	}
	// Header yolu da ayni kotayi paylasir.
	r := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r.SetBasicAuth("alice", "pw")
	r.RemoteAddr = "203.0.113.7:4444"
	w2 := httptest.NewRecorder()
	if h.enforceAccess(w2, r, route) || w2.Code != 429 {
		t.Errorf("header yolunda da 429 bekleniyordu: %d", w2.Code)
	}
	// Baska IP etkilenmez.
	r2 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r2.SetBasicAuth("alice", "pw")
	r2.RemoteAddr = "203.0.113.99:4444"
	if !h.enforceAccess(httptest.NewRecorder(), r2, route) {
		t.Error("baska IP engellenmemeli")
	}
}

func TestZvbPathsNeverForwarded(t *testing.T) {
	route := basicRoute(t, "pw")
	h := newAccessHandler(t, route, nil)
	// Hub nil: istek proxy'ye ulasirsa panikler; 404 donmeli.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://"+testHost+"/_zvb/whatever", nil))
	if w.Code != 404 {
		t.Errorf("/_zvb/* 404 bekleniyordu: %d", w.Code)
	}
	// Basic modunda olmayan tunelde login 404.
	open := newAccessHandler(t, routeFor(t, "oauth", `{}`), nil)
	if w := postLogin(open, "a", "b", "/"); w.Code != 404 {
		t.Errorf("basic olmayan tunelde login 404 olmali: %d", w.Code)
	}
	// Cross-origin POST reddedilir.
	form := url.Values{"user": {"alice"}, "pass": {"pw"}}
	r := httptest.NewRequest("POST", "https://"+testHost+"/_zvb/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, r)
	if cw.Code != 403 || cookieNamed(cw, basicCookie) != nil {
		t.Errorf("cross-origin login 403 olmali: %d", cw.Code)
	}
}

// --- Cerez temizleme -------------------------------------------------------

func TestStripAuthCookies(t *testing.T) {
	got := stripAuthCookies([]string{"a=1; _zva_session=x; b=2", "_zvb_session=y"})
	if len(got) != 1 || got[0] != "a=1; b=2" {
		t.Errorf("stripAuthCookies = %#v", got)
	}
	r := httptest.NewRequest("GET", "http://h/", nil)
	r.Header.Set("Cookie", "_zvb_session=y; _zva_session=z")
	if _, ok := forwardHeaders(r)["Cookie"]; ok {
		t.Error("yalniz Zorven cerezleri varsa Cookie basligi hic gitmemeli")
	}
	r2 := httptest.NewRequest("GET", "http://h/", nil)
	r2.Header.Set("Cookie", "sid=abc; _zvb_session=y")
	if v := forwardHeaders(r2)["Cookie"]; len(v) != 1 || v[0] != "sid=abc" {
		t.Errorf("uygulama cerezi korunmali: %#v", v)
	}
}

func TestPickLang(t *testing.T) {
	cases := map[string]string{
		"": "tr", "en-US,en;q=0.9": "en", "tr-TR,tr;q=0.9,en;q=0.8": "tr",
		"de,fr": "tr", "en;q=0.5, tr;q=0.9": "tr", "fr, en": "en",
	}
	for in, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", in)
		if got := pickLang(r); got != want {
			t.Errorf("pickLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPageEscapesDynamicValues(t *testing.T) {
	vis := newVisitor("google")
	h := newAccessHandler(t, routeFor(t, "oauth", `{"allowed_emails":["a@b.c"]}`), vis)
	r := browserReq("GET", "/")
	r.AddCookie(&http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValue(testHost, `"><script>alert(1)</script>@x.y`)})
	w := httptest.NewRecorder()
	h.enforceAccess(w, r, h.mustRoute(t))
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("e-posta kacislanmamis")
	}
	// Basic: kullanici adi alani kacislanir.
	b := newAccessHandler(t, basicRoute(t, "pw"), nil)
	bw := postLogin(b, `"><script>x</script>`, "bad", "/")
	if strings.Contains(bw.Body.String(), "<script>") {
		t.Error("kullanici adi kacislanmamis")
	}
}

// --- Olay kaydi ------------------------------------------------------------

func TestAccessEventsRecorded(t *testing.T) {
	var mu sync.Mutex
	var got []accesslog.Event
	p := accesslog.NewPersister(func(_ context.Context, b []accesslog.Event) error {
		mu.Lock()
		got = append(got, b...)
		mu.Unlock()
		return nil
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	route := basicRoute(t, "s3cret")
	h := newAccessHandler(t, route, nil)
	h.AccessEvents = p
	postLogin(h, "alice", "wrong-PASSWORD", "/")
	postLogin(h, "alice", "s3cret", "/")
	// Basarili header istegi olay URETMEZ; basarisiz header uretir.
	r := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r.SetBasicAuth("alice", "s3cret")
	h.enforceAccess(httptest.NewRecorder(), r, route)
	r2 := httptest.NewRequest("GET", "https://"+testHost+"/", nil)
	r2.SetBasicAuth("alice", "zzz")
	r2.Header.Set("User-Agent", strings.Repeat("u", 500))
	h.enforceAccess(httptest.NewRecorder(), r2, route)
	// OAuth hook.
	h2 := newAccessHandler(t, routeFor(t, "oauth", `{}`), newVisitor("google"))
	h2.AccessEvents = p
	h2.RecordVisitorEvent(visitorauth.Event{Host: testHost, Provider: "google", Email: "x@y.z",
		Success: false, Reason: "email_not_allowed", ClientIP: "1.2.3.4"})
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("4 olay bekleniyordu, %d geldi: %+v", len(got), got)
	}
	reasons := map[string]int{}
	for _, e := range got {
		reasons[e.Reason]++
		if e.TunnelID != "tun_1" || e.TenantID != "ten_1" || e.ID == "" || e.CreatedAt.IsZero() {
			t.Errorf("eksik alan: %+v", e)
		}
		if len(e.UserAgent) > accesslog.MaxUserAgent {
			t.Errorf("user-agent kesilmemis: %d", len(e.UserAgent))
		}
		if strings.Contains(e.Identity, "PASSWORD") || strings.Contains(e.Identity, "s3cret") {
			t.Error("parola olaya sizmis")
		}
	}
	if reasons["bad_credentials"] != 2 || reasons["ok"] != 1 || reasons["email_not_allowed"] != 1 {
		t.Errorf("neden dagilimi: %v", reasons)
	}
}
