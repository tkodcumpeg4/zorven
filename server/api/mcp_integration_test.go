package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/server/mcpserver"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type meStore struct{ *mockTokenStore }

func (meStore) GetTenant(_ context.Context, id string) (store.Tenant, error) {
	return store.Tenant{ID: id, Slug: "acme"}, nil
}

// TestMCP_EndToEndThroughRealMiddleware: SDK istemcisi -> /mcp -> GERCEK
// api.Middleware (token dogrulama, yuzey, kapsam) -> /me ve bir tunel ucu.
func TestMCP_EndToEndThroughRealMiddleware(t *testing.T) {
	mw, tok, _ := buildTokenMiddleware(t, []string{ScopeTunnelsRead})
	srv := &Server{Store: meStore{mw.Store.(*mockTokenStore)}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", srv.me)
	mux.HandleFunc("GET /api/v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, ScopeTunnelsRead) {
			return
		}
		writeJSON(w, 200, []map[string]any{{"id": "tun_1", "client_id": "c1"}})
	})
	mux.HandleFunc("POST /api/v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, ScopeTunnelsWrite) {
			return
		}
		writeJSON(w, 201, map[string]any{"id": "tun_2"})
	})
	mw.Next = mux

	h := mcpserver.New(mcpserver.Config{API: mw, Version: "t"})
	ts := httptest.NewServer(h)
	defer ts.Close()

	connect := func(token string) (*mcp.ClientSession, error) {
		c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
		return c.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint: ts.URL,
			HTTPClient: &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
				r = r.Clone(r.Context())
				r.Header.Set("Authorization", "Bearer "+token)
				return http.DefaultTransport.RoundTrip(r)
			})},
		}, nil)
	}

	// Gecersiz token: baglanti kurulamaz.
	if cs, err := connect("zrv_api_nope.secret"); err == nil {
		cs.Close()
		t.Fatal("gecersiz token ile baglanti kurulmamali")
	}

	cs, err := connect(tok)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, _ := cs.ListTools(context.Background(), nil)
	for _, tl := range tools.Tools {
		if tl.Name == "create_tunnel" {
			t.Error("salt-okunur token create_tunnel gormemeli")
		}
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tunnels"})
	if err != nil || res.IsError {
		t.Fatalf("list_tunnels: %v %+v", err, res)
	}
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami"})
	if err != nil || res.IsError {
		t.Fatalf("whoami: %v", err)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["scopes"] == nil || sc["tenant"] == nil {
		t.Errorf("whoami scopes/tenant eksik: %v", sc)
	}
}

func TestMCP_NewRoutesAreOnTokenSurface(t *testing.T) {
	for _, rt := range []tokenRoute{
		{"GET", "/api/v1/tunnels/t1/metrics"}, {"GET", "/api/v1/tunnels/t1/access-events"},
		{"GET", "/api/v1/tunnels/t1/door"}, {"PUT", "/api/v1/tunnels/t1/door"},
	} {
		if !tokenMayAccess(rt.method, rt.pattern) {
			t.Errorf("%s %s token yuzeyinde olmali", rt.method, rt.pattern)
		}
	}
	// Silme/izin iptali ve token yonetimi HALA kapali.
	for _, rt := range []tokenRoute{
		{"DELETE", "/api/v1/tunnels/t1/door/grants/g1"}, {"GET", "/api/v1/api-tokens"},
		{"POST", "/api/v1/tokens"}, {"GET", "/api/v1/secrets"},
	} {
		if tokenMayAccess(rt.method, rt.pattern) {
			t.Errorf("%s %s token yuzeyinde OLMAMALI", rt.method, rt.pattern)
		}
	}
	_ = strings.TrimSpace
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
