package oauthas

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

const fullRedirect = "https://claude.ai/api/mcp/auth_callback"

// fullConsent: kayit + authorize sorgusu + onay (scopes acikca verilir).
func (f *fixture) fullConsent(reqScope string, granted []string) (clientID, verifier string, q url.Values, code string, status int, body string) {
	f.t.Helper()
	clientID = f.register(fullRedirect)
	verifier, challenge := pkce()
	q = f.authzQuery(clientID, fullRedirect, challenge, reqScope)
	code, w := f.consent(q, "ten_a", granted, "allow")
	return clientID, verifier, q, code, w.Code, w.Body.String()
}

func (f *fixture) exchange(clientID, verifier, code string) (map[string]any, int) {
	return f.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {fullRedirect}, "code_verifier": {verifier}, "resource": {testRes}})
}

func TestFullRequiresExplicitTick(t *testing.T) {
	f := newFixture(t)
	// Istemci full istese bile, scopes gonderilmezse (varsayilan) full VERILMEZ.
	clientID, verifier, q, code, status, body := f.fullConsent("mcp:read full", nil)
	if status != 200 {
		t.Fatalf("varsayilan onay: %d %s", status, body)
	}
	tok, st := f.exchange(clientID, verifier, code)
	if st != 200 || strings.Contains(tok["scope"].(string), "full") || tok["expires_in"].(float64) != 3600 {
		t.Fatalf("full acikca isaretlenmeden verilmemeli: %v", tok)
	}
	_ = q
}

func TestFullGrantedWhenFreshAndExplicit(t *testing.T) {
	f := newFixture(t)
	// Istemci full ISTEMEDI, kullanici isaretledi -> verilir (taze giris).
	clientID, verifier, _, code, status, body := f.fullConsent("mcp:read", []string{"mcp:read", "full"})
	if status != 200 {
		t.Fatalf("full onayi: %d %s", status, body)
	}
	tok, st := f.exchange(clientID, verifier, code)
	if st != 200 {
		t.Fatalf("token: %v", tok)
	}
	if !strings.Contains(tok["scope"].(string), "full") {
		t.Fatalf("scope full icermeli: %v", tok["scope"])
	}
	// Kisa omur: access 30 dk.
	if tok["expires_in"].(float64) != 1800 {
		t.Fatalf("full access 30 dk olmali: %v", tok["expires_in"])
	}
	at := tok["access_token"].(string)
	p, err := f.s.Authenticate(f.authReq(at), at)
	if err != nil || !p.Full || p.StepUpAt == nil || !p.StepUpAt.Equal(f.now) {
		t.Fatalf("principal full/step-up: %+v %v", p, err)
	}
	// Grant'te step-up zamani saklanir; listede full gorunur.
	gw := f.do("GET", "/oauth/grants", nil, map[string]string{"X-Test-User": "usr_1"})
	var gl struct {
		Grants []struct {
			Full     bool       `json:"full"`
			StepUpAt *time.Time `json:"step_up_at"`
		} `json:"grants"`
	}
	_ = json.Unmarshal(gw.Body.Bytes(), &gl)
	if len(gl.Grants) != 1 || !gl.Grants[0].Full || gl.Grants[0].StepUpAt == nil {
		t.Fatalf("grant listesi: %s", gw.Body)
	}
	// Refresh 7 gun; yenilenen token da 30 dk.
	rt := tok["refresh_token"].(string)
	out, st := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}})
	if st != 200 || out["expires_in"].(float64) != 1800 {
		t.Fatalf("refresh: %v", out)
	}
	f.now = f.now.Add(6 * 24 * time.Hour)
	if _, st := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {out["refresh_token"].(string)}, "client_id": {clientID}}); st != 200 {
		t.Fatalf("6. gun refresh gecmeli: %d", st)
	}
}

func TestFullRefreshExpiresAfterSevenDaysAndHardCap(t *testing.T) {
	f := newFixture(t)
	clientID, verifier, _, code, _, _ := f.fullConsent("mcp:read", []string{"full"})
	tok, _ := f.exchange(clientID, verifier, code)
	rt := tok["refresh_token"].(string)
	f.now = f.now.Add(8 * 24 * time.Hour)
	if _, st := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}); st != 400 {
		t.Fatalf("7 gunden eski full refresh reddedilmeli: %d", st)
	}
	// Kayan yenileme: 31. gunde grant tavani asildi.
	g := newFixture(t)
	cid, ver, _, c2, _, _ := g.fullConsent("mcp:read", []string{"full"})
	tk, _ := g.exchange(cid, ver, c2)
	r := tk["refresh_token"].(string)
	for i := 0; i < 8; i++ {
		g.now = g.now.Add(4 * 24 * time.Hour)
		out, st := g.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {r}, "client_id": {cid}})
		if i < 7 {
			if st != 200 {
				t.Fatalf("adim %d: %d %v", i, st, out)
			}
			r = out["refresh_token"].(string)
			continue
		}
		if st != 400 { // 32. gun > 30 gun
			t.Fatalf("30 gunluk tavan asilinca reddedilmeli: %d", st)
		}
	}
}

