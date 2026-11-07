package api

// Hesap silme uclari (account.go): ic ucun HMAC imzasi, dry_run/engel/basari
// yollari ve oturumlu onizleme. Postgres gerektirmez (sahte store).

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Capraz dil vektoru: web/server/lib/account-internal.test.ts ayni sabitleri dogrular.
// Anahtar: HKDF-SHA256(master "zorven-test-master-secret-0123456789", tuz yok,
// bilgi "zorven-account-internal-v1") (bkz. server/secretderive_test.go).
const (
	vecAccountKeyHex = "fb8ccf24670e3f11f573f12307f799fc3d4ddffb95509cfc5a66b8c6d79b6891"
	vecAccountTs     = "1800000000"
	vecAccountBody   = `{"user_id":"usr_8f3a1c","dry_run":false}`
	vecAccountSig    = "fa8e62c135735ecf3b26bda03024cc8ebaab23e6ada91e5428a5501beec6439d"
)

func vecKey(t *testing.T) []byte {
	t.Helper()
	k, err := hex.DecodeString(vecAccountKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestAccountSignatureVektoru(t *testing.T) {
	if got := accountSignature(vecKey(t), vecAccountTs, []byte(vecAccountBody)); got != vecAccountSig {
		t.Fatalf("imza vektoru sapti:\n got %s\nwant %s", got, vecAccountSig)
	}
}

func TestVerifyAccountSignature(t *testing.T) {
	key := vecKey(t)
	now := time.Unix(1800000000, 0)
	body := []byte(vecAccountBody)
	ok := func(ts, sig string, b []byte, at time.Time) bool {
		return verifyAccountSignature(key, ts, sig, b, at)
	}
	if !ok(vecAccountTs, vecAccountSig, body, now) {
		t.Fatal("gecerli imza reddedildi")
	}
	if !ok(vecAccountTs, vecAccountSig, body, now.Add(119*time.Second)) || !ok(vecAccountTs, vecAccountSig, body, now.Add(-119*time.Second)) {
		t.Fatal("zaman sapmasi sinir icinde kabul edilmeli")
	}
	if ok(vecAccountTs, vecAccountSig, body, now.Add(3*time.Minute)) || ok(vecAccountTs, vecAccountSig, body, now.Add(-3*time.Minute)) {
		t.Fatal("suresi dolmus/gelecek imza reddedilmeli")
	}
	if ok(vecAccountTs, vecAccountSig, []byte(strings.Replace(vecAccountBody, "false", "true", 1)), now) {
		t.Fatal("govde degisince imza gecersiz olmali (dry_run bayragi imzaya bagli)")
	}
	if ok("1800000001", vecAccountSig, body, now) {
		t.Fatal("zaman damgasi imzaya bagli olmali")
	}
	if ok(vecAccountTs, "", body, now) || ok(vecAccountTs, vecAccountSig[:10], body, now) || ok("bozuk", vecAccountSig, body, now) {
		t.Fatal("bos/kisa imza ve bozuk zaman reddedilmeli")
	}
	if verifyAccountSignature(nil, vecAccountTs, vecAccountSig, body, now) {
		t.Fatal("anahtar yoksa hicbir imza gecmemeli")
	}
	other := append([]byte(nil), key...)
	other[0] ^= 0xff
	if verifyAccountSignature(other, vecAccountTs, vecAccountSig, body, now) {
		t.Fatal("baska anahtarla uretilmis imza gecmemeli")
	}
}

type acctFakeStore struct {
	store.Store
	plan     store.AccountDeletionPlan
	planErr  error
	purgeRes store.AccountPurgeResult
	purgeErr error
	purged   []string
	planned  []string
}

func (f *acctFakeStore) PlanAccountDeletion(_ context.Context, userID string) (store.AccountDeletionPlan, error) {
	f.planned = append(f.planned, userID)
	p := f.plan
	p.UserID = userID
	return p, f.planErr
}

func (f *acctFakeStore) PurgeAccount(_ context.Context, userID string) (store.AccountPurgeResult, error) {
	f.purged = append(f.purged, userID)
	return f.purgeRes, f.purgeErr
}

func signedAccountRequest(t *testing.T, key []byte, body string, at time.Time) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/account/purge", strings.NewReader(body))
	req.Header.Set("X-Zorven-Ts", ts)
	req.Header.Set("X-Zorven-Sig", accountSignature(key, ts, []byte(body)))
	return req
}

func TestAccountPurgeInternal_Yetki(t *testing.T) {
	f := &acctFakeStore{}
	s := &Server{Store: f}
	body := `{"user_id":"usr_1","dry_run":false}`

	// Anahtar yapilandirilmamis -> 503, store'a dokunulmaz.
	rec := httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), body, time.Now()))
	if rec.Code != http.StatusServiceUnavailable || len(f.purged) != 0 {
		t.Fatalf("anahtarsiz 503 beklenir: %d purged=%v", rec.Code, f.purged)
	}

	s.AccountKey = vecKey(t)
	// Imzasiz.
	rec = httptest.NewRecorder()
	s.accountPurgeInternal(rec, httptest.NewRequest(http.MethodPost, "/api/v1/internal/account/purge", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("imzasiz 401 beklenir: %d", rec.Code)
	}
	// Yanlis anahtar.
	rec = httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, []byte("baska-anahtar-baska-anahtar-baska"), body, time.Now()))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("yanlis anahtar 401 beklenir: %d", rec.Code)
	}
	// Eski imza.
	rec = httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), body, time.Now().Add(-10*time.Minute)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("eski imza 401 beklenir: %d", rec.Code)
	}
	if len(f.purged) != 0 || len(f.planned) != 0 {
		t.Fatalf("yetkisiz istekler store'a ulasmamali: purged=%v planned=%v", f.purged, f.planned)
	}

	// Gecersiz kullanici kimligi (imza dogru olsa bile).
	for _, bad := range []string{`{"user_id":""}`, `{"user_id":"a b"}`, `{"user_id":"x;drop"}`, `{"user_id":"` + strings.Repeat("a", 200) + `"}`, `bozuk`} {
		rec = httptest.NewRecorder()
		s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), bad, time.Now()))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: 400 beklenir, gelen %d", bad, rec.Code)
		}
	}
}

