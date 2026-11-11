package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// guardStore, token kaydini ve depo hatasini testin ortasinda degistirebilen sahte depo.
type guardStore struct {
	mockTokenStore
	mu      sync.Mutex
	err     error // nil degilse tum token sorgulari bu hatayi doner
	revoked bool
	client  store.Client
	lookups atomic.Int64
}

func (g *guardStore) GetAPITokenByTokenID(ctx context.Context, id string) (store.APIToken, error) {
	g.lookups.Add(1)
	g.mu.Lock()
	err, revoked := g.err, g.revoked
	g.mu.Unlock()
	if err != nil {
		return store.APIToken{}, err
	}
	if revoked {
		// pgstore: revoked_at IS NULL filtresi -> iptal edilen token bulunamaz.
		return store.APIToken{}, store.ErrNotFound
	}
	return g.mockTokenStore.GetAPITokenByTokenID(ctx, id)
}

func (g *guardStore) GetClientByTokenID(_ context.Context, id string) (store.Client, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return store.Client{}, g.err
	}
	if id == g.client.TokenID && g.client.TokenID != "" {
		return g.client, nil
	}
	return store.Client{}, store.ErrNotFound
}

func (g *guardStore) set(err error, revoked bool) {
	g.mu.Lock()
	g.err, g.revoked = err, revoked
	g.mu.Unlock()
}

type errEntitlements struct {
	entitlements.EntitlementService
	err error
}

func (e errEntitlements) CheckFeature(context.Context, string, entitlements.Feature) error {
	return e.err
}

func newGuardMW(t *testing.T) (*Middleware, *guardStore, string) {
	t.Helper()
	full, id, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	gs := &guardStore{mockTokenStore: mockTokenStore{tok: store.APIToken{
		ID: "tok_" + id, TokenID: id, TokenHash: hash, TenantID: "ten_x", Scopes: []string{"read", "write"},
	}}}
	fl := ratelimit.New(failRate, failBurst)
	t.Cleanup(fl.Close)
	mw := &Middleware{
		Store:        gs,
		Entitlements: allowEntitlements{},
		APILimiter:   ratelimit.New(1e6, 1e6),
		Limiter:      ratelimit.New(1e6, 1e6),
		FailLimiter:  fl,
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	return mw, gs, full
}

func doReq(mw *Middleware, path, bearer, remote string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = remote
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	mw.ServeHTTP(w, r)
	return w
}

func fakeAPIToken(i int) string {
	return fmt.Sprintf("zrv_api_%016x_sahteSirSahteSirSahteSirSahteSirSahteSir%04d", i+1, i)
}

// Y7/O10: depo hatasi "token gecersiz" (401) DEGIL, 503 store_unavailable.
func TestAuthStoreErrorIs503(t *testing.T) {
	mw, gs, tok := newGuardMW(t)
	gs.set(errors.New("failed to connect: connection refused"), false)
	w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.1:1000")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "store_unavailable") {
		t.Fatalf("kod %d govde %s, beklenen 503 store_unavailable", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After yok")
	}
	// Ag oturumu ucunda (istemci token'i) mevcut istemciler 503'u kalici sayar: 429.
	ctok, _, _, _ := auth.GenerateClient()
	w = doReq(mw, netSessionPath, ctok, "198.51.100.1:1000")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "store_unavailable") {
		t.Fatalf("net/session kod %d govde %s, beklenen 429 store_unavailable", w.Code, w.Body.String())
	}
	// Depo duzelince ayni token 200.
	gs.set(nil, false)
	if w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.1:1000"); w.Code != http.StatusOK {
		t.Fatalf("depo duzeldi, kod %d", w.Code)
	}
}

