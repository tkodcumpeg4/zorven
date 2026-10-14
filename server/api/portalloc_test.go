package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/portpool"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// fakePortStore, port tahsisi icin gereken Store yuzeyini bellekte taklit eder.
type fakePortStore struct {
	store.Store
	tunnels  map[string]*store.Tunnel
	released []releasedAt
}

type releasedAt struct {
	a  store.PortAddr
	at time.Time
}

func (f *fakePortStore) ListReservedPortTunnels(context.Context) ([]store.Tunnel, error) {
	var out []store.Tunnel
	for _, t := range f.tunnels {
		if t.PublicPort > 0 {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (f *fakePortStore) SetTunnelAddr(_ context.Context, _ string, id, ip string, port int) error {
	for _, t := range f.tunnels {
		if t.PublicPort == port && t.PublicIP == ip && t.ID != id {
			return errors.New("unique")
		}
	}
	f.tunnels[id].PublicIP, f.tunnels[id].PublicPort = ip, port
	return nil
}

func (f *fakePortStore) ListReleasedPorts(_ context.Context, since time.Time) ([]store.PortAddr, error) {
	var out []store.PortAddr
	for _, r := range f.released {
		if r.at.After(since) {
			out = append(out, r.a)
		}
	}
	return out, nil
}

func newPortSrv(t *testing.T, bind, hosts string, min, max int, q time.Duration) (*Server, *fakePortStore) {
	t.Helper()
	cfg, err := portpool.Parse(bind, hosts, min, max, q, "zorven.app")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePortStore{tunnels: map[string]*store.Tunnel{}}
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5"} {
		f.tunnels[id] = &store.Tunnel{ID: id}
	}
	return &Server{Store: f, PortPool: cfg, PlatformDomain: "zorven.app",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, f
}

func TestAllocateTunnelPort_LegacyUnchanged(t *testing.T) {
	// Hic env yok: PortPool nil -> 10000'den baslar, birincil IP, host = apex.
	f := &fakePortStore{tunnels: map[string]*store.Tunnel{"t1": {ID: "t1"}}}
	s := &Server{Store: f, PlatformDomain: "zorven.app", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a, err := s.allocateTunnelPort(context.Background(), "ten", "t1")
	if err != nil || a.Port != 10000 || a.Key != "" {
		t.Fatalf("legacy tahsis: %+v %v", a, err)
	}
	tn := *f.tunnels["t1"]
	s.fillPublicHost(&tn)
	if tn.PublicHost != "zorven.app" {
		t.Fatalf("host: %q", tn.PublicHost)
	}
}

func TestAllocateTunnelPort_MultiIPFillsPrimaryThenSecondary(t *testing.T) {
	s, f := newPortSrv(t, "203.0.113.7,198.51.100.7",
		"tcp.zorven.app=203.0.113.7,tcp2.zorven.app=198.51.100.7", 10000, 10001, 0)
	want := []portpool.Addr{{Key: "", Port: 10000}, {Key: "", Port: 10001},
		{Key: "198.51.100.7", Port: 10000}, {Key: "198.51.100.7", Port: 10001}}
	for i, id := range []string{"t1", "t2", "t3", "t4"} {
		a, err := s.allocateTunnelPort(context.Background(), "ten", id)
		if err != nil || a != want[i] {
			t.Fatalf("%s: %+v %v (beklenen %+v)", id, a, err, want[i])
		}
	}
	if _, err := s.allocateTunnelPort(context.Background(), "ten", "t5"); err == nil {
		t.Fatal("havuz dolu olmali (2 IP x 2 port)")
	}
	tn := *f.tunnels["t3"]
	s.fillPublicHost(&tn)
	if tn.PublicHost != "tcp2.zorven.app" || tn.PublicPort != 10000 {
		t.Fatalf("ikincil IP adresi: %s:%d", tn.PublicHost, tn.PublicPort)
	}
	tn = *f.tunnels["t1"]
	s.fillPublicHost(&tn)
	if tn.PublicHost != "tcp.zorven.app" {
		t.Fatalf("birincil host: %s", tn.PublicHost)
	}
}

func TestAllocateTunnelPort_ExistingTunnelsStayOnPrimary(t *testing.T) {
	s, f := newPortSrv(t, "203.0.113.7,198.51.100.7", "", 10000, 10001, 0)
	// Eski kayit: public_ip "" ve port 10000 (IP eklenmeden once tahsis edilmis).
	f.tunnels["t1"].PublicPort = 10000
	a, err := s.allocateTunnelPort(context.Background(), "ten", "t2")
	if err != nil || a != (portpool.Addr{Key: "", Port: 10001}) {
		t.Fatalf("yeni tahsis eski kaydin yanina dusmeli: %+v %v", a, err)
	}
	if f.tunnels["t1"].PublicPort != 10000 || f.tunnels["t1"].PublicIP != "" {
		t.Fatal("mevcut tunelin adresi degismemeli")
	}
}

func TestAllocateTunnelPort_Quarantine(t *testing.T) {
	s, f := newPortSrv(t, "", "", 10000, 10002, 10*time.Minute)
	f.released = []releasedAt{
		{store.PortAddr{Port: 10000}, time.Now().Add(-time.Minute)}, // karantinada
		{store.PortAddr{Port: 10001}, time.Now().Add(-time.Hour)},   // suresi dolmus
	}
	a, err := s.allocateTunnelPort(context.Background(), "ten", "t1")
	if err != nil || a.Port != 10001 {
		t.Fatalf("karantinadaki 10000 atlanmali, suresi dolan 10001 verilmeli: %+v %v", a, err)
	}
	// Karantina kapali (0): serbest port hemen yeniden verilir.
	s2, f2 := newPortSrv(t, "", "", 10000, 10002, 0)
	f2.released = []releasedAt{{store.PortAddr{Port: 10000}, time.Now()}}
	a, err = s2.allocateTunnelPort(context.Background(), "ten", "t1")
	if err != nil || a.Port != 10000 {
		t.Fatalf("karantina kapaliyken 10000 verilmeli: %+v %v", a, err)
	}
}

func TestAllocateTunnelPort_PoolFullWithQuarantine(t *testing.T) {
	s, f := newPortSrv(t, "", "", 10000, 10000, 10*time.Minute)
	f.released = []releasedAt{{store.PortAddr{Port: 10000}, time.Now()}}
	if _, err := s.allocateTunnelPort(context.Background(), "ten", "t1"); err == nil ||
		!strings.Contains(err.Error(), "karantinada 1") {
		t.Fatalf("karantina nedeniyle dolu olmali: %v", err)
	}
}

type quotaOnly struct {
	entitlements.EntitlementService
	err error
}

func (q quotaOnly) CanReservePort(context.Context, string) error { return q.err }

func TestCheckPortQuota(t *testing.T) {
	s := &Server{}
	if err := s.checkPortQuota(context.Background(), "ten"); err != nil {
		t.Fatalf("Entitlements nil: limitsiz olmali: %v", err)
	}
	s.Entitlements = quotaOnly{err: &entitlements.EntitlementError{Code: "plan_limit_reached"}}
	if err := s.checkPortQuota(context.Background(), "ten"); err == nil {
		t.Fatal("kota hatasi iletilmeli")
	}
	s.Entitlements = quotaOnly{}
	if err := s.checkPortQuota(context.Background(), "ten"); err != nil {
		t.Fatalf("kota uygunken hata olmamali: %v", err)
	}
}
