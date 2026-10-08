package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/accesslog"
)

const insertAccessEventSQL = `INSERT INTO tunnel_access_events
   (id, tenant_id, tunnel_id, hostname, method, provider, identity, success, reason, client_ip, user_agent, created_at, count)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
 ON CONFLICT (id) DO NOTHING`

func queueAccessEvent(b *pgx.Batch, e accesslog.Event) {
	ts := e.CreatedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	cnt := e.Count
	if cnt < 1 {
		cnt = 1
	}
	b.Queue(insertAccessEventSQL,
		pgText(e.ID), pgText(e.TenantID), pgText(e.TunnelID), pgText(e.Hostname),
		pgText(e.Method), pgText(e.Provider), pgText(e.Identity), e.Success, pgText(e.Reason),
		pgText(e.ClientIP), accesslog.TruncateUA(pgText(e.UserAgent)), ts, cnt)
}

// InsertAccessEvents, erisim olaylarini tek batch'te yazar. Toplu yazim basarisiz
// olursa tek tek yeniden dener (tek bozuk kayit tum pencereyi dusurmesin).
func (s *Store) InsertAccessEvents(ctx context.Context, events []accesslog.Event) error {
	if len(events) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range events {
		queueAccessEvent(batch, e)
	}
	if err := s.execBatch(ctx, batch); err == nil {
		return nil
	}
	var firstErr error
	for _, e := range events {
		b := &pgx.Batch{}
		queueAccessEvent(b, e)
		if err := s.execBatch(ctx, b); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ListAccessEvents, bir tunelin olaylarini en yeniden eskiye doner. beforeTS sifir
// degilse (beforeTS, beforeID) cifti ONCESINDEKI kayitlar gelir (keyset sayfalama).
// En fazla limit satir doner; cagiran daha fazlasi var mi anlamak icin limit+1 isteyebilir.
func (s *Store) ListAccessEvents(ctx context.Context, tenantID, tunnelID string, limit int, beforeTS time.Time, beforeID string) ([]accesslog.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, tenant_id, tunnel_id, hostname, method, provider, identity, success, reason, client_ip, user_agent, created_at, count
	      FROM tunnel_access_events
	      WHERE tenant_id=$1 AND tunnel_id=$2`
	args := []any{tenantID, tunnelID}
	if !beforeTS.IsZero() {
		q += ` AND (created_at, id) < ($3, $4)`
		args = append(args, beforeTS, beforeID)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("erisim olaylari sorgulanamadi: %w", err)
	}
	defer rows.Close()
	out := []accesslog.Event{}
	for rows.Next() {
		var e accesslog.Event
		if err := rows.Scan(&e.ID, &e.TenantID, &e.TunnelID, &e.Hostname, &e.Method, &e.Provider,
			&e.Identity, &e.Success, &e.Reason, &e.ClientIP, &e.UserAgent, &e.CreatedAt, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AccessEventSummary, since'ten bu yana ozet sayaclari doner.
func (s *Store) AccessEventSummary(ctx context.Context, tenantID, tunnelID string, since time.Time) (accesslog.Summary, error) {
	sum := accesslog.Summary{
		Since:      since,
		ByMethod:   map[string]int64{},
		ByProvider: map[string]int64{},
		ByReason:   map[string]int64{},
	}
	err := s.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE success),
		        count(*) FILTER (WHERE NOT success),
		        count(DISTINCT identity) FILTER (WHERE identity <> ''),
		        count(DISTINCT client_ip) FILTER (WHERE client_ip <> '')
		   FROM tunnel_access_events
		  WHERE tenant_id=$1 AND tunnel_id=$2 AND created_at >= $3
		    AND reason <> 'blocked_no_grant'`,
		tenantID, tunnelID, since).
		Scan(&sum.Total, &sum.Success, &sum.Failure, &sum.UniqueIdentities, &sum.UniqueIPs)
	if err != nil {
		return sum, fmt.Errorf("erisim ozeti okunamadi: %w", err)
	}
	for _, g := range []struct {
		col string
		dst map[string]int64
	}{{"method", sum.ByMethod}, {"provider", sum.ByProvider}, {"reason", sum.ByReason}} {
		rows, err := s.pool.Query(ctx,
			`SELECT `+g.col+`, count(*) FROM tunnel_access_events
			  WHERE tenant_id=$1 AND tunnel_id=$2 AND created_at >= $3 AND `+g.col+` <> ''
			    AND reason <> 'blocked_no_grant'
			  GROUP BY `+g.col, tenantID, tunnelID, since)
		if err != nil {
			return sum, fmt.Errorf("erisim ozeti okunamadi: %w", err)
		}
		for rows.Next() {
			var k string
			var n int64
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return sum, err
			}
			g.dst[k] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return sum, err
		}
	}
	err = s.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE reason = 'door_opened'),
		        COALESCE(sum(count) FILTER (WHERE reason = 'blocked_no_grant'), 0),
		        count(DISTINCT client_ip) FILTER (WHERE reason = 'blocked_no_grant' AND client_ip <> '')
		   FROM tunnel_access_events
		  WHERE tenant_id=$1 AND tunnel_id=$2 AND created_at >= $3`,
		tenantID, tunnelID, since).
		Scan(&sum.DoorGrants, &sum.DoorBlocked, &sum.DoorBlockedIPs)
	if err != nil {
		return sum, fmt.Errorf("erisim ozeti okunamadi: %w", err)
	}
	return sum, nil
}
