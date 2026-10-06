package pgstore

// FAZ 4 / F24 — UDP sinirlari + dakikalik UDP istatistikleri.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const tunnelUDPCols = `tunnel_id, tenant_id, idle_timeout_sec, max_packet_bytes, max_pps, max_flow_pps, max_flows`

func scanTunnelUDP(row pgx.Row, u *store.TunnelUDP) error {
	return row.Scan(&u.TunnelID, &u.TenantID, &u.IdleTimeoutSec, &u.MaxPacketBytes,
		&u.MaxPPS, &u.MaxFlowPPS, &u.MaxFlows)
}

// GetTunnelUDP, tunelin UDP sinirlari. Kayit yoksa varsayilan.
func (s *Store) GetTunnelUDP(ctx context.Context, tenantID, tunnelID string) (store.TunnelUDP, error) {
	var u store.TunnelUDP
	err := scanTunnelUDP(s.pool.QueryRow(ctx,
		`SELECT `+tunnelUDPCols+` FROM tunnel_udp WHERE tunnel_id = $1 AND tenant_id = $2`,
		tunnelID, tenantID), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		d := store.DefaultTunnelUDP(tunnelID)
		d.TenantID = tenantID
		return d, nil
	}
	return u, err
}

// SetTunnelUDP, sinirlari yazar (upsert).
func (s *Store) SetTunnelUDP(ctx context.Context, tenantID string, u store.TunnelUDP) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_udp (tunnel_id, tenant_id, idle_timeout_sec, max_packet_bytes,
		    max_pps, max_flow_pps, max_flows, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		 ON CONFLICT (tunnel_id) DO UPDATE SET
		    idle_timeout_sec = EXCLUDED.idle_timeout_sec, max_packet_bytes = EXCLUDED.max_packet_bytes,
		    max_pps = EXCLUDED.max_pps, max_flow_pps = EXCLUDED.max_flow_pps,
		    max_flows = EXCLUDED.max_flows, updated_at = now()`,
		u.TunnelID, tenantID, u.IdleTimeoutSec, u.MaxPacketBytes, u.MaxPPS, u.MaxFlowPPS, u.MaxFlows)
	if err != nil {
		return fmt.Errorf("UDP ayari kaydedilemedi: %w", err)
	}
	return nil
}

// ListTunnelUDPs, tum kayitlar (rawproxy yenilemesinde tek sorgu).
func (s *Store) ListTunnelUDPs(ctx context.Context) ([]store.TunnelUDP, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tunnelUDPCols+` FROM tunnel_udp`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TunnelUDP{}
	for rows.Next() {
		var u store.TunnelUDP
		if err := scanTunnelUDP(rows, &u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// InsertUDPStats, dakikalik ozetleri tek toplu islemde yazar. Ayni (tunel,
// dakika) ikinci kez gelirse sayaclar toplanir, tepe flow en buyugu alir.
// Bu arada silinmis tunelin satiri sessizce atlanir (FK hatasi yerine).
func (s *Store) InsertUDPStats(ctx context.Context, stats []store.UDPStatMinute) error {
	if len(stats) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, st := range stats {
		b.Queue(`INSERT INTO udp_stats_minute (tunnel_id, tenant_id, minute, packets_in, packets_out,
		    bytes_in, bytes_out, flows_new, flows_peak, dropped_rate, dropped_size, dropped_flows)
		 SELECT $1::text, $2::text, $3::timestamptz, $4::bigint, $5::bigint, $6::bigint, $7::bigint,
		        $8::bigint, $9::int, $10::bigint, $11::bigint, $12::bigint
		 WHERE EXISTS (SELECT 1 FROM tunnels WHERE id = $1::text)
		 ON CONFLICT (tunnel_id, minute) DO UPDATE SET
		    packets_in = udp_stats_minute.packets_in + EXCLUDED.packets_in,
		    packets_out = udp_stats_minute.packets_out + EXCLUDED.packets_out,
		    bytes_in = udp_stats_minute.bytes_in + EXCLUDED.bytes_in,
		    bytes_out = udp_stats_minute.bytes_out + EXCLUDED.bytes_out,
		    flows_new = udp_stats_minute.flows_new + EXCLUDED.flows_new,
		    flows_peak = GREATEST(udp_stats_minute.flows_peak, EXCLUDED.flows_peak),
		    dropped_rate = udp_stats_minute.dropped_rate + EXCLUDED.dropped_rate,
		    dropped_size = udp_stats_minute.dropped_size + EXCLUDED.dropped_size,
		    dropped_flows = udp_stats_minute.dropped_flows + EXCLUDED.dropped_flows`,
			st.TunnelID, st.TenantID, st.Minute.UTC().Truncate(time.Minute), st.PacketsIn, st.PacketsOut,
			st.BytesIn, st.BytesOut, st.FlowsNew, st.FlowsPeak, st.DroppedRate, st.DroppedSize, st.DroppedFlows)
	}
	br := s.pool.SendBatch(ctx, b)
	defer br.Close()
	for range stats {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("UDP istatistigi yazilamadi: %w", err)
		}
	}
	return nil
}

// ListUDPStats, tunelin since'ten bu yana dakikalik ozetleri (eskiden yeniye).
func (s *Store) ListUDPStats(ctx context.Context, tenantID, tunnelID string, since time.Time) ([]store.UDPStatMinute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT minute, packets_in, packets_out, bytes_in, bytes_out, flows_new, flows_peak,
		        dropped_rate, dropped_size, dropped_flows
		   FROM udp_stats_minute
		  WHERE tunnel_id = $1 AND tenant_id = $2 AND minute >= $3
		  ORDER BY minute`, tunnelID, tenantID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.UDPStatMinute{}
	for rows.Next() {
		st := store.UDPStatMinute{TunnelID: tunnelID, TenantID: tenantID}
		if err := rows.Scan(&st.Minute, &st.PacketsIn, &st.PacketsOut, &st.BytesIn, &st.BytesOut,
			&st.FlowsNew, &st.FlowsPeak, &st.DroppedRate, &st.DroppedSize, &st.DroppedFlows); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// PruneUDPStats, saklama suresini asan ozetleri siler.
func (s *Store) PruneUDPStats(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM udp_stats_minute WHERE minute < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
