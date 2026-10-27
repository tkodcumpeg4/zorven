package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/mcpconf"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- sahte ajan --------------------------------------------------------------

// agentReq, ajanin aldigi tek HTTP istegi.
type agentReq struct {
	req  protocol.HTTPRequest
	body []byte
}

// agentResp, sahte ajanin donecegi yanit. chunks arasinda gate kanali beklenebilir.
type agentResp struct {
	status  int
	headers map[string][]string
	chunks  [][]byte
	// gate != nil ise ilk parcadan sonra bu kanal kapanana kadar beklenir.
	gate chan struct{}
}

type fakeAgent struct {
	t       *testing.T
	agent   *websocket.Conn
	mu      sync.Mutex
	reqs    []agentReq
	handler func(agentReq) agentResp
	cancels chan uint64
}

func (a *fakeAgent) requests() []agentReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]agentReq(nil), a.reqs...)
}

func newFakeAgent(t *testing.T, hub *tunnel.Hub, clientID string, handler func(agentReq) agentResp) *fakeAgent {
	t.Helper()
	ready := make(chan *tunnel.Session, 1)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		s := tunnel.NewSession("s-"+clientID, clientID, "agent", "127.0.0.1:1", c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ready <- s
		_ = s.Run(ctx)
	}))
	t.Cleanup(srv.Close)
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ac.SetReadLimit(protocol.WSReadLimit)
	s := <-ready
	if err := hub.Register(s); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAgent{t: t, agent: ac, handler: handler, cancels: make(chan uint64, 16)}
	t.Cleanup(func() { cancel(); ac.CloseNow() })
	go fa.loop(ctx)
	return fa
}

func (a *fakeAgent) loop(ctx context.Context) {
	pending := map[uint64]*agentReq{}
	for {
		typ, data, err := a.agent.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			pt, _ := protocol.PeekType(data)
			switch pt {
			case protocol.TypeHTTPRequest:
				var m protocol.HTTPRequest
				_ = json.Unmarshal(data, &m)
				ar := &agentReq{req: m}
				pending[m.ReqID] = ar
				if !m.HasBody {
					a.dispatch(ctx, ar)
					delete(pending, m.ReqID)
				}
			case protocol.TypeCancel:
				var m protocol.Cancel
				_ = json.Unmarshal(data, &m)
				select {
				case a.cancels <- m.ReqID:
				default:
				}
			}
			continue
		}
		f, err := protocol.DecodeBodyFrame(data)
		if err != nil || f.FrameType != protocol.FrameRequestBody {
			continue
		}
		ar := pending[f.ReqID]
		if ar == nil {
			continue
		}
		ar.body = append(ar.body, f.Payload...)
		if f.EOF {
			delete(pending, f.ReqID)
			a.dispatch(ctx, ar)
		}
	}
}

func (a *fakeAgent) dispatch(ctx context.Context, ar *agentReq) {
	a.mu.Lock()
	a.reqs = append(a.reqs, *ar)
	a.mu.Unlock()
	go func() {
		resp := a.handler(*ar)
		b, _ := protocol.Marshal(protocol.HTTPResponse{
			Type: protocol.TypeHTTPResponse, ReqID: ar.req.ReqID, Status: resp.status,
			Headers: resp.headers, HasBody: len(resp.chunks) > 0,
		}, protocol.TypeHTTPResponse)
		if err := a.agent.Write(ctx, websocket.MessageText, b); err != nil {
			return
		}
		for i, c := range resp.chunks {
			if i == 1 && resp.gate != nil {
				select {
				case <-resp.gate:
				case <-ctx.Done():
					return
				}
			}
			f := protocol.BodyFrame{FrameType: protocol.FrameResponseBody, ReqID: ar.req.ReqID, Seq: uint32(i),
				EOF: i == len(resp.chunks)-1, Payload: c}
			if err := a.agent.Write(ctx, websocket.MessageBinary, f.Encode()); err != nil {
				return
			}
		}
	}()
}

// --- sahte dogrulayici -------------------------------------------------------

type fakeVerifier struct {
	// token -> kaynak anahtari (aud) ve talepler
	tokens map[string]struct {
		res string
		c   MCPClaims
	}
}

func (f *fakeVerifier) VerifyResource(r *http.Request, token, resourceKey string) (MCPClaims, error) {
	t, ok := f.tokens[token]
	if !ok || t.res != resourceKey {
		return MCPClaims{}, errors.New("gecersiz")
	}
	return t.c, nil
}

func (f *fakeVerifier) SignUpstream(id MCPUpstreamIdentity) (string, error) {
	return "jws." + id.Subject + "." + id.TunnelID, nil
}

// --- kurulum -----------------------------------------------------------------

const (
	mcpHost   = "acme.zorven.test"
	mcpTenant = "ten_acme"
	mcpTunnel = "tun_acme"
)

type mcpEnv struct {
	h      *Handler
	agent  *fakeAgent
	srv    *httptest.Server
	logs   chan store.MCPCall
	mu     sync.Mutex
	calls  []store.MCPCall
	verify *fakeVerifier
}