func TestAccountPurgeInternal_DryRunEngelVeBasari(t *testing.T) {
	f := &acctFakeStore{}
	var changed int32
	s := &Server{Store: f, AccountKey: vecKey(t), OnTunnelChange: func() { atomic.AddInt32(&changed, 1) }}

	// dry_run: plan doner, hicbir sey silinmez.
	f.plan = store.AccountDeletionPlan{
		Delete:   []store.AccountOrg{{ID: "org_a", Slug: "kisisel", Members: 1, Tunnels: 2}},
		Leave:    []store.AccountOrg{{ID: "org_b", Slug: "ekip", Role: "member", Members: 4}},
		Blockers: []store.AccountOrg{},
	}
	rec := httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), `{"user_id":"usr_1","dry_run":true}`, time.Now()))
	if rec.Code != http.StatusOK || len(f.purged) != 0 {
		t.Fatalf("dry_run 200 ve silmeden beklenir: %d purged=%v", rec.Code, f.purged)
	}
	var plan struct {
		UserID    string `json:"user_id"`
		CanDelete bool   `json:"can_delete"`
		Delete    []struct {
			Slug    string `json:"slug"`
			Tunnels int    `json:"tunnels"`
		} `json:"delete_orgs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.UserID != "usr_1" || !plan.CanDelete || len(plan.Delete) != 1 || plan.Delete[0].Slug != "kisisel" || plan.Delete[0].Tunnels != 2 {
		t.Fatalf("plan govdesi beklenenden farkli: %s", rec.Body.String())
	}

	// Engel: 409 + plan; bellek temizligi yapilmaz.
	f.purgeErr = &store.AccountBlockedError{Plan: store.AccountDeletionPlan{
		UserID:   "usr_1",
		Blockers: []store.AccountOrg{{ID: "org_c", Slug: "ortak", Role: "owner", Members: 3, Reason: store.AccountBlockSoleOwner}},
	}}
	rec = httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), `{"user_id":"usr_1"}`, time.Now()))
	if rec.Code != http.StatusConflict {
		t.Fatalf("engel 409 beklenir: %d %s", rec.Code, rec.Body.String())
	}
	var blocked struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Plan struct {
			CanDelete bool `json:"can_delete"`
			Blockers  []struct {
				Slug   string `json:"slug"`
				Reason string `json:"reason"`
			} `json:"blocking_orgs"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Error.Code != "account_blocked" || blocked.Plan.CanDelete ||
		len(blocked.Plan.Blockers) != 1 || blocked.Plan.Blockers[0].Slug != "ortak" || blocked.Plan.Blockers[0].Reason != "sole_owner" {
		t.Fatalf("engel govdesi beklenenden farkli: %s", rec.Body.String())
	}
	if atomic.LoadInt32(&changed) != 0 {
		t.Fatal("engelli silmede yonlendirme tazelenmemeli")
	}

	// Basari: store cagrilir, sonuc doner, yonlendirme tazelenir.
	f.purgeErr = nil
	f.purged = nil
	f.purgeRes = store.AccountPurgeResult{
		Pseudonym:     store.AccountPseudonym("usr_1"),
		DeletedOrgs:   []store.AccountOrg{{ID: "org_a", Slug: "kisisel"}},
		LeftOrgs:      []store.AccountOrg{{ID: "org_b", Slug: "ekip"}},
		ClientIDs:     []string{"cli_1"},
		RevokedTokens: 2,
	}
	rec = httptest.NewRecorder()
	s.accountPurgeInternal(rec, signedAccountRequest(t, vecKey(t), `{"user_id":"usr_1"}`, time.Now()))
	if rec.Code != http.StatusOK || len(f.purged) != 1 || f.purged[0] != "usr_1" {
		t.Fatalf("basari 200 ve PurgeAccount cagrisi beklenir: %d purged=%v %s", rec.Code, f.purged, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), store.AccountPseudonym("usr_1")) {
		t.Fatalf("sonuc takma adi icermeli: %s", rec.Body.String())
	}
	if atomic.LoadInt32(&changed) != 1 {
		t.Fatalf("silme sonrasi yonlendirme bir kez tazelenmeli: %d", changed)
	}
}

