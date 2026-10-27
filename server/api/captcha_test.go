package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/captcha"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// captchaFakeStore, store.Store'u gömer (nil) ve yalnız CAPTCHA + denetim yöntemlerini uygular.
type captchaFakeStore struct {
	store.Store
	mu       sync.Mutex
	sites    map[string]store.CaptchaSite
	counters []store.CaptchaCounterDelta
	n        int
}

func newCaptchaFake() *captchaFakeStore {
	return &captchaFakeStore{sites: map[string]store.CaptchaSite{}}
}

func (f *captchaFakeStore) ListCaptchaSites(_ context.Context, t string) ([]store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.CaptchaSite{}
	for _, s := range f.sites {
		if s.TenantID == t {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *captchaFakeStore) GetCaptchaSite(_ context.Context, t, id string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sites[id]; ok && s.TenantID == t {
		return s, nil
	}
	return store.CaptchaSite{}, store.ErrNotFound
}
func (f *captchaFakeStore) GetCaptchaSiteByKey(_ context.Context, k string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sites {
		if s.SiteKey == k {
			return s, nil
		}
	}
	return store.CaptchaSite{}, store.ErrNotFound
}
func (f *captchaFakeStore) GetCaptchaSiteBySecretHash(_ context.Context, h string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sites {
		if s.SecretHash == h {
			return s, nil
		}
	}
	return store.CaptchaSite{}, store.ErrNotFound
}
func (f *captchaFakeStore) CreateCaptchaSite(_ context.Context, in store.CaptchaSite) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	in.ID = "csite_" + string(rune('a'+f.n))
	in.CreatedAt = time.Now()
	in.UpdatedAt = in.CreatedAt
	f.sites[in.ID] = in
	return in, nil
}
func (f *captchaFakeStore) UpdateCaptchaSite(_ context.Context, t, id string, p store.CaptchaSitePatch) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sites[id]
	if !ok || s.TenantID != t {
		return store.CaptchaSite{}, store.ErrNotFound
	}
	if p.Name != nil {
		s.Name = *p.Name
	}
	if p.SetDomains {
		s.Domains = p.Domains
	}
	if p.Difficulty != nil {
		s.Difficulty = *p.Difficulty
	}
	if p.PassEnabled != nil {
		s.PassEnabled = *p.PassEnabled
	}
	if p.PassTTLMinutes != nil {
		s.PassTTLMinutes = *p.PassTTLMinutes
	}
	f.sites[id] = s
	return s, nil
}
func (f *captchaFakeStore) RotateCaptchaSiteSecret(_ context.Context, t, id, h string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sites[id]
	if !ok || s.TenantID != t {
		return store.CaptchaSite{}, store.ErrNotFound
	}
	s.SecretHash = h
	f.sites[id] = s
	return s, nil
}
func (f *captchaFakeStore) DeleteCaptchaSite(_ context.Context, t, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sites[id]; !ok || s.TenantID != t {
		return store.ErrNotFound
	}
	delete(f.sites, id)
	return nil
}
func (f *captchaFakeStore) AddCaptchaCounters(_ context.Context, _ time.Time, d []store.CaptchaCounterDelta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters = append(f.counters, d...)
	return nil
}
func (f *captchaFakeStore) CaptchaCounters(_ context.Context, ids []string, _ time.Time) ([]store.CaptchaCounterDay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.CaptchaCounterDay
	for _, c := range f.counters {
		for _, id := range ids {
			if c.SiteID == id {
				out = append(out, store.CaptchaCounterDay{SiteID: id, Day: time.Now(), Challenge: c.Challenge, Solve: c.Solve,
					VerifyOK: c.VerifyOK, VerifyFail: c.VerifyFail, Renewals: c.Renewals, UniquePasses: c.UniquePasses})
			}
		}
	}
	return out, nil
}
func (f *captchaFakeStore) PruneCaptchaCounters(context.Context, time.Time) error { return nil }

type captchaRig struct {
	srv *Server
	fs  *captchaFakeStore
	mux http.Handler
}

func newCaptchaRig(t *testing.T) *captchaRig {
	t.Helper()
	fs := newCaptchaFake()
	svc := captcha.New(captcha.Config{Key: []byte("0123456789abcdef0123456789abcdef"), Store: fs,
		PlatformSiteKey: "zcs_platformtest", PlatformSecret: "zcsec_platformtest"})
	srv := &Server{Store: fs, Captcha: svc}
	return &captchaRig{srv: srv, fs: fs, mux: srv.Routes()}
}

