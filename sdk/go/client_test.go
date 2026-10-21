package zorven

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type captured struct {
	method, path, rawQuery string
	header                 http.Header
	body                   string
}

// newTestClient her istegi yakalayip verilen durum/govde ile yanitlar.
func newTestClient(t *testing.T, status int, resp string) (*Client, *captured) {
	t.Helper()
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.method, cap.path, cap.rawQuery, cap.header, cap.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(Options{BaseURL: srv.URL + "/api/v1/", Token: "zrv_api_x", Project: "proj1", UserAgent: "test/1", RetryBaseDelay: time.Millisecond})
	return c, cap
}

func (c *captured) expect(t *testing.T, method, path string) {
	t.Helper()
	if c.method != method || c.path != "/api/v1"+path {
		t.Fatalf("istek %s %s, beklenen %s /api/v1%s", c.method, c.path, method, path)
	}
}

func TestHeaders(t *testing.T) {
	c, cap := newTestClient(t, 200, `{"status":"ok","version":"1","connected_clients":3}`)
	h, err := c.Meta.Health(context.Background())
	if err != nil || h.Status != "ok" || h.ConnectedClients != 3 {
		t.Fatalf("health: %+v %v", h, err)
	}
	cap.expect(t, "GET", "/health")
	if g := cap.header.Get("Authorization"); g != "Bearer zrv_api_x" {
		t.Errorf("auth = %q", g)
	}
	if g := cap.header.Get("X-Zorven-Project"); g != "proj1" {
		t.Errorf("project = %q", g)
	}
	if ua := cap.header.Get("User-Agent"); !strings.HasPrefix(ua, "zorven-go/") || !strings.HasSuffix(ua, " test/1") {
		t.Errorf("ua = %q", ua)
	}
	if cap.header.Get("Content-Type") != "" {
		t.Errorf("govdesiz istekte content-type olmamali")
	}
}

func TestNoProjectHeaderWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["X-Zorven-Project"]; ok {
			t.Error("proje basligi gonderilmemeli")
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("token yokken auth gonderilmemeli")
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL})
	if _, err := c.Meta.Plans(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMetaAndSubscription(t *testing.T) {
	ctx := context.Background()
	c, cap := newTestClient(t, 200, `{"user":"u1"}`)
	me, err := c.Meta.Me(ctx)
	if err != nil || me["user"] != "u1" {
		t.Fatal(me, err)
	}
	cap.expect(t, "GET", "/me")

	c, cap = newTestClient(t, 200, `[{"id":"pro","name":"Pro","price_monthly":900,"max_clients":null,"max_tunnels":5,"has_api_access":true}]`)
	plans, err := c.Meta.Plans(ctx)
	if err != nil || len(plans) != 1 || plans[0].MaxClients != nil || *plans[0].MaxTunnels != 5 || !plans[0].HasAPIAccess {
		t.Fatalf("%+v %v", plans, err)
	}
	cap.expect(t, "GET", "/plans")

	c, cap = newTestClient(t, 200, `{"tenant_id":"t","plan":"pro","status":"active","current_period_end":"2026-11-01T00:00:00Z","plan_details":{"id":"pro"}}`)
	sub, err := c.Analytics.Subscription(ctx)
	if err != nil || sub.Plan != "pro" || sub.CurrentPeriodEnd == nil || sub.PlanDetails.ID != "pro" {
		t.Fatalf("%+v %v", sub, err)
	}
	cap.expect(t, "GET", "/subscription")
}

func TestClients(t *testing.T) {
	ctx := context.Background()
	c, cap := newTestClient(t, 200, `{"id":"cli_1","name":"ev","status":"online","created_at":"2026-01-02T03:04:05Z","last_seen_at":null}`)
	got, err := c.Clients.Get(ctx, "cli_1")
	if err != nil || got.ID != "cli_1" || got.Status != "online" || got.LastSeenAt != nil || got.CreatedAt.Year() != 2026 {
		t.Fatalf("%+v %v", got, err)
	}
	cap.expect(t, "GET", "/clients/cli_1")

	c, cap = newTestClient(t, 201, `{"client":{"id":"cli_2","name":"x"},"token":"zrv_live_abc"}`)
	cr, err := c.Clients.Create(ctx, "x")
	if err != nil || cr.Token != "zrv_live_abc" || cr.Client.ID != "cli_2" {
		t.Fatalf("%+v %v", cr, err)
	}
	cap.expect(t, "POST", "/clients")
	if cap.body != `{"name":"x"}` || cap.header.Get("Content-Type") != "application/json" {
		t.Errorf("govde %q ct %q", cap.body, cap.header.Get("Content-Type"))
	}

	c, cap = newTestClient(t, 200, `{"token":"zrv_live_new"}`)
	tok, err := c.Clients.RotateToken(ctx, "cli_2")
	if err != nil || tok != "zrv_live_new" {
		t.Fatal(tok, err)
	}
	cap.expect(t, "POST", "/clients/cli_2/token")

	c, cap = newTestClient(t, 204, ``)
	if err := c.Clients.Delete(ctx, "cli_2"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "DELETE", "/clients/cli_2")
}

func TestTunnels(t *testing.T) {
	ctx := context.Background()
	tun := `{"id":"tun_1","client_id":"cli_1","target":"http://localhost:8000","enabled":true,"hostnames":[{"id":"hn_1","fqdn":"a.zorven.app","type":"global","verified":true}]}`

	c, cap := newTestClient(t, 200, tun)
	got, err := c.Tunnels.Get(ctx, "tun_1")
	if err != nil || got.Hostnames[0].FQDN != "a.zorven.app" || !got.Enabled {
		t.Fatalf("%+v %v", got, err)
	}
	cap.expect(t, "GET", "/tunnels/tun_1")

	c, cap = newTestClient(t, 201, tun)
	if _, err := c.Tunnels.Create(ctx, CreateTunnelRequest{Name: "api", ClientID: "cli_1", Target: "http://localhost:8000"}); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "POST", "/tunnels")
	if cap.body != `{"name":"api","client_id":"cli_1","target":"http://localhost:8000"}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, tun)
	en := false
	if _, err := c.Tunnels.Update(ctx, "tun_1", UpdateTunnelRequest{Enabled: &en}); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "PATCH", "/tunnels/tun_1")
	if cap.body != `{"enabled":false}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 204, ``)
	if err := c.Tunnels.Delete(ctx, "tun_1"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "DELETE", "/tunnels/tun_1")
}

