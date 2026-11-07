package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func TestParseListPage(t *testing.T) {
	cases := []struct {
		q       string
		limit   int
		offset  int
		wantErr bool
	}{
		{"", 500, 0, false},
		{"limit=50", 50, 0, false},
		{"limit=50&offset=100", 50, 100, false},
		{"limit=9999", 500, 0, false}, // 500'e kelepcelenir
		{"offset=7", 500, 7, false},
		{"limit=0", 0, 0, true},
		{"limit=-1", 0, 0, true},
		{"limit=abc", 0, 0, true},
		{"offset=-1", 0, 0, true},
		{"offset=x", 0, 0, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x?"+c.q, nil)
		pg, err := parseListPage(r)
		if (err != nil) != c.wantErr {
			t.Fatalf("%q: err=%v wantErr=%v", c.q, err, c.wantErr)
		}
		if err == nil && (pg.Limit != c.limit || pg.Offset != c.offset) {
			t.Errorf("%q: got %+v want limit=%d offset=%d", c.q, pg, c.limit, c.offset)
		}
	}
}

func TestPageOfWrites400(t *testing.T) {
	w := httptest.NewRecorder()
	if _, ok := pageOf(w, httptest.NewRequest("GET", "/x?limit=zzz", nil)); ok {
		t.Fatal("gecersiz limit kabul edildi")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "invalid_request" {
		t.Errorf("code=%q", body.Error.Code)
	}
}

func TestSlicePageAndHeaders(t *testing.T) {
	items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	w := httptest.NewRecorder()
	got := slicePage(w, store.Page{Limit: 4, Offset: 8}, items)
	if len(got) != 2 || got[0] != 8 || got[1] != 9 {
		t.Fatalf("got %v", got)
	}
	if w.Header().Get("X-Total-Count") != "10" || w.Header().Get("X-Has-More") != "false" {
		t.Errorf("basliklar: %v", w.Header())
	}
	w = httptest.NewRecorder()
	got = slicePage(w, store.Page{Limit: 4, Offset: 0}, items)
	if len(got) != 4 || w.Header().Get("X-Has-More") != "true" {
		t.Errorf("ilk sayfa: %v %v", got, w.Header())
	}
	// Ofset toplami asarsa bos (nil degil) dizi.
	w = httptest.NewRecorder()
	got = slicePage(w, store.Page{Limit: 4, Offset: 50}, items)
	if got == nil || len(got) != 0 || w.Header().Get("X-Total-Count") != "10" {
		t.Errorf("tasan ofset: %#v %v", got, w.Header())
	}
	// nil giris de bos dizi olarak serilestirilmeli.
	w = httptest.NewRecorder()
	if got := slicePage[int](w, store.Page{Limit: 4}, nil); got == nil {
		t.Error("nil giris icin nil sonuc")
	}
}

// pagingStore, SQL LIMIT/OFFSET davranisini bellekte taklit eden n kayitli sahte depodur.
type pagingStore struct {
	store.Store
	n int
}

func (p *pagingStore) win(pg store.Page) (int, int) {
	lo := min(pg.Offset, p.n)
	hi := p.n
	if pg.Limit > 0 {
		hi = min(lo+pg.Limit, p.n)
	}
	return lo, hi
}

func (p *pagingStore) ListTunnelsPage(_ context.Context, tenantID, _ string, pg store.Page) ([]store.Tunnel, int, error) {
	lo, hi := p.win(pg)
	out := []store.Tunnel{}
	for i := lo; i < hi; i++ {
		out = append(out, store.Tunnel{ID: fmt.Sprintf("t%04d", i), TenantID: tenantID})
	}
	return out, p.n, nil
}
func (p *pagingStore) ListHostnames(context.Context, string) ([]store.Hostname, error) {
	return nil, nil
}
func (p *pagingStore) ListHostnamesPage(_ context.Context, tenantID, _ string, pg store.Page) ([]store.Hostname, int, error) {
	lo, hi := p.win(pg)
	out := []store.Hostname{}
	for i := lo; i < hi; i++ {
		out = append(out, store.Hostname{ID: fmt.Sprintf("h%04d", i), TenantID: tenantID})
	}
	return out, p.n, nil
}
func (p *pagingStore) ListSecretsPage(_ context.Context, tenantID, _ string, pg store.Page) ([]store.Secret, int, error) {
	lo, hi := p.win(pg)
	out := []store.Secret{}
	for i := lo; i < hi; i++ {
		out = append(out, store.Secret{ID: fmt.Sprintf("s%04d", i), TenantID: tenantID})
	}
	return out, p.n, nil
}
func (p *pagingStore) ListTeamMembersPage(_ context.Context, _ string, pg store.Page) ([]store.TeamMember, int, error) {
	lo, hi := p.win(pg)
	out := []store.TeamMember{}
	for i := lo; i < hi; i++ {
		out = append(out, store.TeamMember{ID: fmt.Sprintf("m%04d", i)})
	}
	return out, p.n, nil
}
func (p *pagingStore) GetSubscription(_ context.Context, tenantID string) (store.Subscription, error) {
	return store.Subscription{TenantID: tenantID, Plan: "pro", PlanDetails: &store.Plan{}}, nil
}
func (p *pagingStore) AdminListTenantsWithCountsPage(_ context.Context, pg store.Page) ([]store.TenantWithCounts, int, error) {
	lo, hi := p.win(pg)
	out := []store.TenantWithCounts{}
	for i := lo; i < hi; i++ {
		var tw store.TenantWithCounts
		tw.ID = fmt.Sprintf("ten%04d", i)
		out = append(out, tw)
	}
	return out, p.n, nil
}
func (p *pagingStore) ListClients(_ context.Context, tenantID string) ([]store.Client, error) {
	out := []store.Client{}
	// Kasten ters sirada ver: handler kararli (created_at,id) sirasini kendisi kurmali.
	for i := p.n - 1; i >= 0; i-- {
		out = append(out, store.Client{ID: fmt.Sprintf("c%04d", i), TenantID: tenantID, CreatedAt: time.Unix(int64(i/2), 0)})
	}
	return out, nil
}

func pagedServer(n int) *Server {
	return &Server{Store: &pagingStore{n: n}, Hub: tunnel.NewHub()}
}

func doList(t *testing.T, h http.HandlerFunc, url string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	ctx := withTenant(req.Context(), "ten_x")
	if admin {
		ctx = withPlatformAdmin(ctx)
	}
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	return rec
}

// ids, bir JSON dizisinden ya da {key:[...]} nesnesinden "id" alanlarini cikarir.
func ids(t *testing.T, body []byte, key string) []string {
	t.Helper()
	var arr []map[string]any
	if key == "" {
		if err := json.Unmarshal(body, &arr); err != nil {
			t.Fatalf("govde dizi degil: %v: %s", err, body)
		}
	} else {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("govde nesne degil: %v: %s", err, body)
		}
		if err := json.Unmarshal(obj[key], &arr); err != nil {
			t.Fatalf("%q dizi degil: %v", key, err)
		}
	}
	out := make([]string, 0, len(arr))
	for _, m := range arr {
		out = append(out, fmt.Sprint(m["id"]))
	}
	return out
}

func TestListEndpointsPaginate(t *testing.T) {
	srv := pagedServer(7)
	type ep struct {
		name  string
		h     http.HandlerFunc
		key   string // "" = govde dizi
		admin bool
	}
	eps := []ep{
		{"tunnels", srv.listTunnels, "", false},
		{"hostnames", srv.listHostnames, "", false},
		{"secrets", srv.listSecrets, "secrets", false},
		{"members", srv.listTeamMembers, "members", false},
		{"admin tenants", srv.adminListTenants, "", true},
	}
	for _, e := range eps {
		t.Run(e.name, func(t *testing.T) {
			// Sayfa 2 (limit=3, offset=3): 3 kayit, toplam 7, devami var.
			rec := doList(t, e.h, "/x?limit=3&offset=3", e.admin)
			if rec.Code != 200 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			got := ids(t, rec.Body.Bytes(), e.key)
			if len(got) != 3 {
				t.Fatalf("sayfa boyu=%d (%v)", len(got), got)
			}
			if rec.Header().Get("X-Total-Count") != "7" {
				t.Errorf("X-Total-Count=%q", rec.Header().Get("X-Total-Count"))
			}
			if rec.Header().Get("X-Has-More") != "true" {
				t.Errorf("X-Has-More=%q", rec.Header().Get("X-Has-More"))
			}
			// Son sayfa: devami yok.
			rec = doList(t, e.h, "/x?limit=3&offset=6", e.admin)
			if got := ids(t, rec.Body.Bytes(), e.key); len(got) != 1 {
				t.Errorf("son sayfa=%v", got)
			}
			if rec.Header().Get("X-Has-More") != "false" {
				t.Errorf("son sayfa X-Has-More=%q", rec.Header().Get("X-Has-More"))
			}
			// Limit yoksa varsayilan 500: hepsi gelir, govde degismez.
			rec = doList(t, e.h, "/x", e.admin)
			if got := ids(t, rec.Body.Bytes(), e.key); len(got) != 7 {
				t.Errorf("varsayilan sayfa=%d", len(got))
			}
			if rec.Header().Get("X-Total-Count") != "7" {
				t.Errorf("varsayilan X-Total-Count=%q", rec.Header().Get("X-Total-Count"))
			}
			// Gecersiz parametre -> 400 invalid_request.
			for _, bad := range []string{"limit=-1", "limit=x", "offset=-5", "offset=y", "limit=0"} {
				if rec := doList(t, e.h, "/x?"+bad, e.admin); rec.Code != 400 {
					t.Errorf("%s: status=%d", bad, rec.Code)
				}
			}
		})
	}
}

func TestListLimitCappedAt500(t *testing.T) {
	srv := pagedServer(620)
	rec := doList(t, srv.listTunnels, "/x", false)
	if got := ids(t, rec.Body.Bytes(), ""); len(got) != 500 {
		t.Fatalf("varsayilan sinir: %d kayit", len(got))
	}
	if rec.Header().Get("X-Total-Count") != "620" || rec.Header().Get("X-Has-More") != "true" {
		t.Errorf("basliklar: %v", rec.Header())
	}
	rec = doList(t, srv.listTunnels, "/x?limit=100000", false)
	if got := ids(t, rec.Body.Bytes(), ""); len(got) != 500 {
		t.Fatalf("limit kelepcesi: %d kayit", len(got))
	}
	rec = doList(t, srv.listTunnels, "/x?offset=500", false)
	if got := ids(t, rec.Body.Bytes(), ""); len(got) != 120 {
		t.Fatalf("ikinci sayfa: %d kayit", len(got))
	}
}

// Sayfalar kaymamali: ardisik sayfalar birlesince tum kayitlar tam bir kez gelir.
func TestListOrderingStableAcrossPages(t *testing.T) {
	srv := pagedServer(7)
	var all []string
	for off := 0; off < 7; off += 3 {
		rec := doList(t, srv.listTunnels, fmt.Sprintf("/x?limit=3&offset=%d", off), false)
		all = append(all, ids(t, rec.Body.Bytes(), "")...)
	}
	if len(all) != 7 {
		t.Fatalf("toplam=%d", len(all))
	}
	seen := map[string]bool{}
	for i, id := range all {
		if seen[id] {
			t.Errorf("tekrar eden kayit: %s", id)
		}
		seen[id] = true
		if want := fmt.Sprintf("t%04d", i); id != want {
			t.Errorf("sira[%d]=%s want %s", i, id, want)
		}
	}
}

// Istemciler yetki suzgeci nedeniyle bellekte dilimlenir; sira (created_at,id) kararli olmali.
func TestListClientsSlicedWithStableOrder(t *testing.T) {
	srv := pagedServer(7)
	var all []string
	for off := 0; off < 7; off += 3 {
		rec := doList(t, srv.listClients, fmt.Sprintf("/x?limit=3&offset=%d", off), true)
		if rec.Code != 200 {
			t.Fatalf("status=%d %s", rec.Code, rec.Body)
		}
		if rec.Header().Get("X-Total-Count") != "7" {
			t.Errorf("X-Total-Count=%q", rec.Header().Get("X-Total-Count"))
		}
		all = append(all, ids(t, rec.Body.Bytes(), "")...)
	}
	for i, id := range all {
		if want := fmt.Sprintf("c%04d", i); id != want {
			t.Errorf("sira[%d]=%s want %s", i, id, want)
		}
	}
}

// Eski cursor tabanli mod bozulmaz: cursor verilince X-Next-Cursor doner, X-Total-Count yazilmaz.
func TestLegacyCursorModeStillWorks(t *testing.T) {
	srv := pagedServer(5)
	srv.Store.(*pagingStore).n = 5
	rec := doList(t, srv.listClients, "/x?limit=2&cursor=c0001", true)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("X-Total-Count") != "" {
		t.Error("cursor modunda X-Total-Count beklenmiyordu")
	}
	if got := ids(t, rec.Body.Bytes(), ""); len(got) != 2 || got[0] != "c0002" {
		t.Errorf("cursor sayfasi: %v", got)
	}
}