func (e *mcpEnv) collected() []store.MCPCall {
	// MCPLogger 3 sn'de bir yazar; testlerde dogrudan kuyruga bakmak icin beklenir.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		n := len(e.calls)
		e.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]store.MCPCall(nil), e.calls...)
}

func newMCPEnv(t *testing.T, cfgJSON string, keysJSON string, agentH func(agentReq) agentResp) *mcpEnv {
	t.Helper()
	route := store.HostRoute{
		FQDN: mcpHost, TenantID: mcpTenant, TunnelID: mcpTunnel, ClientID: "cli_acme",
		Target: "http://localhost:3000", Enabled: true, Proto: "http", Exposure: "auto",
		MCPEnabled: true, MCPConfig: []byte(cfgJSON), MCPKeys: []byte(keysJSON),
	}
	router := NewRouter(&fakeStore{routes: []store.HostRoute{route}})
	if err := router.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	hub := tunnel.NewHub()
	env := &mcpEnv{}
	env.agent = newFakeAgent(t, hub, "cli_acme", agentH)
	env.verify = &fakeVerifier{tokens: map[string]struct {
		res string
		c   MCPClaims
	}{
		"jwt.ok.a": {mcpHost + "/mcp", MCPClaims{Subject: "u1", TenantID: mcpTenant, ClientID: "cid1", ClientName: "Claude", Scope: "mcp"}},
		// baska tunel/host icin verilmis token
		"jwt.other.b": {"other.zorven.test/mcp", MCPClaims{Subject: "u1", TenantID: mcpTenant}},
		// baska kiracinin tuneli icin (host eslesse bile tenant uyusmuyor)
		"jwt.tenant.c": {mcpHost + "/mcp", MCPClaims{Subject: "u9", TenantID: "ten_evil"}},
		"jwt.guest.d":  {mcpHost + "/mcp", MCPClaims{Subject: "u2", TenantID: mcpTenant, Scope: "mcp"}},
	}}
	env.logs = make(chan store.MCPCall, 100)
	logger := NewMCPLogger(func(_ context.Context, batch []store.MCPCall) error {
		env.mu.Lock()
		env.calls = append(env.calls, batch...)
		env.mu.Unlock()
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	go logger.Run(ctx)
	t.Cleanup(cancel)
	env.h = &Handler{
		Router: router, Hub: hub, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		MCPVerifier: env.verify, MCPIssuer: "https://zorven.test", MCPLog: logger,
		MCPUsers: func(_ context.Context, tenantID, userID string) (string, bool, error) {
			switch userID {
			case "u1":
				return "ali@acme.test", true, nil
			case "u2":
				return "guest@x.test", false, nil
			}
			return "", false, nil
		},
	}
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = mcpHost
		env.h.ServeHTTP(w, r)
	}))
	t.Cleanup(env.srv.Close)
	return env
}

func jsonHdr() map[string][]string { return map[string][]string{"Content-Type": {"application/json"}} }

func okEcho(ar agentReq) agentResp {
	return agentResp{status: 200, headers: jsonHdr(), chunks: [][]byte{[]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)}}
}

func (e *mcpEnv) do(t *testing.T, method, path, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

const callBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":"gizli-arguman"}}}`

// --- testler -----------------------------------------------------------------

func TestMCP_NoTokenGets401WithResourceMetadataAndUpstreamUntouched(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	resp, _ := env.do(t, "POST", "/mcp", callBody, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	wa := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wa, `resource_metadata="http://`+mcpHost+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("WWW-Authenticate: %q", wa)
	}
	if len(env.agent.requests()) != 0 {
		t.Fatal("kimliksiz istek upstream'e ulasti")
	}
}

func TestMCP_ProtectedResourceMetadataServedByIngress(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		resp, body := env.do(t, "GET", p, "", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		if m["resource"] != "http://"+mcpHost+"/mcp" {
			t.Fatalf("resource: %v", m["resource"])
		}
		as, _ := m["authorization_servers"].([]any)
		if len(as) != 1 || as[0] != "https://zorven.test" {
			t.Fatalf("authorization_servers: %v", m["authorization_servers"])
		}
	}
	// baska yol sonekleri 404 (baska kaynak icin metadata yok)
	if resp, _ := env.do(t, "GET", "/.well-known/oauth-protected-resource/baska", "", nil); resp.StatusCode != 404 {
		t.Fatalf("bilinmeyen sonek: %d", resp.StatusCode)
	}
	if len(env.agent.requests()) != 0 {
		t.Fatal(".well-known istegi upstream'e gitti")
	}
}

func TestMCP_ValidTokenForwardsWithIdentityAndStripsForgedHeaders(t *testing.T) {
	env := newMCPEnv(t, `{"allowed_emails":[]}`, `[]`, okEcho)
	hdr := bearer("jwt.ok.a")
	hdr["X-Zorven-User"] = "admin@evil.test"
	hdr["x-zorven-identity"] = "sahte.jws"
	hdr["X-Zorven-Scopes"] = "root"
	hdr["Accept-Encoding"] = "gzip"
	resp, _ := env.do(t, "POST", "/mcp", callBody, hdr)
	if resp.StatusCode != 200 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	reqs := env.agent.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream istek sayisi %d", len(reqs))
	}
	h := reqs[0].req.Headers
	get := func(k string) string {
		for kk, v := range h {
			if strings.EqualFold(kk, k) && len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	if get("Authorization") != "" {
		t.Fatal("Zorven token'i upstream'e sizdi")
	}
	if get("X-Zorven-User") != "ali@acme.test" || get("X-Zorven-User-Id") != "u1" ||
		get("X-Zorven-Client-Id") != "cid1" || get("X-Zorven-Auth-Method") != "oauth" ||
		get("X-Zorven-Tunnel-Id") != mcpTunnel || get("X-Zorven-Scopes") != "mcp" {
		t.Fatalf("kimlik basliklari: %v", h)
	}
	if get("X-Zorven-Identity") != "jws.u1."+mcpTunnel {
		t.Fatalf("X-Zorven-Identity: %q", get("X-Zorven-Identity"))
	}
	if get("Accept-Encoding") != "identity" {
		t.Fatalf("Accept-Encoding: %q", get("Accept-Encoding"))
	}
	n := 0
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-zorven-") {
			n++
		}
	}
	if n != 7 { // user, user-id, client-id, scopes, tunnel-id, auth-method, identity
		t.Fatalf("x-zorven-* sayisi %d: %v", n, h)
	}
}

