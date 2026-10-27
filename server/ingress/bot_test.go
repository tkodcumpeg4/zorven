package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

type botResolver struct {
	mu  sync.Mutex
	ptr map[string][]string
	fwd map[string][]string
}

func (b *botResolver) LookupAddr(_ context.Context, ip string) ([]string, error) {
	if v, ok := b.ptr[ip]; ok {
		return v, nil
	}
	return nil, errors.New("nxdomain")
}
func (b *botResolver) LookupHost(_ context.Context, h string) ([]string, error) {
	if v, ok := b.fwd[h]; ok {
		return v, nil
	}
	return nil, errors.New("nxdomain")
}

// botHandler: bot/rate ayarli Handler. Gecen istek istemci cevrimdisi oldugu icin 502 alir.
func botHandler(t *testing.T, cfg string, gate func(string) bool) *Handler {
	t.Helper()
	tun := rejectRoute()
	tun.SecurityConfig = []byte(cfg)
	tun.Plan = "free"
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	rt.SecurityGate = gate
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Handler{Router: rt, Hub: tunnel.NewHub(), ReqLog: reqlog.New(500),
		BotKey: []byte("test-bot-key"),
		BotResolver: &botResolver{
			ptr: map[string][]string{"66.249.66.1": {"crawl-66-249-66-1.googlebot.com."}},
			fwd: map[string][]string{"crawl-66-249-66-1.googlebot.com": {"66.249.66.1"}},
		},
		Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
}

func browserGet(path, ip string) *http.Request {
	r := httptest.NewRequest("GET", "https://"+testHost+path, nil)
	r.RemoteAddr = ip + ":4444"
	r.Header.Set("User-Agent", chromeUA)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	return r
}

func lastReason(h *Handler) string {
	l := h.ReqLog.List(0, "")
	if len(l) == 0 {
		return ""
	}
	return l[0].RejectReason // List: en yeni ilk
}

const always12 = `{"bot":{"mode":"always","difficulty":12,"ttl_minutes":30}}`

func TestBotAlwaysServesPageForHTML(t *testing.T) {
	h := botHandler(t, always12, nil)
	w := serve(h, browserGet("/x?a=1", "203.0.113.7"))
	if w.Code != 403 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") ||
		w.Header().Get("X-Zorven-Challenge") != "required" || !strings.Contains(w.Body.String(), "crypto.subtle") {
		t.Fatalf("sorgu sayfasi bekleniyordu: %d %v", w.Code, w.Header())
	}
	if lastReason(h) != RejectBotChallenge {
		t.Fatalf("reqlog: %q", lastReason(h))
	}
	if iss, _, _ := h.BotStats("tun_1"); iss != 1 {
		t.Fatalf("sayac: %d", iss)
	}
	// Dil secimi
	r := browserGet("/", "203.0.113.7")
	r.Header.Set("Accept-Language", "ja,en;q=0.5")
	if w := serve(h, r); !strings.Contains(w.Body.String(), `lang="ja"`) {
		t.Fatal("ja sayfa bekleniyordu")
	}
}

func TestBotNonHTMLGets403Header(t *testing.T) {
	h := botHandler(t, always12, nil)
	r := browserGet("/api/data", "203.0.113.7")
	r.Header.Set("Accept", "application/json")
	w := serve(h, r)
	if w.Code != 403 || w.Header().Get("X-Zorven-Challenge") != "required" || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("XHR: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "json") || !strings.Contains(w.Body.String(), "challenge_required") {
		t.Fatalf("JSON govde: %s", w.Body.String())
	}
	p := browserGet("/form", "203.0.113.7")
	p.Method = "POST"
	if w := serve(h, p); w.Code != 403 || w.Header().Get("X-Zorven-Challenge") != "required" || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("POST: %d", w.Code)
	}
}

var (
	reTok   = regexp.MustCompile(`data-t="([^"]+)"`)
	reNonce = regexp.MustCompile(`data-c="([^"]+)"`)
)

// solve: sorgu sayfasini alip cozer; (token, sayac) doner.
func solve(t *testing.T, h *Handler, ip, path string) (token, counter string) {
	t.Helper()
	w := serve(h, browserGet(path, ip))
	body := w.Body.String()
	tm, nm := reTok.FindStringSubmatch(body), reNonce.FindStringSubmatch(body)
	if tm == nil || nm == nil {
		t.Fatalf("sayfada jeton yok: %d", w.Code)
	}
	return html.UnescapeString(tm[1]), botguard.SolvePoW(html.UnescapeString(nm[1]), 12)
}

