package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// CreateAbuseReport, bir kotuye-kullanim bildirimi kaydeder (FAZ 4).
func (s *Store) CreateAbuseReport(ctx context.Context, fqdn, reason, reporterIP string) (store.AbuseReport, error) {
	return s.CreateAbuseReportFull(ctx, store.AbuseReport{FQDN: fqdn, Reason: reason, ReporterIP: reporterIP})
}

// CreateAbuseReportFull, bildireni tanitan alanlarla birlikte kaydeder.
func (s *Store) CreateAbuseReportFull(ctx context.Context, r store.AbuseReport) (store.AbuseReport, error) {
	r.ID = newID("abr")
	r.CreatedAt = time.Now().UTC()
	r.Handled = false
	_, err := s.pool.Exec(ctx,
		`INSERT INTO abuse_reports (id, fqdn, reason, reporter_ip, handled, created_at,
			category, reporter_ua, reporter_id, reporter_tls, reporter_lang)
		 VALUES ($1,$2,$3,$4,false,$5,$6,$7,$8,$9,$10)`,
		r.ID, r.FQDN, r.Reason, r.ReporterIP, r.CreatedAt,
		r.Category, r.ReporterUA, r.ReporterID, r.ReporterTLS, r.ReporterLang)
	if err != nil {
		return store.AbuseReport{}, fmt.Errorf("abuse bildirimi kaydedilemedi: %w", err)
	}
	return r, nil
}

// AbuseReportCounts, spam sinirlari icin yakin zamanli bildirim sayilari.
func (s *Store) AbuseReportCounts(ctx context.Context, since time.Time, ip, reporterID, fqdn string) (byIP, byID, sameHost int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT
			count(*) FILTER (WHERE reporter_ip = $2),
			count(*) FILTER (WHERE $3 <> '' AND reporter_id = $3),
			count(*) FILTER (WHERE reporter_ip = $2 AND fqdn = $4)
		 FROM abuse_reports WHERE created_at >= $1`,
		since, ip, reporterID, fqdn).Scan(&byIP, &byID, &sameHost)
	return
}

const abuseCols = `id, fqdn, reason, reporter_ip, handled, created_at,
	category, reporter_ua, reporter_id, reporter_tls, reporter_lang`

func scanAbuse(row interface{ Scan(...any) error }, r *store.AbuseReport) error {
	return row.Scan(&r.ID, &r.FQDN, &r.Reason, &r.ReporterIP, &r.Handled, &r.CreatedAt,
		&r.Category, &r.ReporterUA, &r.ReporterID, &r.ReporterTLS, &r.ReporterLang)
}

// ListAbuseReports, en yeni bildirimleri doner.
func (s *Store) ListAbuseReports(ctx context.Context, limit int) ([]store.AbuseReport, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+abuseCols+`
		 FROM abuse_reports ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("abuse bildirimleri listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.AbuseReport
	for rows.Next() {
		var r store.AbuseReport
		if err := scanAbuse(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
