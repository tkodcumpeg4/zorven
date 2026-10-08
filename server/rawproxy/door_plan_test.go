package rawproxy

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// doorSvcStore, gercek door.Service'i besleyen minimal depo.
type doorSvcStore struct {
	doors  []store.TunnelDoor
	grants []store.DoorGrant
}

func (d *doorSvcStore) ListEnabledDoors(context.Context) ([]store.TunnelDoor, error) {
	var out []store.TunnelDoor
	for _, x := range d.doors {
		if x.Enabled {
			out = append(out, x)
		}
	}
	return out, nil
}
func (d *doorSvcStore) ListActiveDoorGrantsByTunnel(_ context.Context, tid string) ([]store.DoorGrant, error) {
	var out []store.DoorGrant
	for _, g := range d.grants {
		if g.TunnelID == tid && g.RevokedAt == nil {
			out = append(out, g)
		}
	}
	return out, nil
}
func (d *doorSvcStore) OpenDoorGrant(_ context.Context, g store.DoorGrant) (store.DoorGrant, bool, error) {
	d.grants = append(d.grants, g)
	return g, true, nil
}
func (d *doorSvcStore) RevokeDoorGrant(context.Context, string, string, string) (store.DoorGrant, error) {
	return store.DoorGrant{}, store.ErrNotFound
}
func (d *doorSvcStore) RevokeDoorGrantsByIP(context.Context, string, string, string) (int, error) {
	return 0, nil
}
func (d *doorSvcStore) RecordDoorRevocation(context.Context, string, string, time.Time) error {
	return nil
}
func (d *doorSvcStore) ListDoorRevocations(context.Context, string) (map[string]time.Time, error) {
	return nil, nil
}
func (d *doorSvcStore) CloseDoorsForTenant(_ context.Context, tenant string, at time.Time) ([]string, []store.DoorGrant, error) {
	var ids []string
	for i := range d.doors {
		if d.doors[i].TenantID == tenant && d.doors[i].Enabled {
			d.doors[i].Enabled, d.doors[i].PlanClosed = false, true
			ids = append(ids, d.doors[i].TunnelID)
		}
	}
	var rv []store.DoorGrant
	for i := range d.grants {
		if d.grants[i].TenantID == tenant && d.grants[i].RevokedAt == nil {
			t := at
			d.grants[i].RevokedAt = &t
			rv = append(rv, d.grants[i])
		}
	}
	return ids, rv, nil
}

// Plan Pro altina dusunce rawproxy artik varsayilan-red uygulamaz: kapi hic acilmamis
// gibi (statik liste yok -> acik) davranir; kapatma ayrica kalici yapilir.
func TestAdmitAfterPlanDowngrade(t *testing.T) {
	st := &doorSvcStore{doors: []store.TunnelDoor{{TunnelID: "tun", TenantID: "ten", Enabled: true}}}
	svc := door.New(st, nil, nil)
	pro := true
	svc.Gate = func(context.Context, string) error {
		if pro {
			return nil
		}
		return door.ErrNotAvailable
	}
	var cut []string
	svc.OnRevoke = func(tid, ip string) { cut = append(cut, tid+"|"+ip) }
	m := newDoorMgr(t, svc)
	ctx := context.Background()

	if ok, _ := m.admit(ctx, ti1, "203.0.113.7"); ok {
		t.Fatal("Pro planda grant'siz IP reddedilmeli")
	}
	if _, _, err := svc.Open(ctx, door.Target{TenantID: "ten", TunnelID: "tun", DurationSec: 3600}, "198.51.100.1", "alice", "basic", ""); err != nil {
		t.Fatal(err)
	}
	if ok, exp := m.admit(ctx, ti1, "198.51.100.1"); !ok || exp.IsZero() {
		t.Fatal("grant'li IP gecmeli")
	}

	// Savunma yolu: plan dustu ama henuz kalici kapatma calismadi (onbellek temizlenir).
	pro = false
	svc.Changed("tun")
	if ok, exp := m.admit(ctx, ti1, "203.0.113.7"); !ok || !exp.IsZero() {
		t.Fatal("plan yokken kapi yokmus gibi acik olmali (varsayilan-red yok)")
	}

	// Kalici kapatma: grant iptal, acik baglantilar kesilir, kapi listeden duser.
	pro = true
	svc.Changed("tun")
	pro = false
	if n, err := svc.EnforcePlan(ctx, "ten"); err != nil || n != 1 {
		t.Fatalf("EnforcePlan: %d %v", n, err)
	}
	if len(cut) != 1 || cut[0] != "tun|198.51.100.1" {
		t.Fatalf("grant iptalinde baglantilar kesilmeli: %v", cut)
	}
	if svc.DoorEnabled("tun") {
		t.Fatal("kapali kapi etkin gorunmemeli")
	}
	if ok, exp := m.admit(ctx, ti1, "203.0.113.7"); !ok || !exp.IsZero() {
		t.Fatal("kalici kapanmadan sonra da varsayilan-red olmamali")
	}
}
