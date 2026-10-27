package mcptools

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeBackend, istekleri kaydeder ve yola gore hazir yanit doner.
type fakeBackend struct {
	mu    sync.Mutex
	reqs  []Request
	reply func(Request) Response
}

func (f *fakeBackend) Do(_ context.Context, r Request) (Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(r), nil
	}
	return Response{Status: 200, Body: []byte(`{}`), Header: http.Header{}}, nil
}

func (f *fakeBackend) last() Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

func jsonResp(status int, body string) Response {
	return Response{Status: status, Body: []byte(body), Header: http.Header{}}
}

// connect, sunucu ile bellek-ici istemci oturumu kurar (stdio ile ayni protokol).
func connect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	sort.Strings(out)
	return out
}

func TestCatalog_NoDeleteTools(t *testing.T) {
	forbidden := []string{"remove", "revoke", "destroy", "drop", "purge", "rotate", "token"}
	for _, ti := range Catalog() {
		for _, f := range forbidden {
			if strings.Contains(ti.Name, f) {
				t.Errorf("yasak arac adi: %s", ti.Name)
			}
		}
		// Silme araclari yalniz ayri :delete kapsamiyla var olabilir.
		if strings.Contains(ti.Name, "delete") != strings.HasSuffix(ti.Scope, ":delete") {
			t.Errorf("%s: silme araci ile :delete kapsami eslesmiyor (%q)", ti.Name, ti.Scope)
		}
		if strings.HasSuffix(ti.Scope, ":delete") || ti.Scope == ScopeFull {
			continue
		}
		if ti.Write && ti.Scope == "" {
			t.Errorf("%s: yazma araci kapsamsiz", ti.Name)
		}
		if ti.Write && !strings.HasSuffix(ti.Scope, ":write") {
			t.Errorf("%s: yazma araci write kapsami istemeli, %q", ti.Name, ti.Scope)
		}
		if !ti.Write && strings.HasSuffix(ti.Scope, ":write") {
			t.Errorf("%s: okuma araci write kapsami istiyor", ti.Name)
		}
	}
}

func TestSchemas_ValidAndDescribed(t *testing.T) {
	cs := connect(t, NewServer(&fakeBackend{}, Options{Scopes: []string{ScopeFull}}))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(Catalog()) {
		t.Fatalf("arac sayisi %d != katalog %d", len(res.Tools), len(Catalog()))
	}
	for _, tl := range res.Tools {
		if tl.Description == "" {
			t.Errorf("%s: aciklama yok", tl.Name)
		}
		in, ok := tl.InputSchema.(map[string]any)
		if !ok || in["type"] != "object" {
			t.Errorf("%s: girdi semasi object degil: %v", tl.Name, tl.InputSchema)
		}
		if tl.OutputSchema == nil {
			t.Errorf("%s: cikti semasi yok", tl.Name)
		}
		if tl.Annotations == nil {
			t.Errorf("%s: annotations yok", tl.Name)
			continue
		}
		if tl.Annotations.ReadOnlyHint == isWrite(tl.Name) {
			t.Errorf("%s: readOnlyHint tutarsiz", tl.Name)
		}
		isDel := strings.HasPrefix(tl.Name, "delete_") || tl.Name == "call_api"
		if tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint != isDel {
			t.Errorf("%s: destructiveHint %v olmali", tl.Name, isDel)
		}
	}
}

func isWrite(name string) bool {
	for _, ti := range Catalog() {
		if ti.Name == name {
			return ti.Write
		}
	}
	return false
}