func TestDoor(t *testing.T) {
	ctx := context.Background()
	door := `{"tunnel_id":"tun_1","enabled":true,"duration_sec":43200,"durations":[3600,43200],"available":true,"connect_address":"zorven.app:10005","access_ready":true}`
	c, cap := newTestClient(t, 200, door)
	d, err := c.Tunnels.GetDoor(ctx, "tun_1")
	if err != nil || d.ConnectAddress != "zorven.app:10005" || len(d.Durations) != 2 {
		t.Fatalf("%+v %v", d, err)
	}
	cap.expect(t, "GET", "/tunnels/tun_1/door")

	c, cap = newTestClient(t, 200, door)
	if _, err := c.Tunnels.SetDoor(ctx, "tun_1", SetDoorRequest{Enabled: true, DurationSec: 3600}); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "PUT", "/tunnels/tun_1/door")
	if cap.body != `{"enabled":true,"duration_sec":3600}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, `[{"id":"g1","tunnel_id":"tun_1","ip":"1.2.3.4","expires_at":"2026-10-16T00:00:00Z"}]`)
	gr, err := c.Tunnels.ListDoorGrants(ctx, "tun_1")
	if err != nil || len(gr) != 1 || gr[0].IP != "1.2.3.4" {
		t.Fatal(gr, err)
	}
	cap.expect(t, "GET", "/tunnels/tun_1/door/grants")

	c, cap = newTestClient(t, 204, ``)
	if err := c.Tunnels.RevokeDoorGrant(ctx, "tun_1", "g1"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "DELETE", "/tunnels/tun_1/door/grants/g1")
}

func TestHostnames(t *testing.T) {
	ctx := context.Background()
	hn := `{"id":"hn_1","fqdn":"api.zorven.app","type":"global","verified":true,"suspended_reason":"plan_limit","suspended_at":"2026-10-01T00:00:00Z"}`

	c, cap := newTestClient(t, 201, hn)
	h, err := c.Hostnames.Create(ctx, CreateHostnameRequest{Name: "api", Kind: "short"})
	if err != nil || h.SuspendedReason != "plan_limit" || h.SuspendedAt == nil {
		t.Fatalf("%+v %v", h, err)
	}
	cap.expect(t, "POST", "/hostnames")
	if cap.body != `{"name":"api","kind":"short"}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 201, `{"id":"hn_2","fqdn":"api.musteri.com","type":"custom","verified":false,"instructions":{"txt":"abc"}}`)
	ch, err := c.Hostnames.CreateCustom(ctx, CreateCustomHostnameRequest{FQDN: "api.musteri.com", TunnelID: "tun_1"})
	if err != nil || ch.Instructions["txt"] != "abc" || ch.FQDN != "api.musteri.com" {
		t.Fatalf("%+v %v", ch, err)
	}
	cap.expect(t, "POST", "/hostnames/custom")
	if cap.body != `{"fqdn":"api.musteri.com","tunnel_id":"tun_1"}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, hn)
	if _, err := c.Hostnames.Attach(ctx, "hn_1", "tun_9"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "PATCH", "/hostnames/hn_1")
	if cap.body != `{"tunnel_id":"tun_9"}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, hn)
	if _, err := c.Hostnames.Detach(ctx, "hn_1"); err != nil {
		t.Fatal(err)
	}
	if cap.body != `{"tunnel_id":null}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, hn)
	if _, err := c.Hostnames.Verify(ctx, "hn_1"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "POST", "/hostnames/hn_1/verify")

	c, cap = newTestClient(t, 204, ``)
	if err := c.Hostnames.Delete(ctx, "hn_1"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "DELETE", "/hostnames/hn_1")
}

func TestIPAllowlist(t *testing.T) {
	ctx := context.Background()
	rule := `{"id":"r1","cidr":"203.0.113.0/24","tunnel_id":null,"enabled":true}`

	c, cap := newTestClient(t, 200, `[`+rule+`]`)
	rs, err := c.IPAllowlist.List(ctx)
	if err != nil || len(rs) != 1 || rs[0].CIDR != "203.0.113.0/24" || rs[0].TunnelID != nil {
		t.Fatal(rs, err)
	}
	cap.expect(t, "GET", "/ip-allowlist")

	c, cap = newTestClient(t, 201, rule)
	tid := "tun_1"
	if _, err := c.IPAllowlist.Create(ctx, CreateIPRuleRequest{CIDR: "203.0.113.0/24", TunnelID: &tid, Description: "ofis"}); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "POST", "/ip-allowlist")
	if cap.body != `{"cidr":"203.0.113.0/24","tunnel_id":"tun_1","description":"ofis"}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 200, rule)
	en := false
	if _, err := c.IPAllowlist.Update(ctx, "r1", UpdateIPRuleRequest{Enabled: &en}); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "PATCH", "/ip-allowlist/r1")
	if cap.body != `{"enabled":false}` {
		t.Errorf("govde %s", cap.body)
	}

	c, cap = newTestClient(t, 204, ``)
	if err := c.IPAllowlist.Delete(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	cap.expect(t, "DELETE", "/ip-allowlist/r1")
}

