// Package entitlements, kiracilarin plan/ozellik haklarini denetleyen arayuzu
// tanimlar. Bu ACIK CEKIRDEK (open-core) surumunde denetim NO-OP'tur: tum
// kaynaklar sinirsizdir ve her ozellik aciktir (self-host icin dogru varsayilan).
//
// Plan tabanli kota zorlama, ekran yayini limitleri ve odeme entegrasyonu Zorven
// Enterprise'da bu arayuzu gercek bir implementasyonla degistirir. Cekirdek kod
// yalnizca arayuze baglidir; ` != nil` guard'lari sayesinde servis hic
// enjekte edilmese bile calisir.
package entitlements

import (
	"context"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type Feature string

const (
	FeatureAPIAccess    Feature = "api_access"
	FeatureIPAllowlist  Feature = "ip_allowlist"
	FeatureCustomDomain Feature = "custom_domain"
)

// ScreenStreamSpec, ekran yayini parametreleri. Cekirdekte sinirsiz/1080p.
type ScreenStreamSpec struct {
	MaxFPS        int    `json:"max_fps"`
	MaxResolution string `json:"max_resolution"`
}

// EntitlementError, detayli limit ve kaynak bilgisi tasiyan hata yapisi.
// Cekirdekte uretilmez; Enterprise implementasyonu kota asiminda dondurur.
type EntitlementError struct {
	Code     string `json:"code"`
	Message  string `json:"error"`
	Resource string `json:"resource"`
	Limit    int    `json:"limit"`
	Current  int    `json:"current"`
}

func (e *EntitlementError) Error() string { return e.Message }

// TenantEntitlements, bir kiracinin plan ve anlik yetkilerinin ozeti.
type TenantEntitlements struct {
	Plan                store.Plan         `json:"plan"`
	Subscription        store.Subscription `json:"subscription"`
	Usage               store.TenantUsage  `json:"usage"`
	ScreenSpec          ScreenStreamSpec   `json:"screen_spec"`
	CanAddCustomDomain  bool               `json:"can_add_custom_domain"`
	CanCreateClient     bool               `json:"can_create_client"`
	CanCreateTunnel     bool               `json:"can_create_tunnel"`
	ActiveScreenStreams int                `json:"active_screen_streams"`
}

// EntitlementService, kiracilarin plan ve ozellik haklarini denetleyen arayuz.
type EntitlementService interface {
	CanCreateClient(ctx context.Context, tenantID string) error
	CanCreateTunnel(ctx context.Context, tenantID string) error
	CanAddCustomDomain(ctx context.Context, tenantID string) error
	CanAddMember(ctx context.Context, tenantID string) error
	AcquireScreenSlot(ctx context.Context, tenantID string) (release func(), spec ScreenStreamSpec, err error)
	CheckFeature(ctx context.Context, tenantID string, feature Feature) error
	GetTenantEntitlements(ctx context.Context, tenantID string) (TenantEntitlements, error)
}

// Service, cekirdekteki NO-OP implementasyon: her sey sinirsiz ve acik.
type Service struct {
	st store.Store

	screenMu    sync.Mutex
	screenSlots map[string]int // yalnizca anlik yayin sayimi (limit yok)
}

// NewService, sinirsiz (self-host) bir entitlement servisi dondurur.
func NewService(st store.Store) *Service {
	return &Service{st: st, screenSlots: make(map[string]int)}
}

func (s *Service) CanCreateClient(context.Context, string) error       { return nil }
func (s *Service) CanCreateTunnel(context.Context, string) error       { return nil }
func (s *Service) CanAddCustomDomain(context.Context, string) error    { return nil }
func (s *Service) CanAddMember(context.Context, string) error          { return nil }
func (s *Service) CheckFeature(context.Context, string, Feature) error { return nil }

// AcquireScreenSlot, limitsiz slot verir; yalnizca anlik sayimi tutar.
func (s *Service) AcquireScreenSlot(_ context.Context, tenantID string) (func(), ScreenStreamSpec, error) {
	s.screenMu.Lock()
	s.screenSlots[tenantID]++
	s.screenMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			s.screenMu.Lock()
			if s.screenSlots[tenantID] > 0 {
				s.screenSlots[tenantID]--
			}
			s.screenMu.Unlock()
		})
	}
	return release, ScreenStreamSpec{MaxFPS: 60, MaxResolution: "1080p"}, nil
}

// GetTenantEntitlements, sinirsiz bir plan + gercek kullanim sayaclarini doner.
func (s *Service) GetTenantEntitlements(ctx context.Context, tenantID string) (TenantEntitlements, error) {
	var usage store.TenantUsage
	if s.st != nil {
		if u, err := s.st.GetTenantUsage(ctx, tenantID); err == nil {
			usage = u
		}
	}
	s.screenMu.Lock()
	active := s.screenSlots[tenantID]
	s.screenMu.Unlock()
	usage.ActiveScreenStreams = active

	return TenantEntitlements{
		Plan:                store.Plan{ID: "self-hosted", Name: "Self-Hosted (sinirsiz)"},
		Subscription:        store.Subscription{TenantID: tenantID, Plan: "self-hosted", Status: "active"},
		Usage:               usage,
		ScreenSpec:          ScreenStreamSpec{MaxFPS: 60, MaxResolution: "1080p"},
		CanAddCustomDomain:  true,
		CanCreateClient:     true,
		CanCreateTunnel:     true,
		ActiveScreenStreams: active,
	}, nil
}
