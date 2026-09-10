package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestSubscriptions_LifecycleAndLimits(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	// 1. Varsayilan kiraci aboneligi Free olarak gelmeli (2 client, 2 tunnel, 5GB)
	sub, err := s.GetSubscription(ctx, store.DefaultTenantID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Plan != store.PlanFree {
		t.Errorf("plan Free olmali: got %s", sub.Plan)
	}
	if sub.MaxClients != 2 || sub.MaxCustomDomains != 0 || sub.MaxTunnels != 2 {
		t.Errorf("varsayilan Free limitleri hatali: %+v", sub)
	}
	if sub.BandwidthLimitBytes != 5*1024*1024*1024 {
		t.Errorf("bant genisligi 5GB olmali: got %d", sub.BandwidthLimitBytes)
	}

	// 2. Yeni kiraci olusturuldugunda Free aboneligi otomatik atanmali
	ten, err := s.CreateTenant(ctx, "acme-test-sub")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	acmeSub, err := s.GetSubscription(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetSubscription for acme: %v", err)
	}
	if acmeSub.Plan != store.PlanFree || acmeSub.MaxClients != 2 {
		t.Errorf("yeni kiracinin aboneligi Free olmali: %+v", acmeSub)
	}

	// 3. Plan Hobby'ye yukseltilsin ($5 / ay, 5 client, 1 domain, 5 tunnel, 20GB)
	if err := s.UpdateTenantPlan(ctx, ten.ID, store.PlanHobby, store.SubStatusActive, nil); err != nil {
		t.Fatalf("UpdateTenantPlan to hobby: %v", err)
	}
	hobbySub, err := s.GetSubscription(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetSubscription after hobby: %v", err)
	}
	if hobbySub.Plan != store.PlanHobby || hobbySub.MaxClients != 5 || hobbySub.MaxCustomDomains != 1 || hobbySub.MaxTunnels != 5 {
		t.Errorf("Hobby plan limitleri hatali: %+v", hobbySub)
	}
	if hobbySub.BandwidthLimitBytes != 20*1024*1024*1024 {
		t.Errorf("Hobby bant genisligi 20GB olmali: got %d", hobbySub.BandwidthLimitBytes)
	}

	// 4. Plan Pro'ya yukseltilsin ($12 / ay, 15 client, 5 domain, 20 tunnel, 100GB)
	end := time.Now().Add(30 * 24 * time.Hour).UTC()
	if err := s.UpdateTenantPlan(ctx, ten.ID, store.PlanPro, store.SubStatusActive, &end); err != nil {
		t.Fatalf("UpdateTenantPlan: %v", err)
	}

	proSub, err := s.GetSubscription(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetSubscription after pro: %v", err)
	}
	if proSub.Plan != store.PlanPro {
		t.Errorf("plan Pro olmali: got %s", proSub.Plan)
	}
	if proSub.MaxClients != 15 || proSub.MaxCustomDomains != 5 || proSub.MaxTunnels != 20 {
		t.Errorf("Pro plan limitleri hatali: %+v", proSub)
	}
	if proSub.BandwidthLimitBytes != 100*1024*1024*1024 {
		t.Errorf("Pro bant genisligi 100GB olmali: got %d", proSub.BandwidthLimitBytes)
	}

	// 5. Kaynak ekleyip kullanim sayaclarini test edelim
	c1, err := s.CreateClient(ctx, ten.ID, "macbook", "tok_s1", "hash_s1")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	tun1, err := s.CreateTunnel(ctx, ten.ID, c1.ID, "http://localhost:3000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	_, err = s.AddCustomHostname(ctx, ten.ID, tun1.ID, "test.acme.com", "tok-123")
	if err != nil {
		t.Fatalf("AddCustomHostname: %v", err)
	}

	// Bant genisligi ekleyelim
	period := time.Now().UTC().Format("2006-01")
	if err := s.RecordBandwidth(ctx, ten.ID, period, 1024, 2048); err != nil {
		t.Fatalf("RecordBandwidth: %v", err)
	}

	usage, err := s.GetTenantUsage(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetTenantUsage: %v", err)
	}
	if usage.ClientsCount != 1 {
		t.Errorf("ClientsCount 1 olmali: got %d", usage.ClientsCount)
	}
	if usage.TunnelsCount != 1 {
		t.Errorf("TunnelsCount 1 olmali: got %d", usage.TunnelsCount)
	}
	if usage.CustomDomainsCount != 1 {
		t.Errorf("CustomDomainsCount 1 olmali: got %d", usage.CustomDomainsCount)
	}
	if usage.BandwidthUsedBytes != 3072 {
		t.Errorf("BandwidthUsedBytes 3072 olmali: got %d", usage.BandwidthUsedBytes)
	}
	if usage.IsThrottled {
		t.Errorf("Kullanim kotayi asmadigi icin IsThrottled false olmali")
	}

	// 6. Team planina gecis testi (50 client, 50 domain, 50 tunnel, 250GB)
	if err := s.UpdateTenantPlan(ctx, ten.ID, store.PlanTeam, store.SubStatusActive, nil); err != nil {
		t.Fatalf("UpdateTenantPlan to team: %v", err)
	}
	teamSub, err := s.GetSubscription(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetSubscription after team: %v", err)
	}
	if teamSub.Plan != store.PlanTeam || teamSub.MaxClients != 50 || teamSub.MaxCustomDomains != 50 || teamSub.MaxTunnels != 50 {
		t.Errorf("Team plan limitleri hatali: %+v", teamSub)
	}

	// 7. Plans tablosu testi
	plans, err := s.ListPlans(ctx)
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) < 5 {
		t.Errorf("En az 5 plan bulunmali: got %d", len(plans))
	} else {
		if plans[0].ID != store.PlanFree {
			t.Errorf("Ilk plan 'free' olmali: got %s", plans[0].ID)
		}
		if plans[len(plans)-1].ID != store.PlanEnterprise {
			t.Errorf("Son plan 'enterprise' olmali: got %s", plans[len(plans)-1].ID)
		}
	}
	proPlan, err := s.GetPlan(ctx, store.PlanPro)
	if err != nil {
		t.Fatalf("GetPlan pro: %v", err)
	}
	if proPlan.PriceMonthly != 1200 || !proPlan.HasAPIAccess || !proPlan.HasIPAllowlist {
		t.Errorf("Pro plan detaylari hatali: %+v", proPlan)
	}
}