func TestAnalytics(t *testing.T) {
	ctx := context.Background()
	c, cap := newTestClient(t, 200, `[{"id":"req_1","tunnel_id":"tun_1","ts":"2026-10-15T10:00:00Z","method":"GET","path":"/x","status":404,"duration_ms":12}]`)
	logs, err := c.Analytics.Requests(ctx, &RequestLogQuery{
		Limit: 10, Offset: 5, TunnelID: "tun_1", Status: "2xx", Method: "GET", Hostname: "a.b", Q: "x y",
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil || len(logs) != 1 || logs[0].Status != 404 || logs[0].DurationMS != 12 {
		t.Fatal(logs, err)
	}
	cap.expect(t, "GET", "/requests")
	for _, want := range []string{"limit=10", "offset=5", "tunnel_id=tun_1", "status=2xx", "method=GET", "hostname=a.b", "q=x+y", "since=2026-10-01T00%3A00%3A00Z", "until=2026-10-02T00%3A00%3A00Z"} {
		if !strings.Contains(cap.rawQuery, want) {
			t.Errorf("sorguda %q yok: %s", want, cap.rawQuery)
		}
	}

	c, cap = newTestClient(t, 200, `[]`)
	if _, err := c.Analytics.Requests(ctx, nil); err != nil || cap.rawQuery != "" {
		t.Fatal(err, cap.rawQuery)
	}

	c, cap = newTestClient(t, 200, `{"window":"7d","total":10,"success":8,"failure":2,"by_method":{"basic":10},"unique_ips":3,"door_blocked":1}`)
	sum, err := c.Analytics.AccessEventSummary(ctx, "tun_1", "7d")
	if err != nil || sum.Total != 10 || sum.ByMethod["basic"] != 10 || sum.UniqueIPs != 3 || sum.DoorBlocked != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	cap.expect(t, "GET", "/tunnels/tun_1/access-events/summary")
	if cap.rawQuery != "window=7d" {
		t.Errorf("sorgu %s", cap.rawQuery)
	}

	c, cap = newTestClient(t, 200, `[{"id":"e1","tunnel_id":"tun_1","method":"oauth","success":false,"reason":"bad_credentials"}]`)
	pg, err := c.Analytics.AccessEvents(ctx, "tun_1", &ListOptions{Limit: 5})
	if err != nil || len(pg.Items) != 1 || pg.Items[0].Reason != "bad_credentials" {
		t.Fatal(pg, err)
	}
	cap.expect(t, "GET", "/tunnels/tun_1/access-events")
	if cap.rawQuery != "limit=5" {
		t.Errorf("sorgu %s", cap.rawQuery)
	}
}

func TestPathEscape(t *testing.T) {
	c, cap := newTestClient(t, 204, ``)
	if err := c.Clients.Delete(context.Background(), "a/b c"); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/api/v1/clients/a/b c" { // r.URL.Path cozulmus hali
		t.Errorf("yol %q", cap.path)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "rid-42")
		w.WriteHeader(403)
		io.WriteString(w, `{"error":{"code":"insufficient_scope","message":"clients:write gerekli"}}`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, Token: "t"})
	_, err := c.Clients.Create(context.Background(), "x")
	ae, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("APIError degil: %v", err)
	}
	if ae.Status != 403 || ae.Code != "insufficient_scope" || ae.Message != "clients:write gerekli" || ae.RequestID != "rid-42" {
		t.Errorf("%+v", ae)
	}
	if !strings.Contains(ae.Error(), "rid-42") || !strings.Contains(ae.Error(), "insufficient_scope") {
		t.Errorf("Error() = %s", ae.Error())
	}
}

