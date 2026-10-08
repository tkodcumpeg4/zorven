// Package door, ham TCP/UDP tunelleri icin "web ile kapi acma" (web door / knock)
// mantigini tasir.
//
// Akis: ziyaretci tunelin kapi adresinde (door--<ad>--<kiraci>.<platform>)
// Basic/OAuth ile giris yapar; basarida kendi IP'si icin sureli bir grant olusur.
// rawproxy, statik IP izin listesi izin vermiyorsa grant'e bakar.
//
// Service iki seyi bellekte tutar (sicak yol veritabanina gitmesin diye):
//   - kapisi etkin tunellerin anlik goruntusu (kisa TTL, degisiklikte hemen tazelenir)
//   - tunel basina aktif grant'ler (kisa TTL, olusturma/iptalde hemen gecersiz kilinir)
//
// Grant'siz reddedilen baglanti/akislar sayilir ve (tunel, ip, dakika) basina
// TEK olay olarak toplu yazilir; saldiri/tarama trafigi tabloyu sisirmez.
package door

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// ErrNotAvailable, kiracinin plani bu ozellige izin vermiyorsa Open'dan doner.
var ErrNotAvailable = errors.New("web door bu planda kullanilamiyor")

const (
	defaultDoorTTL  = 30 * time.Second
	defaultGrantTTL = 10 * time.Second
	// maxBlockedKeys, bellekteki (tunel, ip, dakika) toplayici anahtar siniri.
	// Asilirsa yeni IP'ler tunel basina tek "diger" satirinda toplanir.
	maxBlockedKeys = 20000
)

// Store, Service'in ihtiyac duydugu depo yuzeyi.
type Store interface {
	ListEnabledDoors(ctx context.Context) ([]store.TunnelDoor, error)
	ListActiveDoorGrantsByTunnel(ctx context.Context, tunnelID string) ([]store.DoorGrant, error)
	OpenDoorGrant(ctx context.Context, g store.DoorGrant) (store.DoorGrant, bool, error)
	RevokeDoorGrant(ctx context.Context, tenantID, tunnelID, grantID string) (store.DoorGrant, error)
	RevokeDoorGrantsByIP(ctx context.Context, tenantID, tunnelID, ip string) (int, error)
	RecordDoorRevocation(ctx context.Context, tunnelID, identity string, at time.Time) error
	ListDoorRevocations(ctx context.Context, tunnelID string) (map[string]time.Time, error)
	CloseDoorsForTenant(ctx context.Context, tenantID string, at time.Time) ([]string, []store.DoorGrant, error)
}

// Target, kapisi acilan tunelin ingress'ten gelen bilgisi.
type Target struct {
	TenantID    string
	TunnelID    string
	Host        string // kapi hostname'i (olay kaydi icin)
	DurationSec int
}

type doorInfo struct{ tenantID string }

type planEntry struct {
	ok bool
	at time.Time
}

type revCache struct {
	at time.Time
	m  map[string]time.Time // kucuk harfli kimlik -> son iptal
}

type grantCache struct {
	at   time.Time
	byIP map[string]time.Time // ip -> bitis
}

type blockKey struct {
	tunnelID string
	ip       string
	minute   int64 // unix dakika
}

type blockedAgg struct {
	tenantID string
	n        int
}

// Service, kapi durumunu ve grant onbellegini yonetir.
type Service struct {
	St     Store
	Events *accesslog.Persister // nil olabilir
	Log    *slog.Logger         // nil olabilir
	// Gate, kiracinin plani web-door'a izin veriyor mu (nil = her zaman izinli).
	Gate func(ctx context.Context, tenantID string) error
	// OnRevoke, bir (tunel, ip) icin grant iptal/kapatildiginda cagrilir; rawproxy
	// o IP'nin acik baglantilarini keser. nil olabilir.
	OnRevoke func(tunnelID, ip string)
	// OnChange, plan nedeniyle kapilar kalici kapatildiginda cagrilir (router/rawproxy
	// yeniden yuklensin diye). nil olabilir.
	OnChange func()

	DoorTTL  time.Duration
	GrantTTL time.Duration
	Now      func() time.Time

	mu       sync.Mutex
	doors    map[string]doorInfo
	doorsAt  time.Time
	doorsOK  bool
	grants   map[string]*grantCache
	blocked  map[blockKey]*blockedAgg
	revs     map[string]*revCache // tunel -> oturum iptalleri
	plans    map[string]planEntry // kiraci -> plan kapisi sonucu
}

