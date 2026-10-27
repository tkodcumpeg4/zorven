package oauthas

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer = "https://zorven.app"
	testPanel  = "https://panel.zorven.app"
	testRes    = "https://zorven.app/mcp"
)

type fixture struct {
	t     *testing.T
	s     *Server
	st    *MemStore
	now   time.Time
	orgs  []Org
	seed  []byte
	login string // X-Test-User
	// authAge, panel oturumunun ne kadar once acildigi (0 = simdi, taze);
	// unknownAuth true ise AuthTime bilinmiyor (sifir deger).
	authAge     time.Duration
	unknownAuth bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, st: NewMemStore(), now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
		orgs: []Org{{ID: "ten_a", Name: "Acme", Role: "owner"}, {ID: "ten_b", Name: "Beta", Role: "member"}},
		seed: bytes.Repeat([]byte{7}, 32), login: "usr_1"}
	s, err := New(Config{
		Store: f.st, SigningSeed: f.seed, Issuer: testIssuer, ConsentBase: testPanel,
		Session: func(r *http.Request) (Session, bool) {
			u := r.Header.Get("X-Test-User")
			if u == "" {
				return Session{}, false
			}
			ss := Session{UserID: u, Email: u + "@example.com", AuthTime: f.now.Add(-f.authAge)}
			if f.unknownAuth {
				ss.AuthTime = time.Time{}
			}
			return ss, true
		},
		Orgs: func(_ context.Context, userID string) ([]Org, error) {
			if userID == "usr_2" {
				return []Org{{ID: "ten_a", Name: "Acme", Role: "member"}}, nil
			}
			return f.orgs, nil
		},
		OriginTrusted: func(r *http.Request) bool { return r.Header.Get("Origin") == testPanel },
		Now:           func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	return f
}

func (f *fixture) do(method, path string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://zorven.app"+path, bytes.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, r)
	return w
}

func (f *fixture) register(redirect string) string {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "client_name": "Claude"})
	w := f.do("POST", "/oauth/register", body, map[string]string{"Content-Type": "application/json"})
	if w.Code != 201 {
		f.t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out["client_id"].(string)
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("a1B2c3D4e5", 5)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, b64.EncodeToString(sum[:])
}

func (f *fixture) authzQuery(clientID, redirect, challenge, scope string) url.Values {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", "st123")
	q.Set("scope", scope)
	q.Set("resource", testRes)
	return q
}

// consent, onay sayfasinin yaptigi decision cagrisini yapar ve code'u doner.
func (f *fixture) consent(q url.Values, tenant string, scopes []string, decision string) (code string, w *httptest.ResponseRecorder) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"client_id": q.Get("client_id"), "redirect_uri": q.Get("redirect_uri"), "response_type": "code",
		"code_challenge": q.Get("code_challenge"), "code_challenge_method": "S256", "state": q.Get("state"),
		"scope": q.Get("scope"), "resource": q.Get("resource"), "decision": decision, "tenant_id": tenant, "scopes": scopes,
	})
	w = f.do("POST", "/oauth/consent/decision", body, map[string]string{"Origin": testPanel, "X-Test-User": f.login, "Content-Type": "application/json"})
	if w.Code != 200 {
		return "", w
	}
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	u, _ := url.Parse(out["redirect"])
	if u.Query().Get("state") != "st123" || u.Query().Get("iss") != testIssuer {
		f.t.Fatalf("state/iss eksik: %s", out["redirect"])
	}
	return u.Query().Get("code"), w
}

func (f *fixture) token(form url.Values) (map[string]any, int) {
	f.t.Helper()
	w := f.do("POST", "/oauth/token", []byte(form.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out, w.Code
}

// fullFlow: kayit -> authorize -> onay -> token.
func (f *fixture) fullFlow(scope string) (clientID string, tok map[string]any, verifier string) {
	f.t.Helper()
	redirect := "https://claude.ai/api/mcp/auth_callback"
	clientID = f.register(redirect)
	verifier, challenge := pkce()
	q := f.authzQuery(clientID, redirect, challenge, scope)
	w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), testPanel+"/oauth/consent?") {
		f.t.Fatalf("authorize: %d %s", w.Code, w.Header().Get("Location"))
	}
	// info
	iw := f.do("GET", "/oauth/consent/info?"+q.Encode(), nil, map[string]string{"X-Test-User": f.login})
	if iw.Code != 200 || !strings.Contains(iw.Body.String(), `"client_name":"Claude"`) {
		f.t.Fatalf("info: %d %s", iw.Code, iw.Body)
	}
	code, cw := f.consent(q, "ten_a", nil, "allow")
	if code == "" {
		f.t.Fatalf("consent: %d %s", cw.Code, cw.Body)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {testRes}}
	out, status := f.token(form)
	if status != 200 {
		f.t.Fatalf("token: %d %v", status, out)
	}
	return clientID, out, verifier
}