// tenantReq, oturumlu (tenant+rol) istek uretir.
func (rg *captchaRig) tenantReq(method, path, role, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := withTenant(req.Context(), "ten_1")
	ctx = withUser(ctx, &AuthUser{ID: "u1", Email: "u@x.test", TenantID: "ten_1", Role: role})
	rec := httptest.NewRecorder()
	rg.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func (rg *captchaRig) public(method, path, origin, ctype, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/126.0")
	req.Header.Set("Accept-Language", "tr")
	req.RemoteAddr = "203.0.113.7:4444"
	rec := httptest.NewRecorder()
	rg.mux.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("JSON: %v (%s)", err, rec.Body.String())
	}
}

func (rg *captchaRig) createSite(t *testing.T, body string) (id, key, secret string) {
	t.Helper()
	rec := rg.tenantReq("POST", "/api/v1/captcha/sites", "admin", body)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var o captchaSiteOut
	decodeBody(t, rec, &o)
	return o.ID, o.SiteKey, o.Secret
}

func TestCaptchaAssetServed(t *testing.T) {
	rg := newCaptchaRig(t)
	rec := rg.public("GET", "/captcha/v1/api.js", "", "", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content-type: %s", ct)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=86400") {
		t.Errorf("onbellek basliklari: %q %q", etag, rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(rec.Body.String(), "zorvenCaptcha") || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("widget govdesi/CORS")
	}
	req := httptest.NewRequest("GET", "/captcha/v1/api.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	rg.mux.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("304 beklenir: %d", rec2.Code)
	}
}

func TestCaptchaSitesAuthGuards(t *testing.T) {
	rg := newCaptchaRig(t)
	if rec := rg.tenantReq("POST", "/api/v1/captcha/sites", "member", `{"name":"x"}`); rec.Code != 403 {
		t.Errorf("member olusturamamali: %d", rec.Code)
	}
	id, _, _ := rg.createSite(t, `{"name":"Blog"}`)
	for _, c := range []struct{ m, p, b string }{
		{"PATCH", "/api/v1/captcha/sites/" + id, `{"name":"y"}`},
		{"DELETE", "/api/v1/captcha/sites/" + id, ``},
		{"POST", "/api/v1/captcha/sites/" + id + "/rotate-secret", ``},
	} {
		if rec := rg.tenantReq(c.m, c.p, "member", c.b); rec.Code != 403 {
			t.Errorf("member %s %s: %d", c.m, c.p, rec.Code)
		}
	}
	if rec := rg.tenantReq("GET", "/api/v1/captcha/sites", "member", ""); rec.Code != 200 {
		t.Errorf("member listeleyebilmeli: %d", rec.Code)
	}
	// baska kiracinin sitesine erisim
	req := httptest.NewRequest("DELETE", "/api/v1/captcha/sites/"+id, nil)
	ctx := withUser(withTenant(req.Context(), "ten_other"), &AuthUser{ID: "u2", Role: "owner", TenantID: "ten_other"})
	rec := httptest.NewRecorder()
	rg.mux.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != 404 {
		t.Errorf("baska kiraci 404 almali: %d", rec.Code)
	}
}

func TestCaptchaSecretShownOnceAndHashed(t *testing.T) {
	rg := newCaptchaRig(t)
	id, key, secret := rg.createSite(t, `{"name":"Blog","domains":["HTTPS://Ornek.com"],"difficulty":18}`)
	if !strings.HasPrefix(secret, "zcsec_") || !strings.HasPrefix(key, "zcs_") {
		t.Fatalf("anahtar bicimi: %s %s", key, secret)
	}
	st := rg.fs.sites[id]
	if st.SecretHash == secret || st.SecretHash != captcha.HashSecret(secret) {
		t.Error("DB'de ozet saklanmali, secret degil")
	}
	if len(st.Domains) != 1 || st.Domains[0] != "ornek.com" || st.Difficulty != 18 {
		t.Errorf("normallestirme: %+v", st)
	}
	rec := rg.tenantReq("GET", "/api/v1/captcha/sites", "member", "")
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), st.SecretHash) || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("liste secret/ozet sizdirmamali: %s", rec.Body)
	}
	// yenile: yeni secret bir kez; eskisi gecersiz
	rec = rg.tenantReq("POST", "/api/v1/captcha/sites/"+id+"/rotate-secret", "owner", "")
	var o captchaSiteOut
	decodeBody(t, rec, &o)
	if rec.Code != 200 || o.Secret == "" || o.Secret == secret {
		t.Fatalf("rotate: %d %+v", rec.Code, o)
	}
	rg.srv.Captcha.Invalidate(key)
	if rg.siteverify(t, secret, "x").ErrorCodes[0] != captcha.CodeInvalidSecret {
		t.Error("eski secret gecersiz olmali")
	}
	if rg.siteverify(t, o.Secret, "abc").ErrorCodes[0] != captcha.CodeInvalidResponse {
		t.Error("yeni secret taninmali")
	}
	// sil
	if rec := rg.tenantReq("DELETE", "/api/v1/captcha/sites/"+id, "admin", ""); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := rg.tenantReq("DELETE", "/api/v1/captcha/sites/"+id, "admin", ""); rec.Code != 404 {
		t.Errorf("ikinci delete: %d", rec.Code)
	}
}

