package pgstore

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

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
