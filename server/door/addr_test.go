package door

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// addrFake, fakeStore'a AddrStore yuzeyini ekler.
type addrFake struct {
	*fakeStore
	addErr error
}

func (a *addrFake) AddDoorGrantAddr(_ context.Context, grantID, ip string) (store.DoorGrant, bool, error) {
	if a.addErr != nil {
		return store.DoorGrant{}, false, a.addErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.grants {
		g := &a.grants[i]
		if g.ID != grantID || g.RevokedAt != nil || !g.ExpiresAt.After(time.Now()) {
			continue
		}
		if ipFamily(g.IP) == ipFamily(ip) {
			return *g, false, nil
		}
		g.ExtraIPs = []string{ip}
		return *g, true, nil
	}
	return store.DoorGrant{}, false, store.ErrNotFound
}

func (a *addrFake) DoorGrantAddrsByIP(_ context.Context, _, tid, ip string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, g := range a.grants {
		if g.TunnelID != tid || g.RevokedAt != nil {
			continue
		}
		all := allIPs(g)
		for _, x := range all {
			if x == ip {
				out = append(out, all...)
				break
			}
		}
	}
	return out, nil
}

// RevokeDoorGrantsByIP ek adresleri de eslestirir (gercek depo gibi).
func (a *addrFake) RevokeDoorGrantsByIP(_ context.Context, _, tid, ip string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for i := range a.grants {
		g := &a.grants[i]
		if g.TunnelID != tid || g.RevokedAt != nil {
			continue
		}
		for _, x := range allIPs(*g) {
			if x == ip {
				now := time.Now()
				g.RevokedAt = &now
				n++
				break
			}
		}
	}
	return n, nil
}

func newAddrSvc(t *testing.T) (*Service, *addrFake) {
	t.Helper()
	st := &addrFake{fakeStore: &fakeStore{}}
	s := New(st, nil, nil)
	s.TokenKey = []byte("0123456789abcdef0123456789abcdef")
	s.V4Host, s.V6Host = "door4.example.test", "door6.example.test"
	return s, st
}

var addrTarget = Target{TenantID: "ten", TunnelID: "tun", Host: "door--a--b.example.test", DurationSec: 3600}

func TestAddrTokenSignVerifyExpiryTamper(t *testing.T) {
	s, _ := newAddrSvc(t)
	clock := time.Now()
	s.Now = func() time.Time { return clock }
	g := store.DoorGrant{ID: "dg_1", TunnelID: "tun"}
	tok := s.IssueAddrToken(g, "Door--A--B.example.test")
	c, err := s.ParseAddrToken(tok)
	if err != nil || c.GrantID != "dg_1" || c.TunnelID != "tun" || c.Host != "door--a--b.example.test" {
		t.Fatalf("beklenmeyen: %+v %v", c, err)
	}
	// imza bozuldu
	if _, err := s.ParseAddrToken(tok[:len(tok)-2] + "AA"); !errors.Is(err, ErrAddrToken) {
		t.Fatalf("bozuk imza kabul edildi: %v", err)
	}
	// baska anahtar
	s2, _ := newAddrSvc(t)
	s2.TokenKey = []byte("ffffffffffffffffffffffffffffffff")
	if _, err := s2.ParseAddrToken(tok); !errors.Is(err, ErrAddrToken) {
		t.Fatalf("baska anahtarla kabul edildi: %v", err)
	}
	// sure dolumu
	clock = clock.Add(AddrTokenTTL + time.Second)
	if _, err := s.ParseAddrToken(tok); !errors.Is(err, ErrAddrToken) {
		t.Fatalf("suresi dolan token kabul edildi: %v", err)
	}
	// anahtarsiz yan kanal kapali
	s.TokenKey = nil
	if s.IssueAddrToken(g, "x") != "" || s.AddrEnabled() {
		t.Fatal("anahtar yokken yan kanal kapali olmali")
	}
}

func TestAddrHostPicksOtherFamily(t *testing.T) {
	s, _ := newAddrSvc(t)
	if s.AddrHost("203.0.113.7") != s.V6Host || s.AddrHost("2001:db8::1") != s.V4Host {
		t.Fatal("diger aile hosti secilmedi")
	}
	s.V6Host = ""
	if s.AddrHost("203.0.113.7") != "" {
		t.Fatal("host tanimsizken bos beklenirdi")
	}
}

func TestLearnAddrEnforcementReplayFamily(t *testing.T) {
	s, _ := newAddrSvc(t)
	ctx := context.Background()
	g, _, err := s.Open(ctx, addrTarget, "203.0.113.7", "alice", "basic", "ua")
	if err != nil {
		t.Fatal(err)
	}
	tok := s.IssueAddrToken(g, addrTarget.Host)
	v6 := "2001:db8:1:2::77"
	if _, ok := s.HasGrant("tun", v6); ok {
		t.Fatal("ogrenmeden once v6 izinli olmamali")
	}

	// Aile uyumsuz: v6 uc, v4 kaynakla cagrildi -> reddedilir, token YANMAZ.
	if _, _, err := s.LearnAddr(ctx, tok, 6, "198.51.100.9"); !errors.Is(err, ErrAddrFamily) {
		t.Fatalf("aile uyumsuzlugu: %v", err)
	}
	if _, ok := s.HasGrant("tun", "198.51.100.9"); ok {
		t.Fatal("uyumsuz adres izin almamali")
	}
	// Dogru aile.
	ip, _, err := s.LearnAddr(ctx, tok, 6, "["+v6+"]:5555")
	if err != nil || ip != v6 {
		t.Fatalf("ogrenme: %q %v", ip, err)
	}
	if _, ok := s.HasGrant("tun", v6); !ok {
		t.Fatal("ek v6 adres izinli olmali")
	}
	// ayni /64 icindeki baska adres (gizlilik uzantisi)
	if _, ok := s.HasGrant("tun", "2001:db8:1:2::dead"); !ok {
		t.Fatal("ayni /64 izinli olmali")
	}
	if _, ok := s.HasGrant("tun", "203.0.113.7"); !ok {
		t.Fatal("ana adres izinli kalmali")
	}
	// Tekrar (replay): ayni token ayni aile.
	if _, _, err := s.LearnAddr(ctx, tok, 6, v6); !errors.Is(err, ErrAddrReplay) {
		t.Fatalf("replay engellenmedi: %v", err)
	}
	// Ana adresle ayni aile: etkisiz.
	if _, _, err := s.LearnAddr(ctx, tok, 4, "203.0.113.7"); !errors.Is(err, ErrAddrSame) {
		t.Fatalf("ayni aile: %v", err)
	}
}

func TestRevokeRemovesBothAddrs(t *testing.T) {
	s, _ := newAddrSvc(t)
	ctx := context.Background()
	var cut []string
	s.OnRevoke = func(tid, ip string) { cut = append(cut, ip) }
	g, _, _ := s.Open(ctx, addrTarget, "203.0.113.7", "alice", "basic", "ua")
	tok := s.IssueAddrToken(g, addrTarget.Host)
	if _, _, err := s.LearnAddr(ctx, tok, 6, "2001:db8::5"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, "ten", "tun", g.ID); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"203.0.113.7", "2001:db8::5"} {
		if _, ok := s.HasGrant("tun", ip); ok {
			t.Fatalf("iptal sonrasi %s hala izinli", ip)
		}
	}
	joined := strings.Join(cut, ",")
	if !strings.Contains(joined, "203.0.113.7") || !strings.Contains(joined, "2001:db8::5") {
		t.Fatalf("her iki adresin baglantilari kesilmeli: %v", cut)
	}
}

