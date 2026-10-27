package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// mcpFakeStore, MCP tunel uclarinin ihtiyac duydugu depo yuzeyi.
type mcpFakeStore struct {
	store.Store
	mu    sync.Mutex
	tuns  map[string]store.Tunnel // id -> tunel (tenant ten_a)
	mcp   map[string]store.TunnelMCP
	keys  []store.MCPKey
	calls []store.MCPCall
}

func (f *mcpFakeStore) GetTunnel(_ context.Context, tenantID, id string) (store.Tunnel, error) {
	t, ok := f.tuns[id]
	if !ok || t.TenantID != tenantID {
		return store.Tunnel{}, store.ErrNotFound
	}
	return t, nil
}
func (f *mcpFakeStore) GetTunnelMCP(_ context.Context, tenantID, id string) (store.TunnelMCP, error) {
	if _, err := f.GetTunnel(context.Background(), tenantID, id); err != nil {
		return store.TunnelMCP{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.mcp[id]; ok {
		return m, nil
	}
	return store.TunnelMCP{TunnelID: id, Config: json.RawMessage("{}")}, nil
}
func (f *mcpFakeStore) SetTunnelMCP(_ context.Context, tenantID string, m store.TunnelMCP) error {
	if _, err := f.GetTunnel(context.Background(), tenantID, m.TunnelID); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcp[m.TunnelID] = m
	return nil
}
func (f *mcpFakeStore) ListHostnames(context.Context, string) ([]store.Hostname, error) {
	return []store.Hostname{
		{ID: "h1", TunnelID: "tun_1", FQDN: "app--acme.zorven.app", Type: store.HostTypeScoped},
		{ID: "h2", TunnelID: "tun_1", FQDN: "pending.example.com", Type: store.HostTypeCustom, Verified: false},
	}, nil
}
func (f *mcpFakeStore) CreateTunnelMCPKey(_ context.Context, tenantID string, k store.MCPKey) error {
	if _, err := f.GetTunnel(context.Background(), tenantID, k.TunnelID); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
	return nil
}
func (f *mcpFakeStore) ListTunnelMCPKeys(_ context.Context, _, id string) ([]store.MCPKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.MCPKey{}
	for _, k := range f.keys {
		if k.TunnelID == id {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *mcpFakeStore) DeleteTunnelMCPKey(_ context.Context, _, id, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, k := range f.keys {
		if k.ID == keyID && k.TunnelID == id {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}
func (f *mcpFakeStore) ListMCPCalls(_ context.Context, _, _ string, flt store.MCPCallFilter) ([]store.MCPCall, error) {
	return f.calls, nil
}
func (f *mcpFakeStore) MCPToolStats(context.Context, string, string, time.Time) ([]store.MCPToolStat, error) {
	return []store.MCPToolStat{{Tool: "echo", Calls: 10, Errors: 2, Denied: 1}, {Tool: "x", Calls: 5}}, nil
}

func mcpTestServer(t *testing.T, oauth bool, tenant string) (*httptest.Server, *mcpFakeStore, *int) {
	t.Helper()
	fs := &mcpFakeStore{tuns: map[string]store.Tunnel{
		"tun_1": {ID: "tun_1", TenantID: "ten_a", Proto: store.ProtoHTTP},
		"tun_t": {ID: "tun_t", TenantID: "ten_a", Proto: store.ProtoTCP},
		"tun_b": {ID: "tun_b", TenantID: "ten_b", Proto: store.ProtoHTTP},
	}, mcp: map[string]store.TunnelMCP{}}
	changed := 0
	s := &Server{Store: fs, MCPOAuthReady: oauth, OnTunnelChange: func() { changed++ }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tunnels/{id}/mcp", s.getTunnelMCP)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/mcp", s.setTunnelMCP)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/mcp/calls", s.listMCPCalls)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/mcp/stats", s.getMCPStats)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/mcp/keys", s.listMCPKeys)
	mux.HandleFunc("POST /api/v1/tunnels/{id}/mcp/keys", s.createMCPKey)
	mux.HandleFunc("DELETE /api/v1/tunnels/{id}/mcp/keys/{keyId}", s.deleteMCPKey)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(withTenant(r.Context(), tenant)))
	}))
	t.Cleanup(ts.Close)
	return ts, fs, &changed
}

func mcpReq(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(buf.Bytes(), &m)
	return resp.StatusCode, m, buf.String()
}

func TestMCPTunnelConfigValidationAndTenantIsolation(t *testing.T) {
	ts, fs, changed := mcpTestServer(t, true, "ten_a")

	st, got, _ := mcpReq(t, ts, "GET", "/api/v1/tunnels/tun_1/mcp", "")
	if st != 200 || got["enabled"] != false {
		t.Fatalf("varsayilan: %d %v", st, got)
	}
	if urls, _ := got["urls"].([]any); len(urls) != 1 || urls[0] != "https://app--acme.zorven.app/mcp" {
		t.Fatalf("urls (dogrulanmamis ozel ad dahil edilmemeli): %v", got["urls"])
	}
	if cfg, _ := got["config"].(map[string]any); cfg["auth"] != "oauth" {
		t.Fatalf("varsayilan auth oauth olmali: %v", cfg)
	}

	st, got, _ = mcpReq(t, ts, "PUT", "/api/v1/tunnels/tun_1/mcp",
		`{"enabled":true,"config":{"allow_tools":["echo","db_*"],"deny_tools":["db_drop"],"rate_limit_per_min":30}}`)
	if st != 200 || *changed != 1 {
		t.Fatalf("kayit: %d %v changed=%d", st, got, *changed)
	}
	if !fs.mcp["tun_1"].Enabled || !strings.Contains(string(fs.mcp["tun_1"].Config), `"db_drop"`) {
		t.Fatalf("kaydedilen: %+v", fs.mcp["tun_1"])
	}

	bad := []string{
		`{"enabled":true,"config":{"auth":"none"}}`,                // ack yok
		`{"enabled":true,"config":{"auth":"sifre"}}`,               // bilinmeyen
		`{"enabled":true,"config":{"path":"/a/../b"}}`,             // yol
		`{"enabled":true,"config":{"head_timeout_sec":100000}}`,    // sinir
		`{"enabled":true,"config":{"allow_tools":["bosluk var"]}}`, // desen
		`{"enabled":true,"config":{"rate_limit_per_min":-5}}`,      // negatif
	}
	for _, b := range bad {
		if st, _, body := mcpReq(t, ts, "PUT", "/api/v1/tunnels/tun_1/mcp", b); st != 422 {
			t.Errorf("%s -> %d %s", b, st, body)
		}
	}
	// HTTP olmayan tunel
	if st, _, _ := mcpReq(t, ts, "PUT", "/api/v1/tunnels/tun_t/mcp", `{"enabled":true,"config":{}}`); st != 422 {
		t.Errorf("tcp tunel: %d", st)
	}
	// Baska kiracinin tuneli: 404 (hicbir islem yapilamaz)
	for _, c := range [][2]string{{"GET", "/api/v1/tunnels/tun_b/mcp"}, {"PUT", "/api/v1/tunnels/tun_b/mcp"},
		{"GET", "/api/v1/tunnels/tun_b/mcp/calls"}, {"GET", "/api/v1/tunnels/tun_b/mcp/stats"},
		{"GET", "/api/v1/tunnels/tun_b/mcp/keys"}, {"POST", "/api/v1/tunnels/tun_b/mcp/keys"}} {
		if st, _, _ := mcpReq(t, ts, c[0], c[1], `{"enabled":true,"label":"x","config":{}}`); st != 404 {
			t.Errorf("%s %s: %d (404 beklenir)", c[0], c[1], st)
		}
	}
	if _, ok := fs.mcp["tun_b"]; ok || len(fs.keys) != 0 {
		t.Fatal("baska kiraciya yazildi")
	}
}

func TestMCPTunnelOAuthRequiresAS(t *testing.T) {
	ts, _, _ := mcpTestServer(t, false, "ten_a")
	if st, _, _ := mcpReq(t, ts, "PUT", "/api/v1/tunnels/tun_1/mcp", `{"enabled":true,"config":{"auth":"oauth"}}`); st != 422 {
		t.Fatalf("AS yokken oauth etkinlestirildi: %d", st)
	}
	if st, _, _ := mcpReq(t, ts, "PUT", "/api/v1/tunnels/tun_1/mcp", `{"enabled":true,"config":{"auth":"apikey"}}`); st != 200 {
		t.Fatalf("apikey: %d", st)
	}
}

func TestMCPKeysLifecycleNoSecretLeak(t *testing.T) {
	ts, fs, changed := mcpTestServer(t, true, "ten_a")
	st, got, _ := mcpReq(t, ts, "POST", "/api/v1/tunnels/tun_1/mcp/keys", `{"label":"ci","expires_in_days":30}`)
	if st != 201 {
		t.Fatalf("olustur: %d %v", st, got)
	}
	key, _ := got["key"].(string)
	if !strings.HasPrefix(key, "zmk_") || len(key) < 30 || got["prefix"] != key[:8] {
		t.Fatalf("anahtar bicimi: %v", got)
	}
	if got["expires_at"] == nil || *changed != 1 {
		t.Fatalf("expires/changed: %v %d", got["expires_at"], *changed)
	}
	// Depoda ham anahtar YOK, yalniz SHA-256 ozeti.
	if fs.keys[0].KeyHash == key || len(fs.keys[0].KeyHash) != 64 {
		t.Fatalf("depolanan: %+v", fs.keys[0])
	}
	// Listede ham anahtar/ozet donmez.
	_, _, body := mcpReq(t, ts, "GET", "/api/v1/tunnels/tun_1/mcp/keys", "")
	if strings.Contains(body, key) || strings.Contains(body, fs.keys[0].KeyHash) || !strings.Contains(body, `"label":"ci"`) {
		t.Fatalf("liste: %s", body)
	}
	// Dogrulama
	for _, b := range []string{`{"label":""}`, `{"label":"x","expires_in_days":99999}`, `{"label":"a\nb"}`} {
		if st, _, _ := mcpReq(t, ts, "POST", "/api/v1/tunnels/tun_1/mcp/keys", b); st != 422 {
			t.Errorf("%s: %d", b, st)
		}
	}
	// Silme
	if st, _, _ := mcpReq(t, ts, "DELETE", "/api/v1/tunnels/tun_1/mcp/keys/"+fs.keys[0].ID, ""); st != 204 {
		t.Fatalf("sil: %d", st)
	}
	if st, _, _ := mcpReq(t, ts, "DELETE", "/api/v1/tunnels/tun_1/mcp/keys/yok", ""); st != 404 {
		t.Fatalf("olmayan: %d", st)
	}
}

func TestMCPCallsAndStats(t *testing.T) {
	ts, fs, _ := mcpTestServer(t, true, "ten_a")
	fs.calls = []store.MCPCall{{TunnelID: "tun_1", Method: "tools/call", Tool: "echo", Status: "ok"}}
	st, _, body := mcpReq(t, ts, "GET", "/api/v1/tunnels/tun_1/mcp/calls?status=ok", "")
	if st != 200 || !strings.Contains(body, `"tool":"echo"`) {
		t.Fatalf("calls: %d %s", st, body)
	}
	if st, _, _ := mcpReq(t, ts, "GET", "/api/v1/tunnels/tun_1/mcp/calls?status=bogus", ""); st != 422 {
		t.Fatalf("gecersiz status: %d", st)
	}
	st, got, _ := mcpReq(t, ts, "GET", "/api/v1/tunnels/tun_1/mcp/stats?window=7d", "")
	tot, _ := got["totals"].(map[string]any)
	if st != 200 || got["window"] != "7d" || tot["calls"] != float64(15) || tot["errors"] != float64(2) || tot["denied"] != float64(1) {
		t.Fatalf("stats: %d %v", st, got)
	}
}

func TestMCPKeyAndSessionRoutesNotInTokenSurface(t *testing.T) {
	for _, r := range []struct{ m, p string }{
		{"GET", "/api/v1/tunnels/t1/mcp/keys"}, {"POST", "/api/v1/tunnels/t1/mcp/keys"},
		{"DELETE", "/api/v1/tunnels/t1/mcp/keys/k1"}, {"POST", "/api/v1/tunnels/t1/mcp/test"},
		{"POST", "/api/v1/tunnels/t1/mcp/revoke-sessions"},
	} {
		if tokenMayAccess(r.m, r.p) {
			t.Errorf("%s %s token yuzeyinde olmamali", r.m, r.p)
		}
	}
	for _, r := range []struct{ m, p string }{
		{"GET", "/api/v1/tunnels/t1/mcp"}, {"PUT", "/api/v1/tunnels/t1/mcp"},
		{"GET", "/api/v1/tunnels/t1/mcp/calls"}, {"GET", "/api/v1/tunnels/t1/mcp/stats"},
	} {
		if !tokenMayAccess(r.m, r.p) {
			t.Errorf("%s %s token yuzeyinde olmali", r.m, r.p)
		}
	}
}

func TestRequireScopeAnyDeleteScopes(t *testing.T) {
	check := func(scopes []string) bool {
		r := httptest.NewRequest("DELETE", "/x", nil)
		r = r.WithContext(withAPIScopes(r.Context(), scopes))
		return requireScopeAny(httptest.NewRecorder(), r, ScopeTunnelsWrite, ScopeTunnelsDelete)
	}
	if !check([]string{"tunnels:write"}) || !check([]string{"tunnels:delete"}) || !check([]string{"admin"}) {
		t.Fatal("izinli kapsamlar reddedildi")
	}
	if check([]string{"tunnels:read"}) || check([]string{"hostnames:delete"}) || check([]string{"analytics:read"}) {
		t.Fatal("yetkisiz kapsam gecti")
	}
	// tunnels:delete token'i yazma (PATCH) yapamaz
	r := httptest.NewRequest("PATCH", "/x", nil)
	r = r.WithContext(withAPIScopes(r.Context(), []string{"tunnels:delete"}))
	if requireScope(httptest.NewRecorder(), r, ScopeTunnelsWrite) {
		t.Fatal("tunnels:delete yazma yetkisi verdi")
	}
}