func TestScopeFiltering(t *testing.T) {
	cases := []struct {
		scopes  []string
		has     []string
		hasNot  []string
		toolCnt int
	}{
		{[]string{"tunnels:read"}, []string{"whoami", "list_tunnels", "get_udp_stats", "get_tcp_stats", "get_door"},
			[]string{"create_tunnel", "open_door", "list_devices", "list_requests", "add_domain"}, 0},
		{[]string{"tunnels:write"}, []string{"create_tunnel", "update_tunnel", "open_door", "list_tunnels"},
			[]string{"add_domain", "list_requests"}, 0},
		{[]string{"hostnames:write", "analytics:read"}, []string{"add_domain", "list_domains", "list_requests", "verify_domain"},
			[]string{"create_tunnel", "list_devices"}, 0},
		{[]string{}, []string{"whoami", "get_status"}, []string{"list_tunnels", "create_tunnel"}, 2},
		{[]string{"read"}, []string{"list_devices", "list_requests"}, []string{"create_tunnel", "open_door"}, 0},
		// "*"/admin silme kapsamlarini ICERMEZ: silme araclari yalniz acik kapsamla gorunur.
		{[]string{"*"}, []string{"create_tunnel", "list_requests", "open_door"}, []string{"delete_tunnel", "delete_domain"}, len(Catalog()) - 4},
		{[]string{"write"}, []string{"create_tunnel", "set_tunnel_access"}, []string{"delete_tunnel", "delete_domain"}, 0},
		{[]string{"tunnels:delete"}, []string{"delete_tunnel", "list_tunnels"}, []string{"delete_domain", "create_tunnel"}, 0},
		{[]string{"hostnames:delete"}, []string{"delete_domain", "list_domains"}, []string{"delete_tunnel"}, 0},
		{nil, []string{"create_tunnel"}, []string{"call_api", "list_api_endpoints"}, len(Catalog()) - 2},
	}
	for _, c := range cases {
		cs := connect(t, NewServer(&fakeBackend{}, Options{Scopes: c.scopes}))
		names := toolNames(t, cs)
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		for _, h := range c.has {
			if !set[h] {
				t.Errorf("scopes=%v: %s gorunmeli", c.scopes, h)
			}
		}
		for _, h := range c.hasNot {
			if set[h] {
				t.Errorf("scopes=%v: %s GORUNMEMELI", c.scopes, h)
			}
		}
		if c.toolCnt > 0 && len(names) != c.toolCnt {
			t.Errorf("scopes=%v: %d arac, beklenen %d", c.scopes, len(names), c.toolCnt)
		}
	}
}

// callTool, araci cagirir ve (metin, structured, isError) doner.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, true, err.Error()
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	var out map[string]any
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
	}
	return out, res.IsError, text
}