func (f *fixture) authReq(tok string) *http.Request {
	r := httptest.NewRequest("POST", "https://zorven.app/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

func TestMetadataDocuments(t *testing.T) {
	f := newFixture(t)
	w := f.do("GET", "/.well-known/oauth-authorization-server", nil, nil)
	var as map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &as)
	if w.Code != 200 || as["issuer"] != testIssuer || as["token_endpoint"] != testIssuer+"/oauth/token" ||
		as["registration_endpoint"] != testIssuer+"/oauth/register" || as["authorization_endpoint"] != testIssuer+"/oauth/authorize" ||
		as["revocation_endpoint"] != testIssuer+"/oauth/revoke" {
		t.Fatalf("AS metadata: %d %v", w.Code, as)
	}
	if m, _ := as["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Errorf("yalniz S256 duyurulmali: %v", as["code_challenge_methods_supported"])
	}
	if as["authorization_response_iss_parameter_supported"] != true {
		t.Error("iss parametresi duyurulmali")
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := f.do("GET", p, nil, nil)
		var prm map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &prm)
		if w.Code != 200 || prm["resource"] != testRes {
			t.Fatalf("PRM %s: %d %v", p, w.Code, prm)
		}
		if a, _ := prm["authorization_servers"].([]any); len(a) != 1 || a[0] != testIssuer {
			t.Errorf("authorization_servers: %v", prm["authorization_servers"])
		}
	}
	w = f.do("GET", "/.well-known/jwks.json", nil, nil)
	var jwks struct {
		Keys []map[string]string `json:"keys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &jwks)
	if len(jwks.Keys) != 1 || jwks.Keys[0]["kty"] != "OKP" || jwks.Keys[0]["crv"] != "Ed25519" || jwks.Keys[0]["kid"] != keyID(f.s.pub) {
		t.Fatalf("jwks: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), `"d"`) {
		t.Fatal("JWKS ozel anahtar icermemeli")
	}
}

func TestDCRValidation(t *testing.T) {
	f := newFixture(t)
	bad := []string{
		"javascript:alert(1)", "ftp://x.example/cb", "http://evil.example/cb", "myapp://cb",
		"https://x.example/cb#frag", "https://user:pw@x.example/cb", "/relative", "https:///nohost",
	}
	for _, u := range bad {
		body, _ := json.Marshal(map[string]any{"redirect_uris": []string{u}})
		w := f.do("POST", "/oauth/register", body, nil)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_redirect_uri") {
			t.Errorf("%q reddedilmeliydi: %d %s", u, w.Code, w.Body)
		}
	}
	good := []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:6274/oauth/callback", "http://127.0.0.1:9/cb", "http://[::1]:5/cb"}
	for _, u := range good {
		body, _ := json.Marshal(map[string]any{"redirect_uris": []string{u}, "client_name": "x"})
		if w := f.do("POST", "/oauth/register", body, nil); w.Code != 201 {
			t.Errorf("%q kabul edilmeliydi: %d %s", u, w.Code, w.Body)
		}
	}
	for _, body := range []string{
		`{}`, `{"redirect_uris":[]}`, `not json`,
		`{"redirect_uris":["https://a.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		`{"redirect_uris":["https://a.example/cb"],"grant_types":["password"]}`,
	} {
		if w := f.do("POST", "/oauth/register", []byte(body), nil); w.Code != 400 {
			t.Errorf("%s reddedilmeliydi: %d", body, w.Code)
		}
	}
}

func TestFullFlowAndAuthenticate(t *testing.T) {
	f := newFixture(t)
	_, tok, _ := f.fullFlow("mcp:read")
	if tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 || tok["scope"] != "mcp:read" {
		t.Fatalf("token yaniti: %v", tok)
	}
	p, err := f.s.Authenticate(f.authReq(tok["access_token"].(string)), tok["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != "usr_1" || p.TenantID != "ten_a" || p.ClientName != "Claude" || len(p.APIScopes) != 1 || p.APIScopes[0] != "read" {
		t.Fatalf("principal: %+v", p)
	}
	if !strings.HasPrefix(tok["refresh_token"].(string), "zor_rt_") {
		t.Fatal("refresh token bicimi")
	}
	// Refresh token DB'de ham saklanmaz.
	if _, err := f.st.RotateRefresh(context.Background(), tok["refresh_token"].(string), f.now); err == nil {
		t.Fatal("ham refresh token ile depoda kayit bulunmamali (yalniz ozet saklanir)")
	}
}

func TestManageScopeExpansion(t *testing.T) {
	got := APIScopes([]string{ScopeMCPManage})
	want := map[string]bool{"read": true, "tunnels:write": true, "hostnames:write": true, "clients:write": true}
	if len(got) != len(want) {
		t.Fatalf("mcp:manage: %v", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("beklenmeyen kapsam %s", s)
		}
	}
}

func TestPKCEAndCodeReuse(t *testing.T) {
	f := newFixture(t)
	redirect := "https://claude.ai/api/mcp/auth_callback"
	cid := f.register(redirect)
	verifier, challenge := pkce()
	q := f.authzQuery(cid, redirect, challenge, "mcp:read")
	code, _ := f.consent(q, "ten_a", nil, "allow")
	form := func(c, v string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {c}, "client_id": {cid}, "redirect_uri": {redirect}, "code_verifier": {v}}
	}
	// Yanlis verifier: kod tuketilir ama reddedilir.
	if out, st := f.token(form(code, strings.Repeat("z", 50))); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("yanlis verifier: %d %v", st, out)
	}
	// Dogru verifier ile bile ikinci kullanim olmaz (tek kullanimlik).
	if _, st := f.token(form(code, verifier)); st != 400 {
		t.Fatal("kod yeniden kullanilamamali")
	}
	// Baska istemci kodu kullanamaz.
	code2, _ := f.consent(q, "ten_a", nil, "allow")
	other := f.register("https://other.example/cb")
	fo := form(code2, verifier)
	fo.Set("client_id", other)
	if _, st := f.token(fo); st != 400 {
		t.Fatal("kod baska istemciye ait")
	}
	// redirect_uri uyusmazligi.
	code3, _ := f.consent(q, "ten_a", nil, "allow")
	fr := form(code3, verifier)
	fr.Set("redirect_uri", "https://claude.ai/other")
	if _, st := f.token(fr); st != 400 {
		t.Fatal("redirect_uri eslesmeli")
	}
	// resource uyusmazligi.
	code4, _ := f.consent(q, "ten_a", nil, "allow")
	fres := form(code4, verifier)
	fres.Set("resource", "https://evil.example/mcp")
	if out, st := f.token(fres); st != 400 || out["error"] != "invalid_target" {
		t.Fatalf("resource: %d %v", st, out)
	}
	// plain / PKCE'siz authorize.
	q2 := f.authzQuery(cid, redirect, challenge, "mcp:read")
	q2.Set("code_challenge_method", "plain")
	w := f.do("GET", "/oauth/authorize?"+q2.Encode(), nil, nil)
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "error=invalid_request") || !strings.HasPrefix(w.Header().Get("Location"), redirect) {
		t.Fatalf("plain reddedilmeli ve hata istemciye donmeli: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestAuthorizeRejectsBadClientAndRedirect(t *testing.T) {
	f := newFixture(t)
	_, challenge := pkce()
	cid := f.register("https://claude.ai/cb")
	// Kayitsiz redirect_uri: istemciye YONLENDIRILMEZ.
	q := f.authzQuery(cid, "https://attacker.example/cb", challenge, "mcp:read")
	w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatalf("kayitsiz redirect: %d", w.Code)
	}
	// Bilinmeyen client.
	q = f.authzQuery("zoc_nope", "https://claude.ai/cb", challenge, "mcp:read")
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); w.Code != 400 {
		t.Fatalf("bilinmeyen client: %d", w.Code)
	}
	// Yanlis resource -> invalid_target (redirect ile).
	q = f.authzQuery(cid, "https://claude.ai/cb", challenge, "mcp:read")
	q.Set("resource", "https://evil.example/mcp")
	w = f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "error=invalid_target") {
		t.Fatalf("invalid_target: %d %s", w.Code, w.Header().Get("Location"))
	}
	// Bilinmeyen kapsam.
	q = f.authzQuery(cid, "https://claude.ai/cb", challenge, "root")
	w = f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=invalid_scope") {
		t.Fatalf("invalid_scope: %s", w.Header().Get("Location"))
	}
	// Dongu istemcisinde port serbest.
	lc := f.register("http://127.0.0.1:1111/cb")
	q = f.authzQuery(lc, "http://127.0.0.1:2222/cb", challenge, "mcp:read")
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), testPanel) {
		t.Fatalf("dongu portu: %d", w.Code)
	}
	// Dongu degilse port/yol degisemez.
	q = f.authzQuery(cid, "https://claude.ai/cb?x=1", challenge, "mcp:read")
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); w.Code != 400 {
		t.Fatal("https redirect tam eslesmeli")
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	f := newFixture(t)
	cid, tok, _ := f.fullFlow("mcp:read")
	rt1 := tok["refresh_token"].(string)
	at1 := tok["access_token"].(string)
	ref := func(rt string) (map[string]any, int) {
		return f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {cid}})
	}
	out2, st := ref(rt1)
	if st != 200 || out2["refresh_token"] == rt1 || out2["access_token"] == at1 {
		t.Fatalf("donus: %d %v", st, out2)
	}
	rt2 := out2["refresh_token"].(string)
	at2 := out2["access_token"].(string)
	if _, err := f.s.Authenticate(f.authReq(at2), at2); err != nil {
		t.Fatal(err)
	}
	// Eski refresh yeniden kullanilir: aile iptal edilir.
	if o, st := ref(rt1); st != 400 || o["error"] != "invalid_grant" {
		t.Fatalf("yeniden kullanim: %d %v", st, o)
	}
	if _, st := ref(rt2); st != 400 {
		t.Fatal("yeni refresh de iptal edilmeli (aile)")
	}
	if _, err := f.s.Authenticate(f.authReq(at2), at2); err == nil {
		t.Fatal("aile iptalinden sonra access token gecersiz olmali")
	}
	// Yanlis istemci.
	f2 := newFixture(t)
	cid2, tok2, _ := f2.fullFlow("mcp:read")
	_ = cid2
	out, st := f2.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok2["refresh_token"].(string)}, "client_id": {"zoc_other"}})
	if st != 400 {
		t.Fatalf("baska istemci refresh edemez: %v", out)
	}
}

