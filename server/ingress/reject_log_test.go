package ingress

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// ipRuleStore, IP izin listesi motoru icin tek kural doner.
type ipRuleStore struct {
	fakeStore
	rules []store.IPAllowlistRule
}

func (s *ipRuleStore) ListAllActiveIPRules(context.Context) ([]store.IPAllowlistRule, error) {
	return s.rules, nil
}

func rejectRoute() store.HostRoute {
	return store.HostRoute{
		FQDN: testHost, TenantID: "ten_1", TunnelID: "tun_1", ClientID: "cli",
		Target: "http://localhost:1", Enabled: true,
	}
}

// newRejectHandler, ReqLog halkali ve cevrimdisi istemcili bir Handler kurar.
func newRejectHandler(t *testing.T, tun store.HostRoute, policies ...*compiledPolicy) *Handler {
	t.Helper()
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(policies) > 0 {
		rt.policies[testHost] = policies
	}
	return &Handler{Router: rt, Hub: tunnel.NewHub(), ReqLog: reqlog.New(500),
		Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
}

func serve(h *Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func plainReq(path string) *http.Request {
	r := httptest.NewRequest("GET", "https://"+testHost+path, nil)
	r.RemoteAddr = "203.0.113.7:4444"
	return r
}

func onlyEntry(t *testing.T, h *Handler) reqlog.Entry {
	t.Helper()
	got := h.ReqLog.List(0, "")
	if len(got) != 1 {
		t.Fatalf("1 kayit bekleniyordu, %d: %+v", len(got), got)
	}
	return got[0]
}

func checkEntry(t *testing.T, e reqlog.Entry, status int, reason string) {
	t.Helper()
	if e.Status != status || e.RejectReason != reason {
		t.Errorf("durum/neden = %d/%q, beklenen %d/%q", e.Status, e.RejectReason, status, reason)
	}
	if e.TenantID != "ten_1" || e.TunnelID != "tun_1" {
		t.Errorf("kiraci/tunel = %q/%q", e.TenantID, e.TunnelID)
	}
	if e.Hostname != testHost || e.Method != "GET" || e.ClientIP != "203.0.113.7" || e.ID == "" || e.TS.IsZero() {
		t.Errorf("alanlar eksik: %+v", e)
	}
}

func TestRejectLog_ClientOffline(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	w := serve(h, plainReq("/x"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("502 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 502, RejectClientOffline)
}

func TestRejectLog_PolicyDeny(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/admin"},"action":{"type":"deny","status":403}}]}`, 100)
	h := newRejectHandler(t, rejectRoute(), cp)
	if w := serve(h, plainReq("/admin/x")); w.Code != 403 {
		t.Fatalf("403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 403, RejectPolicyDeny)
}

func TestRejectLog_PolicyRedirect(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/old"},"action":{"type":"redirect","location":"https://x.example/new","status":301}}]}`, 100)
	h := newRejectHandler(t, rejectRoute(), cp)
	if w := serve(h, plainReq("/old")); w.Code != 301 {
		t.Fatalf("301 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 301, RejectPolicyRedirect)
}

func TestRejectLog_PolicyRateLimit(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{},"action":{"type":"rate_limit","requests":1,"window_sec":3600,"burst":1}}]}`, 100)
	h := newRejectHandler(t, rejectRoute(), cp)
	serve(h, plainReq("/a")) // ilk istek gecer (istemci cevrimdisi => 502)
	if w := serve(h, plainReq("/b")); w.Code != 429 {
		t.Fatalf("429 bekleniyordu: %d", w.Code)
	}
	got := h.ReqLog.List(0, "")
	if len(got) != 2 {
		t.Fatalf("2 kayit: %+v", got)
	}
	checkEntry(t, got[0], 429, RejectRateLimited) // en yeni basta
	checkEntry(t, got[1], 502, RejectClientOffline)
}

func TestRejectLog_PolicyMTLS(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{},"action":{"type":"require_mtls"}}]}`, 100)
	h := newRejectHandler(t, rejectRoute(), cp)
	if w := serve(h, plainReq("/")); w.Code != 403 {
		t.Fatalf("403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 403, RejectMTLSRequired)
}

func TestRejectLog_TunnelMTLS(t *testing.T) {
	tun := rejectRoute()
	tun.MTLSEnabled = true
	h := newRejectHandler(t, tun)
	if w := serve(h, plainReq("/")); w.Code != 403 {
		t.Fatalf("403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 403, RejectMTLSRequired)
}

func TestRejectLog_TunnelRateLimit(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	h.ReqLimiter = ratelimit.New(0.001, 1)
	serve(h, plainReq("/a"))
	if w := serve(h, plainReq("/b")); w.Code != 429 {
		t.Fatalf("429 bekleniyordu: %d", w.Code)
	}
	got := h.ReqLog.List(0, "")
	if len(got) != 2 {
		t.Fatalf("2 kayit: %+v", got)
	}
	checkEntry(t, got[0], 429, RejectRateLimited)
}

func TestRejectLog_TrafficRedirectRule(t *testing.T) {
	tun := rejectRoute()
	tun.TrafficEnabled = true
	tun.TrafficConfig = []byte(`{"redirects":[{"match_prefix":"/go","location":"https://x.example/","status":302}]}`)
	h := newRejectHandler(t, tun)
	if w := serve(h, plainReq("/go/now")); w.Code != 302 {
		t.Fatalf("302 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 302, RejectRedirectRule)
}

func TestRejectLog_BasicAuthRequired(t *testing.T) {
	h := newRejectHandler(t, basicRoute(t, "pw"))
	r := plainReq("/secret")
	r.Header.Set("Accept", "application/json") // tarayici degil => 401
	w := serve(h, r)
	e := onlyEntry(t, h)
	checkEntry(t, e, w.Code, RejectAuthRequired)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("beklenmeyen durum %d", w.Code)
	}
}

func TestRejectLog_BasicAuthBrowserRedirect(t *testing.T) {
	h := newRejectHandler(t, basicRoute(t, "pw"))
	w := serve(h, browserReq("GET", "/secret"))
	checkEntry(t, onlyEntry(t, h), w.Code, RejectAuthRequired)
}

func TestRejectLog_AuthFlowPathsNotLogged(t *testing.T) {
	h := newRejectHandler(t, basicRoute(t, "pw"))
	// Yanlis parola ile giris formu gonderimi erisim olaylarina yazilir; istek logu degil.
	h.BasicSecret = []byte("basic-secret-0123456789")
	h.BasicLimiter = ratelimit.New(10.0/600, 10)
	postLogin(h, "alice", "wrong", "/")
	if n := h.ReqLog.Len(); n != 0 {
		t.Fatalf("/_zvb/ yollari loglanmamali, %d kayit", n)
	}
}

func TestRejectLog_Interstitial(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	h.PlatformDomain = "example.com"
	w := serve(h, browserReq("GET", "/"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<") {
		t.Fatalf("interstitial bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), http.StatusOK, RejectInterstitial)
}

func TestRejectLog_IPForbidden(t *testing.T) {
	tid := "tun_1"
	st := &ipRuleStore{rules: []store.IPAllowlistRule{{ID: "r1", TenantID: "ten_1", TunnelID: &tid, CIDR: "198.51.100.0/24", Enabled: true}}}
	eng := ipfilter.NewEngine(st, nil)
	if err := eng.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := newRejectHandler(t, rejectRoute())
	h.IPFilter = eng
	if w := serve(h, plainReq("/")); w.Code != 403 {
		t.Fatalf("403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 403, RejectIPForbidden)
}

func TestRejectLog_Misdirected(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	r := plainReq("/")
	r.TLS = &tls.ConnectionState{ServerName: "other.example.org"}
	if w := serve(h, r); w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("421 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 421, RejectMisdirected)
}

func TestRejectLog_MisdirectedUnknownHostNotLogged(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	r := httptest.NewRequest("GET", "https://nobody.invalid/", nil)
	r.TLS = &tls.ConnectionState{ServerName: "other.example.org"}
	serve(h, r)
	if n := h.ReqLog.Len(); n != 0 {
		t.Fatalf("bilinmeyen host loglanmamali: %d", n)
	}
}

func TestRejectLog_UnknownHostNotLogged(t *testing.T) {
	h := newRejectHandler(t, rejectRoute())
	r := httptest.NewRequest("GET", "https://nobody.invalid/wp-login.php", nil)
	if w := serve(h, r); w.Code != 404 {
		t.Fatalf("404 bekleniyordu: %d", w.Code)
	}
	if n := h.ReqLog.Len(); n != 0 {
		t.Fatalf("bilinmeyen host 404 loglanmamali: %d", n)
	}
}

func TestRejectLog_WebSocketUpgradeDeniedByPolicy(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/ws"},"action":{"type":"deny","status":403}}]}`, 100)
	h := newRejectHandler(t, rejectRoute(), cp)
	r := plainReq("/ws")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	if w := serve(h, r); w.Code != 403 {
		t.Fatalf("403 bekleniyordu: %d", w.Code)
	}
	checkEntry(t, onlyEntry(t, h), 403, RejectPolicyDeny)
}

func TestRejectLog_PersisterAndEventsGetEntry(t *testing.T) {
	var got []reqlog.Entry
	p := reqlog.NewPersister(func(_ context.Context, b []reqlog.Entry) error {
		got = append(got, b...)
		return nil
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	h := newRejectHandler(t, rejectRoute())
	h.LogPersister = p
	serve(h, plainReq("/x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx) // kuyrugu bosaltip doner
	if len(got) != 1 || got[0].RejectReason != RejectClientOffline {
		t.Fatalf("persister kaydi: %+v", got)
	}
}

// --- hiz siniri ---

func TestRejectLog_RateLimitedPerTunnel(t *testing.T) {
	var logBuf bytes.Buffer
	h := newRejectHandler(t, rejectRoute())
	h.Log = slog.New(slog.NewTextHandler(&logBuf, nil))
	now := time.Now()
	h.rejectLim.now = func() time.Time { return now }

	for range 80 {
		serve(h, plainReq("/scan"))
	}
	if n := h.ReqLog.Len(); n != 50 {
		t.Fatalf("patlama siniri 50 kayit olmali, %d", n)
	}
	if logBuf.Len() != 0 {
		t.Fatalf("dakika dolmadan uyari yazilmamali: %s", logBuf.String())
	}

	// Bir dakika sonra: yeni istek hem token alir hem toplu uyariyi tetikler.
	now = now.Add(61 * time.Second)
	serve(h, plainReq("/scan"))
	if !strings.Contains(logBuf.String(), "atilan=30") {
		t.Fatalf("30 atilan kayit icin toplu uyari bekleniyordu: %q", logBuf.String())
	}
	if n := h.ReqLog.Len(); n != 51 {
		t.Fatalf("jeton yenilenince yeni kayit yazilmali: %d", n)
	}
}

func TestRejectLimiter_PerTunnelIsolation(t *testing.T) {
	var l rejectLimiter
	now := time.Now()
	l.now = func() time.Time { return now }
	for range 60 {
		l.allow("a")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("tunel a tukenmis olmali")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("tunel b etkilenmemeli")
	}
	// 1sn sonra ~50 jeton geri gelir.
	now = now.Add(time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("jeton yenilenmeli")
	}
}

func TestAccessRejectReason(t *testing.T) {
	cases := map[int]string{401: RejectAuthRequired, 302: RejectAuthRequired, 303: RejectAuthRequired,
		403: RejectAccessDenied, 429: RejectRateLimited, 200: RejectAccessDenied}
	for st, want := range cases {
		if got := accessRejectReason(st); got != want {
			t.Errorf("accessRejectReason(%d) = %q, beklenen %q", st, got, want)
		}
	}
}