func TestToolsMapToEndpoints(t *testing.T) {
	be := &fakeBackend{reply: func(r Request) Response {
		switch {
		case r.Path == "/api/v1/devices", r.Path == "/api/v1/tunnels" && r.Method == "GET",
			r.Path == "/api/v1/hostnames" && r.Method == "GET", r.Path == "/api/v1/projects",
			r.Path == "/api/v1/requests", strings.HasSuffix(r.Path, "/access-events"):
			return jsonResp(200, `[{"id":"a","status":"online","client_id":"c1","tunnel_id":"t1","fqdn":"x.example.com"},{"id":"b","status":"offline","client_id":"c2","tunnel_id":"t2","fqdn":"y.example.com"}]`)
		}
		return jsonResp(200, `{"id":"x","ok":true}`)
	}}
	cs := connect(t, NewServer(be, Options{}))

	tests := []struct {
		tool   string
		args   map[string]any
		method string
		path   string
		query  map[string]string
		body   map[string]any
	}{
		{"whoami", nil, "GET", "/api/v1/me", nil, nil},
		{"get_status", nil, "GET", "/api/v1/health", nil, nil},
		{"list_projects", nil, "GET", "/api/v1/projects", nil, nil},
		{"list_devices", map[string]any{"status": "online"}, "GET", "/api/v1/devices", nil, nil},
		{"get_device", map[string]any{"device_id": "c1"}, "GET", "/api/v1/devices/c1", nil, nil},
		{"list_tunnels", map[string]any{"device_id": "c1"}, "GET", "/api/v1/tunnels", nil, nil},
		{"get_tunnel", map[string]any{"tunnel_id": "t1"}, "GET", "/api/v1/tunnels/t1", nil, nil},
		{"create_tunnel", map[string]any{"name": "web", "device_id": "c1", "target": "http://localhost:3000", "ttl_sec": 3600},
			"POST", "/api/v1/tunnels", nil, map[string]any{"name": "web", "client_id": "c1", "target": "http://localhost:3000", "ttl_sec": float64(3600)}},
		{"update_tunnel", map[string]any{"tunnel_id": "t1", "enabled": false},
			"PATCH", "/api/v1/tunnels/t1", nil, map[string]any{"enabled": false}},
		{"get_tunnel_metrics", map[string]any{"tunnel_id": "t1", "window": "24h"}, "GET", "/api/v1/tunnels/t1/metrics", map[string]string{"window": "24h"}, nil},
		{"get_udp_stats", map[string]any{"tunnel_id": "t1"}, "GET", "/api/v1/tunnels/t1/udp", nil, nil},
		{"get_tcp_stats", map[string]any{"tunnel_id": "t1"}, "GET", "/api/v1/tunnels/t1/tcp", nil, nil},
		{"list_domains", map[string]any{"tunnel_id": "t1"}, "GET", "/api/v1/hostnames", nil, nil},
		{"get_domain", map[string]any{"domain_id": "a"}, "GET", "/api/v1/hostnames", nil, nil},
		{"add_domain", map[string]any{"fqdn": "app.example.com", "tunnel_id": "t1"},
			"POST", "/api/v1/hostnames/custom", nil, map[string]any{"fqdn": "app.example.com", "tunnel_id": "t1"}},
		{"add_domain", map[string]any{"name": "blog", "kind": "short"},
			"POST", "/api/v1/hostnames", nil, map[string]any{"name": "blog", "kind": "short"}},
		{"check_domain_dns", map[string]any{"domain_id": "a"}, "POST", "/api/v1/hostnames/a/dns-check", nil, nil},
		{"verify_domain", map[string]any{"domain_id": "a"}, "POST", "/api/v1/hostnames/a/verify", nil, nil},
		{"list_requests", map[string]any{"tunnel_id": "t1", "status": "5xx", "method": "get", "rejected": true, "limit": 10},
			"GET", "/api/v1/requests", map[string]string{"tunnel_id": "t1", "status": "5xx", "method": "GET", "rejected": "true", "limit": "10"}, nil},
		{"get_request", map[string]any{"request_id": "r1"}, "GET", "/api/v1/requests/r1", nil, nil},
		{"list_access_events", map[string]any{"tunnel_id": "t1", "limit": 5}, "GET", "/api/v1/tunnels/t1/access-events", map[string]string{"limit": "5"}, nil},
		{"get_door", map[string]any{"tunnel_id": "t1"}, "GET", "/api/v1/tunnels/t1/door", nil, nil},
		{"open_door", map[string]any{"tunnel_id": "t1"}, "PUT", "/api/v1/tunnels/t1/door", nil, map[string]any{"enabled": true, "duration_sec": float64(3600)}},
		{"open_door", map[string]any{"tunnel_id": "t1", "duration_sec": 86400}, "PUT", "/api/v1/tunnels/t1/door", nil, map[string]any{"enabled": true, "duration_sec": float64(86400)}},
		{"close_door", map[string]any{"tunnel_id": "t1"}, "PUT", "/api/v1/tunnels/t1/door", nil, map[string]any{"enabled": false}},
		{"delete_tunnel", map[string]any{"tunnel_id": "t1", "confirm": true, "confirm_name": "x"}, "DELETE", "/api/v1/tunnels/t1", nil, nil},
		{"delete_domain", map[string]any{"domain_id": "a", "confirm": true, "confirm_name": "X.example.com"}, "DELETE", "/api/v1/hostnames/a", nil, nil},
	}
	covered0 := map[string]bool{"set_tunnel_access": true} // ayri testte (ic ice govde)
	covered := covered0
	for _, tc := range tests {
		covered[tc.tool] = true
		out, isErr, text := callTool(t, cs, tc.tool, tc.args)
		if isErr {
			t.Errorf("%s: hata: %s", tc.tool, text)
			continue
		}
		if out == nil {
			t.Errorf("%s: structuredContent yok", tc.tool)
		}
		got := be.last()
		if tc.tool == "get_domain" || tc.tool == "list_domains" {
			// son istek hostnames listesidir
		}
		if got.Method != tc.method || got.Path != tc.path {
			t.Errorf("%s: %s %s, beklenen %s %s", tc.tool, got.Method, got.Path, tc.method, tc.path)
		}
		for k, v := range tc.query {
			if got.Query.Get(k) != v {
				t.Errorf("%s: query %s=%q, beklenen %q", tc.tool, k, got.Query.Get(k), v)
			}
		}
		if tc.body != nil {
			raw, _ := json.Marshal(got.Body)
			var b map[string]any
			_ = json.Unmarshal(raw, &b)
			if len(b) != len(tc.body) {
				t.Errorf("%s: govde %v, beklenen %v", tc.tool, b, tc.body)
			}
			for k, v := range tc.body {
				if b[k] != v {
					t.Errorf("%s: govde[%s]=%v, beklenen %v", tc.tool, k, b[k], v)
				}
			}
		}
	}
	for _, ti := range Catalog() {
		if !covered[ti.Name] && ti.Scope != ScopeFull { // full araclari fulltools_test.go'da
			t.Errorf("%s: testte kapsanmamis", ti.Name)
		}
	}
}

