package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
)

const goodToken = "zrv_api_abc.secret"

// fakeAPI, api.Middleware + mux taklidi: bilinen token'i kabul eder, /me
// kapsamlari doner, digerlerini kaydeder.
type fakeAPI struct {
	mu     sync.Mutex
	scopes []string
	seen   []string
	auths  []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+goodToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_token","message":"x"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true, "tenant": map[string]any{"id": "ten_1", "slug": "acme"},
			"scopes": f.scopes,
		})
	case "/api/v1/tunnels":
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"tun_1","name":"web"}`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":"tun_1","client_id":"c1"}]`))
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

type auditRec struct{ tenant, actor, action, target, detail string }

func setup(t *testing.T, scopes []string, origin func(*http.Request) bool) (*httptest.Server, *fakeAPI, *[]auditRec) {
	t.Helper()
	api := &fakeAPI{scopes: scopes}
	var mu sync.Mutex
	var audits []auditRec
	h := New(Config{
		API: api, OriginTrusted: origin, FailLimiter: ratelimit.New(1000, 1000), Version: "test",
		Audit: func(tenant, actor, ip, action, target, detail string) {
			mu.Lock()
			defer mu.Unlock()
			audits = append(audits, auditRec{tenant, actor, action, target, detail})
		},
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, api, &audits
}

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.tok != "" {
		r.Header.Set("Authorization", "Bearer "+b.tok)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func session(t *testing.T, url, tok string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok}},
	}, nil)
	if err != nil {
		t.Fatalf("baglanti: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestHTTPRoundTrip_ListAndCall(t *testing.T) {
	ts, api, audits := setup(t, []string{"tunnels:write", "tunnels:read"}, nil)
	cs := session(t, ts.URL, goodToken)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
	}
	if !names["create_tunnel"] || !names["list_tunnels"] || !names["whoami"] {
		t.Errorf("beklenen araclar yok: %v", names)
	}
	if names["add_domain"] || names["list_requests"] || names["list_devices"] {
		t.Errorf("kapsam disi araclar listelendi: %v", names)
	}

	out, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_tunnel", Arguments: map[string]any{"name": "web", "device_id": "c1", "target": "http://localhost:3000"}})
	if err != nil || out.IsError {
		t.Fatalf("cagri: %v %+v", err, out)
	}
	// Arka uca cagiranin Authorization'i aynen gitti.
	for _, a := range api.auths {
		if a != "Bearer "+goodToken {
			t.Errorf("sentetik istek yanlis Authorization: %q", a)
		}
	}
	// Denetim: arac adi + durum, kiraci dolu, girdi yok.
	if len(*audits) != 1 {
		t.Fatalf("denetim kaydi: %v", *audits)
	}
	a := (*audits)[0]
	if a.tenant != "ten_1" || a.action != "mcp.tool" || a.target != "create_tunnel" ||
		!strings.Contains(a.detail, "status=201") || strings.Contains(a.detail, "localhost") {
		t.Errorf("denetim: %+v", a)
	}
}

func TestReadOnlyTokenCannotSeeOrCallWriteTools(t *testing.T) {
	ts, _, _ := setup(t, []string{"tunnels:read"}, nil)
	cs := session(t, ts.URL, goodToken)
	res, _ := cs.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		if tl.Name == "create_tunnel" || tl.Name == "open_door" {
			t.Errorf("yazma araci gorunuyor: %s", tl.Name)
		}
	}
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_tunnel", Arguments: map[string]any{}})
	if err == nil {
		t.Error("kayitsiz arac cagrisi reddedilmeli")
	}
}

func post(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", url+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAuth_MissingAndInvalid401(t *testing.T) {
	ts, _, _ := setup(t, []string{"*"}, nil)

	resp := post(t, ts.URL, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("token yok: %d", resp.StatusCode)
	}
	wa := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(wa, "Bearer") || strings.Contains(wa, "error=") {
		t.Errorf("token yok: WWW-Authenticate=%q (RFC 6750: hata kodu olmamali)", wa)
	}

	resp = post(t, ts.URL, map[string]string{"Authorization": "Bearer zrv_api_bad.token"})
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("gecersiz token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// API token olmayan (admin anahtari / oturum) bearer reddedilir, arka uca gitmez.
	resp = post(t, ts.URL, map[string]string{"Authorization": "Bearer zrv_admin_x.y"})
	if resp.StatusCode != 401 {
		t.Errorf("admin anahtari kabul edilmemeli: %d", resp.StatusCode)
	}
	// Sorgu dizgesinde token kabul edilmez.
	req, _ := http.NewRequest("POST", ts.URL+"/mcp?access_token="+goodToken, strings.NewReader(`{}`))
	qr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	qr.Body.Close()
	if qr.StatusCode != 401 {
		t.Errorf("query token: %d", qr.StatusCode)
	}
}

func TestOriginAndCORS(t *testing.T) {
	ts, _, _ := setup(t, []string{"*"}, func(r *http.Request) bool {
		return r.Header.Get("Origin") == "https://zorven.app"
	})
	resp := post(t, ts.URL, map[string]string{"Authorization": "Bearer " + goodToken, "Origin": "https://evil.example"})
	if resp.StatusCode != 403 {
		t.Errorf("yabanci Origin: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/mcp", nil)
	req.Header.Set("Origin", "https://zorven.app")
	req.Header.Set("Access-Control-Request-Method", "POST")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer pre.Body.Close()
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "https://zorven.app" ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("preflight: %d %v", pre.StatusCode, pre.Header)
	}
	// Origin'siz istek (CLI) gecer.
	resp = post(t, ts.URL, map[string]string{"Authorization": "Bearer " + goodToken})
	if resp.StatusCode != 200 {
		t.Errorf("origin'siz: %d", resp.StatusCode)
	}
}

func TestFailLimiter(t *testing.T) {
	api := &fakeAPI{}
	h := New(Config{API: api, FailLimiter: ratelimit.New(0.001, 2)})
	ts := httptest.NewServer(h)
	defer ts.Close()
	codes := []int{}
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if codes[len(codes)-1] != 429 {
		t.Errorf("basarisiz deneme siniri isletilmedi: %v", codes)
	}
}

func TestProjectHeaderForwarded(t *testing.T) {
	ts, api, _ := setup(t, []string{"*"}, nil)
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+goodToken)
			r.Header.Set("X-Zorven-Project", "staging")
			return http.DefaultTransport.RoundTrip(r)
		})},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tunnels", Arguments: map[string]any{"project_id": "prod"}}); err != nil {
		t.Fatal(err)
	}
	_ = api // proje basligi fakeAPI'de kaydedilmiyor; asagidaki test dogrudan backend'i dener
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInprocBackendProjectPrecedence(t *testing.T) {
	var got string
	be := &inprocBackend{
		api: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("X-Zorven-Project") + "|" + r.Header.Get("Authorization") + "|" + r.Host
			w.Write([]byte(`{}`))
		}),
		auth: "Bearer t", host: "zorven.app", project: "hdr", remote: "1.2.3.4:5",
	}
	_, _ = be.Do(context.Background(), reqOf("GET", "/x", ""))
	if got != "hdr|Bearer t|zorven.app" {
		t.Errorf("varsayilan proje: %s", got)
	}
	_, _ = be.Do(context.Background(), reqOf("GET", "/x", "arg"))
	if got != "arg|Bearer t|zorven.app" {
		t.Errorf("arac argumani onceligi: %s", got)
	}
}
