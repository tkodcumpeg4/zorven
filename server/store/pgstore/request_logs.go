package pgstore

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// TunnelMetrics, bir tunelin istek metriklerini zaman dilimlerine (bucket)
// bolerek doner (FAZ 6.3). bucketSec dilim genisligi; since baslangic zamani.
// Bos dilimler DONMEZ (satir yoksa); panel eksik dilimleri sifir sayar.
func (s *Store) TunnelMetrics(ctx context.Context, tenantID, tunnelID string, since time.Time, bucketSec int) ([]reqlog.MetricBucket, error) {
	if bucketSec <= 0 {
		bucketSec = 300
	}
	rows, err := s.pool.Query(ctx,
		`SELECT to_timestamp(floor(extract(epoch from ts)/$4)*$4) AS bucket,
		        count(*),
		        count(*) FILTER (WHERE status >= 500),
		        COALESCE(avg(duration_ms) FILTER (WHERE reject_reason = ''),0),
		        COALESCE(max(duration_ms) FILTER (WHERE reject_reason = ''),0),
		        COALESCE(sum(bytes_in),0),
		        COALESCE(sum(bytes_out),0)
		 FROM request_logs
		 WHERE tenant_id=$1 AND tunnel_id=$2 AND ts >= $3
		 GROUP BY bucket
		 ORDER BY bucket`,
		tenantID, tunnelID, since, bucketSec)
	if err != nil {
		return nil, fmt.Errorf("metrikler sorgulanamadi: %w", err)
	}
	defer rows.Close()

	var out []reqlog.MetricBucket
	for rows.Next() {
		var b reqlog.MetricBucket
		if err := rows.Scan(&b.Bucket, &b.Count, &b.ErrorCount, &b.AvgMs, &b.MaxMs, &b.BytesIn, &b.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InsertRequestLogs, istek kayitlarini tek batch'te yazar. id catisirsa
// (tekrar) yok sayar.
func (s *Store) InsertRequestLogs(ctx context.Context, entries []reqlog.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range entries {
		queueRequestLog(batch, e)
	}
	if err := s.execBatch(ctx, batch); err == nil {
		return nil
	}
	// Toplu yazim tek bir transaction gibi davranir: tek bozuk kayit (ornegin
	// temizlenemeyen bir deger) tum pencereyi dusurmesin diye tek tek yeniden dene.
	var firstErr error
	for _, e := range entries {
		b := &pgx.Batch{}
		queueRequestLog(b, e)
		if err := s.execBatch(ctx, b); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

const insertRequestLogSQL = `INSERT INTO request_logs
   (id, tenant_id, tunnel_id, hostname, client_ip, ts, method, path, status, duration_ms, bytes_in, bytes_out, reject_reason)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
 ON CONFLICT (id) DO NOTHING`

// pgText, Postgres text'in kabul etmedigi NUL baytini ve gecersiz UTF-8'i
// temizler (internet taramalari yol icinde %00 gonderir).
func pgText(v string) string {
	if strings.IndexByte(v, 0) >= 0 {
		v = strings.ReplaceAll(v, "\x00", "")
	}
	if !utf8.ValidString(v) {
		v = strings.ToValidUTF8(v, "\uFFFD")
	}
	return v
}

func queueRequestLog(b *pgx.Batch, e reqlog.Entry) {
	b.Queue(insertRequestLogSQL,
		e.ID, pgText(e.TenantID), pgText(e.TunnelID), pgText(e.Hostname), pgText(e.ClientIP), e.TS,
		pgText(e.Method), pgText(e.Path), e.Status, e.DurationMS, e.BytesIn, e.BytesOut, pgText(e.RejectReason))
}

func (s *Store) execBatch(ctx context.Context, b *pgx.Batch) error {
	br := s.pool.SendBatch(ctx, b)
	defer br.Close()
	for range b.Len() {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// QueryRequestLogs, gelismis filtrelerle en yeniden eskiye dogru kayit doner.
func (s *Store) QueryRequestLogs(ctx context.Context, f reqlog.Filter) ([]reqlog.Entry, error) {
	var where []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	if f.TenantID != "" {
		add("tenant_id = $%d", f.TenantID)
	}
	if f.TunnelID != "" {
		add("tunnel_id = $%d", f.TunnelID)
	}
	if f.Hostname != "" {
		add("hostname = $%d", f.Hostname)
	}
	if f.Method != "" {
		add("method = $%d", strings.ToUpper(f.Method))
	}
	if f.StatusMin > 0 {
		add("status >= $%d", f.StatusMin)
	}
	if f.StatusMax > 0 {
		add("status <= $%d", f.StatusMax)
	}
	if f.Query != "" {
		add("path ILIKE $%d", "%"+f.Query+"%")
	}
	if !f.Since.IsZero() {
		add("ts >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("ts <= $%d", f.Until)
	}
	if f.MinDurMS > 0 {
		add("duration_ms >= $%d", f.MinDurMS)
	}
	if f.MaxDurMS > 0 {
		add("duration_ms <= $%d", f.MaxDurMS)
	}

	if f.Reason != "" {
		add("reject_reason = $%d", f.Reason)
	} else if f.Rejected {
		where = append(where, "reject_reason <> ''")
	}

	sql := `SELECT id, tenant_id, tunnel_id, hostname, client_ip, ts, method, path, status, duration_ms, bytes_in, bytes_out, reject_reason
	        FROM request_logs`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY ts DESC"

	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	if f.Offset > 0 {
		args = append(args, f.Offset)
		sql += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]reqlog.Entry, 0, limit)
	for rows.Next() {
		var e reqlog.Entry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.TunnelID, &e.Hostname, &e.ClientIP,
			&e.TS, &e.Method, &e.Path, &e.Status, &e.DurationMS, &e.BytesIn, &e.BytesOut, &e.RejectReason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
