package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/netctl"
)

// NetStore, netctl.Store'un Postgres uygulamasi (Zorven Net, migration 0077).
// store.Store arayuzune DOKUNMAZ: ayri kucuk bir tur.
type NetStore struct{ pool *pgxpool.Pool }

var _ netctl.Store = (*NetStore)(nil)

// Net, netctl icin kalicilik deposunu doner.
func (s *Store) Net() *NetStore { return &NetStore{pool: s.pool} }

func (n *NetStore) ListNodes(ctx context.Context, tenantID string) ([]netctl.NodeRec, error) {
	rows, err := n.pool.Query(ctx,
		`SELECT id, kind, coalesce(client_id,''), name, host(ip), wg_pub, created_at, last_seen_at
		   FROM wg_nodes WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netctl.NodeRec
	for rows.Next() {
		r := netctl.NodeRec{TenantID: tenantID}
		var ip string
		var seen *time.Time
		if err := rows.Scan(&r.ID, &r.Kind, &r.ClientID, &r.Name, &ip, &r.WGPub, &r.CreatedAt, &seen); err != nil {
			return nil, err
		}
		a, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		r.IP = a
		if seen != nil {
			r.LastSeen = *seen
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (n *NetStore) PutNode(ctx context.Context, r netctl.NodeRec) error {
	var seen *time.Time
	if !r.LastSeen.IsZero() {
		seen = &r.LastSeen
	}
	var cid *string
	if r.ClientID != "" {
		cid = &r.ClientID
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := n.pool.Exec(ctx,
		`INSERT INTO wg_nodes (id, tenant_id, kind, client_id, name, ip, wg_pub, created_at, last_seen_at)
		 VALUES ($1,$2,$3,$4,$5,$6::inet,$7,$8,$9)
		 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, wg_pub = EXCLUDED.wg_pub,
		   last_seen_at = EXCLUDED.last_seen_at`,
		r.ID, r.TenantID, r.Kind, cid, r.Name, r.IP.String(), r.WGPub, created, seen)
	return err
}

func (n *NetStore) DeleteNode(ctx context.Context, tenantID, id string) error {
	_, err := n.pool.Exec(ctx, `DELETE FROM wg_nodes WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	return err
}

func (n *NetStore) ListShares(ctx context.Context, tenantID string) ([]netctl.Share, error) {
	rows, err := n.pool.Query(ctx,
		`SELECT id, owner_client_id, name, proto, port, grantees, ephemeral, expires_at, created_by, created_at
		   FROM wg_shares WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netctl.Share
	for rows.Next() {
		s := netctl.Share{TenantID: tenantID}
		var gj []byte
		if err := rows.Scan(&s.ID, &s.OwnerClientID, &s.Name, &s.Proto, &s.Port, &gj, &s.Ephemeral,
			&s.ExpiresAt, &s.CreatedBy, &s.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(gj, &s.Grantees)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (n *NetStore) PutShare(ctx context.Context, s netctl.Share) error {
	gj, err := json.Marshal(s.Grantees)
	if err != nil {
		return err
	}
	_, err = n.pool.Exec(ctx,
		`INSERT INTO wg_shares (id, tenant_id, owner_client_id, name, proto, port, grantees, ephemeral, expires_at, created_by, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11)
		 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, grantees = EXCLUDED.grantees,
		   expires_at = EXCLUDED.expires_at`,
		s.ID, s.TenantID, s.OwnerClientID, s.Name, s.Proto, s.Port, string(gj), s.Ephemeral, s.ExpiresAt, s.CreatedBy, s.CreatedAt)
	return err
}

func (n *NetStore) DeleteShare(ctx context.Context, tenantID, id string) error {
	_, err := n.pool.Exec(ctx, `DELETE FROM wg_shares WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	return err
}

// --- Mod B (W3, migration 0090): kiraci agi + izin kurallari -----------------------

var _ netctl.TunStore = (*NetStore)(nil)

func (n *NetStore) GetNetwork(ctx context.Context, tenantID string) (netip.Prefix, bool, error) {
	var c string
	err := n.pool.QueryRow(ctx, `SELECT cidr::text FROM wg_networks WHERE tenant_id = $1`, tenantID).Scan(&c)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return netip.Prefix{}, false, nil
		}
		return netip.Prefix{}, false, err
	}
	p, err := netip.ParsePrefix(c)
	if err != nil {
		return netip.Prefix{}, false, nil
	}
	return p, true, nil
}

