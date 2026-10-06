package pgstore

// FAZ 4 / F21 — Tunel basina yuk dengeleme + saglik kontrolu yapilandirmasi.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const tunnelLBCols = `tunnel_id, tenant_id, strategy, weights, health_enabled, health_path,
	interval_sec, timeout_sec, unhealthy_threshold, healthy_threshold`

func scanTunnelLB(row pgx.Row, lb *store.TunnelLB) error {
	var w []byte
	if err := row.Scan(&lb.TunnelID, &lb.TenantID, &lb.Strategy, &w, &lb.HealthEnabled, &lb.HealthPath,
		&lb.IntervalSec, &lb.TimeoutSec, &lb.UnhealthyThreshold, &lb.HealthyThreshold); err != nil {
		return err
	}
	lb.Weights = map[string]int{}
	if len(w) > 0 {
		// Bozuk agirlik kaydi esit agirliga duser; dengeleme calismaya devam eder.
		_ = json.Unmarshal(w, &lb.Weights)
	}
	return nil
}

// GetTunnelLB, tunelin yapilandirmasi. Kayit yoksa varsayilan (eski davranis).
func (s *Store) GetTunnelLB(ctx context.Context, tenantID, tunnelID string) (store.TunnelLB, error) {
	var lb store.TunnelLB
	err := scanTunnelLB(s.pool.QueryRow(ctx,
		`SELECT `+tunnelLBCols+` FROM tunnel_lb WHERE tunnel_id = $1 AND tenant_id = $2`,
		tunnelID, tenantID), &lb)
	if errors.Is(err, pgx.ErrNoRows) {
		d := store.DefaultTunnelLB(tunnelID)
		d.TenantID = tenantID
		return d, nil
	}
	return lb, err
}

// SetTunnelLB, yapilandirmayi yazar (upsert).
func (s *Store) SetTunnelLB(ctx context.Context, tenantID string, lb store.TunnelLB) error {
	w, err := json.Marshal(lb.Weights)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO tunnel_lb (tunnel_id, tenant_id, strategy, weights, health_enabled, health_path,
		    interval_sec, timeout_sec, unhealthy_threshold, healthy_threshold, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		 ON CONFLICT (tunnel_id) DO UPDATE SET
		    strategy = EXCLUDED.strategy, weights = EXCLUDED.weights,
		    health_enabled = EXCLUDED.health_enabled, health_path = EXCLUDED.health_path,
		    interval_sec = EXCLUDED.interval_sec, timeout_sec = EXCLUDED.timeout_sec,
		    unhealthy_threshold = EXCLUDED.unhealthy_threshold,
		    healthy_threshold = EXCLUDED.healthy_threshold, updated_at = now()`,
		lb.TunnelID, tenantID, lb.Strategy, w, lb.HealthEnabled, lb.HealthPath,
		lb.IntervalSec, lb.TimeoutSec, lb.UnhealthyThreshold, lb.HealthyThreshold)
	if err != nil {
		return fmt.Errorf("yuk dengeleme ayari kaydedilemedi: %w", err)
	}
	return nil
}

// ListTunnelLBs, tum kayitlar (router yenilemesinde tek sorgu).
func (s *Store) ListTunnelLBs(ctx context.Context) ([]store.TunnelLB, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tunnelLBCols+` FROM tunnel_lb`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TunnelLB{}
	for rows.Next() {
		var lb store.TunnelLB
		if err := scanTunnelLB(rows, &lb); err != nil {
			return nil, err
		}
		out = append(out, lb)
	}
	return out, rows.Err()
}