// New, Service uretir. Run ayrica baslatilmalidir.
func New(st Store, ev *accesslog.Persister, log *slog.Logger) *Service {
	return &Service{
		St: st, Events: ev, Log: log,
		DoorTTL: defaultDoorTTL, GrantTTL: defaultGrantTTL,
		Now:     time.Now,
		grants:  map[string]*grantCache{},
		blocked: map[blockKey]*blockedAgg{},
		revs:    map[string]*revCache{},
		plans:   map[string]planEntry{},
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logWarn(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, args...)
	}
}

// NormalizeIP, IP'yi karsilastirilabilir tek bicime getirir (IPv4-mapped IPv6 -> IPv4).
// Gecersizse bos doner.
func NormalizeIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	raw = strings.Trim(raw, "[]")
	// Bolge (zone) eki (fe80::1%eth0) at.
	if i := strings.IndexByte(raw, '%'); i >= 0 {
		raw = raw[:i]
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

// --- kapi goruntusu ----------------------------------------------------------

// Refresh, kapisi etkin tunellerin goruntusunu veritabanindan yeniler. Hata olursa
// onceki goruntu korunur (false doner).
func (s *Service) Refresh(ctx context.Context) bool {
	list, err := s.St.ListEnabledDoors(ctx)
	if err != nil {
		s.logWarn("door: kapi listesi yenilenemedi", "hata", err)
		return false
	}
	next := make(map[string]doorInfo, len(list))
	for _, d := range list {
		if d.Enabled {
			next[d.TunnelID] = doorInfo{tenantID: d.TenantID}
		}
	}
	s.mu.Lock()
	s.doors, s.doorsAt, s.doorsOK = next, s.now(), true
	s.mu.Unlock()
	return true
}

// DoorEnabled, tunelin kapisi etkin mi. Goruntu bayatsa (TTL) bir kez yenilenir;
// yenileme basarisizsa onceki goruntu kullanilir.
func (s *Service) DoorEnabled(tunnelID string) bool {
	s.mu.Lock()
	stale := !s.doorsOK || s.now().Sub(s.doorsAt) > s.doorTTL()
	s.mu.Unlock()
	if stale {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ok := s.Refresh(ctx)
		cancel()
		if !ok {
			// Basarisiz yenileme her baglantida DB'yi dovmesin: bir sonraki deneme TTL sonra.
			s.mu.Lock()
			s.doorsAt = s.now()
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	info, ok := s.doors[tunnelID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	// Savunma: kiracinin plani artik izin vermiyorsa kapi hic etkinlestirilmemis gibi
	// davranilir (statik IP listesi / kapisiz). Kalici kapatma bunu zaten yapar; bu,
	// plan degisikligi ile kapatma arasindaki boslugu kapatir.
	return s.PlanAllowed(info.tenantID)
}

// PlanAllowed, kiracinin plani web kapiya izin veriyor mu. Sonuc kisa sureli
// onbelleklenir (Changed/EnforcePlan temizler). Gate gecici hata verirse (plan
// reddi degil) onceki bilinen deger korunur; hic yoksa izinli sayilir.
func (s *Service) PlanAllowed(tenantID string) bool {
	if s.Gate == nil {
		return true
	}
	now := s.now()
	s.mu.Lock()
	e, have := s.plans[tenantID]
	s.mu.Unlock()
	if have && now.Sub(e.at) <= s.doorTTL() {
		return e.ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := s.Gate(ctx, tenantID)
	cancel()
	ok := err == nil
	if err != nil && !errors.Is(err, ErrNotAvailable) {
		// Gecici hata: plan reddi sayma.
		s.logWarn("door: plan kapisi okunamadi", "tenant", tenantID, "hata", err)
		ok = !have || e.ok
	}
	s.mu.Lock()
	s.plans[tenantID] = planEntry{ok: ok, at: now}
	s.mu.Unlock()
	return ok
}

func (s *Service) doorTTL() time.Duration {
	if s.DoorTTL > 0 {
		return s.DoorTTL
	}
	return defaultDoorTTL
}

func (s *Service) grantTTL() time.Duration {
	if s.GrantTTL > 0 {
		return s.GrantTTL
	}
	return defaultGrantTTL
}

// Invalidate, tunelin grant onbellegini hemen gecersiz kilar (grant olustu/iptal edildi).
func (s *Service) Invalidate(tunnelID string) {
	s.mu.Lock()
	delete(s.grants, tunnelID)
	s.mu.Unlock()
}

// Changed, tunelin kapi ayari (acik/kapali) degistiginde cagrilir: hem kapi
// goruntusu hem grant onbellegi hemen gecersiz kilinir.
func (s *Service) Changed(tunnelID string) {
	s.mu.Lock()
	delete(s.grants, tunnelID)
	delete(s.revs, tunnelID)
	s.plans = map[string]planEntry{}
	s.doorsOK = false
	s.mu.Unlock()
}

// --- oturum iptali -----------------------------------------------------------

// NormIdentity, iptal kaydi icin kimligi tek bicime getirir (kucuk harf, kirpilmis).
func NormIdentity(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// RecordRevocation, (tunel, kimlik) icin "su andan once verilmis oturumlar gecersiz"
// kaydini yazar. Kimlik bos ise bir sey yapmaz.
func (s *Service) RecordRevocation(ctx context.Context, tunnelID, identity string) error {
	id := NormIdentity(identity)
	if id == "" {
		return nil
	}
	err := s.St.RecordDoorRevocation(ctx, tunnelID, id, s.now().UTC())
	s.mu.Lock()
	delete(s.revs, tunnelID)
	s.mu.Unlock()
	if err != nil {
		s.logWarn("door: oturum iptali yazilamadi", "tunnel", tunnelID, "hata", err)
	}
	return err
}

// SessionRevoked, (tunel, kimlik) icin verilis zamani issued olan oturum iptal
// edilmis mi (issued <= son iptal). Okunamazsa fail-closed (true): giris sayfasi gosterilir.
func (s *Service) SessionRevoked(tunnelID, identity string, issued time.Time) bool {
	id := NormIdentity(identity)
	if id == "" {
		return false
	}
	now := s.now()
	s.mu.Lock()
	rc := s.revs[tunnelID]
	s.mu.Unlock()
	if rc == nil || now.Sub(rc.at) > s.grantTTL() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		m, err := s.St.ListDoorRevocations(ctx, tunnelID)
		cancel()
		if err != nil {
			s.logWarn("door: oturum iptalleri okunamadi", "tunnel", tunnelID, "hata", err)
			return true
		}
		rc = &revCache{at: now, m: m}
		s.mu.Lock()
		s.revs[tunnelID] = rc
		s.mu.Unlock()
	}
	at, ok := rc.m[id]
	return ok && !issued.After(at)
}

// --- grant sorgulama ---------------------------------------------------------

// HasGrant, (tunel, ip) icin suresi dolmamis bir grant var mi; varsa bitis zamani.
// Tunel basina TEK sorguyla tum aktif grant'ler onbelleklenir: kaynak adres sayisi
// ne olursa olsun (UDP seli dahil) veritabanina istek sayisi TTL ile sinirlidir.
func (s *Service) HasGrant(tunnelID, ip string) (time.Time, bool) {
	ip = NormalizeIP(ip)
	if ip == "" {
		return time.Time{}, false
	}
	now := s.now()
	s.mu.Lock()
	gc := s.grants[tunnelID]
	s.mu.Unlock()
	if gc == nil || now.Sub(gc.at) > s.grantTTL() {
		if fresh := s.loadGrants(tunnelID, now); fresh != nil {
			gc = fresh
		}
		// Yukleme basarisiz ve onbellek yoksa gc nil: reddet (fail-closed).
	}
	if gc == nil {
		return time.Time{}, false
	}
	exp, ok := gc.byIP[ip]
	if !ok || !exp.After(now) {
		return time.Time{}, false
	}
	return exp, true
}

func (s *Service) loadGrants(tunnelID string, now time.Time) *grantCache {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	list, err := s.St.ListActiveDoorGrantsByTunnel(ctx, tunnelID)
	if err != nil {
		s.logWarn("door: aktif izinler okunamadi", "tunnel", tunnelID, "hata", err)
		return nil
	}
	gc := &grantCache{at: now, byIP: make(map[string]time.Time, len(list))}
	for _, g := range list {
		if g.RevokedAt != nil {
			continue
		}
		ip := NormalizeIP(g.IP)
		if ip == "" {
			continue
		}
		if cur, ok := gc.byIP[ip]; !ok || g.ExpiresAt.After(cur) {
			gc.byIP[ip] = g.ExpiresAt
		}
	}
	s.mu.Lock()
	s.grants[tunnelID] = gc
	s.mu.Unlock()
	return gc
}

// --- grant olusturma / iptal -------------------------------------------------

// Open, (tunel, ip) icin grant acar. Aktif grant varsa onu aynen doner (sure
// uzatmaz); created=false. Plan kapisi (Gate) basarisizsa ErrNotAvailable.
func (s *Service) Open(ctx context.Context, t Target, ip, identity, method, userAgent string) (store.DoorGrant, bool, error) {
	ip = NormalizeIP(ip)
	if ip == "" {
		return store.DoorGrant{}, false, errors.New("gecersiz istemci ip adresi")
	}
	if s.Gate != nil {
		if err := s.Gate(ctx, t.TenantID); err != nil {
			return store.DoorGrant{}, false, ErrNotAvailable
		}
	}
	dur := time.Duration(t.DurationSec) * time.Second
	if dur <= 0 {
		dur = time.Duration(store.DoorDuration12h) * time.Second
	}
	now := s.now().UTC()
	g, created, err := s.St.OpenDoorGrant(ctx, store.DoorGrant{
		TenantID: t.TenantID, TunnelID: t.TunnelID, IP: ip,
		Identity: identity, Method: method, ExpiresAt: now.Add(dur), CreatedAt: now,
	})
	if err != nil {
		return store.DoorGrant{}, false, err
	}
	s.Invalidate(t.TunnelID)
	if created {
		s.emit(accesslog.Event{
			TenantID: t.TenantID, TunnelID: t.TunnelID, Hostname: t.Host,
			Method: method, Identity: identity, Success: true,
			Reason: accesslog.ReasonDoorOpened, ClientIP: ip, UserAgent: userAgent,
		})
	}
	return g, created, nil
}

// Close, (tunel, ip) icin aktif tum grant'leri iptal eder ve acik baglantilari keser.
// Iptal edilen grant sayisini doner.
func (s *Service) Close(ctx context.Context, t Target, ip, identity, method, userAgent string) (int, error) {
	ip = NormalizeIP(ip)
	if ip == "" {
		return 0, errors.New("gecersiz istemci ip adresi")
	}
	n, err := s.St.RevokeDoorGrantsByIP(ctx, t.TenantID, t.TunnelID, ip)
	if err != nil {
		return 0, err
	}
	s.Invalidate(t.TunnelID)
	// Ziyaretci kapattiysa kopyalanmis/eski oturum cerezi kapiyi yeniden acamasin.
	_ = s.RecordRevocation(ctx, t.TunnelID, identity)
	if n > 0 {
		if s.OnRevoke != nil {
			s.OnRevoke(t.TunnelID, ip)
		}
		s.emit(accesslog.Event{
			TenantID: t.TenantID, TunnelID: t.TunnelID, Hostname: t.Host,
			Method: method, Identity: identity, Success: true,
			Reason: accesslog.ReasonDoorClosed, ClientIP: ip, UserAgent: userAgent,
		})
	}
	return n, nil
}

// Revoke, tek grant'i (panelden) iptal eder.
func (s *Service) Revoke(ctx context.Context, tenantID, tunnelID, grantID string) (store.DoorGrant, error) {
	g, err := s.St.RevokeDoorGrant(ctx, tenantID, tunnelID, grantID)
	if err != nil {
		return store.DoorGrant{}, err
	}
	s.Invalidate(tunnelID)
	// Kisinin giris oturumu da dusurulur: eski cerezle kapi yeniden acilamaz.
	_ = s.RecordRevocation(ctx, tunnelID, g.Identity)
	if s.OnRevoke != nil {
		s.OnRevoke(tunnelID, NormalizeIP(g.IP))
	}
	s.emit(accesslog.Event{
		TenantID: tenantID, TunnelID: tunnelID,
		Method: g.Method, Identity: g.Identity, Success: true,
		Reason: accesslog.ReasonDoorClosed, ClientIP: g.IP,
	})
	return g, nil
}

func (s *Service) emit(e accesslog.Event) {
	if s.Events != nil {
		s.Events.Enqueue(e)
	}
}

// --- reddedilenlerin toplulastirilmasi --------------------------------------

// NoteBlocked, grant'siz reddedilen bir baglanti/akisi sayar. Bellekte (tunel,
// ip, dakika) basina toplanir; yazim FlushBlocked'tadir.
func (s *Service) NoteBlocked(tenantID, tunnelID, ip string) {
	ip = NormalizeIP(ip)
	k := blockKey{tunnelID: tunnelID, ip: ip, minute: s.now().Unix() / 60}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.blocked[k]; ok {
		a.n++
		return
	}
	if len(s.blocked) >= maxBlockedKeys {
		// Anahtar siniri: yeni IP'ler tunel basina tek "diger" satirina (ip="") toplanir.
		k.ip = ""
		if a, ok := s.blocked[k]; ok {
			a.n++
			return
		}
	}
	s.blocked[k] = &blockedAgg{tenantID: tenantID, n: 1}
}

// FlushBlocked, biten dakikalarin toplayicilarini olay olarak yazar. all=true ise
// icinde bulunulan dakika da yazilir (kapanista).
func (s *Service) FlushBlocked(all bool) int {
	cur := s.now().Unix() / 60
	type item struct {
		k blockKey
		a *blockedAgg
	}
	var out []item
	s.mu.Lock()
	for k, a := range s.blocked {
		if all || k.minute < cur {
			out = append(out, item{k, a})
			delete(s.blocked, k)
		}
	}
	s.mu.Unlock()
	for _, it := range out {
		s.emit(accesslog.Event{
			TenantID: it.a.tenantID, TunnelID: it.k.tunnelID,
			Method: accesslog.MethodDoor, Success: false,
			Reason: accesslog.ReasonBlockedNoGrant, ClientIP: it.k.ip,
			CreatedAt: time.Unix(it.k.minute*60, 0).UTC(), Count: it.a.n,
		})
	}
	return len(out)
}

// --- plan dususu -------------------------------------------------------------

// EnforcePlan, kiracinin plani web kapiya izin vermiyorsa tum kapilarini kalici
// kapatir (enabled=false), aktif grant'lerini iptal eder (acik baglantilar kesilir)
// ve door_closed_plan olaylari yazar. Plan izin veriyorsa hicbir sey yapmaz.
// Kapatilan kapi sayisini doner. Plan degisikligi uygulanan HER yerden cagrilmalidir.
func (s *Service) EnforcePlan(ctx context.Context, tenantID string) (int, error) {
	if s.Gate == nil || tenantID == "" {
		return 0, nil
	}
	s.mu.Lock()
	delete(s.plans, tenantID)
	s.mu.Unlock()
	if err := s.Gate(ctx, tenantID); err == nil || !errors.Is(err, ErrNotAvailable) {
		// Izinli ya da belirsiz (gecici hata): dokunma.
		return 0, nil
	}
	tunnels, grants, err := s.St.CloseDoorsForTenant(ctx, tenantID, s.now().UTC())
	if err != nil {
		s.logWarn("door: plan dususunde kapilar kapatilamadi", "tenant", tenantID, "hata", err)
		return 0, err
	}
	s.mu.Lock()
	s.plans[tenantID] = planEntry{ok: false, at: s.now()}
	s.mu.Unlock()
	withGrant := map[string]bool{}
	for _, g := range grants {
		withGrant[g.TunnelID] = true
		s.Invalidate(g.TunnelID)
		if s.OnRevoke != nil {
			s.OnRevoke(g.TunnelID, NormalizeIP(g.IP))
		}
		s.emit(accesslog.Event{
			TenantID: tenantID, TunnelID: g.TunnelID,
			Method: g.Method, Identity: g.Identity, Success: true,
			Reason: accesslog.ReasonDoorClosedPlan, ClientIP: g.IP,
		})
	}
	for _, id := range tunnels {
		s.Changed(id)
		if !withGrant[id] {
			s.emit(accesslog.Event{
				TenantID: tenantID, TunnelID: id, Method: accesslog.MethodDoor, Success: true,
				Reason: accesslog.ReasonDoorClosedPlan,
			})
		}
	}
	if len(tunnels) > 0 || len(grants) > 0 {
		s.mu.Lock()
		s.doorsOK = false
		s.mu.Unlock()
		if s.OnChange != nil {
			s.OnChange()
		}
	}
	return len(tunnels), nil
}

// EnforceAllPlans, etkin kapisi olan tum kiracilar icin EnforcePlan calistirir
// (deneme bitisi gibi tek tek bildirilmeyen plan dususleri icin guvenlik agi).
func (s *Service) EnforceAllPlans(ctx context.Context) int {
	list, err := s.St.ListEnabledDoors(ctx)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	total := 0
	for _, d := range list {
		if !d.Enabled || d.TenantID == "" || seen[d.TenantID] {
			continue
		}
		seen[d.TenantID] = true
		n, _ := s.EnforcePlan(ctx, d.TenantID)
		total += n
	}
	return total
}

// Run, kapi goruntusunu ve reddedilen sayaclarini periyodik isler; ctx bitince son bosaltmayi yapar.
func (s *Service) Run(ctx context.Context) {
	s.Refresh(ctx)
	s.EnforceAllPlans(ctx)
	tk := time.NewTicker(20 * time.Second)
	defer tk.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			s.FlushBlocked(true)
			return
		case <-tk.C:
			s.FlushBlocked(false)
			// Guvenlik agi: ~2 dakikada bir plan dususlerini tara (deneme bitisi vb.).
			if ticks++; ticks%6 == 0 {
				s.EnforceAllPlans(ctx)
			}
		}
	}
}
