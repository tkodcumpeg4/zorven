package pgstore

// Ham TCP tunelleri icin dakikalik istatistikler (tcp_stats_minute, 0078).

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// InsertTCPStats, dakikalik ozetleri tek toplu islemde yazar. Ayni (tunel,
// dakika) ikinci kez gelirse sayaclar toplanir, tepe/en uzun deger en buyugu alir.
// Silinmis tunelin satiri sessizce atlanir.
func (s *Store) InsertTCPStats(ctx context.Context, stats []store.TCPStatMinute) error {
	if len(stats) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, st := range stats {
		b.Queue(`INSERT INTO tcp_stats_minute (tunnel_id, tenant_id, minute, conns_new, conns_peak,
		    conns_rejected, rejected_ip, rejected_door, rejected_offline, rejected_dial,
		    bytes_in, bytes_out, dur_sum_ms, dur_count, dur_max_ms)
		 SELECT $1::text, $2::text, $3::timestamptz, $4::bigint, $5::int,
		        $6::bigint, $7::bigint, $8::bigint, $9::bigint, $10::bigint,
		        $11::bigint, $12::bigint, $13::bigint, $14::bigint, $15::bigint
		 WHERE EXISTS (SELECT 1 FROM tunnels WHERE id = $1::text)
		 ON CONFLICT (tunnel_id, minute) DO UPDATE SET
		    conns_new = tcp_stats_minute.conns_new + EXCLUDED.conns_new,
		    conns_peak = GREATEST(tcp_stats_minute.conns_peak, EXCLUDED.conns_peak),
		    conns_rejected = tcp_stats_minute.conns_rejected + EXCLUDED.conns_rejected,
		    rejected_ip = tcp_stats_minute.rejected_ip + EXCLUDED.rejected_ip,
		    rejected_door = tcp_stats_minute.rejected_door + EXCLUDED.rejected_door,
		    rejected_offline = tcp_stats_minute.rejected_offline + EXCLUDED.rejected_offline,
		    rejected_dial = tcp_stats_minute.rejected_dial + EXCLUDED.rejected_dial,
		    bytes_in = tcp_stats_minute.bytes_in + EXCLUDED.bytes_in,
		    bytes_out = tcp_stats_minute.bytes_out + EXCLUDED.bytes_out,
		    dur_sum_ms = tcp_stats_minute.dur_sum_ms + EXCLUDED.dur_sum_ms,
		    dur_count = tcp_stats_minute.dur_count + EXCLUDED.dur_count,
		    dur_max_ms = GREATEST(tcp_stats_minute.dur_max_ms, EXCLUDED.dur_max_ms)`,
			st.TunnelID, st.TenantID, st.Minute.UTC().Truncate(time.Minute), st.ConnsNew, st.ConnsPeak,
			st.ConnsRejected, st.RejectedIP, st.RejectedDoor, st.RejectedOffline, st.RejectedDial,
			st.BytesIn, st.BytesOut, st.DurSumMs, st.DurCount, st.DurMaxMs)
	}
	br := s.pool.SendBatch(ctx, b)
	defer br.Close()
	for range stats {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("TCP istatistigi yazilamadi: %w", err)
		}
	}
	return nil
}

// ListTCPStats, tunelin since'ten bu yana dakikalik ozetleri (eskiden yeniye).
func (s *Store) ListTCPStats(ctx context.Context, tenantID, tunnelID string, since time.Time) ([]store.TCPStatMinute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT minute, conns_new, conns_peak, conns_rejected, rejected_ip, rejected_door,
		        rejected_offline, rejected_dial, bytes_in, bytes_out, dur_sum_ms, dur_count, dur_max_ms
		   FROM tcp_stats_minute
		  WHERE tunnel_id = $1 AND tenant_id = $2 AND minute >= $3
		  ORDER BY minute`, tunnelID, tenantID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TCPStatMinute{}
	for rows.Next() {
		st := store.TCPStatMinute{TunnelID: tunnelID, TenantID: tenantID}
		if err := rows.Scan(&st.Minute, &st.ConnsNew, &st.ConnsPeak, &st.ConnsRejected, &st.RejectedIP,
			&st.RejectedDoor, &st.RejectedOffline, &st.RejectedDial, &st.BytesIn, &st.BytesOut,
			&st.DurSumMs, &st.DurCount, &st.DurMaxMs); err != nil {
			return nil, err
		}
		if st.DurCount > 0 {
			st.DurAvgMs = float64(st.DurSumMs) / float64(st.DurCount)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// PruneTCPStats, saklama suresini asan ozetleri siler.
func (s *Store) PruneTCPStats(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tcp_stats_minute WHERE minute < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
