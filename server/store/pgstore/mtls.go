package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.6 — mTLS deposu.

func (s *Store) GetTunnelMTLS(ctx context.Context, tenantID, tunnelID string) (store.TunnelMTLS, error) {
	m := store.TunnelMTLS{TunnelID: tunnelID}
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(mt.enabled,false), COALESCE(mt.ca_pem,'')
		 FROM tunnels t
		 LEFT JOIN tunnel_mtls mt ON mt.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).
		Scan(&m.Enabled, &m.CAPem)
	if err != nil {
		return store.TunnelMTLS{}, store.ErrNotFound
	}
	return m, nil
}

func (s *Store) SetTunnelMTLS(ctx context.Context, tenantID string, m store.TunnelMTLS) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		m.TunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_mtls (tunnel_id, enabled, ca_pem, updated_at)
		 VALUES ($1,$2,$3, now())
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET enabled=EXCLUDED.enabled, ca_pem=EXCLUDED.ca_pem, updated_at=now()`,
		m.TunnelID, m.Enabled, m.CAPem); err != nil {
		return fmt.Errorf("mTLS kaydedilemedi: %w", err)
	}
	return nil
}