func (n *NetStore) PutNetwork(ctx context.Context, tenantID string, cidr netip.Prefix) error {
	_, err := n.pool.Exec(ctx,
		`INSERT INTO wg_networks (tenant_id, cidr) VALUES ($1, $2::cidr)
		 ON CONFLICT (tenant_id) DO UPDATE SET cidr = EXCLUDED.cidr, updated_at = now()`,
		tenantID, cidr.Masked().String())
	return err
}

func (n *NetStore) ListTunRules(ctx context.Context, tenantID string) ([]netctl.TunRule, error) {
	rows, err := n.pool.Query(ctx,
		`SELECT id, name, src_type, src_id, dst, proto, ports, created_by, created_at
		   FROM wg_tun_rules WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netctl.TunRule
	for rows.Next() {
		r := netctl.TunRule{TenantID: tenantID}
		if err := rows.Scan(&r.ID, &r.Name, &r.Src.Type, &r.Src.ID, &r.Dst, &r.Proto, &r.Ports, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (n *NetStore) PutTunRule(ctx context.Context, r netctl.TunRule) error {
	_, err := n.pool.Exec(ctx,
		`INSERT INTO wg_tun_rules (id, tenant_id, name, src_type, src_id, dst, proto, ports, created_by, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, src_type = EXCLUDED.src_type,
		   src_id = EXCLUDED.src_id, dst = EXCLUDED.dst, proto = EXCLUDED.proto, ports = EXCLUDED.ports`,
		r.ID, r.TenantID, r.Name, r.Src.Type, r.Src.ID, r.Dst, r.Proto, r.Ports, r.CreatedBy, r.CreatedAt)
	return err
}

func (n *NetStore) DeleteTunRule(ctx context.Context, tenantID, id string) error {
	_, err := n.pool.Exec(ctx, `DELETE FROM wg_tun_rules WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	return err
}

// --- Z5: Mod B erisim gunlugu (migration 0100) -------------------------------------

var _ netctl.AccessStore = (*NetStore)(nil)

func (n *NetStore) AppendAccess(ctx context.Context, tenantID string, recs []netctl.AccessRecord) error {
	if len(recs) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, r := range recs {
		b.Queue(`INSERT INTO wg_access_events
		  (tenant_id, at, src_ip, src_name, src_client_id, dst_ip, dst_name, dst_client_id, proto, port, decision, rule, resource, flows, packets, bytes)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			tenantID, r.At, r.SrcIP, r.SrcName, r.SrcClientID, r.DstIP, r.DstName, r.DstClientID, r.Proto, r.Port,
			r.Decision, r.Rule, r.Resource, r.Flows, r.Packets, r.Bytes)
	}
	br := n.pool.SendBatch(ctx, b)
	defer br.Close()
	for range recs {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (n *NetStore) ListAccess(ctx context.Context, tenantID string, q netctl.AccessQuery) ([]netctl.AccessRecord, error) {
	args := []any{tenantID}
	where := "tenant_id = $1"
	add := func(cond string, v any) {
		args = append(args, v)
		where += " AND " + strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args)))
	}
	if q.Decision != "" {
		add("decision = ?", q.Decision)
	}
	if q.Resource != "" {
		add("resource = ?", q.Resource)
	}
	if q.Node != "" {
		args = append(args, q.Node)
		i := strconv.Itoa(len(args))
		where += " AND (src_name = $" + i + " OR dst_name = $" + i + " OR src_client_id = $" + i + " OR dst_client_id = $" + i + ")"
	}
	if !q.Since.IsZero() {
		add("at >= ?", q.Since)
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := n.pool.Query(ctx,
		`SELECT id, at, src_ip, src_name, src_client_id, dst_ip, dst_name, dst_client_id, proto, port, decision, rule, resource, flows, packets, bytes
		   FROM wg_access_events WHERE `+where+` ORDER BY at DESC, id DESC LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netctl.AccessRecord
	for rows.Next() {
		r := netctl.AccessRecord{TenantID: tenantID}
		if err := rows.Scan(&r.ID, &r.At, &r.SrcIP, &r.SrcName, &r.SrcClientID, &r.DstIP, &r.DstName, &r.DstClientID,
			&r.Proto, &r.Port, &r.Decision, &r.Rule, &r.Resource, &r.Flows, &r.Packets, &r.Bytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (n *NetStore) PruneAccess(ctx context.Context, before time.Time) error {
	_, err := n.pool.Exec(ctx, `DELETE FROM wg_access_events WHERE at < $1`, before)
	return err
}
