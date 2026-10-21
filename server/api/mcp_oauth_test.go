package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/mcpserver"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// oauthStore: me ucu icin kiraci + uyelik denetimi (mutable).
type oauthStore struct {
	*mockTokenStore
	mu      sync.Mutex
	members map[string]string // tenant|user -> rol
}

func (s *oauthStore) GetTenant(_ context.Context, id string) (store.Tenant, error) {
	return store.Tenant{ID: id, Slug: "acme"}, nil
}

func (s *oauthStore) GetMemberRole(_ context.Context, tenantID, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members[tenantID+"|"+userID], nil
}

type oauthEnv struct {
	t      *testing.T
	ts     *httptest.Server
	as     *oauthas.Server
	st     *oauthStore
	oauth  *oauthas.MemStore
	called map[string]int
}

func newOAuthEnv(t *testing.T, ent entitlements.EntitlementService) *oauthEnv {
	t.Helper()
	mw, _, _ := buildTokenMiddleware(t, nil)
	st := &oauthStore{mockTokenStore: mw.Store.(*mockTokenStore), members: map[string]string{"ten_x|usr_1": "owner"}}
	mw.Store = st
	if ent != nil {
		mw.Entitlements = ent
	}
	env := &oauthEnv{t: t, st: st, oauth: oauthas.NewMemStore(), called: map[string]int{}}
	srv := &Server{Store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", srv.me)
	mux.HandleFunc("GET /api/v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, ScopeTunnelsRead) {
			return
		}
		env.called["list_tunnels"]++
		writeJSON(w, 200, []map[string]any{{"id": "tun_1", "client_id": "c1"}})
	})
	mux.HandleFunc("POST /api/v1/tunnels", func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, ScopeTunnelsWrite) {
			return
		}
		env.called["create_tunnel"]++
		writeJSON(w, 201, map[string]any{"id": "tun_2"})
	})
	mw.Next = mux

	as, err := oauthas.New(oauthas.Config{
		Store: env.oauth, SigningSeed: bytes.Repeat([]byte{3}, 32),
		Session: func(r *http.Request) (oauthas.Session, bool) {
			if u := r.Header.Get("X-Test-User"); u != "" {
				return oauthas.Session{UserID: u}, true
			}
			return oauthas.Session{}, false
		},
		Orgs: func(context.Context, string) ([]oauthas.Org, error) {
			return []oauthas.Org{{ID: "ten_x", Name: "X", Role: "owner"}}, nil
		},
		OriginTrusted:   func(r *http.Request) bool { return r.Header.Get("Origin") != "" },
		RegisterLimiter: ratelimit.New(1000, 1000),
	})
	if err != nil {
		t.Fatal(err)
	}
	env.as = as
	h := mcpserver.New(mcpserver.Config{API: mw, Version: "t", OAuth: as})
	top := http.NewServeMux()
	top.Handle("/mcp", h)
	top.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if oauthas.Match(r.URL.Path) {
			as.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	env.ts = httptest.NewServer(top)
	t.Cleanup(env.ts.Close)
	return env
}

// token, tam OAuth akisini yurutur ve access token ile grant kimligini doner.
func (e *oauthEnv) token(scope string) (access, refresh, clientID string) {
	e.t.Helper()
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirect := "http://localhost:6274/callback"
	reg, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "client_name": "Claude Test"})
	resp, err := cl.Post(e.ts.URL+"/oauth/register", "application/json", bytes.NewReader(reg))
	if err != nil || resp.StatusCode != 201 {
		e.t.Fatalf("register: %v %v", err, resp)
	}
	var rc map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&rc)
	clientID = rc["client_id"].(string)

	verifier := strings.Repeat("k9", 30)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	resource := e.ts.URL + "/mcp"
	dec, _ := json.Marshal(map[string]any{"client_id": clientID, "redirect_uri": redirect, "response_type": "code",
		"code_challenge": challenge, "code_challenge_method": "S256", "state": "s", "scope": scope,
		"resource": resource, "decision": "allow", "tenant_id": "ten_x"})
	req, _ := http.NewRequest("POST", e.ts.URL+"/oauth/consent/decision", bytes.NewReader(dec))
	req.Header.Set("X-Test-User", "usr_1")
	req.Header.Set("Origin", "https://panel.example")
	resp, err = cl.Do(req)
	if err != nil || resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("decision: %v %d %s", err, resp.StatusCode, b)
	}
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	u, _ := url.Parse(out["redirect"])
	form := url.Values{"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {resource}}
	resp, err = cl.PostForm(e.ts.URL+"/oauth/token", form)
	if err != nil || resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("token: %v %d %s", err, resp.StatusCode, b)
	}
	var tk map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tk)
	return tk["access_token"].(string), tk["refresh_token"].(string), clientID
}

func (e *oauthEnv) connect(tok string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: e.ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+tok)
			return http.DefaultTransport.RoundTrip(r)
		})},
	}, nil)
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]bool{}
	for _, tl := range res.Tools {
		m[tl.Name] = true
	}
	return m
}