func TestMCP_TrafficRuleCannotOverrideIdentityHeaders(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	// Trafik politikasi kullanici tanimli X-Zorven-User eklemeye calissin.
	route := store.HostRoute{
		FQDN: mcpHost, TenantID: mcpTenant, TunnelID: mcpTunnel, ClientID: "cli_acme", Enabled: true, Proto: "http",
		MCPEnabled: true, MCPConfig: []byte(`{}`), MCPKeys: []byte(`[]`),
		TrafficEnabled: true, TrafficConfig: []byte(`{"request_headers":{"set":{"X-Zorven-User":"hile","Authorization":"Bearer sizdir"}}}`),
	}
	env.h.Router = NewRouter(&fakeStore{routes: []store.HostRoute{route}})
	if err := env.h.Router.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a")); resp.StatusCode != 200 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	h := env.agent.requests()[0].req.Headers
	for k, v := range h {
		if strings.EqualFold(k, "X-Zorven-User") && v[0] != "ali@acme.test" {
			t.Fatalf("trafik kurali kimligi ezdi: %v", v)
		}
		if strings.EqualFold(k, "Authorization") {
			t.Fatalf("Authorization upstream'e gitti: %v", v)
		}
	}
}

func TestMCP_CrossTunnelAndCrossTenantTokensRejected(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	for _, tok := range []string{"jwt.other.b", "jwt.tenant.c", "jwt.yok.z", "zmk_xxx"} {
		resp, _ := env.do(t, "POST", "/mcp", callBody, bearer(tok))
		if resp.StatusCode != 401 {
			t.Fatalf("%s: durum %d", tok, resp.StatusCode)
		}
	}
	if len(env.agent.requests()) != 0 {
		t.Fatal("gecersiz token upstream'e ulasti")
	}
}

func TestMCP_AllowedEmailsControlAccess(t *testing.T) {
	// u2 org uyesi degil; yalnizca e-posta listesindeyse gecer.
	env := newMCPEnv(t, `{"allow_org_members":false,"allowed_emails":["guest@x.test"]}`, `[]`, okEcho)
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.guest.d")); resp.StatusCode != 200 {
		t.Fatalf("izinli e-posta reddedildi: %d", resp.StatusCode)
	}
	// u1 org uyesi ama allow_org_members=false ve listede degil -> 403
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a")); resp.StatusCode != 403 {
		t.Fatalf("listede olmayan uye: %d", resp.StatusCode)
	}
	// Bos liste + org uyeleri kapali: kimse (visitorauth'taki 'bos = herkes' DEGIL).
	env2 := newMCPEnv(t, `{"allow_org_members":false,"allowed_emails":[]}`, `[]`, okEcho)
	if resp, _ := env2.do(t, "POST", "/mcp", callBody, bearer("jwt.guest.d")); resp.StatusCode != 403 {
		t.Fatalf("bos liste herkese acildi: %d", resp.StatusCode)
	}
	// Org uyesi (varsayilan) gecer; uye degil + listede degil gecmez.
	env3 := newMCPEnv(t, `{}`, `[]`, okEcho)
	if resp, _ := env3.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a")); resp.StatusCode != 200 {
		t.Fatalf("org uyesi: %d", resp.StatusCode)
	}
	if resp, _ := env3.do(t, "POST", "/mcp", callBody, bearer("jwt.guest.d")); resp.StatusCode != 403 {
		t.Fatalf("uye olmayan: %d", resp.StatusCode)
	}
}

