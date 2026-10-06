package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6 — trafik politikası deposu. Erişim politikasıyla aynı desen: tünel başına
// bir satır, kiracı sahipliği doğrulanır.

func (s *Store) GetTunnelTrafficPolicy(ctx context.Context, tenantID, tunnelID string) (store.TunnelTrafficPolicy, error) {
	p := store.TunnelTrafficPolicy{TunnelID: tunnelID, Config: json.RawMessage("{}")}
	var cfg []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(tp.config,'{}'::jsonb), COALESCE(tp.enabled,false)
		 FROM tunnels t
		 LEFT JOIN tunnel_traffic_policies tp ON tp.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).
		Scan(&cfg, &p.Enabled)
	if err != nil {
		return store.TunnelTrafficPolicy{}, store.ErrNotFound
	}
	p.Config = json.RawMessage(cfg)
	return p, nil
}

func (s *Store) SetTunnelTrafficPolicy(ctx context.Context, tenantID string, p store.TunnelTrafficPolicy) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		p.TunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	cfg := p.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_traffic_policies (tunnel_id, config, enabled, updated_at)
		 VALUES ($1,$2,$3, now())
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET config=EXCLUDED.config, enabled=EXCLUDED.enabled, updated_at=now()`,
		p.TunnelID, []byte(cfg), p.Enabled); err != nil {
		return fmt.Errorf("trafik politikasi kaydedilemedi: %w", err)
	}
	return nil
}