func TestCaptchaSiteValidation(t *testing.T) {
	rg := newCaptchaRig(t)
	for name, body := range map[string]string{
		"ad yok":          `{"name":""}`,
		"zorluk dusuk":    `{"name":"a","difficulty":10}`,
		"zorluk yuksek":   `{"name":"a","difficulty":30}`,
		"alan gecersiz":   `{"name":"a","domains":["a b"]}`,
		"pass ttl dusuk":  `{"name":"a","pass_cookie":{"enabled":true,"ttl_minutes":2}}`,
		"pass ttl yuksek": `{"name":"a","pass_cookie":{"enabled":true,"ttl_minutes":50000}}`,
	} {
		if rec := rg.tenantReq("POST", "/api/v1/captcha/sites", "admin", body); rec.Code != 422 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// varsayilanlar
	id, _, _ := rg.createSite(t, `{"name":"a"}`)
	s := rg.fs.sites[id]
	if s.Difficulty != 17 || s.PassEnabled || s.PassTTLMinutes != 1440 {
		t.Errorf("varsayilanlar: %+v", s)
	}
	// guncelleme
	rec := rg.tenantReq("PATCH", "/api/v1/captcha/sites/"+id, "admin", `{"pass_cookie":{"enabled":true,"ttl_minutes":60},"difficulty":20,"domains":[]}`)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	s = rg.fs.sites[id]
	if !s.PassEnabled || s.PassTTLMinutes != 60 || s.Difficulty != 20 {
		t.Errorf("patch sonucu: %+v", s)
	}
	if rec := rg.tenantReq("PATCH", "/api/v1/captcha/sites/"+id, "admin", `{"pass_cookie":{"ttl_minutes":1}}`); rec.Code != 422 {
		t.Errorf("gecersiz ttl patch: %d", rec.Code)
	}
}

func (rg *captchaRig) siteverify(t *testing.T, secret, resp string) captcha.VerifyResult {
	t.Helper()
	form := url.Values{"secret": {secret}, "response": {resp}}
	rec := rg.public("POST", "/api/v1/captcha/siteverify", "", "application/x-www-form-urlencoded", form.Encode())
	var v captcha.VerifyResult
	decodeBody(t, rec, &v)
	if rec.Code != 200 {
		t.Fatalf("siteverify HTTP %d", rec.Code)
	}
	return v
}

// widgetSolve, tam widget akisini HTTP uzerinden yurutur.
func (rg *captchaRig) widgetSolve(t *testing.T, key, origin string) (resp captcha.SolveResult, code int) {
	t.Helper()
	rec := rg.public("POST", "/api/v1/captcha/challenge", origin, "text/plain;charset=UTF-8",
		`{"sitekey":"`+key+`","signals":{"mv":3,"fc":1}}`)
	if rec.Code != 200 {
		return captcha.SolveResult{}, rec.Code
	}
	var ch captcha.ChallengeResult
	decodeBody(t, rec, &ch)
	time.Sleep(40 * time.Millisecond)
	body, _ := json.Marshal(map[string]any{"token": ch.Token, "counter": botguard.SolvePoW(ch.Nonce, ch.Difficulty),
		"signals": map[string]any{"mv": 3, "fc": 1, "t": 40}})
	rec = rg.public("POST", "/api/v1/captcha/solve", origin, "text/plain;charset=UTF-8", string(body))
	if rec.Code != 200 {
		return captcha.SolveResult{}, rec.Code
	}
	decodeBody(t, rec, &resp)
	return resp, 200
}

func TestCaptchaPublicFlowCORSAndOrigins(t *testing.T) {
	rg := newCaptchaRig(t)
	_, key, secret := rg.createSite(t, `{"name":"Blog","domains":["ornek.com"],"difficulty":14}`)

	// preflight
	rec := rg.public("OPTIONS", "/api/v1/captcha/challenge", "https://www.ornek.com", "", "")
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://www.ornek.com" ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight: %d %v", rec.Code, rec.Header())
	}

	// izinli origin: CORS basligi + tam akis + siteverify
	rec = rg.public("POST", "/api/v1/captcha/challenge", "https://www.ornek.com", "text/plain", `{"sitekey":"`+key+`"}`)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://www.ornek.com" {
		t.Fatalf("challenge: %d %v", rec.Code, rec.Header())
	}
	res, code := rg.widgetSolve(t, key, "https://www.ornek.com")
	if code != 200 || res.Pass != "" {
		t.Fatalf("solve: %d %+v", code, res)
	}
	v := rg.siteverify(t, secret, res.Response)
	if !v.Success || v.Hostname != "www.ornek.com" {
		t.Fatalf("siteverify: %+v", v)
	}
	if v2 := rg.siteverify(t, secret, res.Response); v2.Success || v2.ErrorCodes[0] != captcha.CodeTimeoutDup {
		t.Errorf("tekrar: %+v", v2)
	}

	// JSON siteverify
	res2, _ := rg.widgetSolve(t, key, "https://ornek.com")
	jb, _ := json.Marshal(map[string]string{"secret": secret, "response": res2.Response})
	rec = rg.public("POST", "/api/v1/captcha/siteverify", "", "application/json", string(jb))
	var jv captcha.VerifyResult
	decodeBody(t, rec, &jv)
	if !jv.Success {
		t.Errorf("JSON siteverify: %+v", jv)
	}

	// izinsiz origin / Origin yok -> 403
	if _, code := rg.widgetSolve(t, key, "https://evil.test"); code != 403 {
		t.Errorf("izinsiz origin: %d", code)
	}
	if _, code := rg.widgetSolve(t, key, ""); code != 403 {
		t.Errorf("Origin'siz istek (alan listesi dolu): %d", code)
	}
	// bilinmeyen site, eksik alan
	if rec := rg.public("POST", "/api/v1/captcha/challenge", "https://ornek.com", "text/plain", `{"sitekey":"zcs_yok"}`); rec.Code != 404 {
		t.Errorf("bilinmeyen site: %d", rec.Code)
	}
	if rec := rg.public("POST", "/api/v1/captcha/challenge", "https://ornek.com", "text/plain", `{}`); rec.Code != 400 {
		t.Errorf("eksik sitekey: %d", rec.Code)
	}
	// solve: baska origin'den sorgu kullanimi
	rec = rg.public("POST", "/api/v1/captcha/challenge", "https://ornek.com", "text/plain", `{"sitekey":"`+key+`"}`)
	var ch captcha.ChallengeResult
	decodeBody(t, rec, &ch)
	body, _ := json.Marshal(map[string]any{"token": ch.Token, "counter": botguard.SolvePoW(ch.Nonce, ch.Difficulty), "signals": map[string]any{"mv": 1}})
	if rec := rg.public("POST", "/api/v1/captcha/solve", "https://sub.ornek.com", "text/plain", string(body)); rec.Code != 403 {
		t.Errorf("origin uyusmazligi: %d", rec.Code)
	}
}

