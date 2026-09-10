package ipfilter

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// mockStore, ipfilter testleri icin hafif store implementasyonu
type mockStore struct {
	store.Store
	rules []store.IPAllowlistRule
}

func (m *mockStore) ListAllActiveIPRules(ctx context.Context) ([]store.IPAllowlistRule, error) {
	var active []store.IPAllowlistRule
	for _, r := range m.rules {
		if r.Enabled {
			active = append(active, r)
		}
	}
	return active, nil
}

func (m *mockStore) ListIPRules(ctx context.Context, tenantID string, tunnelID *string) ([]store.IPAllowlistRule, error) {
	var res []store.IPAllowlistRule
	for _, r := range m.rules {
		if r.TenantID == tenantID {
			if tunnelID == nil || *tunnelID == "" || r.TunnelID == nil || *r.TunnelID == *tunnelID {
				res = append(res, r)
			}
		}
	}
	return res, nil
}

// mockEntitlementService, plan ozellik denetimini taklit eder
type mockEntitlementService struct {
	entitlements.EntitlementService
	ipAllowlistAllowed bool
}

func (m *mockEntitlementService) CheckFeature(ctx context.Context, tenantID string, feature entitlements.Feature) error {
	if feature == entitlements.FeatureIPAllowlist && !m.ipAllowlistAllowed {
		return &entitlements.EntitlementError{
			Code:    "feature_not_available",
			Message: "IP izin listesi ozelligi planinizda mevcut degil",
		}
	}
	return nil
}

func TestParseCIDR(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		wantNet string
	}{
		{"192.168.1.1", false, "192.168.1.1/32"},
		{"10.0.0.0/8", false, "10.0.0.0/8"},
		{"172.16.50.0/24", false, "172.16.50.0/24"},
		{"::1", false, "::1/128"},
		{"2001:db8::/32", false, "2001:db8::/32"},
		{"invalid-ip", true, ""},
		{"999.999.999.999", true, ""},
	}

	for _, tt := range tests {
		_, ipNet, err := ParseCIDR(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseCIDR(%q) hata bekleniyordu ama nil dondu", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseCIDR(%q) beklenmeyen hata: %v", tt.input, err)
			} else if ipNet.String() != tt.wantNet {
				t.Errorf("ParseCIDR(%q) = %s, beklenen %s", tt.input, ipNet.String(), tt.wantNet)
			}
		}
	}
}

func TestEngine_CheckAllowed(t *testing.T) {
	ctx := context.Background()
	tenantID := "ten_test"
	tunnel1 := "tun_1"
	tunnel2 := "tun_2"

	st := &mockStore{
		rules: []store.IPAllowlistRule{
			// Tunnel 1 icin ozel kural: 192.168.1.0/24
			{
				ID:        "r1",
				TenantID:  tenantID,
				TunnelID:  &tunnel1,
				CIDR:      "192.168.1.0/24",
				Enabled:   true,
				CreatedAt: time.Now(),
			},
			// Tenant-wide kural: 10.50.0.1/32
			{
				ID:        "r2",
				TenantID:  tenantID,
				TunnelID:  nil, // Tum tuneller
				CIDR:      "10.50.0.1",
				Enabled:   true,
				CreatedAt: time.Now(),
			},
			// Pasif kural: 8.8.8.8
			{
				ID:        "r3",
				TenantID:  tenantID,
				TunnelID:  &tunnel1,
				CIDR:      "8.8.8.8/32",
				Enabled:   false,
				CreatedAt: time.Now(),
			},
		},
	}

	ent := &mockEntitlementService{ipAllowlistAllowed: true}
	engine := NewEngine(st, ent)

	if err := engine.Reload(ctx); err != nil {
		t.Fatalf("engine.Reload: %v", err)
	}

	// 1. Tunnel 1: 192.168.1.50 eslesmeli (tunnel1 ozel kurali)
	ok, err := engine.CheckAllowed(ctx, tenantID, tunnel1, "192.168.1.50")
	if err != nil || !ok {
		t.Errorf("192.168.1.50 tun_1 icin izin verilmeliydi, ok=%v, err=%v", ok, err)
	}

	// 2. Tunnel 1: 10.50.0.1 eslesmeli (tenant-wide kural)
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel1, "10.50.0.1")
	if err != nil || !ok {
		t.Errorf("10.50.0.1 tun_1 icin izin verilmeliydi (tenant-wide), ok=%v, err=%v", ok, err)
	}

	// 3. Tunnel 1: 192.168.2.1 reddedilmeli (eslesme yok)
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel1, "192.168.2.1")
	if err != nil || ok {
		t.Errorf("192.168.2.1 tun_1 icin engellenmeliydi, ok=%v", ok)
	}

	// 4. Tunnel 1: Pasif kural 8.8.8.8 reddedilmeli
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel1, "8.8.8.8")
	if err != nil || ok {
		t.Errorf("8.8.8.8 pasif oldugundan engellenmeliydi, ok=%v", ok)
	}

	// 5. Tunnel 2: 192.168.1.50 reddedilmeli (bu kural sadece tunnel 1 icindi)
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel2, "192.168.1.50")
	if err != nil || ok {
		t.Errorf("192.168.1.50 tun_2 icin engellenmeliydi, ok=%v", ok)
	}

	// 6. Tunnel 2: 10.50.0.1 eslesmeli (tenant-wide kural her iki tunele de uygulanir)
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel2, "10.50.0.1")
	if err != nil || !ok {
		t.Errorf("10.50.0.1 tun_2 icin de izin verilmeliydi (tenant-wide), ok=%v", ok)
	}

	// 7. Hic kurali olmayan baska bir kiraci: varsayilan acik (open by default)
	ok, err = engine.CheckAllowed(ctx, "another_tenant", "tun_x", "1.2.3.4")
	if err != nil || !ok {
		t.Errorf("kurali olmayan baska kiraci varsayilan acik olmaliydi, ok=%v", ok)
	}

	// 8. Planda FeatureIPAllowlist kapali ise: filtre bypass edilmeli
	ent.ipAllowlistAllowed = false
	ok, err = engine.CheckAllowed(ctx, tenantID, tunnel1, "192.168.2.1")
	if err != nil || !ok {
		t.Errorf("ozellik planda kapaliyken filtre bypass edilmeliydi, ok=%v", ok)
	}
}