func TestRefreshScopeNarrowing(t *testing.T) {
	f := newFixture(t)
	cid, tok, _ := f.fullFlow("mcp:manage")
	out, st := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {cid}, "scope": {"mcp:read"}})
	if st != 200 || out["scope"] != "mcp:read" {
		t.Fatalf("daraltma: %d %v", st, out)
	}
	// Genisletme olmaz.
	cid2, tok2, _ := f.fullFlow("mcp:read")
	_, st = f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok2["refresh_token"].(string)}, "client_id": {cid2}, "scope": {"mcp:manage"}})
	if st != 400 {
		t.Fatal("kapsam genisletilemez")
	}
}

func TestAudienceMismatchExpiredRevoked(t *testing.T) {
	f := newFixture(t)
	_, tok, _ := f.fullFlow("mcp:read")
	at := tok["access_token"].(string)
	g, _ := f.st.ListGrants(context.Background(), "usr_1", "")
	if len(g) != 1 {
		t.Fatalf("grant sayisi %d", len(g))
	}
	mint := func(mut func(*claims)) string {
		c := claims{Iss: testIssuer, Sub: "usr_1", Aud: testRes, Scope: "mcp:read", Tid: "ten_a", Gid: g[0].ID,
			Iat: f.now.Unix(), Exp: f.now.Add(time.Hour).Unix(), Jti: "x"}
		mut(&c)
		s, err := signJWT(f.s.priv, c)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Audience: baska kaynak.
	bad := mint(func(c *claims) { c.Aud = "https://acme.zorven.app/mcp" })
	if _, err := f.s.Authenticate(f.authReq(bad), bad); err == nil {
		t.Fatal("aud uyusmazligi reddedilmeli")
	}
	bad = mint(func(c *claims) { c.Iss = "https://evil.example" })
	if _, err := f.s.Authenticate(f.authReq(bad), bad); err == nil {
		t.Fatal("iss uyusmazligi reddedilmeli")
	}
	// Kapsam yukseltme: grant mcp:read, token mcp:manage diyor.
	bad = mint(func(c *claims) { c.Scope = "mcp:manage" })
	if _, err := f.s.Authenticate(f.authReq(bad), bad); err == nil {
		t.Fatal("grant disi kapsam reddedilmeli")
	}
	// Baska anahtarla imza.
	_, otherPriv, _ := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	forged, _ := signJWT(otherPriv, claims{Iss: testIssuer, Sub: "usr_1", Aud: testRes, Scope: "mcp:read", Tid: "ten_a", Gid: g[0].ID, Exp: f.now.Add(time.Hour).Unix()})
	if _, err := f.s.Authenticate(f.authReq(forged), forged); err == nil {
		t.Fatal("sahte imza reddedilmeli")
	}
	// alg=none benzeri biçim.
	parts := strings.Split(at, ".")
	none := b64.EncodeToString([]byte(`{"alg":"none","kid":"`+keyID(f.s.pub)+`"}`)) + "." + parts[1] + "."
	if _, err := f.s.Authenticate(f.authReq(none), none); err == nil {
		t.Fatal("alg=none reddedilmeli")
	}
	// Govde kurcalama.
	tampered := parts[0] + "." + b64.EncodeToString([]byte(`{"iss":"`+testIssuer+`","sub":"usr_2","aud":"`+testRes+`","tid":"ten_a","gid":"`+g[0].ID+`","scope":"mcp:read","exp":9999999999}`)) + "." + parts[2]
	if _, err := f.s.Authenticate(f.authReq(tampered), tampered); err == nil {
		t.Fatal("kurcalanmis govde reddedilmeli")
	}
	// Suresi dolmus.
	if _, err := f.s.Authenticate(f.authReq(at), at); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if _, err := f.s.Authenticate(f.authReq(at), at); err == nil {
		t.Fatal("suresi dolmus token reddedilmeli")
	}
	f.now = f.now.Add(-2 * time.Hour)
	// Grant iptali (revoke ucu).
	w := f.do("POST", "/oauth/revoke", []byte(url.Values{"token": {tok["refresh_token"].(string)}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, err := f.s.Authenticate(f.authReq(at), at); err == nil {
		t.Fatal("iptal edilen grant reddedilmeli")
	}
	// Bilinmeyen token da 200 (RFC 7009).
	if w := f.do("POST", "/oauth/revoke", []byte("token=garbage"), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); w.Code != 200 {
		t.Fatalf("bilinmeyen token revoke: %d", w.Code)
	}
}

func TestConsentSecurity(t *testing.T) {
	f := newFixture(t)
	redirect := "https://claude.ai/cb"
	cid := f.register(redirect)
	_, challenge := pkce()
	q := f.authzQuery(cid, redirect, challenge, "mcp:read")

	// Oturumsuz info -> 401.
	if w := f.do("GET", "/oauth/consent/info?"+q.Encode(), nil, nil); w.Code != 401 {
		t.Fatalf("oturumsuz: %d", w.Code)
	}
	// Origin'siz decision -> 403 (CSRF).
	body, _ := json.Marshal(map[string]any{"client_id": cid, "redirect_uri": redirect, "response_type": "code",
		"code_challenge": challenge, "code_challenge_method": "S256", "scope": "mcp:read", "resource": testRes, "decision": "allow", "tenant_id": "ten_a"})
	if w := f.do("POST", "/oauth/consent/decision", body, map[string]string{"X-Test-User": "usr_1"}); w.Code != 403 {
		t.Fatalf("origin yok: %d", w.Code)
	}
	if w := f.do("POST", "/oauth/consent/decision", body, map[string]string{"X-Test-User": "usr_1", "Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatalf("yabanci origin: %d", w.Code)
	}
	// Uye olunmayan organizasyon.
	if _, w := f.consent(q, "ten_zzz", nil, "allow"); w.Code != 403 {
		t.Fatalf("uye olmayan org: %d", w.Code)
	}
	// Istenenden fazla kapsam.
	if _, w := f.consent(q, "ten_a", []string{"mcp:manage"}, "allow"); w.Code != 400 {
		t.Fatalf("kapsam yukseltme: %d", w.Code)
	}
	// Bos kapsam.
	if _, w := f.consent(q, "ten_a", []string{}, "allow"); w.Code != 400 {
		t.Fatalf("bos kapsam: %d", w.Code)
	}
	// Reddet -> access_denied yonlendirmesi.
	_, w := f.consent(q, "ten_a", nil, "deny")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "access_denied") {
		t.Fatalf("deny: %d %s", w.Code, w.Body)
	}
	// Kapsam daraltma: manage istendi, read verildi.
	q2 := f.authzQuery(cid, redirect, challenge, "mcp:manage")
	code, cw := f.consent(q2, "ten_a", []string{"mcp:read"}, "allow")
	if code == "" {
		t.Fatalf("daraltma: %s", cw.Body)
	}
	vf, _ := pkce()
	out, st := f.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid}, "redirect_uri": {redirect}, "code_verifier": {vf}})
	if st != 200 || out["scope"] != "mcp:read" {
		t.Fatalf("verilen kapsam: %d %v", st, out)
	}
}

func TestGrantsListAndRevokePermissions(t *testing.T) {
	f := newFixture(t)
	f.fullFlow("mcp:read")
	list := func(user, query string) (int, []grantView) {
		w := f.do("GET", "/oauth/grants"+query, nil, map[string]string{"X-Test-User": user})
		var out struct {
			Grants []grantView `json:"grants"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Grants
	}
	code, gs := list("usr_1", "")
	if code != 200 || len(gs) != 1 || gs[0].ClientName != "Claude" || gs[0].TenantName != "Acme" || !gs[0].Own {
		t.Fatalf("liste: %d %+v", code, gs)
	}
	// Baska kullanici kendi listesinde gormez.
	if _, gs2 := list("usr_9", ""); len(gs2) != 0 {
		t.Fatal("baskasinin grant'i gorunmemeli")
	}
	// Kiraci gorunumu: yalniz owner/admin.
	if code, _ := list("usr_2", "?tenant=ten_a"); code != 403 {
		t.Fatalf("member kiraci listesi: %d", code)
	}
	if code, gs := list("usr_1", "?tenant=ten_a"); code != 200 || len(gs) != 1 {
		t.Fatalf("owner kiraci listesi: %d", code)
	}
	del := func(user, id string, origin bool) int {
		h := map[string]string{"X-Test-User": user}
		if origin {
			h["Origin"] = testPanel
		}
		return f.do("DELETE", "/oauth/grants/"+id, nil, h).Code
	}
	id := gs[0].ID
	if del("usr_1", id, false) != 403 {
		t.Fatal("origin'siz silme 403 olmali")
	}
	if del("usr_2", id, true) != 404 {
		t.Fatal("yetkisiz kullanici silememeli")
	}
	if del("usr_1", id, true) != 204 {
		t.Fatal("sahibi silebilmeli")
	}
	if _, gs = list("usr_1", ""); len(gs) != 0 {
		t.Fatal("iptal sonrasi liste bos olmali")
	}
}

func TestPruneAndMatch(t *testing.T) {
	f := newFixture(t)
	_ = f.st.CreateCode(context.Background(), "h", Code{ExpiresAt: f.now.Add(-time.Hour)})
	if err := f.s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ConsumeCode(context.Background(), "h", f.now.Add(-2*time.Hour)); err == nil {
		t.Fatal("suresi dolmus kod silinmis olmali")
	}
	for p, want := range map[string]bool{"/oauth/token": true, "/oauth/grants/zog_1": true, "/.well-known/oauth-protected-resource/mcp": true,
		"/oauth/consent": false, "/oauth/consent/info": true, "/mcp": false} {
		if Match(p) != want {
			t.Errorf("Match(%s) != %v", p, want)
		}
	}
}

func TestNewRequiresSeed(t *testing.T) {
	if _, err := New(Config{Store: NewMemStore(), SigningSeed: []byte("short")}); err == nil {
		t.Fatal("kisa tohum reddedilmeli")
	}
}

// Kod ikinci kez kullanilinca o kodla verilmis grant iptal edilir (RFC 6749 4.1.2).
func TestCodeReuseRevokesGrant(t *testing.T) {
	f := newFixture(t)
	redirect := "https://claude.ai/api/mcp/auth_callback"
	cid := f.register(redirect)
	verifier, challenge := pkce()
	q := f.authzQuery(cid, redirect, challenge, "mcp:read")
	code, _ := f.consent(q, "ten_a", nil, "allow")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	if out, st := f.token(form); st != 200 || out["access_token"] == nil {
		t.Fatalf("ilk degisim: %d %v", st, out)
	}
	if _, st := f.token(form); st != 400 {
		t.Fatal("ikinci kullanim reddedilmeli")
	}
	gs, err := f.st.ListGrants(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		if g.ClientID == cid && g.Active() {
			t.Fatalf("tekrar kullanimdan sonra grant aktif kalmamali: %+v", g)
		}
	}
}
