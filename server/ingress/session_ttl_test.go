package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
)

// K4: ziyaretci oturumu TTL / kayan yenileme / mutlak tavan / iptal testleri.

var (
	ttlHashOnce sync.Once
	ttlHash     string
)

func ttlBasicRoute(t *testing.T, extra string) store.HostRoute {
	t.Helper()
	ttlHashOnce.Do(func() { ttlHash, _ = auth.HashSecret("pw") })
	hash := ttlHash
	cfg := `{"username":"alice","password_hash":"` + hash + `"` + extra + `}`
	return routeFor(t, "basic", cfg)
}

func ttlHandler(t *testing.T, tun store.HostRoute, vis *visitorauth.Manager) *Handler {
	t.Helper()
	h := newAccessHandler(t, tun, vis)
	h.Door = door.New(&memDoorStore{}, nil, nil)
	h.BasicLimiter = ratelimit.New(10.0/600, 10)
	return h
}

func basicCookieAt(h *Handler, tun store.HostRoute, iat, orig time.Time, life time.Duration) *http.Cookie {
	cfg := parseBasicConfig(tun.AccessConfig)
	return &http.Cookie{Name: basicCookie, Value: h.signBasic(basicClaims{
		Tunnel: tun.TunnelID, Host: testHost, FP: passFingerprint(cfg),
		Exp: iat.Add(life).Unix(), IatMs: iat.UnixMilli(), OrigMs: orig.UnixMilli(),
	})}
}

func enforce(h *Handler, tun store.HostRoute, c *http.Cookie) (bool, *httptest.ResponseRecorder) {
	r := browserReq("GET", "/")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	ok := h.enforceAccess(w, r, tun)
	return ok, w
}

func TestSessionTTL_LoginCookieUsesTunnelTTL(t *testing.T) {
	for _, tc := range []struct {
		extra string
		want  time.Duration
	}{
		{``, 12 * time.Hour},
		{`,"session_ttl_minutes":15`, 15 * time.Minute},
		{`,"session_ttl_minutes":43200`, 30 * 24 * time.Hour},
		{`,"session_ttl_minutes":5`, 12 * time.Hour}, // aralik disi -> varsayilan (fail-safe)
	} {
		tun := ttlBasicRoute(t, tc.extra)
		h := ttlHandler(t, tun, nil)
		w := postLogin(h, "alice", "pw", "/")
		c := cookieNamed(w, basicCookie)
		if c == nil {
			t.Fatalf("%q: cerez yok (%d)", tc.extra, w.Code)
		}
		if d := time.Until(c.Expires); d < tc.want-time.Minute || d > tc.want+time.Minute {
			t.Errorf("%q: omur %v, istenen %v", tc.extra, d, tc.want)
		}
	}
}

func TestSessionTTL_ShortenedAppliesToExistingCookie(t *testing.T) {
	// 12 saatlik pencereyle verilmis, 1 saat once giris yapilmis cerez.
	tun12 := ttlBasicRoute(t, ``)
	h := ttlHandler(t, tun12, nil)
	c := basicCookieAt(h, tun12, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour), 12*time.Hour)
	if ok, _ := enforce(h, tun12, c); !ok {
		t.Fatal("12 saatlik ayarda gecerli olmali")
	}
	// Tunel TTL'i 30 dk'ya indirilince ayni cerez gecersiz.
	tun30 := ttlBasicRoute(t, `,"session_ttl_minutes":30`)
	if ok, _ := enforce(h, tun30, c); ok {
		t.Fatal("TTL kisaltilinca eski uzun omurlu cerez reddedilmeli")
	}
}

func TestSessionTTL_LegacyCookieWithoutIat(t *testing.T) {
	tun := ttlBasicRoute(t, `,"session_ttl_minutes":43200`) // 30 gun
	h := ttlHandler(t, tun, nil)
	cfg := parseBasicConfig(tun.AccessConfig)
	legacy := func(exp time.Time) *http.Cookie {
		return &http.Cookie{Name: basicCookie, Value: h.signBasic(basicClaims{
			Tunnel: "tun_1", Host: testHost, FP: passFingerprint(cfg), Exp: exp.Unix()})}
	}
	// Exp gelecekte: eski cerez, TTL 30 gun olsa da en fazla 12 saat (Exp) gecerli.
	if ok, _ := enforce(h, tun, legacy(time.Now().Add(time.Hour))); !ok {
		t.Fatal("suresi dolmamis eski bicim cerez gecmeli")
	}
	if ok, _ := enforce(h, tun, legacy(time.Now().Add(-time.Minute))); ok {
		t.Fatal("suresi dolmus eski bicim cerez reddedilmeli")
	}
	// Eski cerezin verilisi exp-12s: tunel TTL'i 15 dk ise (verilis 11 saat once) gecersiz.
	tun15 := ttlBasicRoute(t, `,"session_ttl_minutes":15`)
	if ok, _ := enforce(h, tun15, legacy(time.Now().Add(time.Hour))); ok {
		t.Fatal("kisa TTL eski cerezi de kisaltmali")
	}
}

