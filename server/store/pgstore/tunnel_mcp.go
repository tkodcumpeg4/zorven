package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// MCP tunel modu deposu (MCP M3/M4). Trafik politikasiyla ayni desen: tunel basina
// bir satir; her islem kiraci sahipligini dogrular.

// MaxMCPKeysPerTunnel, tunel basina en fazla API anahtari.
const MaxMCPKeysPerTunnel = 20

func (s *Store) tunnelOwned(ctx context.Context, tenantID, tunnelID string) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		tunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) GetTunnelMCP(ctx context.Context, tenantID, tunnelID string) (store.TunnelMCP, error) {
	m := store.TunnelMCP{TunnelID: tunnelID, Config: json.RawMessage("{}")}
	var cfg []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(m.config,'{}'::jsonb), COALESCE(m.enabled,false)
		 FROM tunnels t
		 LEFT JOIN tunnel_mcp m ON m.tunnel_id = t.id
		 WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).Scan(&cfg, &m.Enabled)
	if err != nil {
		return store.TunnelMCP{}, store.ErrNotFound
	}
	m.Config = json.RawMessage(cfg)
	return m, nil
}

func (s *Store) SetTunnelMCP(ctx context.Context, tenantID string, m store.TunnelMCP) error {
	if err := s.tunnelOwned(ctx, tenantID, m.TunnelID); err != nil {
		return err
	}
	cfg := m.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_mcp (tunnel_id, config, enabled, updated_at)
		 VALUES ($1,$2,$3, now())
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET config=EXCLUDED.config, enabled=EXCLUDED.enabled, updated_at=now()`,
		m.TunnelID, []byte(cfg), m.Enabled); err != nil {
		return fmt.Errorf("mcp ayari kaydedilemedi: %w", err)
	}
	return nil
}

func (s *Store) CreateTunnelMCPKey(ctx context.Context, tenantID string, k store.MCPKey) error {
	if err := s.tunnelOwned(ctx, tenantID, k.TunnelID); err != nil {
		return err
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tunnel_mcp_keys WHERE tunnel_id=$1`, k.TunnelID).Scan(&n); err != nil {
		return err
	}
	if n >= MaxMCPKeysPerTunnel {
		return store.ErrMCPKeyLimit
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_mcp_keys (id, tunnel_id, tenant_id, label, prefix, key_hash, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		k.ID, k.TunnelID, tenantID, k.Label, k.Prefix, k.KeyHash, k.ExpiresAt)
	if err != nil {
		return fmt.Errorf("mcp anahtari eklenemedi: %w", err)
	}
	return nil
}

func (s *Store) ListTunnelMCPKeys(ctx context.Context, tenantID, tunnelID string) ([]store.MCPKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tunnel_id, label, prefix, created_at, expires_at
		 FROM tunnel_mcp_keys WHERE tunnel_id=$1 AND tenant_id=$2 ORDER BY created_at`, tunnelID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.MCPKey{}
	for rows.Next() {
		var k store.MCPKey
		if err := rows.Scan(&k.ID, &k.TunnelID, &k.Label, &k.Prefix, &k.CreatedAt, &k.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTunnelMCPKey(ctx context.Context, tenantID, tunnelID, keyID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tunnel_mcp_keys WHERE id=$1 AND tunnel_id=$2 AND tenant_id=$3`, keyID, tunnelID, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) InsertMCPCalls(ctx context.Context, calls []store.MCPCall) error {
	if len(calls) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, c := range calls {
		ts := c.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		// Tunel silinmis olabilir: sahiplik dogrulamasi INSERT ... SELECT ile.
		if _, err := tx.Exec(ctx,
			`INSERT INTO mcp_calls (tenant_id, tunnel_id, ts, session, client, principal, method, tool, status, error_code, duration_ms, bytes_in, bytes_out)
			 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13
			 WHERE EXISTS (SELECT 1 FROM tunnels WHERE id=$2 AND tenant_id=$1)`,
			c.TenantID, c.TunnelID, ts, c.Session, c.Client, c.Principal, c.Method, c.Tool, c.Status,
			c.ErrorCode, c.DurationMS, c.BytesIn, c.BytesOut); err != nil {
			return fmt.Errorf("mcp cagrisi yazilamadi: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListMCPCalls(ctx context.Context, tenantID, tunnelID string, f store.MCPCallFilter) ([]store.MCPCall, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	since := f.Since
	if since.IsZero() {
		since = time.Unix(0, 0)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, tunnel_id, ts, session, client, principal, method, tool, status, error_code, duration_ms, bytes_in, bytes_out
		 FROM mcp_calls
		 WHERE tenant_id=$1 AND tunnel_id=$2 AND ts >= $3
		   AND ($4 = '' OR tool = $4) AND ($5 = '' OR status = $5)
		 ORDER BY ts DESC, id DESC LIMIT $6 OFFSET $7`,
		tenantID, tunnelID, since, f.Tool, f.Status, limit, max(f.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.MCPCall{}
	for rows.Next() {
		var c store.MCPCall
		if err := rows.Scan(&c.ID, &c.TunnelID, &c.TS, &c.Session, &c.Client, &c.Principal, &c.Method, &c.Tool,
			&c.Status, &c.ErrorCode, &c.DurationMS, &c.BytesIn, &c.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MCPToolStats(ctx context.Context, tenantID, tunnelID string, since time.Time) ([]store.MCPToolStat, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tool, count(*),
		        count(*) FILTER (WHERE status = 'error'),
		        count(*) FILTER (WHERE status IN ('denied','rate_limited')),
		        COALESCE(avg(duration_ms),0)::float8,
		        COALESCE(max(duration_ms),0),
		        COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms),0)
		 FROM mcp_calls
		 WHERE tenant_id=$1 AND tunnel_id=$2 AND ts >= $3 AND method = 'tools/call' AND tool <> ''
		 GROUP BY tool ORDER BY count(*) DESC, tool LIMIT 200`, tenantID, tunnelID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.MCPToolStat{}
	for rows.Next() {
		var st store.MCPToolStat
		if err := rows.Scan(&st.Tool, &st.Calls, &st.Errors, &st.Denied, &st.AvgMS, &st.MaxMS, &st.P95MS); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
