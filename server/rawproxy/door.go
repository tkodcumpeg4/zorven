package rawproxy

import (
	"context"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/door"
)

// Web ile kapi acma (web door). Bir tunelin kapisi etkinse ham TCP/SNI/UDP
// baglantisi su iki yoldan biriyle kabul edilir:
//
//  1. statik IP izin listesi (varsa) izin veriyorsa,
//  2. bu tunel + kaynak IP icin suresi dolmamis bir grant varsa.
//
// Kapi etkinken statik liste yoksa varsayilan REDDETTIR (grant sart). Grant'siz
// reddedilenler DoorGate.NoteBlocked ile sayilir (yazim dakikada bir, toplu).

// DoorGate, rawproxy'nin kapi durumunu sordugu yuzeydir (door.Service uygular).
type DoorGate interface {
	// DoorEnabled, tunelin kapisi etkin mi (bellekten, ucuz).
	DoorEnabled(tunnelID string) bool
	// HasGrant, (tunel, ip) icin suresi dolmamis grant var mi; varsa bitisi.
	HasGrant(tunnelID, ip string) (time.Time, bool)
	// NoteBlocked, grant'siz reddedilen bir baglanti/akisi sayar.
	NoteBlocked(tenantID, tunnelID, ip string)
}

// admit, (tunel, kaynak ip) icin yeni bir baglanti/akisin kabul edilip edilmeyecegini
// soyler. grantExp sifir degilse kabul YALNIZCA grant sayesindedir ve baglanti
// bu ana kadar yasayabilir (sure dolunca kesilmelidir).
func (m *Manager) admit(ctx context.Context, ti TunnelInfo, ip string) (ok bool, grantExp time.Time) {
	doorOn := m.Door != nil && m.Door.DoorEnabled(ti.TunnelID)

	if !doorOn {
		// Eski davranis: statik liste varsa ve IP listede degilse reddet.
		if m.IPFilter != nil && ti.TenantID != "" {
			if allowed, err := m.IPFilter.CheckAllowed(ctx, ti.TenantID, ti.TunnelID, ip); err == nil && !allowed {
				return false, time.Time{}
			}
		}
		return true, time.Time{}
	}

	// Kapi etkin: statik liste izin veriyorsa gec.
	if m.IPFilter != nil && ti.TenantID != "" && m.IPFilter.HasRules(ctx, ti.TenantID, ti.TunnelID) {
		if allowed, err := m.IPFilter.CheckAllowed(ctx, ti.TenantID, ti.TunnelID, ip); err == nil && allowed {
			return true, time.Time{}
		}
	}
	// Aksi halde grant sart.
	if exp, has := m.Door.HasGrant(ti.TunnelID, ip); has {
		return true, exp
	}
	m.Door.NoteBlocked(ti.TenantID, ti.TunnelID, ip)
	return false, time.Time{}
}

// connRegistry, grant ile kabul edilmis acik baglanti/akislarin kapatici
// fonksiyonlarini (tunel, ip) anahtariyla tutar; grant iptalinde hepsi kesilir.
type connRegistry struct {
	mu   sync.Mutex
	next uint64
	m    map[string]map[uint64]func()
}

func regKey(tunnelID, ip string) string { return tunnelID + "|" + ip }

// add, kapaticiyi kaydeder; donen fonksiyon kaydi siler.
func (r *connRegistry) add(tunnelID, ip string, closeFn func()) (remove func()) {
	k := regKey(tunnelID, door.NormalizeIP(ip))
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]map[uint64]func(){}
	}
	r.next++
	id := r.next
	if r.m[k] == nil {
		r.m[k] = map[uint64]func(){}
	}
	r.m[k][id] = closeFn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if set := r.m[k]; set != nil {
			delete(set, id)
			if len(set) == 0 {
				delete(r.m, k)
			}
		}
		r.mu.Unlock()
	}
}

// closeAll, (tunel, ip) icin kayitli tum kapaticilari calistirir.
func (r *connRegistry) closeAll(tunnelID, ip string) int {
	k := regKey(tunnelID, door.NormalizeIP(ip))
	r.mu.Lock()
	set := r.m[k]
	fns := make([]func(), 0, len(set))
	for _, f := range set {
		fns = append(fns, f)
	}
	delete(r.m, k)
	r.mu.Unlock()
	for _, f := range fns {
		f()
	}
	return len(fns)
}

// CloseDoorConns, bir grant iptal edildiginde o (tunel, ip)'nin acik TCP
// baglantilarini ve UDP flow'larini keser. Kesilen sayiyi doner.
func (m *Manager) CloseDoorConns(tunnelID, ip string) int {
	return m.doorConns.closeAll(tunnelID, ip)
}
