package ingress

// Z2 eksigi: acik ziyaretci WebSocket'lerinin yeniden degerlendirilmesi.
//
// Ziyaretci OAuth/Basic oturumlari ve tunel erisim ayari ZATEN her HTTP isteginde
// tunelin GUNCEL ayarina gore degerlendirilir (enforceAccess; imzali cerez
// durumsuzdur). Uzun omurlu bir WebSocket ise yalniz YUKSELTME aninda denetlendiginden
// ayar degisse de (e-posta izin listesi, erisim modu, Geo, IP listesi, tunel
// pasif/dondurulmus, oturum iptali) acik kalirdi. Burada acik WS'ler kaydedilir ve
// olay noktalarinda (api.OnAuthzChange) + periyodik turda ayni denetimden yeniden
// gecirilir; gecemeyen baglanti kapatilir.

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type wsEntry struct {
	tunnelID, tenantID, ip, host, path string
	r                                  *http.Request // yukseltme istegi (cerez/basliklar)
	closeFn                            func()
}

type wsRegistry struct {
	mu   sync.Mutex
	next uint64
	m    map[uint64]*wsEntry
}

func (reg *wsRegistry) add(e *wsEntry) (remove func()) {
	reg.mu.Lock()
	if reg.m == nil {
		reg.m = map[uint64]*wsEntry{}
	}
	reg.next++
	id := reg.next
	reg.m[id] = e
	reg.mu.Unlock()
	return func() {
		reg.mu.Lock()
		delete(reg.m, id)
		reg.mu.Unlock()
	}
}

func (reg *wsRegistry) snapshot(tenantID string) []*wsEntry {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]*wsEntry, 0, len(reg.m))
	for _, e := range reg.m {
		if tenantID == "" || e.tenantID == tenantID {
			out = append(out, e)
		}
	}
	return out
}

// registerWS, acik bir WebSocket'i yeniden degerlendirme icin kaydeder.
func (h *Handler) registerWS(r *http.Request, tunnelID, tenantID string, closeFn func()) (remove func()) {
	return h.wsLive.add(&wsEntry{
		tunnelID: tunnelID, tenantID: tenantID, ip: clientIP(r),
		host: r.Host, path: r.URL.Path,
		r:       r.Clone(context.Background()),
		closeFn: closeFn,
	})
}

// LiveWebSockets, kayitli acik WebSocket sayisi (tenantID bos = hepsi).
func (h *Handler) LiveWebSockets(tenantID string) int { return len(h.wsLive.snapshot(tenantID)) }

// nullWriter, yeniden degerlendirmede erisim denetiminin yanit yazmasini yutar.
type nullWriter struct{ hdr http.Header }

func (n *nullWriter) Header() http.Header {
	if n.hdr == nil {
		n.hdr = http.Header{}
	}
	return n.hdr
}
func (n *nullWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *nullWriter) WriteHeader(int)             {}

// ReevaluateVisitors, kiracinin (bos = tum kiracilar) acik WebSocket'lerini guncel
// tunel ayarina gore yeniden degerlendirir; gecemeyenleri kapatir ve sayisini doner.
func (h *Handler) ReevaluateVisitors(tenantID string) int {
	if h.Router == nil {
		return 0
	}
	closed := 0
	for _, e := range h.wsLive.snapshot(tenantID) {
		reason := h.wsDenyReason(e)
		if reason == "" {
			continue
		}
		e.closeFn()
		closed++
		if h.Log != nil {
			h.Log.Info("acik WebSocket yeniden degerlendirmede kapatildi",
				"tunel", e.tunnelID, "ip", e.ip, "neden", reason)
		}
	}
	return closed
}

func (h *Handler) wsDenyReason(e *wsEntry) string {
	tun, ok := h.Router.LookupPath(e.host, e.path)
	if !ok || tun.TunnelID != e.tunnelID || !tun.Enabled || tun.Frozen || tun.Door != nil {
		return "tunnel_unavailable"
	}
	if sec := h.Router.SecurityFor(tun.TunnelID); sec.GeoActive() {
		if allowed, _ := sec.GeoCheck(h.Router, e.ip); !allowed {
			return "geo"
		}
	}
	if h.IPFilter != nil && tun.TenantID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		allowed, err := h.IPFilter.CheckAllowed(ctx, tun.TenantID, tun.TunnelID, e.ip)
		cancel()
		if err != nil || !allowed {
			return "ip_forbidden"
		}
	}
	if !h.enforceAccess(&nullWriter{}, e.r, tun) {
		return "access_revoked"
	}
	return ""
}

// RunVisitorReevaluator, zamana bagli degisiklikler (oturum suresi dolumu, IP listesi,
// Geo DB) icin acik WebSocket'leri periyodik tarar. Bloklar; ctx ile durur.
func (h *Handler) RunVisitorReevaluator(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			h.ReevaluateVisitors("")
		}
	}
}
