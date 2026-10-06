package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.4 — metrik uyarıları deposu.

func (s *Store) GetTunnelAlert(ctx context.Context, tenantID, tunnelID string) (store.TunnelAlert, error) {
	a := store.TunnelAlert{TunnelID: tunnelID, ErrorRatePct: 10, WindowMin: 5, MinRequests: 20, State: "ok"}
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(al.enabled,false), COALESCE(al.error_rate_pct,10), COALESCE(al.window_min,5),
		        COALESCE(al.min_requests,20), COALESCE(al.notify_email,''), COALESCE(al.state,'ok'),
		        al.last_changed_at, al.last_notified_at
		 FROM tunnels t
		 LEFT JOIN tunnel_alerts al ON al.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).
		Scan(&a.Enabled, &a.ErrorRatePct, &a.WindowMin, &a.MinRequests, &a.NotifyEmail, &a.State,
			&a.LastChangedAt, &a.LastNotifiedAt)
	if err != nil {
		return store.TunnelAlert{}, store.ErrNotFound
	}
	return a, nil
}

func (s *Store) SetTunnelAlert(ctx context.Context, tenantID string, a store.TunnelAlert) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		a.TunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	if a.ErrorRatePct <= 0 || a.ErrorRatePct > 100 {
		a.ErrorRatePct = 10
	}
	if a.WindowMin <= 0 || a.WindowMin > 1440 {
		a.WindowMin = 5
	}
	if a.MinRequests < 0 {
		a.MinRequests = 0
	}
	// Durum alanlarina DOKUNMA: yalnizca yapilandirma upsert edilir.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_alerts (tunnel_id, enabled, error_rate_pct, window_min, min_requests, notify_email)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET enabled=EXCLUDED.enabled, error_rate_pct=EXCLUDED.error_rate_pct,
		       window_min=EXCLUDED.window_min, min_requests=EXCLUDED.min_requests,
		       notify_email=EXCLUDED.notify_email`,
		a.TunnelID, a.Enabled, a.ErrorRatePct, a.WindowMin, a.MinRequests, a.NotifyEmail); err != nil {
		return fmt.Errorf("uyari kaydedilemedi: %w", err)
	}
	return nil
}

func (s *Store) ListEnabledAlerts(ctx context.Context) ([]store.TunnelAlert, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT al.tunnel_id, t.tenant_id, al.error_rate_pct, al.window_min, al.min_requests,
		        al.notify_email, al.state, al.last_changed_at, al.last_notified_at
		 FROM tunnel_alerts al
		 JOIN tunnels t ON t.id = al.tunnel_id
		 WHERE al.enabled = true`)
	if err != nil {
		return nil, fmt.Errorf("etkin uyarilar listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.TunnelAlert
	for rows.Next() {
		a := store.TunnelAlert{Enabled: true}
		if err := rows.Scan(&a.TunnelID, &a.TenantID, &a.ErrorRatePct, &a.WindowMin, &a.MinRequests,
			&a.NotifyEmail, &a.State, &a.LastChangedAt, &a.LastNotifiedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAlertState(ctx context.Context, tunnelID, state string, notified bool) error {
	if notified {
		_, err := s.pool.Exec(ctx,
			`UPDATE tunnel_alerts SET state=$2, last_changed_at=now(), last_notified_at=now() WHERE tunnel_id=$1`,
			tunnelID, state)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE tunnel_alerts SET state=$2, last_changed_at=now() WHERE tunnel_id=$1`,
		tunnelID, state)
	return err
}

func (s *Store) TunnelRequestStats(ctx context.Context, tenantID, tunnelID string, since time.Time) (int64, int64, error) {
	var total, errors int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE status >= 500)
		 FROM request_logs
		 WHERE tenant_id=$1 AND tunnel_id=$2 AND ts >= $3`,
		tenantID, tunnelID, since).Scan(&total, &errors)
	if err != nil {
		return 0, 0, fmt.Errorf("uyari istatistikleri sorgulanamadi: %w", err)
	}
	return total, errors, nil
}
