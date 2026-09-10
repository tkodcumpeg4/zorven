package ipfilter

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type compiledRule struct {
	RuleID      string
	TunnelID    string // bos ise tenant-wide
	IPNet       *net.IPNet
	Description string
}

// Engine, ingress trafigi icin yuksek performansli bellek-ici CIDR denetleyicisi.
type Engine struct {
	st  store.Store
	ent entitlements.EntitlementService

	mu    sync.RWMutex
	rules map[string][]compiledRule // tenantID -> kurallar
}

func NewEngine(st store.Store, ent entitlements.EntitlementService) *Engine {
	return &Engine{
		st:    st,
		ent:   ent,
		rules: make(map[string][]compiledRule),
	}
}

// Reload, aktif tum IP kurallarini veritabanindan okuyup bellege derler.
func (e *Engine) Reload(ctx context.Context) error {
	allRules, err := e.st.ListAllActiveIPRules(ctx)
	if err != nil {
		return fmt.Errorf("ip kurallari yuklenemedi: %w", err)
	}

	next := make(map[string][]compiledRule)
	for _, r := range allRules {
		if !r.Enabled {
			continue
		}
		_, ipNet, err := ParseCIDR(r.CIDR)
		if err != nil {
			continue
		}
		tid := ""
		if r.TunnelID != nil {
			tid = *r.TunnelID
		}
		cr := compiledRule{
			RuleID:      r.ID,
			TunnelID:    tid,
			IPNet:       ipNet,
			Description: r.Description,
		}
		next[r.TenantID] = append(next[r.TenantID], cr)
	}

	e.mu.Lock()
	e.rules = next
	e.mu.Unlock()
	return nil
}

// InvalidateTenant, belirli bir kiracinin kurallarini veritabanindan yeniden okur.
func (e *Engine) InvalidateTenant(ctx context.Context, tenantID string) error {
	rules, err := e.st.ListIPRules(ctx, tenantID, nil)
	if err != nil {
		return err
	}

	var compiled []compiledRule
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		_, ipNet, err := ParseCIDR(r.CIDR)
		if err != nil {
			continue
		}
		tid := ""
		if r.TunnelID != nil {
			tid = *r.TunnelID
		}
		compiled = append(compiled, compiledRule{
			RuleID:      r.ID,
			TunnelID:    tid,
			IPNet:       ipNet,
			Description: r.Description,
		})
	}

	e.mu.Lock()
	if len(compiled) == 0 {
		delete(e.rules, tenantID)
	} else {
		e.rules[tenantID] = compiled
	}
	e.mu.Unlock()
	return nil
}

// CheckAllowed, gelen istegin IP adresinin tünel izin listesine uygun olup olmadığını kontrol eder.
//
// Dönüş kuralları:
// - Kiracının planında FeatureIPAllowlist yoksa -> true (engelleme yapılmaz)
// - Kiracı/Tünel için tanımlı aktif kural yoksa -> true (varsayılan açık)
// - Kural tanımlıysa: clientIP en az bir kural ile eşleşirse -> true, aksi halde -> false
func (e *Engine) CheckAllowed(ctx context.Context, tenantID, tunnelID, clientIPStr string) (bool, error) {
	if e.ent != nil {
		if err := e.ent.CheckFeature(ctx, tenantID, entitlements.FeatureIPAllowlist); err != nil {
			// Plana gore ozellik acik degilse ingress filtrelemesi bypass edilir
			return true, nil
		}
	}

	e.mu.RLock()
	tenantRules, exists := e.rules[tenantID]
	e.mu.RUnlock()

	if !exists || len(tenantRules) == 0 {
		return true, nil // Tanimli kural yok, gecise izin ver
	}

	// Tünel bazlı veya tenant-wide kuralları filtrele
	var relevant []compiledRule
	for _, r := range tenantRules {
		if r.TunnelID == "" || r.TunnelID == tunnelID {
			relevant = append(relevant, r)
		}
	}

	// Bu tüneli kapsayan aktif kural yoksa açık
	if len(relevant) == 0 {
		return true, nil
	}

	ip := net.ParseIP(strings.TrimSpace(clientIPStr))
	if ip == nil {
		return false, fmt.Errorf("gecersiz istemci ip adresi: %s", clientIPStr)
	}

	// Kurallardan en az biriyle eslesmesi yeterlidir
	for _, r := range relevant {
		if r.IPNet.Contains(ip) {
			return true, nil
		}
	}

	return false, nil
}

// ParseCIDR, tek IP veya CIDR girdisini standart *net.IPNet yapisina cevirir.
func ParseCIDR(s string) (net.IP, *net.IPNet, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, nil, fmt.Errorf("gecersiz ip adresi: %s", s)
		}
		if ip.To4() != nil {
			s = s + "/32"
		} else {
			s = s + "/128"
		}
	}
	return net.ParseCIDR(s)
}