func TestAPIErrorNonJSON(t *testing.T) {
	c, _ := newTestClient(t, 404, `not found page`)
	_, err := c.Tunnels.Get(context.Background(), "nope")
	ae, ok := AsAPIError(err)
	if !ok || ae.Status != 404 || !ae.IsNotFound() || ae.Message != "not found page" {
		t.Fatalf("%v", err)
	}
}

func TestRetry429RespectsRetryAfter(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"code":"rate_limited","message":"yavas"}}`)
			return
		}
		io.WriteString(w, `{"id":"cli_1","name":"a"}`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, RetryBaseDelay: time.Millisecond})
	// POST bile 429'da tekrarlanir
	req := func() error { _, err := c.Clients.Get(context.Background(), "cli_1"); return err }
	if err := req(); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestRetryAfterHeaderParsed(t *testing.T) {
	var n int32
	var first, second time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			first = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		second = time.Now()
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, RetryBaseDelay: time.Millisecond})
	if _, err := c.Meta.Plans(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := second.Sub(first); d < 900*time.Millisecond {
		t.Errorf("Retry-After 1sn beklenirdi, bekleme %v", d)
	}
}

func TestRetry5xxOnlyIdempotent(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"code":"unavailable","message":"x"}}`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, MaxRetries: 2, RetryBaseDelay: time.Millisecond})

	// GET: 1 + 2 deneme
	_, err := c.Meta.Plans(context.Background())
	if ae, ok := AsAPIError(err); !ok || ae.Status != 503 || n != 3 {
		t.Fatalf("GET n=%d err=%v", n, err)
	}
	// POST: 5xx'te tekrar yok
	atomic.StoreInt32(&n, 0)
	_, err = c.Clients.Create(context.Background(), "x")
	if _, ok := AsAPIError(err); !ok || n != 1 {
		t.Fatalf("POST n=%d err=%v", n, err)
	}
}

func TestRetryBodyReplayedOn429(t *testing.T) {
	var n int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `{"client":{"id":"c"},"token":"t"}`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, RetryBaseDelay: time.Millisecond})
	if _, err := c.Clients.Create(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[1] != `{"name":"x"}` {
		t.Errorf("govdeler %v", bodies)
	}
}

func TestRetriesDisabled(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, MaxRetries: -1})
	if _, err := c.Meta.Plans(context.Background()); err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(422)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, RetryBaseDelay: time.Millisecond})
	if _, err := c.Meta.Plans(context.Background()); err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestContextCancelStopsRetryWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Meta.Plans(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v sure=%v", err, time.Since(start))
	}
}

func TestRetryNetworkErrorIdempotent(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL, RetryBaseDelay: time.Millisecond})
	if _, err := c.Meta.Plans(context.Background()); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// pageServer cursor tabanli sayfalama taklit eder: 5 oge, sayfa boyutu limit.