func TestMCP_RevokedEmailCutsAccess(t *testing.T) {
	env := newMCPEnv(t, `{"allow_org_members":false,"allowed_emails":["guest@x.test"]}`, `[]`, okEcho)
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.guest.d")); resp.StatusCode != 200 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	// Ayar degisti (Reload): e-posta listeden cikarildi. Kullanici onbellegi 30 sn;
	// eligibility yine de guncel ayarla degerlendirilir (cache yalniz kimlik verisi tutar).
	route := store.HostRoute{FQDN: mcpHost, TenantID: mcpTenant, TunnelID: mcpTunnel, ClientID: "cli_acme", Enabled: true,
		Proto: "http", MCPEnabled: true, MCPConfig: []byte(`{"allow_org_members":false,"allowed_emails":["baska@x.test"]}`), MCPKeys: []byte(`[]`)}
	env.h.Router = NewRouter(&fakeStore{routes: []store.HostRoute{route}})
	_ = env.h.Router.Reload(context.Background())
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.guest.d")); resp.StatusCode != 403 {
		t.Fatalf("e-posta cikarildiktan sonra erisim suruyor: %d", resp.StatusCode)
	}
}

func TestMCP_APIKeyAuth(t *testing.T) {
	good := "zmk_" + strings.Repeat("a", 40)
	expired := "zmk_" + strings.Repeat("b", 40)
	keys, _ := json.Marshal([]map[string]any{
		{"id": "k1", "label": "ci-bot", "hash": hashKey(good), "exp": nil},
		{"id": "k2", "label": "eski", "hash": hashKey(expired), "exp": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)},
	})
	env := newMCPEnv(t, `{"auth":"apikey"}`, string(keys), okEcho)
	if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer(good)); resp.StatusCode != 200 {
		t.Fatalf("gecerli anahtar: %d", resp.StatusCode)
	}
	if resp, _ := env.do(t, "POST", "/mcp", callBody, map[string]string{"X-API-Key": good}); resp.StatusCode != 200 {
		t.Fatalf("X-API-Key: %d", resp.StatusCode)
	}
	for _, tok := range []string{expired, "zmk_" + strings.Repeat("c", 40), "jwt.ok.a"} {
		if resp, _ := env.do(t, "POST", "/mcp", callBody, bearer(tok)); resp.StatusCode != 401 {
			t.Fatalf("%q: %d", tok[:8], resp.StatusCode)
		}
	}
	resp, _ := env.do(t, "POST", "/mcp", callBody, nil)
	if resp.StatusCode != 401 || strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("apikey modunda PRM yayinlanmamali: %q", resp.Header.Get("WWW-Authenticate"))
	}
	h := env.agent.requests()[0].req.Headers
	for k, v := range h {
		if strings.EqualFold(k, "X-Api-Key") || strings.EqualFold(k, "Authorization") {
			t.Fatalf("anahtar upstream'e gitti: %s=%v", k, v)
		}
		if strings.EqualFold(k, "X-Zorven-User") && v[0] != "apikey:ci-bot" {
			t.Fatalf("X-Zorven-User: %v", v)
		}
	}
	// apikey modunda PRM 404
	if r, _ := env.do(t, "GET", "/.well-known/oauth-protected-resource", "", nil); r.StatusCode != 404 {
		t.Fatalf("apikey PRM: %d", r.StatusCode)
	}
}

func TestMCP_AuthNonePassesAuthorizationButStripsZorvenHeaders(t *testing.T) {
	env := newMCPEnv(t, `{"auth":"none","ack_no_auth":true}`, `[]`, okEcho)
	hdr := map[string]string{"Authorization": "Bearer kendi-upstream-token", "X-Zorven-User": "sahte"}
	if resp, _ := env.do(t, "POST", "/mcp", callBody, hdr); resp.StatusCode != 200 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	h := env.agent.requests()[0].req.Headers
	var auth, zu string
	for k, v := range h {
		if strings.EqualFold(k, "Authorization") {
			auth = v[0]
		}
		if strings.EqualFold(k, "X-Zorven-User") {
			zu = v[0]
		}
	}
	if auth != "Bearer kendi-upstream-token" {
		t.Fatalf("none modunda kullanicinin kendi Authorization'i silindi: %q", auth)
	}
	if zu != "" {
		t.Fatalf("sahte X-Zorven-User silinmedi: %q", zu)
	}
	// auth=none ack olmadan gecersiz -> ParseLenient oauth'a doner
	if c := mcpconf.ParseLenient([]byte(`{"auth":"none"}`)); c.Auth != mcpconf.AuthOAuth {
		t.Fatalf("ack'siz none oauth'a dusmedi: %s", c.Auth)
	}
}