// CheckFeature'daki DB hatasi "planiniz desteklemiyor" (403) olarak donmemeli.
func TestCheckFeatureDBErrorIs503(t *testing.T) {
	mw, _, tok := newGuardMW(t)
	mw.Entitlements = errEntitlements{err: errors.New("closed pool")}
	if w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.2:1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("DB hatasi: kod %d, beklenen 503", w.Code)
	}
	for _, deny := range []error{
		&entitlements.EntitlementError{Code: "feature_not_available", Message: "x"},
		&entitlements.EntitlementError{Code: "feature_not_available", Message: "plan bulunamadi"},
		store.ErrNotFound,
		fmt.Errorf("abonelik: %w", store.ErrNotFound),
		store.ErrTenantNotFound,
	} {
		mw.Entitlements = errEntitlements{err: deny}
		if w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.2:1"); w.Code != http.StatusForbidden {
			t.Fatalf("plan reddi %v: kod %d, beklenen 403", deny, w.Code)
		}
	}
	// Ayni metni tasiyan TIPSIZ hata artik ret sayilmaz (metin eslesmesi kaldirildi).
	mw.Entitlements = errEntitlements{err: errors.New("plan bulunamadi")}
	if w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.2:1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("tipsiz hata: kod %d, beklenen 503", w.Code)
	}
}

// K1: tek IP'den bilinmeyen token seli, kota bitince argon2 calismadan 429 alir;
// es zamanli seldeki fazla istekler de argon2'ye ulasamaz.
func TestUnknownTokenFloodGetsRateLimited(t *testing.T) {
	mw, _, tok := newGuardMW(t)
	before := obs.AuthFailures(auth.KindAPIToken, auth.ReasonUnknown)
	var n401, n429 atomic.Int64
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch doReq(mw, "/api/v1/tunnels", fakeAPIToken(i), "203.0.113.9:4444").Code {
			case http.StatusUnauthorized:
				n401.Add(1)
			case http.StatusTooManyRequests:
				n429.Add(1)
			}
		}()
	}
	wg.Wait()
	if n401.Load() > failBurst || n401.Load()+n429.Load() != 64 {
		t.Fatalf("401=%d 429=%d; en fazla %d argon2'li 401 bekleniyordu", n401.Load(), n429.Load(), failBurst)
	}
	if got := obs.AuthFailures(auth.KindAPIToken, auth.ReasonUnknown) - before; got != uint64(n401.Load()) {
		t.Fatalf("unknown sayaci %d arti, 401 sayisi %d", got, n401.Load())
	}
	// Ayni IP'den gecerli token: ilk kez (onbellekte degil) -> 429 (kilitli IP).
	// Baska IP'den dogrulanip onbellege girdikten sonra kilitli IP'den de gecer.
	if w := doReq(mw, "/api/v1/tunnels", tok, "198.51.100.77:1"); w.Code != http.StatusOK {
		t.Fatalf("baska IP gecerli token: %d", w.Code)
	}
	if w := doReq(mw, "/api/v1/tunnels", tok, "203.0.113.9:4444"); w.Code != http.StatusOK {
		t.Fatalf("onbellekteki gecerli token ayni (kilitli) IP'den: %d", w.Code)
	}
	// Baska IP etkilenmez: bilinmeyen token orada 401.
	if w := doReq(mw, "/api/v1/tunnels", fakeAPIToken(999), "198.51.100.3:1"); w.Code != http.StatusUnauthorized {
		t.Fatalf("baska IP: %d", w.Code)
	}
}

// Basarili istekler basarisiz deneme kotasini harcamaz.
func TestSuccessDoesNotConsumeFailQuota(t *testing.T) {
	mw, _, tok := newGuardMW(t)
	for range 50 {
		if w := doReq(mw, "/api/v1/tunnels", tok, "192.0.2.50:1"); w.Code != http.StatusOK {
			t.Fatalf("gecerli token: %d", w.Code)
		}
	}
	if rem := mw.FailLimiter.Remaining("192.0.2.50"); rem != failBurst {
		t.Fatalf("kalan kota %d, beklenen %d", rem, failBurst)
	}
}

