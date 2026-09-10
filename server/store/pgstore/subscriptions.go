package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// DefaultSubscriptionForPlan, belirtilen plan icin standart limitleri uretir.
func DefaultSubscriptionForPlan(tenantID, plan string) store.Subscription {
	now := time.Now().UTC()
	sub := store.Subscription{
		TenantID:  tenantID,
		Plan:      plan,
		Status:    store.SubStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	switch plan {
	case store.PlanHobby:
		sub.MaxClients = 5
		sub.MaxCustomDomains = 1
		sub.MaxTunnels = 5
		sub.BandwidthLimitBytes = 20 * 1024 * 1024 * 1024 // 20 GB
	case store.PlanPro:
		sub.MaxClients = 15
		sub.MaxCustomDomains = 5
		sub.MaxTunnels = 20
		sub.BandwidthLimitBytes = 100 * 1024 * 1024 * 1024 // 100 GB
	case store.PlanTeam:
		sub.MaxClients = 50
		sub.MaxCustomDomains = 50
		sub.MaxTunnels = 50
		sub.BandwidthLimitBytes = 250 * 1024 * 1024 * 1024 // 250 GB
	case store.PlanEnterprise:
		sub.MaxClients = 999999
		sub.MaxCustomDomains = 999999
		sub.MaxTunnels = 999999
		sub.BandwidthLimitBytes = 10 * 1024 * 1024 * 1024 * 1024 // 10 TB
	default: // Free
		sub.Plan = store.PlanFree
		sub.MaxClients = 2
		sub.MaxCustomDomains = 0
		sub.MaxTunnels = 2
		sub.BandwidthLimitBytes = 5 * 1024 * 1024 * 1024 // 5 GB
	}
	return sub
}

// GetPlan, id'ye gore plan tanimini getirir.
func (s *Store) GetPlan(ctx context.Context, id string) (store.Plan, error) {
	var p store.Plan
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, price_monthly, max_clients, max_tunnels, max_custom_domains,
		        bandwidth_limit_bytes, bandwidth_normal_mbps, bandwidth_throttled_mbps,
		        max_screen_streams, screen_max_fps, log_retention_days, max_members,
		        has_api_access, has_ip_allowlist, extra_device_price, extra_gb_price
		 FROM plans WHERE id = $1`, id).
		Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.MaxClients, &p.MaxTunnels, &p.MaxCustomDomains,
			&p.BandwidthLimitBytes, &p.BandwidthNormalMbps, &p.BandwidthThrottledMbps,
			&p.MaxScreenStreams, &p.ScreenMaxFPS, &p.LogRetentionDays, &p.MaxMembers,
			&p.HasAPIAccess, &p.HasIPAllowlist, &p.ExtraDevicePrice, &p.ExtraGBPrice)
	return p, err
}

// ListPlans, sistemde tanimli tum planlari sirali olarak listeler.
func (s *Store) ListPlans(ctx context.Context) ([]store.Plan, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, price_monthly, max_clients, max_tunnels, max_custom_domains,
		        bandwidth_limit_bytes, bandwidth_normal_mbps, bandwidth_throttled_mbps,
		        max_screen_streams, screen_max_fps, log_retention_days, max_members,
		        has_api_access, has_ip_allowlist, extra_device_price, extra_gb_price
		 FROM plans
		 ORDER BY 
		     CASE id 
		         WHEN 'free' THEN 1 
		         WHEN 'hobby' THEN 2 
		         WHEN 'pro' THEN 3 
		         WHEN 'team' THEN 4 
		         WHEN 'enterprise' THEN 5 
		         ELSE 6 
		     END ASC, price_monthly ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []store.Plan
	for rows.Next() {
		var p store.Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceMonthly, &p.MaxClients, &p.MaxTunnels, &p.MaxCustomDomains,
			&p.BandwidthLimitBytes, &p.BandwidthNormalMbps, &p.BandwidthThrottledMbps,
			&p.MaxScreenStreams, &p.ScreenMaxFPS, &p.LogRetentionDays, &p.MaxMembers,
			&p.HasAPIAccess, &p.HasIPAllowlist, &p.ExtraDevicePrice, &p.ExtraGBPrice); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

// GetSubscription, kiracinin abonelik kaydini ve plan detaylarini getirir. Satir yoksa Free olusturur.
func (s *Store) GetSubscription(ctx context.Context, tenantID string) (store.Subscription, error) {
	var sub store.Subscription
	var curPeriodEnd *time.Time
	var stripeCustID, stripeSubID *string

	err := s.pool.QueryRow(ctx,
		`SELECT tenant_id, plan, status, stripe_customer_id, stripe_subscription_id,
		        current_period_end, max_clients, max_custom_domains, max_tunnels,
		        bandwidth_limit_bytes, created_at, updated_at
		 FROM subscriptions
		 WHERE tenant_id = $1`, tenantID).
		Scan(&sub.TenantID, &sub.Plan, &sub.Status, &stripeCustID, &stripeSubID,
			&curPeriodEnd, &sub.MaxClients, &sub.MaxCustomDomains, &sub.MaxTunnels,
			&sub.BandwidthLimitBytes, &sub.CreatedAt, &sub.UpdatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		defaultSub := DefaultSubscriptionForPlan(tenantID, store.PlanFree)
		_ = s.UpsertSubscription(ctx, defaultSub)
		return defaultSub, nil
	}
	if err != nil {
		return store.Subscription{}, err
	}

	if stripeCustID != nil {
		sub.StripeCustomerID = *stripeCustID
	}
	if stripeSubID != nil {
		sub.StripeSubscriptionID = *stripeSubID
	}
	sub.CurrentPeriodEnd = curPeriodEnd

	// Plana bagli ayrintilari doldur
	if plan, err := s.GetPlan(ctx, sub.Plan); err == nil {
		sub.PlanDetails = &plan
	}

	return sub, nil
}

// UpsertSubscription, kiraci aboneligini yazar veya gunceller.
func (s *Store) UpsertSubscription(ctx context.Context, sub store.Subscription) error {
	now := time.Now().UTC()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	sub.UpdatedAt = now

	_, err := s.pool.Exec(ctx,
		`INSERT INTO subscriptions (
			tenant_id, plan, status, stripe_customer_id, stripe_subscription_id,
			current_period_end, max_clients, max_custom_domains, max_tunnels,
			bandwidth_limit_bytes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant_id) DO UPDATE SET
			plan = EXCLUDED.plan,
			status = EXCLUDED.status,
			stripe_customer_id = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
			stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
			current_period_end = COALESCE(EXCLUDED.current_period_end, subscriptions.current_period_end),
			max_clients = EXCLUDED.max_clients,
			max_custom_domains = EXCLUDED.max_custom_domains,
			max_tunnels = EXCLUDED.max_tunnels,
			bandwidth_limit_bytes = EXCLUDED.bandwidth_limit_bytes,
			updated_at = EXCLUDED.updated_at`,
		sub.TenantID, sub.Plan, sub.Status,
		nullableString(sub.StripeCustomerID), nullableString(sub.StripeSubscriptionID),
		sub.CurrentPeriodEnd, sub.MaxClients, sub.MaxCustomDomains, sub.MaxTunnels,
		sub.BandwidthLimitBytes, sub.CreatedAt, sub.UpdatedAt)
	return err
}

// UpdateTenantPlan, kiracinin planini degistirir ve planin standart limitlerini uygular.
func (s *Store) UpdateTenantPlan(ctx context.Context, tenantID, plan, status string, periodEnd *time.Time) error {
	defaults := DefaultSubscriptionForPlan(tenantID, plan)
	now := time.Now().UTC()

	_, err := s.pool.Exec(ctx,
		`INSERT INTO subscriptions (
			tenant_id, plan, status, current_period_end,
			max_clients, max_custom_domains, max_tunnels, bandwidth_limit_bytes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (tenant_id) DO UPDATE SET
			plan = EXCLUDED.plan,
			status = EXCLUDED.status,
			current_period_end = EXCLUDED.current_period_end,
			max_clients = EXCLUDED.max_clients,
			max_custom_domains = EXCLUDED.max_custom_domains,
			max_tunnels = EXCLUDED.max_tunnels,
			bandwidth_limit_bytes = EXCLUDED.bandwidth_limit_bytes,
			updated_at = EXCLUDED.updated_at`,
		tenantID, plan, status, periodEnd,
		defaults.MaxClients, defaults.MaxCustomDomains, defaults.MaxTunnels, defaults.BandwidthLimitBytes,
		now)
	return err
}

// CountClients, kiracinin kayitli istemci sayisini doner.
func (s *Store) CountClients(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients WHERE tenant_id = $1`, tenantID).Scan(&count)
	return count, err
}

// CountCustomDomains, kiracinin ozel alan adi sayisini doner.
func (s *Store) CountCustomDomains(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hostnames WHERE tenant_id = $1 AND type = $2`,
		tenantID, store.HostTypeCustom).Scan(&count)
	return count, err
}

// CountTunnels, kiracinin tanimli tunel sayisini doner.
func (s *Store) CountTunnels(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tunnels WHERE tenant_id = $1`, tenantID).Scan(&count)
	return count, err
}