func TestListFilteringAndPaging(t *testing.T) {
	be := &fakeBackend{reply: func(Request) Response {
		return jsonResp(200, `[{"id":"a","status":"online"},{"id":"b","status":"offline"},{"id":"c","status":"online"}]`)
	}}
	cs := connect(t, NewServer(be, Options{}))
	out, _, _ := callTool(t, cs, "list_devices", map[string]any{"status": "online", "limit": 1})
	if out["total"] != float64(2) || out["count"] != float64(1) || out["has_more"] != true {
		t.Errorf("sayfalama: %v", out)
	}
	out, _, _ = callTool(t, cs, "list_devices", map[string]any{"status": "online", "limit": 1, "offset": 1})
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "c" || out["has_more"] != false {
		t.Errorf("offset: %v", out)
	}
}

func TestInputValidation(t *testing.T) {
	be := &fakeBackend{}
	cs := connect(t, NewServer(be, Options{}))
	bad := []struct {
		tool string
		args map[string]any
	}{
		{"get_tunnel_metrics", map[string]any{"tunnel_id": "t", "window": "99y"}}, // enum disi
		{"get_tunnel", map[string]any{}},                                          // zorunlu alan yok
		{"open_door", map[string]any{"tunnel_id": "t", "duration_sec": 5}},        // enum disi
		{"list_devices", map[string]any{"limit": 100000}},                         // maksimum asildi
		{"create_tunnel", map[string]any{"name": "a", "device_id": "b", "target": "c", "bogus": 1}},
		{"update_tunnel", map[string]any{"tunnel_id": "t"}}, // hicbir alan yok
		{"add_domain", map[string]any{}},                    // fqdn/name yok
		{"add_domain", map[string]any{"fqdn": "a.b", "name": "x"}},
		{"get_tunnel", map[string]any{"tunnel_id": "   "}},
	}
	for _, b := range bad {
		_, isErr, _ := callTool(t, cs, b.tool, b.args)
		if !isErr {
			t.Errorf("%s %v: hata beklenirdi", b.tool, b.args)
		}
	}
	if len(be.reqs) != 0 {
		t.Errorf("gecersiz girdi arka uca ulasmamali, %d istek: %v", len(be.reqs), be.reqs)
	}
}

func TestPathEscaping(t *testing.T) {
	be := &fakeBackend{}
	cs := connect(t, NewServer(be, Options{}))
	_, isErr, _ := callTool(t, cs, "get_tunnel", map[string]any{"tunnel_id": "../admin/stats"})
	if isErr {
		t.Fatal("beklenmeyen hata")
	}
	if p := be.last().Path; strings.Contains(p, "/admin/") || !strings.Contains(p, "%2F") {
		t.Errorf("yol kacirilmadi: %s", p)
	}
}

func TestErrorMapping(t *testing.T) {
	be := &fakeBackend{reply: func(Request) Response {
		return jsonResp(403, `{"error":{"code":"insufficient_scope","message":"tunnels:write gerekli"}}`)
	}}
	cs := connect(t, NewServer(be, Options{}))
	_, isErr, text := callTool(t, cs, "create_tunnel", map[string]any{"name": "a", "device_id": "b", "target": "http://x"})
	if !isErr || !strings.Contains(text, "insufficient_scope") || !strings.Contains(text, "403") {
		t.Errorf("hata eslemesi: isErr=%v %q", isErr, text)
	}
	// 429 Retry-After
	be.reply = func(Request) Response {
		return Response{Status: 429, Body: []byte(`{"error":{"code":"rate_limited","message":"yavas"}}`), Header: http.Header{"Retry-After": {"7"}}}
	}
	_, isErr, text = callTool(t, cs, "list_tunnels", nil)
	if !isErr || !strings.Contains(text, "7 sn") {
		t.Errorf("429: %q", text)
	}
}

