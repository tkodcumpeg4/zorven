package mcptools

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

const sampleSpec = `openapi: 3.1.0
info:
  title: X
paths:
  /health:
    get:
      tags: [Meta]
      summary: Service health
  /tunnels:
    get:
      tags: [Tunnels]
      summary: List tunnels
    post:
      tags: [Tunnels]
      summary: Create tunnel
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name, client_id, target]
  /tunnels/{id}:
    parameters:
      - $ref: "#/x"
    delete:
      tags: [Tunnels]
      summary: "Delete tunnel"
components:
  schemas:
    Foo:
      type: object
`

func TestParseOpenAPIEndpoints(t *testing.T) {
	eps := ParseOpenAPIEndpoints([]byte(sampleSpec))
	if len(eps) != 4 {
		t.Fatalf("4 islem beklenir, %d: %+v", len(eps), eps)
	}
	var post Endpoint
	for _, e := range eps {
		if e.Method == "POST" {
			post = e
		}
	}
	if post.Path != "/api/v1/tunnels" || post.Summary != "Create tunnel" || post.Tag != "Tunnels" ||
		strings.Join(post.Required, ",") != "name,client_id,target" {
		t.Fatalf("POST /tunnels yanlis: %+v", post)
	}
	if got := FilterEndpoints(eps, "delete", ""); len(got) != 1 || got[0].Path != "/api/v1/tunnels/{id}" || got[0].Summary != "Delete tunnel" {
		t.Fatalf("filtre: %+v", got)
	}
	if got := FilterEndpoints(eps, "", "HEALTH"); len(got) != 1 {
		t.Fatalf("metin filtresi buyuk/kucuk harf duyarsiz olmali: %+v", got)
	}
}

func TestParseRealOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../server/api/openapi.en.yaml")
	if err != nil {
		t.Skip("openapi.en.yaml yok")
	}
	eps := ParseOpenAPIEndpoints(raw)
	if len(eps) < 25 {
		t.Fatalf("gercek belgede cok az islem: %d", len(eps))
	}
	for _, e := range eps {
		if !strings.HasPrefix(e.Path, "/api/v1/") || e.Summary == "" {
			t.Errorf("eksik ozet/yol: %+v", e)
		}
	}
}

func fullServer(t *testing.T, be *fakeBackend) func(name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	s := connect(t, NewServer(be, Options{Scopes: []string{ScopeFull}}))
	return func(name string, args map[string]any) (map[string]any, bool, string) {
		return callTool(t, s, name, args)
	}
}

func TestFullToolsOnlyWithFullScope(t *testing.T) {
	for _, sc := range [][]string{{"*"}, {"admin"}, {"write"}, {"tunnels:delete"}, nil} {
		cs := connect(t, NewServer(&fakeBackend{}, Options{Scopes: sc}))
		for _, n := range toolNames(t, cs) {
			if n == "call_api" || n == "list_api_endpoints" {
				t.Errorf("scopes=%v: %s gorunmemeli", sc, n)
			}
		}
	}
	cs := connect(t, NewServer(&fakeBackend{}, Options{Scopes: []string{"full"}}))
	set := map[string]bool{}
	for _, n := range toolNames(t, cs) {
		set[n] = true
	}
	for _, want := range []string{"call_api", "list_api_endpoints", "create_tunnel", "delete_tunnel", "list_requests"} {
		if !set[want] {
			t.Errorf("full ile %s gorunmeli", want)
		}
	}
}