func TestAccountDeletionPreview(t *testing.T) {
	f := &acctFakeStore{plan: store.AccountDeletionPlan{
		Delete: []store.AccountOrg{{ID: "org_a", Slug: "kisisel", Members: 1}},
	}}
	s := &Server{Store: f}

	// Oturumsuz / API token'i / admin anahtari: reddedilir.
	rec := httptest.NewRecorder()
	s.accountDeletionPreview(rec, httptest.NewRequest(http.MethodGet, "/api/v1/account/deletion-preview", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("kullanicisiz istek 403 olmali: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/account/deletion-preview", nil)
	ctx := withUser(req.Context(), &AuthUser{ID: "usr_9", Email: "a@example.com", Role: "api_token"})
	ctx = withAPIScopes(ctx, []string{"read"})
	rec = httptest.NewRecorder()
	s.accountDeletionPreview(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("API token 403 olmali: %d", rec.Code)
	}
	if len(f.planned) != 0 {
		t.Fatalf("reddedilen istekler store'a ulasmamali: %v", f.planned)
	}

	// Oturumlu kullanici kendi hesabinin onizlemesini alir.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/account/deletion-preview", nil)
	req = req.WithContext(withUser(req.Context(), &AuthUser{ID: "usr_9", Email: "a@example.com", Role: "owner"}))
	rec = httptest.NewRecorder()
	s.accountDeletionPreview(rec, req)
	if rec.Code != http.StatusOK || len(f.planned) != 1 || f.planned[0] != "usr_9" {
		t.Fatalf("oturumlu onizleme 200 ve kendi kimligiyle beklenir: %d planned=%v", rec.Code, f.planned)
	}
	var got struct {
		UserID    string `json:"user_id"`
		CanDelete bool   `json:"can_delete"`
		Leave     []any  `json:"leave_orgs"`
		Blockers  []any  `json:"blocking_orgs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.UserID != "usr_9" || !got.CanDelete {
		t.Fatalf("onizleme govdesi beklenenden farkli: %s", rec.Body.String())
	}
}