func pageServer(t *testing.T, calls *[]string) *httptest.Server {
	items := []string{"a", "b", "c", "d", "e"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.RawQuery)
		limit := 2
		start := 0
		if r.URL.Query().Get("limit") != "200" && r.URL.Query().Get("limit") != "" {
			limit = atoi(r.URL.Query().Get("limit"))
		}
		if cur := r.URL.Query().Get("cursor"); cur != "" {
			start = atoi(strings.TrimPrefix(cur, "c"))
		}
		end := start + limit
		more := end < len(items)
		if end > len(items) {
			end = len(items)
		}
		var out []map[string]string
		for _, id := range items[start:end] {
			out = append(out, map[string]string{"id": id, "name": id})
		}
		if more {
			w.Header().Set("X-Has-More", "true")
			w.Header().Set("X-Next-Cursor", "c"+itoa(end))
		} else {
			w.Header().Set("X-Has-More", "false")
		}
		json.NewEncoder(w).Encode(out)
	}))
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
func itoa(n int) string { return string(rune('0' + n)) }

func TestListPageHeaders(t *testing.T) {
	var calls []string
	srv := pageServer(t, &calls)
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL})
	p, err := c.Clients.List(context.Background(), &ListOptions{Limit: 2})
	if err != nil || len(p.Items) != 2 || !p.HasMore || p.NextCursor != "c2" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = c.Clients.List(context.Background(), &ListOptions{Limit: 2, Cursor: "c4"})
	if err != nil || len(p.Items) != 1 || p.HasMore || p.NextCursor != "" {
		t.Fatalf("%+v %v", p, err)
	}
	if calls[1] != "cursor=c4&limit=2" {
		t.Errorf("sorgu %s", calls[1])
	}
}

func TestIterAndListAll(t *testing.T) {
	var calls []string
	srv := pageServer(t, &calls)
	defer srv.Close()
	c := NewClient(Options{BaseURL: srv.URL})
	ctx := context.Background()

	var ids []string
	for cl, err := range c.Clients.Iter(ctx, &ListOptions{Limit: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cl.ID)
	}
	if strings.Join(ids, "") != "abcde" || len(calls) != 3 {
		t.Fatalf("ids=%v calls=%v", ids, calls)
	}

	// erken break sonraki sayfayi cekmez
	calls = nil
	for _, err := range c.Tunnels.Iter(ctx, &ListOptions{Limit: 2}) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if len(calls) != 1 {
		t.Errorf("calls=%v", calls)
	}

	calls = nil
	all, err := c.Hostnames.ListAll(ctx, &ListOptions{Limit: 2})
	if err != nil || len(all) != 5 || len(calls) != 3 {
		t.Fatalf("%v %v %v", all, err, calls)
	}
	evs, err := collect(c.Analytics.IterAccessEvents(ctx, "t1", &ListOptions{Limit: 3}))
	if err != nil || len(evs) != 5 {
		t.Fatal(evs, err)
	}
}

func TestIterError(t *testing.T) {
	c, _ := newTestClient(t, 403, `{"error":{"code":"insufficient_scope","message":"m"}}`)
	var n int
	var gotErr error
	for _, err := range c.Clients.Iter(context.Background(), nil) {
		n++
		gotErr = err
	}
	if _, ok := AsAPIError(gotErr); !ok || n != 1 {
		t.Fatalf("n=%d err=%v", n, gotErr)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	c := NewClient(Options{})
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %s", c.baseURL)
	}
}

func TestConnectValidation(t *testing.T) {
	if _, err := Connect(context.Background(), ConnectOptions{}); err == nil {
		t.Error("token olmadan hata beklenir")
	}
	if _, err := Connect(context.Background(), ConnectOptions{Token: "t"}); err == nil {
		t.Error("hedefsiz hata beklenir")
	}
	if _, err := Connect(context.Background(), ConnectOptions{Token: "t", Port: 1, Bin: "/nonexistent/zorven"}); err == nil {
		t.Error("CLI yokken hata beklenir")
	}
}

func TestConnectFakeCLI(t *testing.T) {
	script := t.TempDir() + "/zorven"
	body := "#!/bin/sh\necho \"args: $@\"\necho 'zorven-url: https://x.zorven.app'\nexec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Connect(context.Background(), ConnectOptions{Token: "tok", Port: 8080, Bin: script, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://x.zorven.app" {
		t.Errorf("url %s", s.URL)
	}
	s.Close()
}