func TestOnCallHook(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := NewServer(&fakeBackend{}, Options{OnCall: func(tool string, status int, err error) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, tool)
		if status != 200 || err != nil {
			t.Errorf("hook: %d %v", status, err)
		}
	}})
	cs := connect(t, srv)
	callTool(t, cs, "whoami", nil)
	if len(got) != 1 || got[0] != "whoami" {
		t.Errorf("hook: %v", got)
	}
}

func TestGetRequestBodiesOmittedByDefault(t *testing.T) {
	body := `{"id":"r1","req_body":"secret","resp_body":"data","req_headers":{"X-A":["` + strings.Repeat("a", 1000) + `"]},"resp_headers":{}}`
	be := &fakeBackend{reply: func(Request) Response { return jsonResp(200, body) }}
	cs := connect(t, NewServer(be, Options{}))

	out, _, _ := callTool(t, cs, "get_request", map[string]any{"request_id": "r1"})
	if _, has := out["req_body"]; has {
		t.Error("govde varsayilan olarak donmemeli")
	}
	if out["bodies_omitted"] != true {
		t.Error("bodies_omitted bekleniyordu")
	}
	h := out["req_headers"].(map[string]any)["X-A"].([]any)[0].(string)
	if len(h) > maxHeaderValue+40 {
		t.Errorf("baslik kisaltilmadi: %d", len(h))
	}
	out, _, _ = callTool(t, cs, "get_request", map[string]any{"request_id": "r1", "include_bodies": true})
	if out["req_body"] != "secret" {
		t.Errorf("include_bodies: %v", out)
	}
}

func TestCapOutput(t *testing.T) {
	items := make([]any, 5000)
	for i := range items {
		items[i] = map[string]any{"id": i, "path": strings.Repeat("x", 50)}
	}
	out := pageOut(items, 0, 200)
	// pageOut zaten 200'e sinirli; sisirilmis cikti icin elle:
	out["items"] = items
	capOutput(out)
	raw, _ := json.Marshal(out)
	if len(raw) > maxOutputBytes {
		t.Errorf("cikti %d bayt", len(raw))
	}
	if out["truncated"] != true {
		t.Error("truncated isareti yok")
	}
}

func TestExpandScopes(t *testing.T) {
	s := ExpandScopes([]string{"tunnels:write"})
	if !s["tunnels:read"] || !s["tunnels:write"] || s["hostnames:read"] {
		t.Errorf("%v", s)
	}
	if !ExpandScopes([]string{"admin"})["analytics:read"] {
		t.Error("admin tum kapsamlari vermeli")
	}
}

func TestHTTPBackend(t *testing.T) {
	// HTTPBackend: baslik ve yol dogrulamasi httptest ile.
	var gotAuth, gotProj, gotPath, gotQuery string
	ts := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotProj, gotPath, gotQuery = r.Header.Get("Authorization"), r.Header.Get("X-Zorven-Project"), r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`{"ok":true}`))
	})
	defer ts.Close()
	b := &HTTPBackend{BaseURL: ts.URL + "/", Token: "zrv_api_x"}
	resp, err := b.Do(context.Background(), Request{Method: "GET", Path: "/api/v1/me", Query: map[string][]string{"a": {"1"}}, Project: "p1"})
	if err != nil || resp.Status != 200 {
		t.Fatal(err, resp)
	}
	if gotAuth != "Bearer zrv_api_x" || gotProj != "p1" || gotPath != "/api/v1/me" || gotQuery != "a=1" {
		t.Errorf("%q %q %q %q", gotAuth, gotProj, gotPath, gotQuery)
	}
}

