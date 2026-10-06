package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// CreateAbuseReport, bir kotuye-kullanim bildirimi kaydeder (FAZ 4).
func (s *Store) CreateAbuseReport(ctx context.Context, fqdn, reason, reporterIP string) (store.AbuseReport, error) {
	r := store.AbuseReport{
		ID:         newID("abr"),
		FQDN:       fqdn,
		Reason:     reason,
		ReporterIP: reporterIP,
		CreatedAt:  time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO abuse_reports (id, fqdn, reason, reporter_ip, handled, created_at)
		 VALUES ($1,$2,$3,$4,false,$5)`,
		r.ID, r.FQDN, r.Reason, r.ReporterIP, r.CreatedAt)
	if err != nil {
		return store.AbuseReport{}, fmt.Errorf("abuse bildirimi kaydedilemedi: %w", err)
	}
	return r, nil
}

// ListAbuseReports, en yeni bildirimleri doner.
func (s *Store) ListAbuseReports(ctx context.Context, limit int) ([]store.AbuseReport, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, fqdn, reason, reporter_ip, handled, created_at
		 FROM abuse_reports ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("abuse bildirimleri listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.AbuseReport
	for rows.Next() {
		var r store.AbuseReport
		if err := rows.Scan(&r.ID, &r.FQDN, &r.Reason, &r.ReporterIP, &r.Handled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