func TestFullStepUpStaleUnknownAndFuture(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		unknown bool
		ok      bool
	}{
		{"taze", 30 * time.Second, false, true},
		{"sinirda", 5 * time.Minute, false, true},
		{"eski", 5*time.Minute + time.Second, false, false},
		{"cok eski", 3 * time.Hour, false, false},
		{"bilinmiyor", 0, true, false},
		{"gelecek", -10 * time.Minute, false, false},
	}
	for _, c := range cases {
		f := newFixture(t)
		f.authAge, f.unknownAuth = c.age, c.unknown
		_, _, _, code, status, body := f.fullConsent("mcp:read", []string{"full"})
		if c.ok && (status != 200 || code == "") {
			t.Errorf("%s: onaylanmali: %d %s", c.name, status, body)
		}
		if !c.ok {
			if status != 403 || !strings.Contains(body, "reauth_required") {
				t.Errorf("%s: 403 reauth_required beklenir: %d %s", c.name, status, body)
			}
		}
	}
}

func TestFullStaleSessionStillAllowsNormalScopes(t *testing.T) {
	f := newFixture(t)
	f.authAge = 2 * time.Hour
	_, _, _, code, status, _ := f.fullConsent("mcp:read", []string{"mcp:read"})
	if status != 200 || code == "" {
		t.Fatalf("full olmayan onay eski oturumla da calismali: %d", status)
	}
}

func TestFullConsentInfoReportsFreshness(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(fullRedirect)
	_, challenge := pkce()
	q := f.authzQuery(clientID, fullRedirect, challenge, "mcp:read full")
	info := func() map[string]any {
		w := f.do("GET", "/oauth/consent/info?"+q.Encode(), nil, map[string]string{"X-Test-User": "usr_1"})
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	m := info()
	if m["full_available"] != true || m["full_requested"] != true || m["fresh_auth"] != true {
		t.Fatalf("info: %v", m)
	}
	for _, s := range m["scopes"].([]any) {
		if s.(map[string]any)["id"] == "full" {
			t.Fatal("full normal kapsam listesinde olmamali (ayri bolum)")
		}
	}
	f.authAge = time.Hour
	if info()["fresh_auth"] != false {
		t.Fatal("eski oturum fresh_auth=false olmali")
	}
}

func TestFullNotForTunnelResourceAndTooMuchScope(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(fullRedirect)
	_, challenge := pkce()
	// full olmayan istemcinin fazla kapsami hala reddedilir.
	q := f.authzQuery(clientID, fullRedirect, challenge, "mcp:read")
	if _, w := f.consent(q, "ten_a", []string{"mcp:read", "tunnels:write"}, "allow"); w.Code != 400 {
		t.Fatalf("fazla kapsam reddedilmeli: %d", w.Code)
	}
	// full istenirse kullanici diger kapsamlari serbestce secebilir (full hepsini kapsar).
	q = f.authzQuery(clientID, fullRedirect, challenge, "full")
	if code, w := f.consent(q, "ten_a", []string{"tunnels:write"}, "allow"); code == "" {
		t.Fatalf("full isteyen istemcide kapsam secimi: %d %s", w.Code, w.Body)
	}
	// Tunel kaynaginda full gecersiz kapsam.
	q = f.authzQuery(clientID, fullRedirect, challenge, "full")
	q.Set("resource", "https://tun--x.zorven.app/mcp")
	if w := f.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil); w.Code == 302 && strings.Contains(w.Header().Get("Location"), "/oauth/consent?") {
		t.Fatalf("tunel kaynaginda full onaya gitmemeli: %s", w.Header().Get("Location"))
	}
}

func TestFullRevoke(t *testing.T) {
	f := newFixture(t)
	clientID, verifier, _, code, _, _ := f.fullConsent("full", []string{"full"})
	tok, _ := f.exchange(clientID, verifier, code)
	at := tok["access_token"].(string)
	if _, err := f.s.Authenticate(f.authReq(at), at); err != nil {
		t.Fatal(err)
	}
	gw := f.do("GET", "/oauth/grants", nil, map[string]string{"X-Test-User": "usr_1"})
	var gl struct {
		Grants []struct {
			ID string `json:"id"`
		} `json:"grants"`
	}
	_ = json.Unmarshal(gw.Body.Bytes(), &gl)
	dw := f.do("DELETE", "/oauth/grants/"+gl.Grants[0].ID, nil, map[string]string{"X-Test-User": "usr_1", "Origin": testPanel})
	if dw.Code != 204 {
		t.Fatalf("iptal: %d", dw.Code)
	}
	f.now = f.now.Add(time.Minute) // grant onbellegi
	if _, err := f.s.Authenticate(f.authReq(at), at); err == nil {
		t.Fatal("iptal sonrasi full token gecmemeli")
	}
}

func TestScopeCoversFull(t *testing.T) {
	if !subsetOf([]string{"tunnels:write", "mcp:manage", "analytics:read"}, []string{"full"}) {
		t.Fatal("full her yonetim kapsamini kapsamali")
	}
	if subsetOf([]string{"mcp"}, []string{"full"}) {
		t.Fatal("full tunel 'mcp' kapsamini kapsamamali")
	}
	if subsetOf([]string{"full"}, []string{"mcp:manage"}) {
		t.Fatal("mcp:manage full'u kapsamamali")
	}
}
