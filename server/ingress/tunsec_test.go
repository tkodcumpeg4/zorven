package ingress

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// secHandler, verilen guvenlik ayariyla (ve varsa mmdb ile) bir Handler kurar.
func secHandler(t *testing.T, cfg string, plan string, gate func(string) bool, mmdb map[string]string) *Handler {
	t.Helper()
	tun := rejectRoute()
	tun.SecurityConfig = []byte(cfg)
	tun.Plan = plan
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	rt.SecurityGate = gate
	if mmdb != nil {
		path := filepath.Join(t.TempDir(), "c.mmdb")
		writeTestMMDB(t, path, mmdb)
		if !rt.SetGeoPath(path) {
			t.Fatal("mmdb yuklenemedi")
		}
	}
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Handler{Router: rt, Hub: tunnel.NewHub(), ReqLog: reqlog.New(500),
		Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
}

func reqFrom(ip, path string) *http.Request {
	r := httptest.NewRequest("GET", "https://"+testHost+path, nil)
	r.RemoteAddr = ip + ":4444"
	return r
}

var testGeoDB = map[string]string{
	"203.0.113.0/24":  "AU:OC",
	"198.51.100.0/24": "TR:AS",
	"2001:db8::/32":   "DE:EU",
}

func TestTunnelGeoAllowList(t *testing.T) {
	h := secHandler(t, `{"geo":{"mode":"allow","countries":["TR"]}}`, "pro", nil, testGeoDB)
	// AU engellenir
	w := serve(h, reqFrom("203.0.113.9", "/x"))
	if w.Code != 403 {
		t.Fatalf("AU icin 403 bekleniyordu: %d", w.Code)
	}
	id := w.Header().Get(requestIDHeader)
	if id == "" || !strings.Contains(w.Body.String(), id) {
		t.Fatalf("istek kimligi yanita yazilmaliydi: %q", id)
	}
	e := onlyEntry(t, h)
	if e.RejectReason != "geo:AU" || e.Status != 403 || e.ID != id {
		t.Fatalf("log yanlis: %+v (id %s)", e, id)
	}
	// IPv6 DE de engellenir
	if w := serve(h, reqFrom("2001:db8::5", "/x")); w.Code != 403 {
		t.Fatalf("IPv6 DE 403 olmali: %d", w.Code)
	}
	// TR gecer (istemci cevrimdisi => 502)
	if w := serve(h, reqFrom("198.51.100.4", "/x")); w.Code != http.StatusBadGateway {
		t.Fatalf("TR gecmeli (502 offline): %d", w.Code)
	}
}

func TestTunnelGeoDenyList(t *testing.T) {
	h := secHandler(t, `{"geo":{"mode":"deny","countries":["AU"]}}`, "pro", nil, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/")); w.Code != 403 {
		t.Fatalf("AU reddedilmeli: %d", w.Code)
	}
	if w := serve(h, reqFrom("198.51.100.4", "/")); w.Code == 403 {
		t.Fatal("TR reddedilmemeli")
	}
}

func TestTunnelGeoUnknown(t *testing.T) {
	// unknown_is=allow (vars.): DB'de olmayan IP gecer
	h := secHandler(t, `{"geo":{"mode":"allow","countries":["TR"]}}`, "pro", nil, testGeoDB)
	if w := serve(h, reqFrom("192.0.2.1", "/")); w.Code == 403 {
		t.Fatal("unknown_is=allow: gecmeli")
	}
	// unknown_is=deny: reddedilir ve geo:unknown yazilir
	h = secHandler(t, `{"geo":{"mode":"allow","countries":["TR"],"unknown_is":"deny"}}`, "pro", nil, testGeoDB)
	if w := serve(h, reqFrom("192.0.2.1", "/")); w.Code != 403 {
		t.Fatalf("unknown_is=deny: 403 bekleniyordu: %d", w.Code)
	}
	if e := onlyEntry(t, h); e.RejectReason != "geo:unknown" {
		t.Fatalf("neden: %q", e.RejectReason)
	}
	// DB hic yokken de ayni karar (unknown)
	h = secHandler(t, `{"geo":{"mode":"deny","countries":["AU"],"unknown_is":"deny"}}`, "pro", nil, nil)
	if w := serve(h, reqFrom("203.0.113.9", "/")); w.Code != 403 {
		t.Fatalf("DB yok + unknown_is=deny: 403 bekleniyordu: %d", w.Code)
	}
	h = secHandler(t, `{"geo":{"mode":"deny","countries":["AU"]}}`, "pro", nil, nil)
	if w := serve(h, reqFrom("203.0.113.9", "/")); w.Code == 403 {
		t.Fatal("DB yok + unknown_is=allow: gecmeli")
	}
}

func TestTunnelWAFBlockAndLog(t *testing.T) {
	h := secHandler(t, `{"waf":{"mode":"block"}}`, "pro", nil, nil)
	w := serve(h, reqFrom("198.51.100.4", "/p?id=1+union+select+1"))
	if w.Code != 403 {
		t.Fatalf("WAF block 403 olmali: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "union") || strings.Contains(w.Body.String(), "sqli") {
		t.Fatal("kural adi yanita sizmamali")
	}
	id := w.Header().Get(requestIDHeader)
	e := onlyEntry(t, h)
	if e.RejectReason != "waf:sqli-union" || e.Status != 403 || e.ID != id || id == "" {
		t.Fatalf("log yanlis: %+v", e)
	}
	// normal istek gecer
	if w := serve(h, reqFrom("198.51.100.4", "/api/v1/items?page=2")); w.Code != http.StatusBadGateway {
		t.Fatalf("normal istek gecmeli: %d", w.Code)
	}

	// log kipi: istek gecer, bulgu waf_log: olarak yazilir
	h = secHandler(t, `{"waf":{"mode":"log"}}`, "free", func(string) bool { return false }, nil)
	w = serve(h, reqFrom("198.51.100.4", "/p?id=1+union+select+1"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("log kipinde istek gecmeli: %d", w.Code)
	}
	var found bool
	for _, e := range h.ReqLog.List(0, "") {
		if e.RejectReason == "waf_log:sqli-union" {
			found = true
		}
	}
	if !found {
		t.Fatalf("waf_log kaydi yok: %+v", h.ReqLog.List(0, ""))
	}
}

func TestTunnelWAFInternalPathsExempt(t *testing.T) {
	h := secHandler(t, `{"waf":{"mode":"block"}}`, "pro", nil, nil)
	w := serve(h, reqFrom("198.51.100.4", "/_zva/start?rd=/%3Cscript%3E"))
	if w.Header().Get(requestIDHeader) != "" {
		t.Fatalf("/_zva/ WAF'tan muaf olmali: %d", w.Code)
	}
}

func TestTunnelWAFBody(t *testing.T) {
	h := secHandler(t, `{"waf":{"mode":"block","body":true}}`, "pro", nil, nil)
	r := httptest.NewRequest("POST", "https://"+testHost+"/submit", strings.NewReader("q=1+union+select+1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "198.51.100.4:1"
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("govde WAF 403 olmali: %d", w.Code)
	}
	// govde kapaliyken (vars.) gecer
	h = secHandler(t, `{"waf":{"mode":"block"}}`, "pro", nil, nil)
	r = httptest.NewRequest("POST", "https://"+testHost+"/submit", strings.NewReader("q=1+union+select+1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "198.51.100.4:1"
	if w := serve(h, r); w.Code == 403 {
		t.Fatal("govde taranmamali")
	}
}

func TestTunnelSecurityPlanGate(t *testing.T) {
	gate := func(plan string) bool { return plan == "pro" }
	// free: WAF block -> log, Geo kapali
	h := secHandler(t, `{"waf":{"mode":"block"},"geo":{"mode":"deny","countries":["AU"]}}`, "free", gate, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/p?id=1+union+select+1")); w.Code == 403 {
		t.Fatal("free planda ne WAF block ne Geo uygulanmali")
	}
	// pro: ikisi de acik
	h = secHandler(t, `{"geo":{"mode":"deny","countries":["AU"]}}`, "pro", gate, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/")); w.Code != 403 {
		t.Fatal("pro planda Geo uygulanmali")
	}
	// kapi nil (open core): kisitlama yok
	h = secHandler(t, `{"geo":{"mode":"deny","countries":["AU"]}}`, "free", nil, testGeoDB)
	if w := serve(h, reqFrom("203.0.113.9", "/")); w.Code != 403 {
		t.Fatal("open core'da kapi olmamali")
	}
}

func TestGeoResolverHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.mmdb")
	writeTestMMDB(t, path, map[string]string{"203.0.113.0/24": "AU:OC"})
	g, err := newGeoResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	now := time.Now()
	g.now = func() time.Time { return now }
	if info := g.lookup("203.0.113.5"); info.country != "AU" {
		t.Fatalf("ilk yukleme: %+v", info)
	}
	// dosya degisir (mtime ileri)
	// GERCEK kullanimdaki gibi atomik degistirme (gecici dosya + rename): yerinde
	// ustune yazmak mmap'li okuyucuyu bozar; docs/ztna-waf-geo.md uyarir.
	tmp := path + ".new"
	writeTestMMDB(t, tmp, map[string]string{"203.0.113.0/24": "TR:AS"})
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(tmp, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	// denetim araligi dolmadan eski deger
	if info := g.lookup("203.0.113.5"); info.country != "AU" {
		t.Fatalf("aralik dolmadan yenilenmemeli: %+v", info)
	}
	now = now.Add(geoRecheckEvery + time.Second)
	if info := g.lookup("203.0.113.5"); info.country != "TR" {
		t.Fatalf("yeniden yuklenmeliydi: %+v", info)
	}
	// bozuk dosya: eski veritabani korunur
	if err := os.WriteFile(tmp, []byte("bozuk"), 0o644); err != nil {
		t.Fatal(err)
	}
	future2 := future.Add(time.Hour)
	_ = os.Chtimes(tmp, future2, future2)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(geoRecheckEvery + time.Second)
	if info := g.lookup("203.0.113.5"); info.country != "TR" {
		t.Fatalf("bozuk dosyada eski DB korunmali: %+v", info)
	}
}

func TestGeoAutoResolverFileAppearsLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.mmdb")
	g := newAutoGeoResolver(path)
	now := time.Now()
	g.now = func() time.Time { return now }
	if g.loaded() || g.lookup("203.0.113.5").ok {
		t.Fatal("dosya yokken ok=false olmali")
	}
	writeTestMMDB(t, path, map[string]string{"203.0.113.0/24": "AU:OC"})
	now = now.Add(geoRecheckEvery + time.Second)
	if info := g.lookup("203.0.113.5"); info.country != "AU" {
		t.Fatalf("sonradan eklenen dosya yuklenmeli: %+v", info)
	}
}

func TestPolicyCountryNotAndUnknown(t *testing.T) {
	cp := compileForTest(t, `{"rules":[
	  {"match":{"country_not":["TR"]},"action":{"type":"deny","status":403}},
	  {"match":{"unknown":true},"action":{"type":"deny","status":451}}]}`, 10)
	rt := routerWith(cp)
	path := filepath.Join(t.TempDir(), "p.mmdb")
	writeTestMMDB(t, path, testGeoDB)
	rt.SetGeoPath(path)
	eval := func(ip string) policyOutcome {
		return rt.evaluatePolicies("example.com", geoReq("http://example.com/"), ip, "tun_1", false)
	}
	if o := eval("198.51.100.4"); o.terminal() { // TR
		t.Fatalf("TR gecmeli: %+v", o)
	}
	if o := eval("203.0.113.9"); o.denyStatus != 403 { // AU
		t.Fatalf("AU country_not ile reddedilmeli: %+v", o)
	}
	if o := eval("192.0.2.1"); o.denyStatus != 451 { // bilinmeyen
		t.Fatalf("unknown kurali 451 vermeli: %+v", o)
	}
	// DB yokken country_not eslesmez, unknown eslesir
	rt2 := routerWith(cp)
	o := rt2.evaluatePolicies("example.com", geoReq("http://example.com/"), "203.0.113.9", "tun_1", false)
	if o.denyStatus != 451 {
		t.Fatalf("DB yok: yalniz unknown eslesmeli: %+v", o)
	}
}

func TestPolicyChallengeFlow(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/admin"},"action":{"type":"challenge"}}]}`, 10)
	// Ziyaretci OAuth'u yok => 403 ve challenge nedeni
	h := newRejectHandler(t, rejectRoute(), cp)
	w := serve(h, browserLike("/admin/x"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("OAuth yokken 403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), http.StatusForbidden, RejectChallenge)

	// Eslesmeyen yol etkilenmez
	h = newRejectHandler(t, rejectRoute(), cp)
	if w := serve(h, plainReq("/public")); w.Code != http.StatusBadGateway {
		t.Fatalf("eslesmeyen yol gecmeli: %d", w.Code)
	}

	// Ziyaretci OAuth'u var, oturum yok => tarayici GET icin giris sayfasi (401), JSON/non-browser icin 302
	h = newRejectHandler(t, rejectRoute(), cp)
	h.Visitor = newVisitor("google")
	r := plainReq("/admin/x")
	w = serve(h, r)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/_zva/start") {
		t.Fatalf("OAuth'a yonlendirme bekleniyordu: %d %q", w.Code, w.Header().Get("Location"))
	}
	if got := h.ReqLog.List(0, ""); len(got) != 1 || got[0].RejectReason != RejectChallenge {
		t.Fatalf("challenge logu: %+v", got)
	}

	// Gecerli ziyaretci oturumu => gecer (istemci cevrimdisi 502)
	h = newRejectHandler(t, rejectRoute(), cp)
	h.Visitor = newVisitor("google")
	r = plainReq("/admin/x")
	r.AddCookie(&http.Cookie{Name: "_zva_session", Value: h.Visitor.IssueSessionValue(testHost, "u@example.com")})
	if w := serve(h, r); w.Code != http.StatusBadGateway {
		t.Fatalf("gecerli oturumla gecmeli: %d", w.Code)
	}
}

func browserLike(path string) *http.Request {
	r := plainReq(path)
	r.Header.Set("Accept", "text/html")
	return r
}

func BenchmarkTunnelSecurityPath(b *testing.B) {
	tun := rejectRoute()
	tun.SecurityConfig = []byte(`{"waf":{"mode":"block"},"geo":{"mode":"deny","countries":["AU"]}}`)
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	path := filepath.Join(b.TempDir(), "c.mmdb")
	writeTestMMDB(b, path, testGeoDB)
	rt.SetGeoPath(path)
	if err := rt.Reload(context.Background()); err != nil {
		b.Fatal(err)
	}
	h := &Handler{Router: rt, Hub: tunnel.NewHub(), ReqLog: reqlog.New(500)}
	r := reqFrom("198.51.100.4", "/api/v1/items?page=2&limit=20")
	r.Header.Set("User-Agent", "Mozilla/5.0 Chrome/126")
	sec := rt.SecurityFor(tun.TunnelID)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if h.enforceTunnelGeo(w, r, tun, sec, time.Now()) || h.enforceTunnelWAF(w, r, tun, sec, time.Now()) {
			b.Fatal("engellenmemeliydi")
		}
	}
}
