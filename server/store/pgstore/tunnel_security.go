package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ZTNA Z3 — tunel seviyesinde WAF + Geo ayari deposu.

func (s *Store) GetTunnelSecurity(ctx context.Context, tenantID, tunnelID string) (store.TunnelSecurity, error) {
	ts := store.TunnelSecurity{TunnelID: tunnelID, TenantID: tenantID}
	var cfg []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(sc.config,'{}'::jsonb), COALESCE(sc.updated_at, t.created_at)
		 FROM tunnels t
		 LEFT JOIN tunnel_security sc ON sc.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).Scan(&cfg, &ts.UpdatedAt)
	if err != nil {
		return store.TunnelSecurity{}, store.ErrNotFound
	}
	ts.Config = cfg
	return ts, nil
}

func (s *Store) SetTunnelSecurity(ctx context.Context, tenantID string, ts store.TunnelSecurity) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		ts.TunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	if len(ts.Config) == 0 {
		ts.Config = json.RawMessage(`{}`)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_security (tunnel_id, tenant_id, config, updated_at)
		 VALUES ($1,$2,$3::jsonb, now())
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET config=EXCLUDED.config, updated_at=now()`,
		ts.TunnelID, tenantID, []byte(ts.Config)); err != nil {
		return fmt.Errorf("tunel guvenligi kaydedilemedi: %w", err)
	}
	return nil
}

func (s *Store) ListTunnelSecurities(ctx context.Context) (map[string]store.TunnelSecurity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tunnel_id, tenant_id, config, updated_at FROM tunnel_security`)
	if err != nil {
		return nil, fmt.Errorf("tunel guvenligi listelenemedi: %w", err)
	}
	defer rows.Close()
	out := map[string]store.TunnelSecurity{}
	for rows.Next() {
		var ts store.TunnelSecurity
		var cfg []byte
		if err := rows.Scan(&ts.TunnelID, &ts.TenantID, &cfg, &ts.UpdatedAt); err != nil {
			return nil, err
		}
		ts.Config = cfg
		out[ts.TunnelID] = ts
	}
	return out, rows.Err()
}
