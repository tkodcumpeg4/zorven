package door

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type fakeStore struct {
	mu      sync.Mutex
	doors   []store.TunnelDoor
	grants  []store.DoorGrant
	listed  int
	nextID  int
	failing bool
	revs    map[string]time.Time // "tunel|kimlik" -> son iptal
	closed  []string
}

func (f *fakeStore) RecordDoorRevocation(_ context.Context, tid, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revs == nil {
		f.revs = map[string]time.Time{}
	}
	if cur, ok := f.revs[tid+"|"+id]; !ok || at.After(cur) {
		f.revs[tid+"|"+id] = at
	}
	return nil
}

func (f *fakeStore) ListDoorRevocations(_ context.Context, tid string) (map[string]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		return nil, errors.New("db down")
	}
	out := map[string]time.Time{}
	for k, v := range f.revs {
		if len(k) > len(tid)+1 && k[:len(tid)+1] == tid+"|" {
			out[k[len(tid)+1:]] = v
		}
	}
	return out, nil
}

func (f *fakeStore) CloseDoorsForTenant(_ context.Context, tenant string, at time.Time) ([]string, []store.DoorGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for i := range f.doors {
		if f.doors[i].TenantID == tenant && f.doors[i].Enabled {
			f.doors[i].Enabled = false
			f.doors[i].PlanClosed = true
			ids = append(ids, f.doors[i].TunnelID)
		}
	}
	var rv []store.DoorGrant
	for i := range f.grants {
		if f.grants[i].TenantID == tenant && f.grants[i].RevokedAt == nil {
			t := at
			f.grants[i].RevokedAt = &t
			rv = append(rv, f.grants[i])
			if f.revs == nil {
				f.revs = map[string]time.Time{}
			}
			f.revs[f.grants[i].TunnelID+"|"+strings.ToLower(f.grants[i].Identity)] = at
		}
	}
	f.closed = append(f.closed, ids...)
	return ids, rv, nil
}