func TestSessionTTL_SlidingRenewalBasic(t *testing.T) {
	tun := ttlBasicRoute(t, `,"session_ttl_minutes":60,"session_sliding":true`)
	h := ttlHandler(t, tun, nil)

	// Kalan 50 dk (> TTL/2): yenileme yok.
	fresh := basicCookieAt(h, tun, time.Now().Add(-10*time.Minute), time.Now().Add(-10*time.Minute), time.Hour)
	ok, w := enforce(h, tun, fresh)
	if !ok || cookieNamed(w, basicCookie) != nil {
		t.Fatalf("taze cerez yenilenmemeli (ok=%v)", ok)
	}
	// Kalan 20 dk (< TTL/2): yeniden imzalanir, Orig korunur, omur ~60 dk.
	orig := time.Now().Add(-40 * time.Minute)
	old := basicCookieAt(h, tun, orig, orig, time.Hour)
	ok, w = enforce(h, tun, old)
	nc := cookieNamed(w, basicCookie)
	if !ok || nc == nil {
		t.Fatalf("yarisi gecen cerez yenilenmeli (ok=%v)", ok)
	}
	if d := time.Until(nc.Expires); d < 59*time.Minute {
		t.Errorf("yenilenen omur %v", d)
	}
	claims, vok := h.verifyBasic(nc.Value)
	if !vok || claims.OrigMs != orig.UnixMilli() || claims.IatMs <= orig.UnixMilli() {
		t.Errorf("Orig korunmali, Iat ilerlemeli: %+v", claims)
	}
	// Yenilenen cerez gecerli.
	if ok, _ := enforce(h, tun, nc); !ok {
		t.Fatal("yenilenen cerez gecmeli")
	}
}

func TestSessionTTL_SlidingOffNoRenewal(t *testing.T) {
	tun := ttlBasicRoute(t, `,"session_ttl_minutes":60`)
	h := ttlHandler(t, tun, nil)
	orig := time.Now().Add(-50 * time.Minute)
	ok, w := enforce(h, tun, basicCookieAt(h, tun, orig, orig, time.Hour))
	if !ok || cookieNamed(w, basicCookie) != nil {
		t.Fatal("kayan yenileme kapaliyken Set-Cookie olmamali")
	}
}

func TestSessionTTL_AbsoluteCap(t *testing.T) {
	tun := ttlBasicRoute(t, `,"session_ttl_minutes":60,"session_sliding":true`)
	h := ttlHandler(t, tun, nil)
	// Ilk giris 7 gunu 10 dk asmis: cerez kendi Exp'i icinde olsa da reddedilir.
	orig := time.Now().Add(-7*24*time.Hour - 10*time.Minute)
	iat := time.Now().Add(-5 * time.Minute)
	if ok, _ := enforce(h, tun, basicCookieAt(h, tun, iat, orig, time.Hour)); ok {
		t.Fatal("mutlak tavan asilinca oturum reddedilmeli")
	}
	// Tavana 20 dk kala yenileme: yeni bitis tavani asamaz.
	orig = time.Now().Add(-7*24*time.Hour + 20*time.Minute)
	iat = time.Now().Add(-45 * time.Minute)
	ok, w := enforce(h, tun, basicCookieAt(h, tun, iat, orig, time.Hour))
	if !ok {
		t.Fatal("tavana yakin ama gecerli cerez gecmeli")
	}
	// Mevcut Exp = iat+60dk = 15 dk sonra; tavan = 20 dk sonra: yenileme tavanla sinirli.
	nc := cookieNamed(w, basicCookie)
	if nc == nil {
		t.Fatal("yenileme bekleniyordu")
	}
	if cap := orig.Add(7 * 24 * time.Hour); nc.Expires.After(cap.Add(time.Second)) {
		t.Errorf("yenileme tavani asti: %v > %v", nc.Expires, cap)
	}
}