// Onbellek iptali bozmaz: token iptal edilince (depo bulamaz) bir sonraki istek
// aninda 401; onbellekteki basari kaydi kullanilmaz. Depo her istekte okunur.
func TestRevokedTokenRejectedDespiteCache(t *testing.T) {
	mw, gs, tok := newGuardMW(t)
	for range 3 {
		if w := doReq(mw, "/api/v1/tunnels", tok, "192.0.2.60:1"); w.Code != http.StatusOK {
			t.Fatalf("gecerli token: %d", w.Code)
		}
	}
	if gs.lookups.Load() != 3 {
		t.Fatalf("depo sorgusu %d, her istekte 1 olmali", gs.lookups.Load())
	}
	gs.set(nil, true)
	if w := doReq(mw, "/api/v1/tunnels", tok, "192.0.2.60:1"); w.Code != http.StatusUnauthorized {
		t.Fatalf("iptal edilen token: %d, beklenen 401", w.Code)
	}
}

// Bicimsiz admin anahtari ("Bearer garbage") argon2 calistirmaz ve kotayi
// harcamaz (suresi dolmus oturum token'i tasiyan istemciler 429'a dusmesin).
func TestMalformedBearerNoQuota(t *testing.T) {
	mw, _, _ := newGuardMW(t)
	before := obs.AuthFailures(auth.KindAdminKey, auth.ReasonMalformed)
	for range 40 {
		if w := doReq(mw, "/api/v1/tunnels", "garbage", "192.0.2.70:1"); w.Code != http.StatusUnauthorized {
			t.Fatalf("garbage: %d", w.Code)
		}
	}
	if rem := mw.FailLimiter.Remaining("192.0.2.70"); rem != failBurst {
		t.Fatalf("kalan kota %d, beklenen %d", rem, failBurst)
	}
	if got := obs.AuthFailures(auth.KindAdminKey, auth.ReasonMalformed) - before; got != 40 {
		t.Fatalf("malformed sayaci %d arti, beklenen 40", got)
	}
}

// Admin anahtari yolu da ayni sinirdan gecer: yanlis tokenID seli 429'a duser.
func TestAdminKeyFloodRateLimited(t *testing.T) {
	mw, _, _ := newGuardMW(t)
	_, kid, khash, _ := auth.GenerateAdmin()
	mw.Key = AdminKey{TokenID: kid, Hash: khash}
	n429 := 0
	for i := range failBurst + 5 {
		tok := fmt.Sprintf("zrv_admin_%016x_yanlis", i+1)
		if doReq(mw, "/api/v1/tunnels", tok, "192.0.2.80:1").Code == http.StatusTooManyRequests {
			n429++
		}
	}
	if n429 != 5 {
		t.Fatalf("429 sayisi %d, beklenen 5", n429)
	}
	// Dogru tokenID + yanlis sir de sayilir; kilitli IP'de argon2 calismaz -> 429.
	if w := doReq(mw, "/api/v1/tunnels", "zrv_admin_"+kid+"_yanlis", "192.0.2.80:1"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("kilitli IP, dogru tokenID yanlis sir: %d", w.Code)
	}
}

// Istemci token'i ile ag oturumu: bilinmeyen token seli sinirlanir.
func TestClientTokenNetSessionGuard(t *testing.T) {
	mw, gs, _ := newGuardMW(t)
	full, id, hash, _ := auth.GenerateClient()
	gs.client = store.Client{ID: "cli_1", TokenID: id, TokenHash: hash, TenantID: "ten_x"}
	if w := doReq(mw, netSessionPath, full, "192.0.2.90:1"); w.Code != http.StatusOK {
		t.Fatalf("gecerli istemci token'i: %d %s", w.Code, w.Body.String())
	}
	n429 := 0
	for i := range failBurst + 3 {
		tok := fmt.Sprintf("zrv_live_%016x_yanlis", i+1)
		if doReq(mw, netSessionPath, tok, "192.0.2.91:1").Code == http.StatusTooManyRequests {
			n429++
		}
	}
	if n429 != 3 {
		t.Fatalf("429 sayisi %d, beklenen 3", n429)
	}
}