func TestCallAPI(t *testing.T) {
	be := &fakeBackend{reply: func(r Request) Response {
		switch {
		case r.Path == "/api/v1/big":
			return jsonResp(200, `"`+strings.Repeat("a", 300<<10)+`"`)
		case r.Path == "/api/v1/forbidden":
			return jsonResp(403, `{"error":{"code":"full_scope_excluded","message":"haric"}}`)
		}
		return jsonResp(200, `{"ok":true}`)
	}}
	do := fullServer(t, be)

	// Gecersiz yollar: hicbir istek gonderilmez.
	for _, p := range []string{"/api/v2/x", "/oauth/token", "http://evil/api/v1/x", "/api/v1/../admin", "/api/v1/a//b",
		"/api/v1/x?y=1", "/api/v1/x%2e%2e", "/api/v1/x#f", "api/v1/x", "/api/v1/x/", "/api/v1/a b", "https://x.com/api/v1/me"} {
		if _, isErr, _ := do("call_api", map[string]any{"method": "GET", "path": p}); !isErr {
			t.Errorf("%q reddedilmeli", p)
		}
	}
	if len(be.reqs) != 0 {
		t.Fatalf("gecersiz yollarda istek gonderilmemeli: %+v", be.reqs)
	}
	// Akis/WS uclari.
	for _, p := range []string{"/api/v1/events", "/api/v1/clients/c1/terminal", "/api/v1/clients/c1/screen"} {
		if _, isErr, _ := do("call_api", map[string]any{"method": "GET", "path": p}); !isErr {
			t.Errorf("%s reddedilmeli", p)
		}
	}
	// Gecersiz yontem.
	if _, isErr, _ := do("call_api", map[string]any{"method": "TRACE", "path": "/api/v1/me"}); !isErr {
		t.Error("TRACE reddedilmeli")
	}
	// DELETE ve yikici fiiller confirm ister.
	for _, c := range [][2]string{{"DELETE", "/api/v1/tunnels/t1"}, {"POST", "/api/v1/tunnels/t1/access/revoke-sessions"},
		{"POST", "/api/v1/api/x/rotate"}, {"POST", "/api/v1/admin/abuse/freeze"}} {
		_, isErr, text := do("call_api", map[string]any{"method": c[0], "path": c[1]})
		if !isErr || !strings.Contains(text, "confirm_required") {
			t.Errorf("%v confirm istemeli: %v %s", c, isErr, text)
		}
	}
	if len(be.reqs) != 0 {
		t.Fatalf("confirm'siz yikici cagrida istek gonderilmemeli")
	}
	out, isErr, _ := do("call_api", map[string]any{"method": "DELETE", "path": "/api/v1/tunnels/t1", "confirm": true})
	if isErr || out["status"] != float64(200) {
		t.Fatalf("confirm ile DELETE gitmeli: %v %v", out, isErr)
	}
	if r := be.last(); r.Method != "DELETE" || r.Path != "/api/v1/tunnels/t1" {
		t.Fatalf("yanlis istek: %+v", r)
	}

	// Sorgu + govde + proje.
	out, isErr, _ = do("call_api", map[string]any{"method": "POST", "path": "/api/v1/tunnels", "query": map[string]any{"a": "1"},
		"body": map[string]any{"name": "x"}, "project_id": "p1"})
	if isErr {
		t.Fatalf("POST: %v", out)
	}
	r := be.last()
	if r.Query.Get("a") != "1" || r.Project != "p1" {
		t.Fatalf("query/proje iletilmedi: %+v", r)
	}
	if b, _ := r.Body.(map[string]any); b["name"] != "x" {
		t.Fatalf("govde iletilmedi: %+v", r.Body)
	}
	// Yanit JSON olarak cozulur.
	if body, _ := out["body"].(map[string]any); body["ok"] != true {
		t.Fatalf("govde cozulmedi: %v", out["body"])
	}

	// HTTP hatasi MCP hatasi degil, durum olarak doner (model ayrintiyi gorsun).
	out, isErr, _ = do("call_api", map[string]any{"method": "GET", "path": "/api/v1/forbidden"})
	if isErr || out["status"] != float64(403) || out["ok"] != false {
		t.Fatalf("403 durum olarak donmeli: %v %v", out, isErr)
	}

	// Buyuk yanit kesilir ve belirtilir.
	out, isErr, _ = do("call_api", map[string]any{"method": "GET", "path": "/api/v1/big"})
	if isErr || out["truncated"] != true {
		t.Fatalf("buyuk yanit kesilmeli: %v", out["truncated"])
	}
	if s, _ := out["body"].(string); len(s) > maxCallAPIBody {
		t.Fatalf("govde %d bayt, sinir %d", len(s), maxCallAPIBody)
	}
}

func TestCallAPIAuditHook(t *testing.T) {
	be := &fakeBackend{}
	var got []string
	cs := connect(t, NewServer(be, Options{Scopes: []string{ScopeFull}, OnAPICall: func(tool, m, p string, st int) {
		got = append(got, tool+" "+m+" "+p)
	}}))
	callTool(t, cs, "call_api", map[string]any{"method": "GET", "path": "/api/v1/me", "query": map[string]any{"secret": "x"}})
	callTool(t, cs, "list_tunnels", nil)
	if len(got) != 2 || got[0] != "call_api GET /api/v1/me" || got[1] != "list_tunnels GET /api/v1/tunnels" {
		t.Fatalf("denetim kancasi: %v", got)
	}
	if !IsTicketPath("/api/v1/terminal-ticket") || IsTicketPath("/api/v1/me") {
		t.Fatal("IsTicketPath")
	}
}

func TestListAPIEndpointsTool(t *testing.T) {
	be := &fakeBackend{reply: func(r Request) Response {
		if r.Path != "/api/v1/openapi.en.yaml" {
			return Response{Status: http.StatusNotFound, Header: http.Header{}}
		}
		return jsonResp(200, sampleSpec)
	}}
	do := fullServer(t, be)
	out, isErr, _ := do("list_api_endpoints", map[string]any{"filter": "tunnel"})
	if isErr {
		t.Fatalf("hata: %v", out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("3 tunel islemi beklenir: %v", out)
	}
	out, _, _ = do("list_api_endpoints", map[string]any{"method": "POST"})
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Fatalf("POST filtresi: %v", out)
	}
}