func TestSessionTTL_RevokeAllAndIdentityBasic(t *testing.T) {
	tun := ttlBasicRoute(t, ``)
	h := ttlHandler(t, tun, nil)
	c := basicCookieAt(h, tun, time.Now().Add(-time.Minute), time.Now().Add(-time.Minute), time.Hour)
	if ok, _ := enforce(h, tun, c); !ok {
		t.Fatal("iptalden once gecmeli")
	}
	// Baska kimligin iptali etkilemez.
	_ = h.Door.RecordRevocation(context.Background(), "tun_1", "bob")
	if ok, _ := enforce(h, tun, c); !ok {
		t.Fatal("baska kimligin iptali alice'i etkilememeli")
	}
	// Kimlik bazli iptal.
	_ = h.Door.RecordRevocation(context.Background(), "tun_1", "Alice")
	if ok, _ := enforce(h, tun, c); ok {
		t.Fatal("kimlik iptalinden sonra cerez reddedilmeli")
	}
	time.Sleep(5 * time.Millisecond)
	w := postLogin(h, "alice", "pw", "/")
	nc := cookieNamed(w, basicCookie)
	if nc == nil {
		t.Fatal("yeniden giris cerez vermeli")
	}
	if ok, _ := enforce(h, tun, nc); !ok {
		t.Fatal("iptalden sonraki taze giris gecmeli")
	}
	// Tum oturumlar.
	time.Sleep(5 * time.Millisecond)
	_ = h.Door.RecordRevocation(context.Background(), "tun_1", door.AllIdentities)
	if ok, _ := enforce(h, tun, nc); ok {
		t.Fatal("'tum oturumlar' iptalinden sonra cerez reddedilmeli")
	}
	// Dogrudan Authorization: Basic (taze kimlik bilgisi) etkilenmez.
	r := browserReq("GET", "/")
	r.SetBasicAuth("alice", "pw")
	if !h.enforceAccess(httptest.NewRecorder(), r, tun) {
		t.Fatal("Authorization basligi iptalden etkilenmemeli")
	}
}

func TestSessionTTL_PasswordChangeInvalidates(t *testing.T) {
	tun := ttlBasicRoute(t, `,"session_ttl_minutes":1440`)
	h := ttlHandler(t, tun, nil)
	c := basicCookieAt(h, tun, time.Now(), time.Now(), time.Hour)
	if ok, _ := enforce(h, tun, c); !ok {
		t.Fatal("gecmeli")
	}
	hash2, _ := auth.HashSecret("other")
	tun2 := routeFor(t, "basic", `{"username":"alice","password_hash":"`+hash2+`","session_ttl_minutes":1440}`)
	if ok, _ := enforce(h, tun2, c); ok {
		t.Fatal("parola degisince oturum gecersiz olmali")
	}
}

func TestSessionTTL_OAuth(t *testing.T) {
	tun := routeFor(t, "oauth", `{"providers":["google"],"allowed_emails":[],"session_ttl_minutes":60,"session_sliding":true}`)
	vis := newVisitor("google")
	h := ttlHandler(t, tun, vis)
	pol := visitorauth.SessionPolicy{TTLMinutes: 60, Sliding: true}
	mk := func(iat, orig time.Time, p visitorauth.SessionPolicy) *http.Cookie {
		return &http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValueWith(testHost, "bob@example.com", iat, orig, p)}
	}
	// Taze: yenileme yok.
	now := time.Now()
	if ok, w := enforce(h, tun, mk(now.Add(-5*time.Minute), now.Add(-5*time.Minute), pol)); !ok || cookieNamed(w, "_zva_session") != nil {
		t.Fatal("taze oauth oturumu yenilenmemeli")
	}
	// Yarisi gecmis: yenilenir.
	orig := now.Add(-45 * time.Minute)
	ok, w := enforce(h, tun, mk(orig, orig, pol))
	nc := cookieNamed(w, "_zva_session")
	if !ok || nc == nil || time.Until(nc.Expires) < 59*time.Minute {
		t.Fatalf("oauth kayan yenileme beklenirdi ok=%v c=%v", ok, nc)
	}
	// Mutlak tavan.
	old := now.Add(-7*24*time.Hour - time.Minute)
	if ok, _ := enforce(h, tun, mk(now.Add(-time.Minute), old, pol)); ok {
		t.Fatal("oauth mutlak tavan asilmamali")
	}
	// Iptal ("tum oturumlar").
	c := mk(now.Add(-time.Minute), now.Add(-time.Minute), pol)
	if ok, _ := enforce(h, tun, c); !ok {
		t.Fatal("iptalden once gecmeli")
	}
	_ = h.Door.RecordRevocation(context.Background(), "tun_1", door.AllIdentities)
	if ok, _ := enforce(h, tun, c); ok {
		t.Fatal("iptalden sonra oauth cerezi reddedilmeli")
	}
	// Eski bicim (iat'siz) cerez iptalden sonra da gecersiz kalir (issued = exp-12s <= iptal).
	leg := &http.Cookie{Name: "_zva_session", Value: vis.IssueSessionValueLegacy(testHost, "bob@example.com", now.Add(time.Hour))}
	if ok, _ := enforce(h, tun, leg); ok {
		t.Fatal("iptalden sonra eski bicim cerez reddedilmeli")
	}
}