func TestMCP_ToolDenyAndAllowList(t *testing.T) {
	env := newMCPEnv(t, `{"allow_tools":["echo","read_*"],"deny_tools":["read_secret"]}`, `[]`, okEcho)
	call := func(name string) (int, string) {
		b := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
		resp, body := env.do(t, "POST", "/mcp", b, bearer("jwt.ok.a"))
		return resp.StatusCode, body
	}
	for _, n := range []string{"echo", "read_file"} {
		if st, _ := call(n); st != 200 {
			t.Fatalf("%s: %d", n, st)
		}
	}
	before := len(env.agent.requests())
	for _, n := range []string{"read_secret", "delete_all", "ECHO", ""} {
		st, body := call(n)
		if st != 200 {
			t.Fatalf("%q: HTTP %d (JSON-RPC hata govdesi beklenir)", n, st)
		}
		var m struct {
			ID    int `json:"id"`
			Error struct {
				Code int            `json:"code"`
				Data map[string]any `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &m); err != nil || m.ID != 7 || m.Error.Code != -32602 || m.Error.Data["reason"] != "mcp_tool_denied" {
			t.Fatalf("%q: govde %s", n, body)
		}
	}
	if len(env.agent.requests()) != before {
		t.Fatal("reddedilen cagri upstream'e ulasti")
	}
}

func TestMCP_PolicyFailClosedOnAmbiguousBodies(t *testing.T) {
	env := newMCPEnv(t, `{"deny_tools":["tehlikeli"]}`, `[]`, okEcho)
	bodies := map[string]string{
		"yinelenen name": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tehlikeli","name":"echo"}}`,
		"buyuk harf":     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"NAME":"tehlikeli"}}`,
		"yinelenen mth":  `{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/call","params":{"name":"tehlikeli"}}`,
		"METHOD":         `{"jsonrpc":"2.0","id":1,"METHOD":"tools/call","params":{"name":"tehlikeli"}}`,
		"bozuk":          `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`,
		"name sayi":      `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":5}}`,
	}
	for n, b := range bodies {
		resp, body := env.do(t, "POST", "/mcp", b, bearer("jwt.ok.a"))
		if len(env.agent.requests()) != 0 {
			t.Fatalf("%s: belirsiz govde upstream'e ulasti (HTTP %d %s)", n, resp.StatusCode, body)
		}
	}
	// Politikasiz tunelde bozuk govde iletilir (yalniz log modu)
	env2 := newMCPEnv(t, `{}`, `[]`, okEcho)
	env2.do(t, "POST", "/mcp", bodies["bozuk"], bearer("jwt.ok.a"))
	if len(env2.agent.requests()) != 1 {
		t.Fatal("politikasiz modda bozuk govde iletilmeliydi")
	}
	// Sikistirilmis istek govdesi politika modunda reddedilir
	req, _ := http.NewRequest("POST", env.srv.URL+"/mcp", strings.NewReader("xx"))
	req.Header.Set("Authorization", "Bearer jwt.ok.a")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 415 || len(env.agent.requests()) != 0 {
		t.Fatalf("gzip govde: %d", resp.StatusCode)
	}
}

func TestMCP_BatchPartialDenyRewritesAndMergesResponse(t *testing.T) {
	var seen string
	env := newMCPEnv(t, `{"allow_tools":["echo"]}`, `[]`, func(ar agentReq) agentResp {
		seen = string(ar.body)
		return agentResp{status: 200, headers: jsonHdr(), chunks: [][]byte{[]byte(`[{"jsonrpc":"2.0","id":1,"result":{"ok":1}}]`)}}
	})
	batch := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo"}},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"yasak"}},` +
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"yasak2"}}]`
	resp, body := env.do(t, "POST", "/mcp", batch, bearer("jwt.ok.a"))
	if resp.StatusCode != 200 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
	if strings.Contains(seen, "yasak") || !strings.Contains(seen, `"echo"`) {
		t.Fatalf("upstream'in gordugu govde: %s", seen)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(body), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("birlesik yanit: %s", body)
	}
	foundErr := false
	for _, m := range arr {
		if _, ok := m["error"]; ok && m["id"].(float64) == 2 {
			foundErr = true
		}
	}
	if !foundErr {
		t.Fatalf("id=2 icin sentetik hata yok: %s", body)
	}
	// Bildirim (id'siz) yasak -> sessizce dusurulur: sentetik yanit uretilmez (2 oge).
}

func TestMCP_BatchAllDeniedAnswersWithoutUpstream(t *testing.T) {
	env := newMCPEnv(t, `{"deny_tools":["*"]}`, `[]`, okEcho)
	batch := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a"}},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"b"}}]`
	resp, body := env.do(t, "POST", "/mcp", batch, bearer("jwt.ok.a"))
	var arr []map[string]any
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &arr) != nil || len(arr) != 2 || len(env.agent.requests()) != 0 {
		t.Fatalf("durum %d govde %s upstream=%d", resp.StatusCode, body, len(env.agent.requests()))
	}
}