func (f *fakeStore) ListEnabledDoors(context.Context) ([]store.TunnelDoor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.TunnelDoor
	for _, d := range f.doors {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeStore) ListActiveDoorGrantsByTunnel(_ context.Context, tid string) ([]store.DoorGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	if f.failing {
		return nil, errors.New("db down")
	}
	var out []store.DoorGrant
	for _, g := range f.grants {
		if g.TunnelID == tid && g.RevokedAt == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeStore) OpenDoorGrant(_ context.Context, g store.DoorGrant) (store.DoorGrant, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.grants {
		if c.TunnelID == g.TunnelID && c.IP == g.IP && c.RevokedAt == nil && c.ExpiresAt.After(time.Now()) {
			return c, false, nil
		}
	}
	f.nextID++
	g.ID = "dg_" + string(rune('a'+f.nextID))
	f.grants = append(f.grants, g)
	return g, true, nil
}

func (f *fakeStore) RevokeDoorGrant(_ context.Context, _, tid, id string) (store.DoorGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.grants {
		if f.grants[i].ID == id && f.grants[i].TunnelID == tid && f.grants[i].RevokedAt == nil {
			now := time.Now()
			f.grants[i].RevokedAt = &now
			return f.grants[i], nil
		}
	}
	return store.DoorGrant{}, store.ErrNotFound
}

func (f *fakeStore) RevokeDoorGrantsByIP(_ context.Context, _, tid, ip string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i := range f.grants {
		if f.grants[i].TunnelID == tid && f.grants[i].IP == ip && f.grants[i].RevokedAt == nil {
			now := time.Now()
			f.grants[i].RevokedAt = &now
			n++
		}
	}
	return n, nil
}

func TestNormalizeIP(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":        "203.0.113.7",
		"203.0.113.7:4444":   "203.0.113.7",
		"::ffff:203.0.113.7": "203.0.113.7",
		"[2001:db8::1]:80":   "2001:db8::1",
		"nonsense":           "",
	}
	for in, want := range cases {
		if got := NormalizeIP(in); got != want {
			t.Errorf("NormalizeIP(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestOpenHasGrantExpiryRevoke(t *testing.T) {
	st := &fakeStore{}
	s := New(st, nil, nil)
	clock := time.Now()
	s.Now = func() time.Time { return clock }
	ctx := context.Background()
	tg := Target{TenantID: "ten", TunnelID: "tun", Host: "door--a--b.x", DurationSec: 3600}

	if _, ok := s.HasGrant("tun", "203.0.113.7"); ok {
		t.Fatal("grant yokken izin olmamali")
	}
	g, created, err := s.Open(ctx, tg, "203.0.113.7:55", "alice", "basic", "ua")
	if err != nil || !created {
		t.Fatalf("Open: created=%v err=%v", created, err)
	}
	if _, ok := s.HasGrant("tun", "203.0.113.7"); !ok {
		t.Fatal("grant sonrasi izin beklenirdi (olusturmada onbellek gecersiz kilinmali)")
	}
	if _, ok := s.HasGrant("tun", "203.0.113.8"); ok {
		t.Fatal("baska IP izin almamali")
	}
	// Ikinci giris sureyi uzatmaz.
	g2, created2, _ := s.Open(ctx, tg, "203.0.113.7", "alice", "basic", "ua")
	if created2 || !g2.ExpiresAt.Equal(g.ExpiresAt) {
		t.Fatal("aktif grant varken yenisi acilmamali / sure uzamamali")
	}

	// Onbellek TTL icinde DB'ye gitmez (UDP sel korumasi).
	s.HasGrant("tun", "198.51.100.1") // onbellegi isit
	before := st.listed
	for i := 0; i < 50; i++ {
		s.HasGrant("tun", "198.51.100.1")
	}
	if st.listed != before {
		t.Fatalf("TTL icinde DB sorgusu yapildi: %d -> %d", before, st.listed)
	}

	// Sure dolumu.
	clock = clock.Add(2 * time.Hour)
	if _, ok := s.HasGrant("tun", "203.0.113.7"); ok {
		t.Fatal("suresi dolan grant gecerli sayildi")
	}

	// Iptal: acik baglanti kesme kancasi + aninda gecersiz kilma.
	clock = time.Now()
	s.Now = func() time.Time { return clock }
	var revoked []string
	s.OnRevoke = func(tid, ip string) { revoked = append(revoked, tid+"|"+ip) }
	st.grants = nil
	g, _, _ = s.Open(ctx, tg, "203.0.113.9", "bob", "oauth", "")
	if _, ok := s.HasGrant("tun", "203.0.113.9"); !ok {
		t.Fatal("grant bekleniyordu")
	}
	if _, err := s.Revoke(ctx, "ten", "tun", g.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.HasGrant("tun", "203.0.113.9"); ok {
		t.Fatal("iptal sonrasi izin kalmamali")
	}
	if len(revoked) != 1 || revoked[0] != "tun|203.0.113.9" {
		t.Fatalf("OnRevoke beklenmedik: %v", revoked)
	}
	if _, err := s.Revoke(ctx, "ten", "tun", g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ikinci iptal ErrNotFound olmali: %v", err)
	}

	// Close: ziyaretcinin kendi IP'si.
	s.Open(ctx, tg, "203.0.113.10", "bob", "oauth", "")
	if n, _ := s.Close(ctx, tg, "203.0.113.10", "bob", "oauth", ""); n != 1 {
		t.Fatalf("Close 1 grant iptal etmeli: %d", n)
	}
	if _, ok := s.HasGrant("tun", "203.0.113.10"); ok {
		t.Fatal("kapatma sonrasi izin kalmamali")
	}
}

func TestHasGrantFailClosedOnDBError(t *testing.T) {
	st := &fakeStore{failing: true}
	s := New(st, nil, nil)
	if _, ok := s.HasGrant("tun", "203.0.113.7"); ok {
		t.Fatal("DB hatasinda ve onbellek yokken reddedilmeli")
	}
}

func TestGatePlan(t *testing.T) {
	s := New(&fakeStore{}, nil, nil)
	s.Gate = func(context.Context, string) error { return errors.New("plan") }
	_, _, err := s.Open(context.Background(), Target{TenantID: "t", TunnelID: "x"}, "203.0.113.7", "a", "basic", "")
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("ErrNotAvailable bekleniyordu: %v", err)
	}
}

func TestDoorEnabledSnapshot(t *testing.T) {
	st := &fakeStore{doors: []store.TunnelDoor{{TunnelID: "tun", TenantID: "ten", Enabled: true}}}
	s := New(st, nil, nil)
	if !s.DoorEnabled("tun") || s.DoorEnabled("other") {
		t.Fatal("kapi goruntusu yanlis")
	}
	st.mu.Lock()
	st.doors = nil
	st.mu.Unlock()
	if !s.DoorEnabled("tun") {
		t.Fatal("TTL icinde eski goruntu korunmali")
	}
	s.Changed("tun")
	if s.DoorEnabled("tun") {
		t.Fatal("Changed sonrasi goruntu yenilenmeli")
	}
}

func TestBlockedAggregation(t *testing.T) {
	var mu sync.Mutex
	var got []accesslog.Event
	p := accesslog.NewPersister(func(_ context.Context, b []accesslog.Event) error {
		mu.Lock()
		got = append(got, b...)
		mu.Unlock()
		return nil
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx)

	s := New(&fakeStore{}, p, nil)
	clock := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return clock }
	for i := 0; i < 500; i++ {
		s.NoteBlocked("ten", "tun", "203.0.113.7")
	}
	s.NoteBlocked("ten", "tun", "203.0.113.8")
	if n := s.FlushBlocked(false); n != 0 {
		t.Fatalf("icinde bulunulan dakika bosaltilmamali: %d", n)
	}
	clock = clock.Add(2 * time.Minute)
	if n := s.FlushBlocked(false); n != 2 {
		t.Fatalf("2 toplu satir bekleniyordu: %d", n)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, e := range got {
		if e.Reason != accesslog.ReasonBlockedNoGrant || e.Success {
			t.Fatalf("beklenmedik olay: %+v", e)
		}
		total += e.Count
	}
	if len(got) != 2 || total != 501 {
		t.Fatalf("olay=%d toplam=%d, 2/501 bekleniyordu", len(got), total)
	}
}

func TestRevocationInvalidatesOlderSessions(t *testing.T) {
	st := &fakeStore{}
	s := New(st, nil, nil)
	clock := time.Now()
	s.Now = func() time.Time { return clock }
	ctx := context.Background()
	tg := Target{TenantID: "ten", TunnelID: "tun", DurationSec: 3600}

	issued := clock.Add(-time.Minute)
	if s.SessionRevoked("tun", "Alice", issued) {
		t.Fatal("iptal yokken oturum gecerli olmali")
	}
	g, _, _ := s.Open(ctx, tg, "203.0.113.7", "Alice", "basic", "")
	if _, err := s.Revoke(ctx, "ten", "tun", g.ID); err != nil {
		t.Fatal(err)
	}
	// Iptal onbellegi Revoke ile gecersiz kilinir: hemen etkili.
	if !s.SessionRevoked("tun", "alice", issued) {
		t.Fatal("iptalden once verilen oturum gecersiz sayilmali (kimlik buyuk/kucuk harf duyarsiz)")
	}
	if s.SessionRevoked("tun", "bob", issued) || s.SessionRevoked("other", "alice", issued) {
		t.Fatal("baska kimlik/tunel etkilenmemeli")
	}
	// Iptalden SONRA verilen oturum (yeni giris) gecerli.
	if s.SessionRevoked("tun", "alice", clock.Add(time.Millisecond)) {
		t.Fatal("iptalden sonra verilen oturum gecerli olmali")
	}
	// Ayni anda verilen (<=) gecersiz.
	if !s.SessionRevoked("tun", "alice", clock) {
		t.Fatal("issued == iptal ani gecersiz olmali")
	}
	// DB hatasi: fail-closed.
	s2 := New(&fakeStore{failing: true}, nil, nil)
	if !s2.SessionRevoked("tun", "alice", issued) {
		t.Fatal("DB hatasinda oturum gecersiz sayilmali")
	}
	// Visitor close da iptal yazar (grant olmasa bile).
	clock = clock.Add(time.Hour)
	if _, err := s.Close(ctx, tg, "203.0.113.50", "carol", "oauth", ""); err != nil {
		t.Fatal(err)
	}
	if !s.SessionRevoked("tun", "carol", clock.Add(-time.Second)) {
		t.Fatal("Close sonrasi eski oturum gecersiz olmali")
	}
}

func TestEnforcePlanClosesDoorsAndGrants(t *testing.T) {
	st := &fakeStore{doors: []store.TunnelDoor{
		{TunnelID: "t1", TenantID: "ten", Enabled: true},
		{TunnelID: "t2", TenantID: "ten", Enabled: true},
		{TunnelID: "t3", TenantID: "other", Enabled: true},
	}}
	s := New(st, nil, nil)
	allowed := true
	s.Gate = func(_ context.Context, tenant string) error {
		if allowed || tenant == "other" {
			return nil
		}
		return ErrNotAvailable
	}
	var cut []string
	s.OnRevoke = func(tid, ip string) { cut = append(cut, tid+"|"+ip) }
	changed := 0
	s.OnChange = func() { changed++ }
	ctx := context.Background()
	s.Open(ctx, Target{TenantID: "ten", TunnelID: "t1", DurationSec: 3600}, "203.0.113.7", "alice", "basic", "")

	if n, _ := s.EnforcePlan(ctx, "ten"); n != 0 || len(st.closed) != 0 {
		t.Fatal("plan yeterliyken kapi kapatilmamali")
	}
	if !s.DoorEnabled("t1") || !s.DoorEnabled("t3") {
		t.Fatal("kapilar etkin olmali")
	}

	allowed = false
	// Savunma: kalici kapatma OLMADAN bile (onbellek temizlenince) kapi devre disi sayilir.
	s.Changed("t1")
	if s.DoorEnabled("t1") {
		t.Fatal("plan yokken DoorEnabled false olmali")
	}
	if !s.DoorEnabled("t3") {
		t.Fatal("baska kiracinin kapisi etkilenmemeli")
	}

	allowed = true
	s.Changed("t1")
	allowed = false
	n, err := s.EnforcePlan(ctx, "ten")
	if err != nil || n != 2 {
		t.Fatalf("2 kapi kapatilmali: %d %v", n, err)
	}
	if len(cut) != 1 || cut[0] != "t1|203.0.113.7" {
		t.Fatalf("acik baglantilar kesilmeli: %v", cut)
	}
	if changed != 1 {
		t.Fatalf("OnChange 1 kez cagrilmali: %d", changed)
	}
	if _, ok := s.HasGrant("t1", "203.0.113.7"); ok {
		t.Fatal("grant iptal edilmis olmali")
	}
	if !s.SessionRevoked("t1", "alice", time.Now().Add(-time.Minute)) {
		t.Fatal("plan dususunde oturumlar da iptal edilmeli")
	}
	for _, d := range st.doors {
		if d.TenantID == "ten" && (d.Enabled || !d.PlanClosed) {
			t.Fatalf("kapi kalici kapanmali: %+v", d)
		}
		if d.TenantID == "other" && !d.Enabled {
			t.Fatal("baska kiracinin kapisi dokunulmamali")
		}
	}
	// Ikinci cagri idempotent.
	if n, _ := s.EnforcePlan(ctx, "ten"); n != 0 {
		t.Fatalf("ikinci cagri bir sey kapatmamali: %d", n)
	}
}

func TestEnforceAllPlansAndTransientGate(t *testing.T) {
	st := &fakeStore{doors: []store.TunnelDoor{
		{TunnelID: "a", TenantID: "free", Enabled: true},
		{TunnelID: "b", TenantID: "pro", Enabled: true},
	}}
	s := New(st, nil, nil)
	s.Gate = func(_ context.Context, tenant string) error {
		if tenant == "free" {
			return ErrNotAvailable
		}
		return nil
	}
	if n := s.EnforceAllPlans(context.Background()); n != 1 {
		t.Fatalf("yalniz free kiracinin kapisi kapanmali: %d", n)
	}
	if st.doors[0].Enabled || !st.doors[1].Enabled {
		t.Fatalf("beklenmedik kapi durumu: %+v", st.doors)
	}

	// Gecici hata plan reddi sayilmaz: kapi kapatilmaz.
	s2 := New(&fakeStore{doors: []store.TunnelDoor{{TunnelID: "x", TenantID: "t", Enabled: true}}}, nil, nil)
	s2.Gate = func(context.Context, string) error { return errors.New("db down") }
	if n, _ := s2.EnforcePlan(context.Background(), "t"); n != 0 {
		t.Fatal("gecici hata kapi kapatmamali")
	}
	if !s2.DoorEnabled("x") {
		t.Fatal("gecici hatada kapi etkin kalmali")
	}
}
