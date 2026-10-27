package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
)

// direct, OAuth kimligini middleware'e surec-ici dogrudan verir (MCP kopruyu atlar).
func (e *oauthEnv) direct(p oauthas.Principal, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(oauthas.WithPrincipal(r.Context(), p))
	w := httptest.NewRecorder()
	e.mw.ServeHTTP(w, r)
	return w
}

func fullPrin() oauthas.Principal {
	return oauthas.Principal{UserID: "usr_1", TenantID: "ten_x", GrantID: "g", ClientName: "C",
		Scopes: []string{"full"}, APIScopes: oauthas.APIScopes([]string{"full"}), Full: true}
}

func errCodeOf(w *httptest.ResponseRecorder) string {
	var o struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &o)
	return o.Error.Code
}

func TestFull_NonFullTokenStaysOnAllowlist(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	p := oauthas.Principal{UserID: "usr_1", TenantID: "ten_x", Scopes: []string{"mcp:manage"}, APIScopes: oauthas.APIScopes([]string{"mcp:manage"})}
	for _, path := range []string{"/api/v1/team/members", "/api/v1/admin/stats", "/api/v1/policies"} {
		w := env.direct(p, "GET", path)
		if w.Code != 403 || errCodeOf(w) != "endpoint_not_available_for_token" {
			t.Errorf("full olmayan token %s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := env.direct(p, "GET", "/api/v1/tunnels"); w.Code != 200 {
		t.Errorf("allowlist ucu calismali: %d", w.Code)
	}
}

func TestFull_PanelEquivalentRoleAndPolicies(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	// Owner: panel-only uc calisir, API kapsam listesi YOK (panel gibi).
	w := env.direct(fullPrin(), "GET", "/api/v1/team/members")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"owner"`) || !strings.Contains(w.Body.String(), `"scoped":false`) {
		t.Fatalf("owner: %d %s", w.Code, w.Body)
	}
	if w := env.direct(fullPrin(), "GET", "/api/v1/policies"); w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"owner"`) {
		t.Fatalf("gercek rol iletilmeli: %d %s", w.Code, w.Body)
	}
	// Member: admin'in yapabildigini YAPAMAZ (kullanici yetkisini asmaz).
	env.st.mu.Lock()
	env.st.members["ten_x|usr_1"] = "member"
	env.st.mu.Unlock()
	w = env.direct(fullPrin(), "GET", "/api/v1/team/members")
	if w.Code != 403 || errCodeOf(w) != "forbidden" {
		t.Fatalf("member owner/admin ucuna erismemeli: %d %s", w.Code, w.Body)
	}
	// Uyelik bitti: 401.
	env.st.mu.Lock()
	delete(env.st.members, "ten_x|usr_1")
	env.st.mu.Unlock()
	if w := env.direct(fullPrin(), "GET", "/api/v1/policies"); w.Code != 401 {
		t.Fatalf("uyeligi biten full token: %d", w.Code)
	}
}

func TestFull_PlatformAdminOnlyIfReallyPlatformAdmin(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	// Owner olsa bile ten_x platform kiracisi degil -> admin uclari 403.
	if w := env.direct(fullPrin(), "GET", "/api/v1/admin/stats"); w.Code != 403 || errCodeOf(w) != "forbidden" {
		t.Fatalf("platform admin degil: %d %s", w.Code, w.Body)
	}
	// ten_default owner/admin -> platform admin.
	env.st.mu.Lock()
	env.st.members["ten_default|usr_1"] = "owner"
	env.st.mu.Unlock()
	p := fullPrin()
	p.TenantID = "ten_default"
	if w := env.direct(p, "GET", "/api/v1/admin/stats"); w.Code != 200 {
		t.Fatalf("gercek platform admin: %d %s", w.Code, w.Body)
	}
}

func TestFull_ExcludedEndpoints(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	cases := [][2]string{
		{"POST", "/api/v1/api-tokens"}, {"GET", "/api/v1/api-tokens"}, {"DELETE", "/api/v1/api-tokens/abc"}, {"POST", "/api/v1/api-tokens/abc/rotate"},
		{"POST", "/api/v1/tokens"}, {"DELETE", "/api/v1/tokens/abc"},
		{"POST", "/api/v1/team/members/m1/tokens"}, {"DELETE", "/api/v1/team/members/m1/tokens/c1"},
		{"POST", "/api/v1/service-accounts"}, {"DELETE", "/api/v1/service-accounts/s1"},
		{"GET", "/api/v1/mail/app-passwords"}, {"POST", "/api/v1/mail/app-passwords"},
		{"POST", "/api/v1/tunnels/t1/mcp/keys"}, {"DELETE", "/api/v1/tunnels/t1/mcp/keys/k1"},
		{"POST", "/api/v1/device/approve"}, {"GET", "/api/v1/auth/sso/start"}, {"POST", "/api/v1/auth/logout"},
		{"DELETE", "/api/v1/organization"},
	}
	for _, c := range cases {
		w := env.direct(fullPrin(), c[0], c[1])
		if w.Code != 403 || errCodeOf(w) != "full_scope_excluded" {
			t.Errorf("%s %s haric olmali: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
	if env.called["api_token_create"] != 0 || env.called["org_delete"] != 0 {
		t.Fatalf("haric uclarin handler'i cagrilmamali: %v", env.called)
	}
	// Haric olmayan benzer yollar calisir.
	for _, c := range [][2]string{{"POST", "/api/v1/terminal-ticket"}, {"GET", "/api/v1/tunnels/t1"}} {
		if w := env.direct(fullPrin(), c[0], c[1]); w.Code >= 400 {
			t.Errorf("%s %s haric olmamali: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}
	// Alan disi yol.
	if w := env.direct(fullPrin(), "GET", "/oauth/grants"); w.Code != 403 {
		t.Errorf("/api/v1 disi yol: %d", w.Code)
	}
}

func TestFull_PlanGateStillApplies(t *testing.T) {
	env := newOAuthEnv(t, denyEntitlements{})
	if w := env.direct(fullPrin(), "GET", "/api/v1/policies"); w.Code != 403 || errCodeOf(w) != "feature_not_available" {
		t.Fatalf("plan kapisi full'da da gecerli: %d %s", w.Code, w.Body)
	}
}

func TestFull_ForgedContextFromOutsideImpossible(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	// Disaridan gelen istek Principal tasiyamaz; baslikla full taklidi isler yapmaz.
	r := httptest.NewRequest("GET", "/api/v1/team/members", nil)
	r.Header.Set("Authorization", "Bearer eyJhbGciOiJFZERTQSJ9.eyJzY29wZSI6ImZ1bGwifQ.sig")
	r.Header.Set("X-Oauth-Full", "1")
	w := httptest.NewRecorder()
	env.mw.ServeHTTP(w, r)
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("taklit reddedilmeli: %d", w.Code)
	}
}

func TestMCPFull_ToolsCallAPIAndAudit(t *testing.T) {
	env := newOAuthEnv(t, allowEntitlements{})
	var audits []string
	env.mcpAudit = &audits
	at, _, _ := env.token("mcp:read full")
	// full isaretlenmediyse call_api yok (varsayilan onay full vermez).
	cs0, err := env.connect(at)
	if err != nil {
		t.Fatal(err)
	}
	if toolNames(t, cs0)["call_api"] {
		t.Fatal("full isaretlenmeden call_api olmamali")
	}
	cs0.Close()

	env.explicit = []string{"full"}
	at, _, _ = env.token("mcp:read full")
	cs, err := env.connect(at)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	names := toolNames(t, cs)
	if !names["call_api"] || !names["list_api_endpoints"] || !names["create_tunnel"] {
		t.Fatalf("full arac kumesi: %v", names)
	}
	call := func(args map[string]any) (map[string]any, bool) {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "call_api", Arguments: args})
		if err != nil {
			return nil, true
		}
		sc, _ := res.StructuredContent.(map[string]any)
		return sc, res.IsError
	}
	// Panel-only uc, gercek rolle.
	out, isErr := call(map[string]any{"method": "GET", "path": "/api/v1/team/members"})
	body, _ := out["body"].(map[string]any)
	if isErr || out["status"] != float64(200) || body["role"] != "owner" {
		t.Fatalf("call_api team/members: %v %v", out, isErr)
	}
	// Haric uc: 403 full_scope_excluded durum olarak doner.
	out, isErr = call(map[string]any{"method": "POST", "path": "/api/v1/api-tokens", "body": map[string]any{"name": "x"}})
	if isErr || out["status"] != float64(403) || !strings.Contains(toJSON(out["body"]), "full_scope_excluded") {
		t.Fatalf("haric uc: %v %v", out, isErr)
	}
	// DELETE confirm'siz hic gitmez; confirm ile gider.
	if _, isErr = call(map[string]any{"method": "DELETE", "path": "/api/v1/tunnels/tun_1"}); !isErr {
		t.Fatal("confirm'siz DELETE reddedilmeli")
	}
	if env.called["delete_tunnel"] != 0 {
		t.Fatal("confirm'siz DELETE handler'a ulasmamali")
	}
	if out, isErr = call(map[string]any{"method": "DELETE", "path": "/api/v1/tunnels/tun_1", "confirm": true}); isErr || out["status"] != float64(204) {
		t.Fatalf("confirm'li DELETE: %v", out)
	}
	if env.called["delete_tunnel"] != 1 {
		t.Fatal("DELETE handler'a ulasmali")
	}
	// Terminal bileti ozel denetim eylemi.
	call(map[string]any{"method": "POST", "path": "/api/v1/terminal-ticket", "body": map[string]any{}})
	// Platform admin degil.
	out, _ = call(map[string]any{"method": "GET", "path": "/api/v1/admin/stats"})
	if out["status"] != float64(403) {
		t.Fatalf("admin ucu 403: %v", out)
	}
	// list_api_endpoints, test mux'ta openapi ucu olmadigindan hata doner; arac kayitli olmasi yeterli.

	joined := strings.Join(audits, "\n")
	for _, want := range []string{"mcp.full_call|GET /api/v1/team/members", "mcp.full_ticket|POST /api/v1/terminal-ticket", "scope=full", "mcp:oauth:Claude Test:usr_1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("denetim kaydinda %q yok:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\"name\"") || strings.Contains(joined, "secret") {
		t.Errorf("govde denetime yazilmamali:\n%s", joined)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = http.StatusOK