func TestMCP_LegacySSEMessagesDenialIs403(t *testing.T) {
	env := newMCPEnv(t, `{"transport":"sse","deny_tools":["x"]}`, `[]`, okEcho)
	resp, body := env.do(t, "POST", "/messages?sessionId=abc", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x"}}`, bearer("jwt.ok.a"))
	if resp.StatusCode != 403 || !strings.Contains(body, "mcp_tool_denied") || len(env.agent.requests()) != 0 {
		t.Fatalf("eski tasima reddi: %d %s", resp.StatusCode, body)
	}
}

func TestMCP_ToolsListFilteredJSON(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":5,"result":{"tools":[{"name":"echo","description":"e"},{"name":"db_drop"},{"name":"db_read","inputSchema":{"type":"object"}},{"description":"adsiz"}],"nextCursor":"c1"}}`
	env := newMCPEnv(t, `{"allow_tools":["echo","db_*"],"deny_tools":["db_drop"]}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: jsonHdr(), chunks: [][]byte{[]byte(list)}}
	})
	resp, body := env.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, bearer("jwt.ok.a"))
	if resp.StatusCode != 200 {
		t.Fatalf("durum %d %s", resp.StatusCode, body)
	}
	var m struct {
		Result struct {
			Tools      []map[string]any `json:"tools"`
			NextCursor string           `json:"nextCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err, body)
	}
	var names []string
	for _, tl := range m.Result.Tools {
		names = append(names, tl["name"].(string))
	}
	if strings.Join(names, ",") != "echo,db_read" || m.Result.NextCursor != "c1" {
		t.Fatalf("suzulmus liste: %v (%s)", names, body)
	}
	if cl := resp.Header.Get("Content-Length"); cl != itoa(len(body)) {
		t.Fatalf("Content-Length %q govde %d", cl, len(body))
	}
}

func TestMCP_ToolsListFilteredSSEEventByEvent(t *testing.T) {
	ev1 := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n"
	// ikinci olay: coklu satir data + parcali gelecek
	ev2 := "id: 9\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":5,\n" +
		"data: \"result\":{\"tools\":[{\"name\":\"echo\"},{\"name\":\"yasak\"}]}}\n\n"
	env := newMCPEnv(t, `{"allow_tools":["echo"]}`, `[]`, func(ar agentReq) agentResp {
		all := []byte(ev1 + ev2)
		// olay sinirlarini bolecek sekilde parcala
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}},
			chunks: [][]byte{all[:30], all[30 : len(ev1)+25], all[len(ev1)+25:]}}
	})
	resp, body := env.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, bearer("jwt.ok.a"))
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("durum %d ct %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "notifications/progress") {
		t.Fatalf("ilgisiz olay kayboldu: %q", body)
	}
	if strings.Contains(body, "yasak") || !strings.Contains(body, `"echo"`) {
		t.Fatalf("SSE tools/list suzulmedi: %q", body)
	}
	if !strings.Contains(body, "id: 9") {
		t.Fatalf("olay id satiri korunmadi: %q", body)
	}
}

func TestMCP_ToolsListCompressedResponseFailsClosed(t *testing.T) {
	env := newMCPEnv(t, `{"allow_tools":["echo"]}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
			chunks: [][]byte{[]byte("sikistirilmis-bayt")}}
	})
	resp, body := env.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, bearer("jwt.ok.a"))
	if resp.StatusCode != 502 || strings.Contains(body, "sikistirilmis-bayt") {
		t.Fatalf("durum %d govde %q", resp.StatusCode, body)
	}
}

func TestMCP_StreamingFirstEventNotBufferedAndStreamHeaders(t *testing.T) {
	gate := make(chan struct{})
	env := newMCPEnv(t, `{}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}},
			chunks: [][]byte{[]byte("event: message\ndata: {\"n\":1}\n\n"), []byte("event: message\ndata: {\"n\":2}\n\n")}, gate: gate}
	})
	req, _ := http.NewRequest("GET", env.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer jwt.ok.a")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Accel-Buffering") != "no" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-transform") {
		t.Fatalf("akis basliklari: %v", resp.Header)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := resp.Body.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, `"n":1`) {
			t.Fatalf("ilk olay: %q", s)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("ilk SSE olayi upstream sessizken bile 1 sn icinde ulasmaliydi (tamponlama)")
	}
	close(gate)
}

