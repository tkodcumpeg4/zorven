package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Store) AdminGetGlobalStats(ctx context.Context) (store.AdminGlobalStats, error) {
	var stats store.AdminGlobalStats

	query := `
		SELECT
			(SELECT count(*) FROM tenants),
			(SELECT count(*) FROM clients),
			(SELECT count(*) FROM tunnels),
			(SELECT count(*) FROM tunnels WHERE enabled = true),
			(SELECT count(*) FROM hostnames),
			(SELECT count(*) FROM hostnames WHERE type = 'custom')
	`
	err := s.pool.QueryRow(ctx, query).Scan(
		&stats.TenantsCount,
		&stats.ClientsCount,
		&stats.TunnelsCount,
		&stats.ActiveTunnelsCount,
		&stats.HostnamesCount,
		&stats.CustomDomainsCount,
	)
	if err != nil {
		return stats, fmt.Errorf("global istatistikler alinamadi: %w", err)
	}

	return stats, nil
}

func (s *Store) AdminListTenantsWithCounts(ctx context.Context) ([]store.TenantWithCounts, error) {
	query := `
		SELECT t.id, t.slug, t.created_at,
		       (SELECT count(*) FROM clients WHERE tenant_id = t.id),
		       (SELECT count(*) FROM tunnels WHERE tenant_id = t.id),
		       (SELECT count(*) FROM hostnames WHERE tenant_id = t.id),
		       COALESCE(s.plan, 'free')
		FROM tenants t
		LEFT JOIN subscriptions s ON s.tenant_id = t.id
		ORDER BY t.created_at DESC
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("kiracilar ve sayilari listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.TenantWithCounts
	for rows.Next() {
		var item store.TenantWithCounts
		if err := rows.Scan(
			&item.ID, &item.Slug, &item.CreatedAt,
			&item.ClientsCount, &item.TunnelsCount, &item.HostnamesCount,
			&item.Plan,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) AdminListAllClients(ctx context.Context) ([]store.ClientWithTenant, error) {
	query := `
		SELECT c.id, c.tenant_id, c.name, c.token_id, c.token_hash, c.created_at, t.slug
		FROM clients c
		JOIN tenants t ON t.id = c.tenant_id
		ORDER BY c.created_at DESC
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("tum istemciler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.ClientWithTenant
	for rows.Next() {
		var item store.ClientWithTenant
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Name, &item.TokenID, &item.TokenHash, &item.CreatedAt,
			&item.TenantSlug,
		); err != nil {
			return nil, err
		}
		item.Status = "offline"
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) AdminListAllHostnames(ctx context.Context) ([]store.HostnameWithTenant, error) {
	query := `
		SELECT h.id, h.tenant_id, COALESCE(h.tunnel_id, ''), h.fqdn, h.type, h.verified, COALESCE(h.verify_token, ''), h.created_at,
		       t.slug, COALESCE(tu.target, '')
		FROM hostnames h
		JOIN tenants t ON t.id = h.tenant_id
		LEFT JOIN tunnels tu ON tu.id = h.tunnel_id
		ORDER BY h.created_at DESC
	`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("tum domainler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.HostnameWithTenant
	for rows.Next() {
		var item store.HostnameWithTenant
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.TunnelID, &item.FQDN, &item.Type, &item.Verified, &item.VerifyToken, &item.CreatedAt,
			&item.TenantSlug, &item.Target,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
