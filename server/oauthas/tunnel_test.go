package oauthas

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const tunnelRes = "https://acme-tools.zorven.app/mcp"

func tunnelFixture(t *testing.T, mutate func(*ResourceInfo)) *fixture {
	t.Helper()
	f := newFixture(t)
	info := ResourceInfo{TenantID: "ten_a", TunnelID: "tun_1", Host: "acme-tools.zorven.app",
		Auth: "oauth", AllowOrgMembers: true, AllowedEmails: []string{"misafir@example.com"}}
	if mutate != nil {
		mutate(&info)
	}
	f.s.cfg.ResolveResource = func(_ context.Context, key string) (ResourceInfo, bool) {
		if key == "acme-tools.zorven.app/mcp" {
			return info, true
		}
		return ResourceInfo{}, false
	}
	f.s.cfg.UserInfo = func(_ context.Context, tenantID, userID string) (string, bool, error) {
		switch userID {
		case "usr_1":
			return "usr_1@example.com", tenantID == "ten_a", nil
		case "misafir":
			return "misafir@example.com", false, nil
		case "yabanci":
			return "yabanci@example.com", false, nil
		}
		return "", false, nil
	}
	return f
}

// tunnelFlow: kayit -> authorize -> onay -> token (tunel kaynagi).
func tunnelFlow(t *testing.T, f *fixture, user, scope, res string) (map[string]any, int, string) {
	t.Helper()
	redirect := "https://claude.ai/api/mcp/auth_callback"
	clientID := f.register(redirect)
	verifier, challenge := pkce()
	q := f.authzQuery(clientID, redirect, challenge, scope)
	q.Set("resource", res)
	if scope == "" {
		q.Del("scope")
	}
	f.login = user
	w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if w.Code != 302 {
		t.Fatalf("authorize: %d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(w.Header().Get("Location"), testPanel+"/oauth/consent?") {
		// redirect ile hata (istemciye)
		return map[string]any{"error": w.Header().Get("Location")}, 400, ""
	}
	code, cw := f.consent(q, "", nil, "allow")
	if code == "" {
		return map[string]any{"error": cw.Body.String()}, cw.Code, ""
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {res}}
	out, status := f.token(form)
	return out, status, clientID
}

func TestTunnelResourceFlowAndVerify(t *testing.T) {
	f := tunnelFixture(t, nil)
	tok, status, clientID := tunnelFlow(t, f, "usr_1", "", tunnelRes)
	if status != 200 || tok["scope"] != "mcp" {
		t.Fatalf("token: %d %v", status, tok)
	}
	at := tok["access_token"].(string)
	r := f.authReq(at)

	c, err := f.s.VerifyResource(r, at, "acme-tools.zorven.app/mcp")
	if err != nil || c.Subject != "usr_1" || c.TenantID != "ten_a" || c.ClientName != "Claude" || c.Scope != "mcp" {
		t.Fatalf("VerifyResource: %+v %v", c, err)
	}
	// Baska tunelin/hostun kaynagi icin gecmez.
	for _, key := range []string{"other.zorven.app/mcp", "acme-tools.zorven.app/baska", "zorven.app/mcp"} {
		if _, err := f.s.VerifyResource(r, at, key); err == nil {
			t.Fatalf("%s icin token kabul edildi", key)
		}
	}
	// Tunel token'i Zorven yonetim API'sinde GECMEZ.
	if _, err := f.s.Authenticate(r, at); err == nil {
		t.Fatal("tunel token'i yonetim MCP'sinde kabul edildi")
	}
	// Yonetim token'i tunel kaynaginda GECMEZ.
	f2 := tunnelFixture(t, nil)
	f2.login = "usr_1"
	_, mgmt, _ := f2.fullFlow("mcp:manage")
	mt := mgmt["access_token"].(string)
	if _, err := f2.s.VerifyResource(f2.authReq(mt), mt, "acme-tools.zorven.app/mcp"); err == nil {
		t.Fatal("yonetim token'i tunel kaynaginda kabul edildi")
	}
	if _, err := f2.s.VerifyResource(f2.authReq(mt), mt, "zorven.app/mcp"); err == nil {
		t.Fatal("yonetim token'i (mcp kapsami olmadan) VerifyResource'ta kabul edildi")
	}

	// Refresh: kaynak ve kapsam korunur.
	rt := tok["refresh_token"].(string)
	out, st := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}, "resource": {tunnelRes + "/"}})
	if st != 200 {
		t.Fatalf("refresh: %d %v", st, out)
	}
	at2 := out["access_token"].(string)
	if _, err := f.s.VerifyResource(r, at2, "acme-tools.zorven.app/mcp"); err != nil {
		t.Fatalf("yenilenen token: %v", err)
	}

	// Grant iptal edilince token gecmez.
	f.forgetAll()
	g, _ := f.st.ListGrants(context.Background(), "usr_1", "")
	if len(g) == 0 {
		t.Fatal("grant yok")
	}
	_ = f.st.RevokeGrant(context.Background(), g[0].ID, f.now)
	f.forgetAll()
	if _, err := f.s.VerifyResource(r, at2, "acme-tools.zorven.app/mcp"); err == nil {
		t.Fatal("iptal edilmis grant token'i kabul edildi")
	}
}