func TestMCP_StreamLimit(t *testing.T) {
	gate := make(chan struct{})
	env := newMCPEnv(t, `{"max_streams":2}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}},
			chunks: [][]byte{[]byte("data: 1\n\n"), []byte("data: 2\n\n")}, gate: gate}
	})
	var bodies []io.Closer
	defer func() {
		close(gate)
		for _, b := range bodies {
			b.Close()
		}
	}()
	open := func() int {
		req, _ := http.NewRequest("GET", env.srv.URL+"/mcp", nil)
		req.Header.Set("Authorization", "Bearer jwt.ok.a")
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, resp.Body)
		return resp.StatusCode
	}
	if open() != 200 || open() != 200 {
		t.Fatal("ilk iki akis acilamadi")
	}
	if st := open(); st != 429 {
		t.Fatalf("ucuncu akis: %d (429 beklenir)", st)
	}
}

func TestMCP_RateLimitReturnsJSONRPCError(t *testing.T) {
	env := newMCPEnv(t, `{"rate_limit_per_min":6}`, `[]`, okEcho)
	denied := 0
	for i := 0; i < 20; i++ {
		_, body := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a"))
		if strings.Contains(body, "rate_limited") {
			denied++
		}
	}
	if denied == 0 || denied == 20 {
		t.Fatalf("hiz siniri davranisi: %d/20 reddedildi", denied)
	}
	if got := len(env.agent.requests()); got != 20-denied {
		t.Fatalf("upstream'e giden %d, beklenen %d", got, 20-denied)
	}
}

func TestMCP_RequestBodyTooLarge(t *testing.T) {
	env := newMCPEnv(t, `{"max_request_bytes":200}`, `[]`, okEcho)
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":"` + strings.Repeat("a", 500) + `"}}}`
	resp, _ := env.do(t, "POST", "/mcp", big, bearer("jwt.ok.a"))
	if resp.StatusCode != 413 || len(env.agent.requests()) != 0 {
		t.Fatalf("durum %d", resp.StatusCode)
	}
}

func TestMCP_CallLogRecordsMetadataOnly(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: jsonHdr(), chunks: [][]byte{[]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"GIZLI-SONUC"}],"isError":true}}`)}}
	})
	env.do(t, "POST", "/mcp", callBody, map[string]string{"Authorization": "Bearer jwt.ok.a", "Mcp-Session-Id": "sess-123"})
	calls := env.collected()
	if len(calls) != 1 {
		t.Fatalf("cagri sayisi %d", len(calls))
	}
	c := calls[0]
	if c.Method != "tools/call" || c.Tool != "echo" || c.Status != "error" || c.TenantID != mcpTenant || c.TunnelID != mcpTunnel {
		t.Fatalf("log: %+v", c)
	}
	if c.Principal != "ali@acme.test" || c.Client != "Claude" || len(c.Session) != 12 || c.BytesOut == 0 {
		t.Fatalf("log alanlari: %+v", c)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "gizli-arguman") || strings.Contains(string(raw), "GIZLI-SONUC") || strings.Contains(string(raw), "sess-123") {
		t.Fatalf("log govde/oturum kimligi icerigi sizdirdi: %s", raw)
	}
}

func TestMCP_LogLevelNoneWritesNothing(t *testing.T) {
	env := newMCPEnv(t, `{"log_level":"none"}`, `[]`, okEcho)
	env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a"))
	time.Sleep(3500 * time.Millisecond)
	env.mu.Lock()
	n := len(env.calls)
	env.mu.Unlock()
	if n != 0 {
		t.Fatalf("log_level=none iken %d satir yazildi", n)
	}
}

func TestMCP_SSEResponseLogsDurationAndError(t *testing.T) {
	ev := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32601,\"message\":\"x\"}}\n\n"
	env := newMCPEnv(t, `{}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}}, chunks: [][]byte{[]byte(ev)}}
	})
	_, body := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a"))
	if body != ev {
		t.Fatalf("filtre gerekmedigi halde SSE govdesi degisti: %q", body)
	}
	c := env.collected()
	if len(c) != 1 || c[0].Status != "error" || c[0].ErrorCode != -32601 {
		t.Fatalf("log: %+v", c)
	}
}

func TestMCP_HeadTimeoutIsConfigurable(t *testing.T) {
	// 30 sn'lik UpstreamTimeout yerine ayarlanan sure kullanilir: head_timeout_sec=1.
	env := newMCPEnv(t, `{"head_timeout_sec":1}`, `[]`, func(ar agentReq) agentResp {
		time.Sleep(2500 * time.Millisecond)
		return okEcho(ar)
	})
	start := time.Now()
	resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a"))
	if resp.StatusCode != 504 || time.Since(start) > 2400*time.Millisecond {
		t.Fatalf("durum %d sure %v", resp.StatusCode, time.Since(start))
	}
}

func TestMCP_ClientDisconnectCancelsUpstream(t *testing.T) {
	gate := make(chan struct{})
	env := newMCPEnv(t, `{}`, `[]`, func(ar agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}},
			chunks: [][]byte{[]byte("data: 1\n\n"), []byte("data: 2\n\n")}, gate: gate}
	})
	defer close(gate)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", env.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer jwt.ok.a")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_, _ = resp.Body.Read(buf)
	cancel()
	resp.Body.Close()
	select {
	case <-env.agent.cancels:
	case <-time.After(3 * time.Second):
		t.Fatal("istemci kopunca ajana iptal gitmedi (sessiz akis)")
	}
}

func TestMCP_CaptureDisabledForMCPTunnels(t *testing.T) {
	// MCP modunda proxy()'ye capt hic kurulmaz: mc != nil kosulu kodla zorlanir.
	// Davranis testi: Captures.Enabled'a bakilmadan istek gecer (nil Captures da calisir)
	// ve govde yine akar.
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	if resp, body := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a")); resp.StatusCode != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("durum %d", resp.StatusCode)
	}
}