// RecordBandwidth, belirli bir donem icin gelen ve giden bayt miktarini atomik olarak ekler.
func (s *Store) RecordBandwidth(ctx context.Context, tenantID, period string, bytesIn, bytesOut int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenant_bandwidth (tenant_id, period, bytes_in, bytes_out, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (tenant_id, period) DO UPDATE SET
		     bytes_in = tenant_bandwidth.bytes_in + EXCLUDED.bytes_in,
		     bytes_out = tenant_bandwidth.bytes_out + EXCLUDED.bytes_out,
		     updated_at = now()`,
		tenantID, period, bytesIn, bytesOut)
	return err
}

// FlushBandwidthDeltas, biriken kiraci trafik farklarini tek toplu (batch) islemde veritabanina aktarir.
func (s *Store) FlushBandwidthDeltas(ctx context.Context, period string, deltas map[string][2]int64) error {
	if len(deltas) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for tenantID, d := range deltas {
		if d[0] == 0 && d[1] == 0 {
			continue
		}
		batch.Queue(
			`INSERT INTO tenant_bandwidth (tenant_id, period, bytes_in, bytes_out, updated_at)
			 VALUES ($1, $2, $3, $4, now())
			 ON CONFLICT (tenant_id, period) DO UPDATE SET
			     bytes_in = tenant_bandwidth.bytes_in + EXCLUDED.bytes_in,
			     bytes_out = tenant_bandwidth.bytes_out + EXCLUDED.bytes_out,
			     updated_at = now()`,
			tenantID, period, d[0], d[1])
	}
	if batch.Len() == 0 {
		return nil
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range batch.Len() {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// GetBandwidthUsage, belirli bir donem icin kiracinin tukettigi toplam baytlari doner.
func (s *Store) GetBandwidthUsage(ctx context.Context, tenantID, period string) (bytesIn, bytesOut int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(bytes_in, 0), COALESCE(bytes_out, 0)
		 FROM tenant_bandwidth
		 WHERE tenant_id = $1 AND period = $2`, tenantID, period).
		Scan(&bytesIn, &bytesOut)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	return bytesIn, bytesOut, err
}