func (f *fixture) forgetAll() {
	f.s.mu.Lock()
	f.s.cache = map[string]cachedGrant{}
	f.s.mu.Unlock()
}

func TestTunnelConsentEligibility(t *testing.T) {
	// uye -> izinli; izinli e-posta -> izinli; yabanci -> reddedilir.
	f := tunnelFixture(t, nil)
	for user, wantOK := range map[string]bool{"usr_1": true, "misafir": true, "yabanci": false} {
		out, status, _ := tunnelFlow(t, f, user, "", tunnelRes)
		if wantOK && status != 200 {
			t.Fatalf("%s reddedildi: %d %v", user, status, out)
		}
		if !wantOK && status != 403 {
			t.Fatalf("%s kabul edildi/yanlis hata: %d %v", user, status, out)
		}
	}
	// Org uyeleri kapali: usr_1 de listede olmadigi icin reddedilir.
	f2 := tunnelFixture(t, func(i *ResourceInfo) { i.AllowOrgMembers = false })
	if _, status, _ := tunnelFlow(t, f2, "usr_1", "", tunnelRes); status != 403 {
		t.Fatalf("org uyeleri kapaliyken uye: %d", status)
	}
	// Bos liste + uyeler kapali: kimse.
	f3 := tunnelFixture(t, func(i *ResourceInfo) { i.AllowOrgMembers = false; i.AllowedEmails = nil })
	if _, status, _ := tunnelFlow(t, f3, "misafir", "", tunnelRes); status != 403 {
		t.Fatalf("bos liste herkese acildi: %d", status)
	}
	// consent/info 'allowed' alanini dogru doner ve orgs sizdirmaz.
	q := f.authzQuery(f.register("https://claude.ai/api/mcp/auth_callback"), "https://claude.ai/api/mcp/auth_callback", strings.Repeat("a", 43), "mcp")
	q.Set("resource", tunnelRes)
	iw := f.do("GET", "/oauth/consent/info?"+q.Encode(), nil, map[string]string{"X-Test-User": "yabanci"})
	var info map[string]any
	_ = json.Unmarshal(iw.Body.Bytes(), &info)
	if iw.Code != 200 || info["resource_kind"] != "tunnel" || info["allowed"] != false || info["resource_host"] != "acme-tools.zorven.app" {
		t.Fatalf("info: %d %s", iw.Code, iw.Body)
	}
	if orgs, _ := info["orgs"].([]any); len(orgs) != 0 {
		t.Fatalf("tunel onayinda kullanicinin organizasyonlari sizdi: %v", orgs)
	}
}

func TestTunnelResourceRejections(t *testing.T) {
	f := tunnelFixture(t, nil)
	redirect := "https://claude.ai/api/mcp/auth_callback"
	cid := f.register(redirect)
	_, ch := pkce()
	for name, res := range map[string]string{
		"bilinmeyen":    "https://yok.zorven.app/mcp",
		"http":          "http://acme-tools.zorven.app/mcp",
		"yol farkli":    "https://acme-tools.zorven.app/baska",
		"kullanici bil": "https://u@acme-tools.zorven.app/mcp",
	} {
		q := f.authzQuery(cid, redirect, ch, "mcp")
		q.Set("resource", res)
		w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
		if !strings.Contains(w.Header().Get("Location"), "error=invalid_target") {
			t.Fatalf("%s: %s", name, w.Header().Get("Location"))
		}
	}
	// apikey modundaki tunel OAuth ile yetkilendirilemez.
	f2 := tunnelFixture(t, func(i *ResourceInfo) { i.Auth = "apikey" })
	cid2 := f2.register(redirect)
	q := f2.authzQuery(cid2, redirect, ch, "mcp")
	q.Set("resource", tunnelRes)
	if w := f2.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); !strings.Contains(w.Header().Get("Location"), "error=invalid_target") {
		t.Fatalf("apikey tunel: %s", w.Header().Get("Location"))
	}
	// Kapsam kurallari: tunelde mcp:read, yonetimde mcp gecersiz.
	q = f.authzQuery(cid, redirect, ch, "mcp:read")
	q.Set("resource", tunnelRes)
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); !strings.Contains(w.Header().Get("Location"), "error=invalid_scope") {
		t.Fatalf("tunelde mcp:read: %s", w.Header().Get("Location"))
	}
	q = f.authzQuery(cid, redirect, ch, "mcp")
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); !strings.Contains(w.Header().Get("Location"), "error=invalid_scope") {
		t.Fatalf("yonetimde mcp: %s", w.Header().Get("Location"))
	}
	// ResolveResource yoksa tunel kaynagi hic yetkilendirilemez.
	f3 := newFixture(t)
	cid3 := f3.register(redirect)
	q = f3.authzQuery(cid3, redirect, ch, "mcp")
	q.Set("resource", tunnelRes)
	if w := f3.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); !strings.Contains(w.Header().Get("Location"), "error=invalid_target") {
		t.Fatalf("resolver'siz: %s", w.Header().Get("Location"))
	}
}