func TestMCP_NonMCPPathsAlsoRequireAuth(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	for _, p := range []string{"/", "/MCP", "/mcp/", "/admin", "//mcp", "/%6dcp"} {
		resp, _ := env.do(t, "GET", p, "", nil)
		if resp.StatusCode != 401 {
			t.Fatalf("%s: %d (kimliksiz her yol 401 olmali)", p, resp.StatusCode)
		}
	}
	if len(env.agent.requests()) != 0 {
		t.Fatal("kimliksiz istek upstream'e ulasti")
	}
}

func TestMCP_NotificationAndPingNotLogged(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, func(ar agentReq) agentResp { return agentResp{status: 202} })
	env.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, bearer("jwt.ok.a"))
	env.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, bearer("jwt.ok.a"))
	time.Sleep(3500 * time.Millisecond)
	env.mu.Lock()
	n := len(env.calls)
	env.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d gereksiz satir", n)
	}
}

func TestMCP_RejectLimitsFailedAuth(t *testing.T) {
	env := newMCPEnv(t, `{}`, `[]`, okEcho)
	saw429 := false
	for i := 0; i < 80; i++ {
		resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.gecersiz.x"))
		if resp.StatusCode == 429 {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("basarisiz kimlik denemeleri IP basina sinirlanmadi")
	}
}

func TestParseRPCStrict(t *testing.T) {
	msgs, batch, err := parseRPC([]byte(`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"x","arguments":{"name":"ic"}}}`))
	if err != nil || batch || len(msgs) != 1 || msgs[0].Tool != "x" || msgs[0].ID != `"a"` {
		t.Fatalf("%+v %v", msgs, err)
	}
	// ic ice "name" (arguments icinde) dogru ayirt edilir
	if msgs[0].Tool == "ic" {
		t.Fatal("arguments.name ile karisti")
	}
	if _, _, err := parseRPC([]byte(`[]`)); err == nil {
		t.Fatal("bos batch kabul edildi")
	}
	if _, _, err := parseRPC([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("art arda iki nesne kabul edildi")
	}
	if m, _, _ := parseRPC([]byte(`{"jsonrpc":"2.0","method":"notifications/x"}`)); m[0].HasID {
		t.Fatal("bildirim id tasimamali")
	}
	if got := uriSchemeHost("https://kullanici:parola@host.example/yol?q=gizli#f"); strings.Contains(got, "gizli") || strings.Contains(got, "yol") {
		t.Fatalf("uri log: %q", got)
	}
}

func TestMCPProbeInitializeAndToolsListThroughAgent(t *testing.T) {
	env := newMCPEnv(t, `{"allow_tools":["echo"]}`, `[]`, func(ar agentReq) agentResp {
		b := string(ar.body)
		switch {
		case strings.Contains(b, `"initialize"`):
			return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"application/json"}, "Mcp-Session-Id": {"s1"}},
				chunks: [][]byte{[]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"demo"}}}`)}}
		case strings.Contains(b, "tools/list"):
			return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/event-stream"}},
				chunks: [][]byte{[]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"tools\":[{\"name\":\"echo\"},{\"name\":\"yasak\"}]}}\n\n")}}
		}
		return agentResp{status: 202}
	})
	res := env.h.MCPProbe(context.Background(), mcpTunnel)
	if !res.AgentOnline || !res.Initialize.OK || !res.ToolsList.OK || res.ServerName != "demo" || res.ToolCount != 2 {
		t.Fatalf("probe: %+v", res)
	}
	// Sinama kimlik basligi tasimaz ve oturum kimligini tools/list'e iletir.
	var sawSession bool
	for _, r := range env.agent.requests() {
		for k, v := range r.req.Headers {
			if strings.EqualFold(k, "Authorization") {
				t.Fatal("probe Authorization tasidi")
			}
			if strings.EqualFold(k, "Mcp-Session-Id") && v[0] == "s1" {
				sawSession = true
			}
		}
	}
	if !sawSession {
		t.Fatal("Mcp-Session-Id iletilmedi")
	}
	// MCP kapaliysa sonuc bos
	if r := env.h.MCPProbe(context.Background(), "tun_yok"); r.Enabled {
		t.Fatal("olmayan tunel etkin sayildi")
	}
}

func TestSplitSSE(t *testing.T) {
	buf := []byte("data: a\n\n\r\ndata: b\r\n\r\ndata: c")
	ev, rest, ok := splitSSE(buf)
	if !ok || string(ev) != "data: a\n" {
		t.Fatalf("1: %q %v", ev, ok)
	}
	ev, rest, ok = splitSSE(rest)
	if !ok || !bytes.Contains(ev, []byte("data: b")) {
		t.Fatalf("2: %q %v", ev, ok)
	}
	if _, _, ok = splitSSE(rest); ok {
		t.Fatal("tamamlanmamis olay donduruldu")
	}
}