func TestCaptchaRenewEndpoint(t *testing.T) {
	rg := newCaptchaRig(t)
	_, key, secret := rg.createSite(t, `{"name":"Blog","difficulty":14,"pass_cookie":{"enabled":true,"ttl_minutes":30}}`)
	res, code := rg.widgetSolve(t, key, "https://ornek.com")
	if code != 200 || res.Pass == "" || res.PassTTLSeconds != 1800 {
		t.Fatalf("pass beklenir: %d %+v", code, res)
	}
	rb, _ := json.Marshal(map[string]string{"sitekey": key, "pass": res.Pass})
	rec := rg.public("POST", "/api/v1/captcha/renew", "https://ornek.com", "text/plain", string(rb))
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://ornek.com" {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body)
	}
	var rn captcha.SolveResult
	decodeBody(t, rec, &rn)
	if !rg.siteverify(t, secret, rn.Response).Success {
		t.Error("renew yaniti siteverify'dan gecmeli")
	}
	bad, _ := json.Marshal(map[string]string{"sitekey": key, "pass": res.Pass + "x"})
	if rec := rg.public("POST", "/api/v1/captcha/renew", "https://ornek.com", "text/plain", string(bad)); rec.Code != 400 {
		t.Errorf("gecersiz pass: %d", rec.Code)
	}
	if rec := rg.public("POST", "/api/v1/captcha/renew", "https://ornek.com", "text/plain", `{"sitekey":"`+key+`"}`); rec.Code != 400 {
		t.Errorf("pass eksik: %d", rec.Code)
	}
	// sayaclar listede (bekleyen artislar dahil)
	rec = rg.tenantReq("GET", "/api/v1/captcha/sites", "member", "")
	var out struct {
		Sites []captchaSiteOut `json:"sites"`
	}
	decodeBody(t, rec, &out)
	st := out.Sites[0].Stats
	if st == nil || st.Today.Renewals != 1 || st.Today.UniquePasses != 1 || st.Today.Challenge != 1 || st.Last7.VerifyOK != 1 {
		t.Errorf("istatistik: %+v", st)
	}
	if !out.Sites[0].PassCookie.Enabled || out.Sites[0].PassCookie.TTLMinutes != 30 {
		t.Errorf("pass_cookie ciktisi: %+v", out.Sites[0].PassCookie)
	}
}

