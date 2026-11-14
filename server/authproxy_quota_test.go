package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
)

type fakeQuota struct {
	deny   bool
	called string
}

func (f *fakeQuota) CanAddMember(_ context.Context, tenantID string) error {
	f.called = tenantID
	if f.deny {
		return &entitlements.EntitlementError{Code: "member_limit_reached", Message: "uye limiti", Resource: "members", Limit: 1, Current: 1}
	}
	return nil
}

func quotaProxy(t *testing.T, q *fakeQuota, hits *int) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	gate := &memberQuotaGate{Quota: q, ActiveOrg: func(context.Context, string) string { return "ten_active" }}
	front := httptest.NewServer(newAuthProxy(u, nil, gate))
	t.Cleanup(front.Close)
	return front
}

func TestAuthProxyInviteMemberQuota(t *testing.T) {
	hits := 0
	q := &fakeQuota{deny: true}
	front := quotaProxy(t, q, &hits)

	resp, err := http.Post(front.URL+"/api/auth/organization/invite-member", "application/json",
		strings.NewReader(`{"email":"a@b.c","organizationId":"ten_x"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || hits != 0 || q.called != "ten_x" {
		t.Fatalf("kota asiminda 403 ve proxy'siz olmali: %d hits=%d org=%q", resp.StatusCode, hits, q.called)
	}
	if !strings.Contains(string(body), "member_limit_reached") {
		t.Fatalf("entitlement hata bicimi bekleniyordu: %s", body)
	}

	// organizationId yoksa aktif organizasyon kullanilir.
	resp, _ = http.Post(front.URL+"/api/auth/organization/invite-member", "application/json", strings.NewReader(`{"email":"a@b.c"}`))
	resp.Body.Close()
	if q.called != "ten_active" || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("aktif org beklenirdi: %q %d", q.called, resp.StatusCode)
	}

	// Kota uygunsa istek govdesi bozulmadan Better Auth'a gider; diger uclar etkilenmez.
	q.deny = false
	resp, _ = http.Post(front.URL+"/api/auth/organization/invite-member", "application/json", strings.NewReader(`{"email":"a@b.c"}`))
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if hits != 1 || string(got) != `{"email":"a@b.c"}` {
		t.Fatalf("govde korunmali: hits=%d %q", hits, got)
	}
	q.deny, q.called = true, ""
	resp, _ = http.Post(front.URL+"/api/auth/sign-in/email", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || q.called != "" {
		t.Fatalf("baska uc kota kontrolune girmemeli")
	}
}

// seatQuota: sayac tabanli sahte kota; upstream davet ekleyince sayac artar.
type seatQuota struct {
	mu    sync.Mutex // sayac korumasi
	seats int
	limit int
}

func (q *seatQuota) CanAddMember(context.Context, string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.seats >= q.limit {
		return &entitlements.EntitlementError{Code: "member_limit_reached", Resource: "members", Limit: q.limit, Current: q.seats}
	}
	return nil
}

type tenantLocker struct {
	mu       sync.Mutex
	acquired int
	released int
	fail     bool
}

func (l *tenantLocker) LockQuota(_ context.Context, _, res string) (func(), error) {
	if l.fail {
		return nil, errors.New("kilit yok")
	}
	l.mu.Lock()
	l.acquired++
	return func() { l.released++; l.mu.Unlock() }, nil
}

// Iki es zamanli davet, tek kalan koltuk: kilit vekil cagrisini da kapsarsa
// yalniz biri gecer; kilitsiz (eski) davranista ikisi de gecerdi.
func runConcurrentInvites(t *testing.T, locker memberQuotaLocker) (okCount int, seats int) {
	t.Helper()
	q := &seatQuota{limit: 1}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond) // Better Auth davet ekleme suresi
		q.mu.Lock()
		q.seats++
		q.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	gate := &memberQuotaGate{Quota: q, Locker: locker}
	front := httptest.NewServer(newAuthProxy(u, nil, gate))
	defer front.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(front.URL+"/api/auth/organization/invite-member", "application/json",
				strings.NewReader(`{"email":"a@b.c","organizationId":"ten_x"}`))
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return okCount, q.seats
}

func TestAuthProxyInviteQuotaLockCoversProxyCall(t *testing.T) {
	l := &tenantLocker{}
	ok, seats := runConcurrentInvites(t, l)
	if ok != 1 || seats != 1 {
		t.Fatalf("kilit altinda yalniz bir davet gecmeli: ok=%d koltuk=%d", ok, seats)
	}
	if l.acquired != 2 || l.released != 2 {
		t.Fatalf("kilit alinip birakilmali: %d/%d", l.acquired, l.released)
	}
}

func TestAuthProxyInviteQuotaWithoutLockerKeepsOldBehavior(t *testing.T) {
	// Kilit alinamazsa (depo desteklemiyor) mevcut kilitsiz davranis: yaris mumkun, istek calisir.
	ok, _ := runConcurrentInvites(t, &tenantLocker{fail: true})
	if ok < 1 {
		t.Fatalf("kilit hatasinda istek yine de islenmeli: ok=%d", ok)
	}
}

// Kilitsiz kapi (Locker nil) iki istegi de gecirir: duzeltmenin gerekliligini kanitlar.
func TestAuthProxyInviteQuotaRaceWithoutLockDemonstrated(t *testing.T) {
	ok, seats := runConcurrentInvites(t, nil)
	if ok != 2 || seats != 2 {
		t.Skipf("yaris bu kosuda olusmadi (ok=%d)", ok)
	}
}