func TestCloseFromExtraAddrClosesBoth(t *testing.T) {
	s, _ := newAddrSvc(t)
	ctx := context.Background()
	var cut []string
	s.OnRevoke = func(tid, ip string) { cut = append(cut, ip) }
	g, _, _ := s.Open(ctx, addrTarget, "203.0.113.7", "alice", "basic", "ua")
	tok := s.IssueAddrToken(g, addrTarget.Host)
	if _, _, err := s.LearnAddr(ctx, tok, 6, "2001:db8::5"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Close(ctx, addrTarget, "2001:db8::5", "alice", "basic", "ua")
	if err != nil || n != 1 {
		t.Fatalf("Close: %d %v", n, err)
	}
	if _, ok := s.HasGrant("tun", "203.0.113.7"); ok {
		t.Fatal("ana adres de kapanmali")
	}
	if len(cut) != 2 {
		t.Fatalf("iki adres kesilmeli: %v", cut)
	}
}

func TestSideChannelFailureDoesNotAffectMainGrant(t *testing.T) {
	s, st := newAddrSvc(t)
	ctx := context.Background()
	g, _, _ := s.Open(ctx, addrTarget, "203.0.113.7", "alice", "basic", "ua")
	tok := s.IssueAddrToken(g, addrTarget.Host)
	st.addErr = errors.New("db down")
	if _, _, err := s.LearnAddr(ctx, tok, 6, "2001:db8::5"); err == nil {
		t.Fatal("hata beklenirdi")
	}
	if _, ok := s.HasGrant("tun", "203.0.113.7"); !ok {
		t.Fatal("yan kanal hatasi ana grant'i etkiledi")
	}
	if _, ok := s.HasGrant("tun", "2001:db8::5"); ok {
		t.Fatal("basarisiz ogrenme adres izinlemis")
	}
}

func TestAddrHandler(t *testing.T) {
	s, _ := newAddrSvc(t)
	ctx := context.Background()
	g, _, _ := s.Open(ctx, addrTarget, "203.0.113.7", "alice", "basic", "ua")
	tok := s.IssueAddrToken(g, addrTarget.Host)
	origin := "https://" + addrTarget.Host
	remote := "2001:db8::9"
	h := s.AddrHandler(func(*http.Request) string { return remote })

	do := func(host, path, token, org string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://"+host+path+"?t="+token, nil)
		if org != "" {
			r.Header.Set("Origin", org)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Bilinmeyen host / yol -> 404
	if w := do("evil.example.test", AddrPath, tok, origin); w.Code != 404 {
		t.Fatalf("bilinmeyen host: %d", w.Code)
	}
	if w := do(s.V6Host, "/x", tok, origin); w.Code != 404 {
		t.Fatalf("yanlis yol: %d", w.Code)
	}
	// Baska origin -> 403 ve ACAO yok.
	w := do(s.V6Host, AddrPath, tok, "https://attacker.example")
	if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("yabanci origin: %d %v", w.Code, w.Header())
	}
	// v6 uc, v4 kaynakla -> 409 (aile), ek adres yok.
	remote = "198.51.100.9"
	if w := do(s.V6Host, AddrPath, tok, origin); w.Code != 409 {
		t.Fatalf("aile uyumsuz: %d", w.Code)
	}
	if _, ok := s.HasGrant("tun", "198.51.100.9"); ok {
		t.Fatal("uyumsuz kaynak izinlendi")
	}
	// Dogru: v6 kaynak, v6 host.
	remote = "2001:db8::9"
	w = do(s.V6Host, AddrPath, tok, origin)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != origin || !strings.Contains(w.Body.String(), "2001:db8::9") {
		t.Fatalf("basari: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if _, ok := s.HasGrant("tun", "2001:db8::9"); !ok {
		t.Fatal("ek adres izinli olmali")
	}
	// Replay -> 409
	if w := do(s.V6Host, AddrPath, tok, origin); w.Code != 409 {
		t.Fatalf("replay: %d", w.Code)
	}
	// Bozuk token -> 400
	if w := do(s.V6Host, AddrPath, "x.y", ""); w.Code != 400 {
		t.Fatalf("bozuk token: %d", w.Code)
	}
	// Yalniz GET
	r := httptest.NewRequest(http.MethodPost, "https://"+s.V6Host+AddrPath+"?t="+tok, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
}