func TestMCPOAuth_ReadScopeListsAndCalls(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	at, _, _ := env.token("mcp:read")
	cs, err := env.connect(at)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	names := toolNames(t, cs)
	if !names["list_tunnels"] || !names["whoami"] {
		t.Errorf("okuma araclari gorunmeli: %v", names)
	}
	if names["create_tunnel"] || names["update_tunnel"] {
		t.Errorf("mcp:read yazma araclarini GORMEMELI: %v", names)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tunnels"})
	if err != nil || res.IsError || env.called["list_tunnels"] != 1 {
		t.Fatalf("list_tunnels: %v %+v", err, res)
	}
	// Yazma araci zorlansa bile hata (arac yok / kapsam yok), REST'e ulasmaz.
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_tunnel", Arguments: map[string]any{"name": "x", "device_id": "c1", "target": "http://127.0.0.1:1"}}); err == nil && !res.IsError {
		t.Error("mcp:read ile create_tunnel calismamali")
	}
	if env.called["create_tunnel"] != 0 {
		t.Error("yazma handler'i cagrilmamali")
	}
	// whoami: kapsamlar ve kiraci OAuth kimliginden gelir.
	res, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami"})
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["tenant"] == nil || sc["scopes"] == nil {
		t.Errorf("whoami: %v", sc)
	}
}

func TestMCPOAuth_ManageScopeCanWrite(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	at, _, _ := env.token("mcp:manage")
	cs, err := env.connect(at)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if !toolNames(t, cs)["create_tunnel"] {
		t.Fatal("mcp:manage create_tunnel gormeli")
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_tunnel", Arguments: map[string]any{"name": "x", "device_id": "c1", "target": "http://127.0.0.1:1"}})
	if err != nil || res.IsError || env.called["create_tunnel"] != 1 {
		t.Fatalf("create_tunnel: %v %+v", err, res)
	}
}

func TestMCPOAuth_RejectsAndChallenge(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	post := func(tok string) *http.Response {
		req, _ := http.NewRequest("POST", env.ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// Token yok: 401 + resource_metadata.
	resp := post("")
	wa := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != 401 || !strings.HasPrefix(wa, "Bearer ") || !strings.Contains(wa, `resource_metadata="`+env.ts.URL+`/.well-known/oauth-protected-resource"`) {
		t.Fatalf("401 basligi: %d %q", resp.StatusCode, wa)
	}
	// Gecersiz JWT: invalid_token + resource_metadata.
	resp = post("aaa.bbb.ccc")
	wa = resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != 401 || !strings.Contains(wa, `error="invalid_token"`) || !strings.Contains(wa, "resource_metadata=") {
		t.Fatalf("gecersiz token: %d %q", resp.StatusCode, wa)
	}
	// Admin/istemci anahtari biçimi hala reddedilir.
	resp = post("zrv_admin_x.y")
	if resp.StatusCode != 401 {
		t.Fatalf("admin anahtari reddedilmeli: %d", resp.StatusCode)
	}
	// PRM belgesi aynı sunucuda.
	pr, _ := http.Get(env.ts.URL + "/.well-known/oauth-protected-resource")
	var prm map[string]any
	_ = json.NewDecoder(pr.Body).Decode(&prm)
	if prm["resource"] != env.ts.URL+"/mcp" {
		t.Errorf("PRM: %v", prm)
	}
}

func TestMCPOAuth_RevokedGrantMemberRemovedAndPlan(t *testing.T) {
	// Iptal edilen grant.
	env := newOAuthEnv(t, allowEntitlements{})
	at, rt, cid := env.token("mcp:read")
	if cs, err := env.connect(at); err != nil {
		t.Fatal(err)
	} else {
		cs.Close()
	}
	resp, err := http.PostForm(env.ts.URL+"/oauth/revoke", url.Values{"token": {rt}, "client_id": {cid}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("revoke: %v", err)
	}
	if cs, err := env.connect(at); err == nil {
		cs.Close()
		t.Fatal("iptal edilen grant ile baglanti kurulmamali")
	}

	// Kullanici organizasyondan cikarildi: token (suresi dolmasa da) gecmez.
	env2 := newOAuthEnv(t, allowEntitlements{})
	at2, _, _ := env2.token("mcp:read")
	env2.st.mu.Lock()
	delete(env2.st.members, "ten_x|usr_1")
	env2.st.mu.Unlock()
	if cs, err := env2.connect(at2); err == nil {
		cs.Close()
		t.Fatal("uyeligi biten kullanicinin token'i gecmemeli")
	}

	// Plan API erisimi vermiyor: REST ile ayni 403.
	env3 := newOAuthEnv(t, denyEntitlements{})
	at3, _, _ := env3.token("mcp:read")
	req, _ := http.NewRequest("POST", env3.ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Authorization", "Bearer "+at3)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	r3, _ := http.DefaultClient.Do(req)
	if r3.StatusCode != 403 {
		t.Fatalf("plan kapisi: %d", r3.StatusCode)
	}
}

type denyEntitlements struct {
	entitlements.EntitlementService
}

func (denyEntitlements) CheckFeature(context.Context, string, entitlements.Feature) error {
	return context.DeadlineExceeded
}
