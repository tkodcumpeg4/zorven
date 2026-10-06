package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// GetTunnelAccessPolicy, tunelin erişim politikasını doner. Politika kaydı yoksa
// varsayilan (mode=none, enabled=false, config={}) doner. Kiraci sahipligini
// dogrular: tunel bu kiraciya ait degilse ErrNotFound.
func (s *Store) GetTunnelAccessPolicy(ctx context.Context, tenantID, tunnelID string) (store.TunnelAccessPolicy, error) {
	p := store.TunnelAccessPolicy{TunnelID: tunnelID, Mode: "none", Config: json.RawMessage("{}")}
	var cfg []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(ap.mode,'none'), COALESCE(ap.config,'{}'::jsonb), COALESCE(ap.enabled,false)
		 FROM tunnels t
		 LEFT JOIN tunnel_access_policies ap ON ap.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).
		Scan(&p.Mode, &cfg, &p.Enabled)
	if err != nil {
		return store.TunnelAccessPolicy{}, store.ErrNotFound
	}
	p.Config = json.RawMessage(cfg)
	return p, nil
}

// SetTunnelAccessPolicy, politikayi upsert eder. Kiraci sahipligini dogrular.
func (s *Store) SetTunnelAccessPolicy(ctx context.Context, tenantID string, p store.TunnelAccessPolicy) error {
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
		`INSERT INTO tunnel_access_policies (tunnel_id, mode, config, enabled, updated_at)
		 VALUES ($1,$2,$3,$4, now())
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET mode=EXCLUDED.mode, config=EXCLUDED.config, enabled=EXCLUDED.enabled, updated_at=now()`,
		p.TunnelID, p.Mode, []byte(cfg), p.Enabled); err != nil {
		return fmt.Errorf("erisim politikasi kaydedilemedi: %w", err)
	}
	return nil
}