func TestCaptchaPlatformConfigAndUnsupported(t *testing.T) {
	rg := newCaptchaRig(t)
	rec := rg.public("GET", "/api/v1/captcha/platform-config", "", "", "")
	var pc struct {
		Enabled bool   `json:"enabled"`
		Sitekey string `json:"sitekey"`
	}
	decodeBody(t, rec, &pc)
	if !pc.Enabled || pc.Sitekey != "zcs_platformtest" {
		t.Errorf("platform-config: %+v", pc)
	}
	// platform sitesi env ile calisir; pass_cookie kapali
	res, code := rg.widgetSolve(t, "zcs_platformtest", "https://zorven.app")
	if code != 200 || res.Pass != "" {
		t.Fatalf("platform solve: %d %+v", code, res)
	}
	if !rg.siteverify(t, "zcsec_platformtest", res.Response).Success {
		t.Error("platform siteverify")
	}
	// servis yok -> devre disi + 501
	s2 := &Server{Store: newCaptchaFake()}
	mux := s2.Routes()
	req := httptest.NewRequest("GET", "/api/v1/captcha/platform-config", nil)
	r1 := httptest.NewRecorder()
	mux.ServeHTTP(r1, req)
	decodeBody(t, r1, &pc)
	if pc.Enabled {
		t.Error("servis yokken enabled=false")
	}
	req = httptest.NewRequest("POST", "/api/v1/captcha/challenge", strings.NewReader(`{"sitekey":"x"}`))
	r2 := httptest.NewRecorder()
	mux.ServeHTTP(r2, req)
	if r2.Code != 501 {
		t.Errorf("501 beklenir: %d", r2.Code)
	}
}

func TestIsCaptchaPublicPath(t *testing.T) {
	for p, want := range map[string][2]bool{
		"/captcha/v1/api.js":                    {true, false},
		"/api/v1/captcha/challenge":             {true, false},
		"/api/v1/captcha/renew":                 {true, false},
		"/api/v1/captcha/siteverify":            {true, true},
		"/api/v1/captcha/sites":                 {false, false},
		"/api/v1/captcha/sites/x/rotate-secret": {false, false},
	} {
		pub, s2s := IsCaptchaPublicPath(p)
		if pub != want[0] || s2s != want[1] {
			t.Errorf("%s: %v %v", p, pub, s2s)
		}
	}
}
