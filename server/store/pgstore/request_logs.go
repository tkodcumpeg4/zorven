package pgstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// InsertRequestLogs, istek kayitlarini tek batch'te yazar. id catisirsa
// (tekrar) yok sayar.
func (s *Store) InsertRequestLogs(ctx context.Context, entries []reqlog.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO request_logs
			   (id, tenant_id, tunnel_id, hostname, client_ip, ts, method, path, status, duration_ms, bytes_in, bytes_out)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			 ON CONFLICT (id) DO NOTHING`,
			e.ID, e.TenantID, e.TunnelID, e.Hostname, e.ClientIP, e.TS,
			e.Method, e.Path, e.Status, e.DurationMS, e.BytesIn, e.BytesOut)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range batch.Len() {
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

	sql := `SELECT id, tenant_id, tunnel_id, hostname, client_ip, ts, method, path, status, duration_ms, bytes_in, bytes_out
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
			&e.TS, &e.Method, &e.Path, &e.Status, &e.DurationMS, &e.BytesIn, &e.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