// GetTenantUsage, kiracinin anlik kaynak kullanim sayilarini ve trafik durumunu paketler.
func (s *Store) GetTenantUsage(ctx context.Context, tenantID string) (store.TenantUsage, error) {
	var usage store.TenantUsage

	clients, err := s.CountClients(ctx, tenantID)
	if err != nil {
		return usage, err
	}
	customDomains, err := s.CountCustomDomains(ctx, tenantID)
	if err != nil {
		return usage, err
	}
	tunnels, err := s.CountTunnels(ctx, tenantID)
	if err != nil {
		return usage, err
	}

	currentPeriod := time.Now().UTC().Format("2006-01")
	bytesIn, bytesOut, _ := s.GetBandwidthUsage(ctx, tenantID, currentPeriod)
	totalBytes := bytesIn + bytesOut

	sub, _ := s.GetSubscription(ctx, tenantID)
	isThrottled := false
	if sub.BandwidthLimitBytes > 0 && totalBytes >= sub.BandwidthLimitBytes {
		isThrottled = true
	}

	usage.ClientsCount = clients
	usage.CustomDomainsCount = customDomains
	usage.TunnelsCount = tunnels
	usage.BandwidthUsedBytes = totalBytes
	usage.IsThrottled = isThrottled

	return usage, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