func verifyReq(ip, token, counter, next string) *http.Request {
	b, _ := json.Marshal(map[string]string{"token": token, "counter": counter, "next": next})
	r := httptest.NewRequest("POST", "https://"+testHost+"/.zorven/challenge", bytes.NewReader(b))
	r.RemoteAddr = ip + ":4444"
	r.Header.Set("User-Agent", chromeUA)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestBotVerifyFlow(t *testing.T) {
	h := botHandler(t, always12, nil)
	ip := "203.0.113.7"
	tok, ctr := solve(t, h, ip, "/page")

	// Yanlis sayac: ayni jeton ile (nonce yanmaz) -> bot:fail
	w := serve(h, verifyReq(ip, tok, "1", "/page"))
	if w.Code != 403 || lastReason(h) != RejectBotFail {
		t.Fatalf("yanlis sayac: %d %q", w.Code, lastReason(h))
	}
	// Dogru cozum; acik yonlendirme denemesi "/" ye indirilir.
	w = serve(h, verifyReq(ip, tok, ctr, "//evil.example/x"))
	if w.Code != 200 {
		t.Fatalf("dogrulama: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		OK   bool   `json:"ok"`
		Next string `json:"next"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.OK || out.Next != "/" {
		t.Fatalf("acik yonlendirme engellenmeli: %+v", out)
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == botguard.CookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Domain != "" || ck.MaxAge != 1800 {
		t.Fatalf("cerez ozellikleri: %+v", ck)
	}
	// Tekrar oynatma: ayni jeton+sayac ikinci kez reddedilir.
	if w := serve(h, verifyReq(ip, tok, ctr, "/")); w.Code != 403 || lastReason(h) != RejectBotFail {
		t.Fatalf("replay kabul edildi: %d", w.Code)
	}
	if _, passed, failed := h.BotStats("tun_1"); passed != 1 || failed != 2 {
		t.Fatalf("sayaclar passed=%d failed=%d", passed, failed)
	}
	// Ayni-host goreli yol korunur.
	tok2, ctr2 := solve(t, h, ip, "/p")
	w = serve(h, verifyReq(ip, tok2, ctr2, "/deep/link?x=1&y=2"))
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Next != "/deep/link?x=1&y=2" {
		t.Fatalf("next: %q", out.Next)
	}

	// Cerezle gecis (istemci cevrimdisi -> 502, 403 degil).
	r := browserGet("/page", ip)
	r.AddCookie(ck)
	if w := serve(h, r); w.Code != http.StatusBadGateway {
		t.Fatalf("cerezle gecmeli: %d", w.Code)
	}
	// Baska UA ile cerez gecmez.
	r = browserGet("/page", ip)
	r.Header.Set("User-Agent", chromeUA+" X")
	r.AddCookie(ck)
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("baska UA: %d", w.Code)
	}
	// Baska /24 ile gecmez.
	r = browserGet("/page", "198.51.100.9")
	r.AddCookie(ck)
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("baska IP: %d", w.Code)
	}
	// Bozuk cerez.
	r = browserGet("/page", ip)
	r.AddCookie(&http.Cookie{Name: botguard.CookieName, Value: ck.Value + "x"})
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("bozuk cerez: %d", w.Code)
	}
	// Baska IP onegi icin uretilen jeton dogrulanmaz.
	if w := serve(h, verifyReq("198.51.100.9", tok2, ctr2, "/")); w.Code != 403 {
		t.Fatalf("baska IP jetonu: %d", w.Code)
	}
}

func TestBotReservedPath(t *testing.T) {
	h := botHandler(t, always12, nil)
	// GET: tunele iletilmez (502 degil), 405
	r := browserGet("/.zorven/challenge", "203.0.113.7")
	if w := serve(h, r); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /.zorven/challenge: %d", w.Code)
	}
	if w := serve(h, browserGet("/.zorven/other", "203.0.113.7")); w.Code != http.StatusNotFound {
		t.Fatalf("/.zorven/other: %d", w.Code)
	}
	// Bot kapali tunelde de rezerve (tunele gitmez).
	h2 := botHandler(t, `{"waf":{"mode":"log"}}`, nil)
	if w := serve(h2, browserGet("/.zorven/challenge", "203.0.113.7")); w.Code != http.StatusNotFound {
		t.Fatalf("bot kapali: %d", w.Code)
	}
	h3 := botHandler(t, ``, nil)
	if w := serve(h3, browserGet("/.zorven/challenge", "203.0.113.7")); w.Code != http.StatusNotFound {
		t.Fatalf("ayar yok: %d", w.Code)
	}
}

func TestBotExcludePaths(t *testing.T) {
	h := botHandler(t, `{"bot":{"mode":"always","difficulty":12,"exclude_paths":["/api/*","/health"]}}`, nil)
	for _, p := range []string{"/api/v1/x", "/health"} {
		if w := serve(h, browserGet(p, "203.0.113.7")); w.Code != http.StatusBadGateway {
			t.Fatalf("%s muaf olmali: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/", "/healthz", "/api"} {
		if w := serve(h, browserGet(p, "203.0.113.7")); w.Code != 403 {
			t.Fatalf("%s sorgulanmali: %d", p, w.Code)
		}
	}
}

func TestBotBlockEmptyUA(t *testing.T) {
	h := botHandler(t, `{"bot":{"mode":"suspicious","block_empty_ua":true}}`, nil)
	r := browserGet("/", "203.0.113.7")
	r.Header.Del("User-Agent")
	w := serve(h, r)
	if w.Code != 403 || lastReason(h) != RejectBotBlock || w.Header().Get("X-Zorven-Challenge") != "" {
		t.Fatalf("bos UA: %d %q", w.Code, lastReason(h))
	}
	// bayrak kapaliyken bos UA yalniz skorlanir (supheli -> sorgu sayfasi).
	h = botHandler(t, `{"bot":{"mode":"suspicious"}}`, nil)
	r = browserGet("/", "203.0.113.7")
	r.Header.Del("User-Agent")
	if w := serve(h, r); w.Code != 403 || lastReason(h) != RejectBotChallenge {
		t.Fatalf("bos UA skor: %d %q", w.Code, lastReason(h))
	}
}

func TestBotSuspiciousScoring(t *testing.T) {
	h := botHandler(t, `{"bot":{"mode":"suspicious","difficulty":12}}`, nil)
	// Gercek tarayici basliklari gecer.
	if w := serve(h, browserGet("/", "203.0.113.7")); w.Code != http.StatusBadGateway {
		t.Fatalf("tarayici sorgulanmamali: %d", w.Code)
	}
	for _, ua := range []string{"curl/8.5.0", "python-requests/2.31", "Go-http-client/1.1", "Scrapy/2.11",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/130.0.0.0 Safari/537.36"} {
		r := browserGet("/", "203.0.113.7")
		r.Header.Set("User-Agent", ua)
		if w := serve(h, r); w.Code != 403 {
			t.Fatalf("%s sorgulanmali: %d", ua, w.Code)
		}
	}
}

func TestBotVerifiedBots(t *testing.T) {
	gb := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	for _, mode := range []string{"suspicious", "always"} {
		h := botHandler(t, `{"bot":{"mode":"`+mode+`","allow_verified_bots":true}}`, nil)
		r := browserGet("/", "66.249.66.1")
		r.Header.Set("User-Agent", gb)
		if w := serve(h, r); w.Code != http.StatusBadGateway {
			t.Fatalf("%s: dogrulanmis Googlebot gecmeli: %d", mode, w.Code)
		}
		// Sahte Googlebot (ters DNS yok)
		r = browserGet("/", "203.0.113.50")
		r.Header.Set("User-Agent", gb)
		if w := serve(h, r); w.Code != 403 {
			t.Fatalf("%s: sahte Googlebot sorgulanmali: %d", mode, w.Code)
		}
	}
	// allow_verified_bots=false: gercek Googlebot da sorgulanir.
	h := botHandler(t, `{"bot":{"mode":"always","allow_verified_bots":false}}`, nil)
	r := browserGet("/", "66.249.66.1")
	r.Header.Set("User-Agent", gb)
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("kapali beyaz liste: %d", w.Code)
	}
}

func TestBotPlanGating(t *testing.T) {
	free := func(string) bool { return false }
	// always -> suspicious: gercek tarayici sorgulanmaz.
	h := botHandler(t, always12, free)
	if w := serve(h, browserGet("/", "203.0.113.7")); w.Code != http.StatusBadGateway {
		t.Fatalf("always Pro altinda suspicious olmali: %d", w.Code)
	}
	r := browserGet("/", "203.0.113.7")
	r.Header.Set("User-Agent", "curl/8")
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("suspicious yine de curl'u sorgulamali: %d", w.Code)
	}
	// rate uygulanmaz.
	h = botHandler(t, `{"rate":{"enabled":true,"requests":1,"window_seconds":60}}`, free)
	for i := 0; i < 5; i++ {
		if w := serve(h, browserGet("/", "203.0.113.7")); w.Code == 429 {
			t.Fatal("rate Pro altinda uygulanmamali")
		}
	}
	// Pro: uygulanir.
	h = botHandler(t, `{"rate":{"enabled":true,"requests":1,"window_seconds":60}}`, func(string) bool { return true })
	serve(h, browserGet("/", "203.0.113.7"))
	if w := serve(h, browserGet("/", "203.0.113.7")); w.Code != 429 {
		t.Fatalf("rate Pro'da uygulanmali: %d", w.Code)
	}
}

func TestRateBlock(t *testing.T) {
	h := botHandler(t, `{"rate":{"enabled":true,"requests":3,"window_seconds":60,"action":"block","exclude_paths":["/static/*"]}}`, nil)
	ip := "203.0.113.7"
	for i := 0; i < 3; i++ {
		if w := serve(h, browserGet("/", ip)); w.Code != http.StatusBadGateway {
			t.Fatalf("istek %d: %d", i, w.Code)
		}
	}
	w := serve(h, browserGet("/", ip))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || lastReason(h) != RejectRateBlock {
		t.Fatalf("429 bekleniyordu: %d %v %q", w.Code, w.Header(), lastReason(h))
	}
	// Baska IP etkilenmez; muaf yol etkilenmez.
	if w := serve(h, browserGet("/", "203.0.113.8")); w.Code != http.StatusBadGateway {
		t.Fatalf("baska IP: %d", w.Code)
	}
	if w := serve(h, browserGet("/static/a.js", ip)); w.Code != http.StatusBadGateway {
		t.Fatalf("muaf yol: %d", w.Code)
	}
	// IPv6 /64 birlesir.
	h = botHandler(t, `{"rate":{"enabled":true,"requests":2,"window_seconds":60}}`, nil)
	serve(h, browserGet("/", "2001:db8:0:1::1"))
	serve(h, browserGet("/", "2001:db8:0:1::2"))
	if w := serve(h, browserGet("/", "2001:db8:0:1:ffff::3")); w.Code != 429 {
		t.Fatalf("ayni /64: %d", w.Code)
	}
}

func TestRateChallenge(t *testing.T) {
	h := botHandler(t, `{"bot":{"mode":"off","difficulty":12},"rate":{"enabled":true,"requests":2,"window_seconds":60,"action":"challenge"}}`, nil)
	ip := "203.0.113.7"
	for i := 0; i < 2; i++ {
		if w := serve(h, browserGet("/", ip)); w.Code != http.StatusBadGateway {
			t.Fatalf("istek %d: %d", i, w.Code)
		}
	}
	w := serve(h, browserGet("/", ip))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "crypto.subtle") || lastReason(h) != RejectRateChallenge {
		t.Fatalf("rate:challenge sayfasi: %d %q", w.Code, lastReason(h))
	}
	// XHR -> 403 + baslik
	r := browserGet("/api", ip)
	r.Header.Set("Accept", "application/json")
	if w := serve(h, r); w.Code != 403 || w.Header().Get("X-Zorven-Challenge") != "required" {
		t.Fatalf("rate:challenge XHR: %d", w.Code)
	}
	// Sorguyu coz -> cerez -> 3x ile gecer ama sonsuz degil.
	tok, ctr := solve(t, h, ip, "/")
	vw := serve(h, verifyReq(ip, tok, ctr, "/"))
	if vw.Code != 200 {
		t.Fatalf("dogrulama: %d", vw.Code)
	}
	var ck *http.Cookie
	for _, c := range vw.Result().Cookies() {
		ck = c
	}
	pass := 0
	for i := 0; i < 20; i++ {
		r := browserGet("/", ip)
		r.AddCookie(ck)
		w := serve(h, r)
		if w.Code == http.StatusBadGateway {
			pass++
		} else if w.Code != 429 {
			t.Fatalf("cerezli asim 429 olmali: %d", w.Code)
		}
	}
	if pass != 6 { // 2 istek x 3
		t.Fatalf("cerez sahibi 3x (6) istek gecmeli, gecen: %d", pass)
	}
}

func TestBotSkipsAuthenticatedVisitor(t *testing.T) {
	tun := routeFor(t, "oauth", `{"providers":["google"],"allowed_emails":["bob@example.com"]}`)
	tun.SecurityConfig = []byte(always12)
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	vis := newVisitor("google")
	h := &Handler{Router: rt, Hub: tunnel.NewHub(), ReqLog: reqlog.New(100), Visitor: vis, BotKey: []byte("k"),
		Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	// Oturumsuz: once bot sorgusu.
	if w := serve(h, browserGet("/", "203.0.113.7")); w.Code != 403 || lastReason(h) != RejectBotChallenge {
		t.Fatalf("oturumsuz: %d %q", w.Code, lastReason(h))
	}
	// OAuth oturumlu: sorgu atlanir (istemci cevrimdisi -> 502).
	r := browserGet("/", "203.0.113.7")
	r.AddCookie(&http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValue(testHost, "bob@example.com")})
	if w := serve(h, r); w.Code != http.StatusBadGateway {
		t.Fatalf("oturumlu ziyaretci sorgulanmamali: %d", w.Code)
	}
}

func TestBotRejectLogFilter(t *testing.T) {
	h := botHandler(t, always12, nil)
	serve(h, browserGet("/", "203.0.113.7"))
	e := h.ReqLog.List(0, "")[0]
	if !reqlog.MatchReason("bot:*,rate:*", e.RejectReason) || reqlog.MatchReason("waf:*", e.RejectReason) {
		t.Fatalf("bot:* filtresi: %q", e.RejectReason)
	}
}