func TestDestructiveScopesDefaultOff(t *testing.T) {
	f := newFixture(t)
	redirect := "https://claude.ai/api/mcp/auth_callback"
	cid := f.register(redirect)
	_, ch := pkce()
	scope := "mcp:read tunnels:delete hostnames:delete"
	q := f.authzQuery(cid, redirect, ch, scope)

	// Istemci scopes listesi gondermezse yikici kapsamlar VERILMEZ.
	code, w := f.consent(q, "ten_a", nil, "allow")
	if code == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	verifier, _ := pkce()
	out, st := f.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid},
		"redirect_uri": {redirect}, "code_verifier": {verifier}})
	if st != 200 || out["scope"] != "mcp:read" {
		t.Fatalf("varsayilan kapsam: %d %v", st, out)
	}

	// Acikca secilirse verilir; APIScopes silme + okuma icerir, yazma icermez.
	code, _ = f.consent(q, "ten_a", []string{"mcp:read", "tunnels:delete"}, "allow")
	out, _ = f.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid},
		"redirect_uri": {redirect}, "code_verifier": {verifier}})
	if out["scope"] != "mcp:read tunnels:delete" {
		t.Fatalf("acik secim: %v", out)
	}
	at := out["access_token"].(string)
	p, err := f.s.Authenticate(f.authReq(at), at)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, s := range p.APIScopes {
		has[s] = true
	}
	if !has["tunnels:delete"] || has["tunnels:write"] || has["hostnames:delete"] {
		t.Fatalf("API kapsamlari: %v", p.APIScopes)
	}
	// mcp:manage silme kapsamlarini KAPSAMAZ.
	for _, s := range APIScopes([]string{ScopeMCPManage}) {
		if strings.HasSuffix(s, ":delete") {
			t.Fatalf("mcp:manage silme kapsami icerdi: %s", s)
		}
	}
	// Silme kapsami tek basina mcp:manage ile elde edilemez (alt kume kurali).
	if subsetOf([]string{"tunnels:delete"}, []string{ScopeMCPManage}) {
		t.Fatal("mcp:manage tunnels:delete'i kapsiyor")
	}
	// ScopeInfo yikici isaretini tasir.
	if si, _ := scopeInfo("tunnels:delete"); !si.Destructive {
		t.Fatal("tunnels:delete yikici isaretli degil")
	}
}

func TestUpstreamIdentitySignedAndNotAnAccessToken(t *testing.T) {
	f := tunnelFixture(t, nil)
	jws, err := f.s.SignUpstream(UpstreamIdentity{Subject: "usr_1", Email: "a@b.c", TenantID: "ten_a", TunnelID: "tun_1",
		ClientID: "cid", Scope: "mcp", AuthMethod: "oauth"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatal("JWS bicimi")
	}
	sig, _ := b64.DecodeString(parts[2])
	if !ed25519.Verify(f.s.pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatal("imza JWKS anahtariyla dogrulanamadi")
	}
	pb, _ := b64.DecodeString(parts[1])
	var c map[string]any
	_ = json.Unmarshal(pb, &c)
	if c["aud"] != UpstreamAudience || c["sub"] != "usr_1" || c["tunnel"] != "tun_1" || c["exp"].(float64)-c["iat"].(float64) != 60 {
		t.Fatalf("talepler: %v", c)
	}
	// upstream kimligi access token olarak kabul EDILMEZ (typ ayrimi).
	if _, err := parseJWT(f.s.pub, jws); err == nil {
		t.Fatal("upstream kimligi access token olarak ayristirildi")
	}
	if _, err := f.s.VerifyResource(f.authReq(jws), jws, "acme-tools.zorven.app/mcp"); err == nil {
		t.Fatal("upstream kimligi VerifyResource'ta kabul edildi")
	}
}