func TestDeleteToolsRequireConfirmationAndName(t *testing.T) {
	be := &fakeBackend{reply: func(r Request) Response {
		if r.Method == "GET" && r.Path == "/api/v1/tunnels/t1" {
			return jsonResp(200, `{"id":"t1","public_host":"app--acme.zorven.app","hostnames":[{"fqdn":"app--acme.zorven.app"}]}`)
		}
		if r.Method == "GET" && r.Path == "/api/v1/hostnames" {
			return jsonResp(200, `[{"id":"h1","fqdn":"www.example.com"}]`)
		}
		return jsonResp(204, ``)
	}}
	cs := connect(t, NewServer(be, Options{Scopes: []string{"tunnels:delete", "hostnames:delete"}}))
	deleted := func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		for _, r := range be.reqs {
			if r.Method == "DELETE" {
				return true
			}
		}
		return false
	}
	for name, args := range map[string]map[string]any{
		"delete_tunnel": {"tunnel_id": "t1"},
		"delete_domain": {"domain_id": "h1"},
	} {
		// onaysiz, adsiz, yanlis adli: asla DELETE gitmemeli
		for _, a := range []map[string]any{
			args,
			{"confirm": true},
			{"confirm": true, "confirm_name": "baska.example.com"},
			{"confirm": false, "confirm_name": "app--acme.zorven.app"},
		} {
			full := map[string]any{}
			for k, v := range args {
				full[k] = v
			}
			for k, v := range a {
				full[k] = v
			}
			_, isErr, text := callTool(t, cs, name, full)
			if !isErr || !strings.Contains(text, "confirm") {
				t.Errorf("%s %v: onay hatasi beklenirdi: %v %q", name, a, isErr, text)
			}
			if deleted() {
				t.Fatalf("%s %v: onaysiz DELETE gitti", name, a)
			}
		}
	}
	// dogru onay
	if _, isErr, text := callTool(t, cs, "delete_tunnel", map[string]any{"tunnel_id": "t1", "confirm": true, "confirm_name": "app--acme.zorven.app"}); isErr {
		t.Fatalf("dogru onay reddedildi: %s", text)
	}
	if !deleted() || be.last().Path != "/api/v1/tunnels/t1" {
		t.Fatalf("DELETE gitmedi: %+v", be.last())
	}
	// yol enjeksiyonu
	if _, isErr, _ := callTool(t, cs, "delete_tunnel", map[string]any{"tunnel_id": "../x", "confirm": true, "confirm_name": "x"}); !isErr {
		t.Fatal("tunnel_id yol enjeksiyonu kabul edildi")
	}
	// annotations
	res, _ := cs.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		if strings.HasPrefix(tl.Name, "delete_") && (tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Errorf("%s: destructiveHint=true/readOnly=false olmali", tl.Name)
		}
	}
}

func TestSetTunnelAccessOnlyOAuthWithEmails(t *testing.T) {
	mode := "none"
	be := &fakeBackend{reply: func(r Request) Response {
		if r.Method == "GET" {
			return jsonResp(200, `{"mode":"`+mode+`","enabled":true}`)
		}
		return jsonResp(200, `{"ok":true}`)
	}}
	cs := connect(t, NewServer(be, Options{}))
	put := func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		for _, r := range be.reqs {
			if r.Method == "PUT" {
				return true
			}
		}
		return false
	}
	bad := []map[string]any{
		{"tunnel_id": "t1", "mode": "basic", "allowed_emails": []string{"a@b.co"}},
		{"tunnel_id": "t1", "mode": "none", "allowed_emails": []string{"a@b.co"}},
		{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{}},
		{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{"gecersiz"}},
		{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{"a@b.co"}, "providers": []string{"evil"}},
		{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{"a@b.co,c@d.co"}},
	}
	for _, a := range bad {
		if _, isErr, _ := callTool(t, cs, "set_tunnel_access", a); !isErr {
			t.Errorf("kabul edildi: %v", a)
		}
	}
	if put() {
		t.Fatal("gecersiz girdiyle PUT gitti")
	}
	// parola (basic) politikasi uzerine yazilmaz
	mode = "basic"
	if _, isErr, _ := callTool(t, cs, "set_tunnel_access", map[string]any{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{"a@b.co"}}); !isErr || put() {
		t.Fatal("basic politikanin uzerine yazildi")
	}
	mode = "none"
	if _, isErr, text := callTool(t, cs, "set_tunnel_access", map[string]any{"tunnel_id": "t1", "mode": "oauth", "allowed_emails": []string{"A@B.co"}}); isErr {
		t.Fatalf("gecerli cagri: %s", text)
	}
	raw, _ := json.Marshal(be.last().Body)
	if !strings.Contains(string(raw), `"allowed_emails":["a@b.co"]`) || strings.Contains(string(raw), "password") {
		t.Fatalf("govde: %s", raw)
	}
}
