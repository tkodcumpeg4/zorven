package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ListDoorGrantsPage, tunelin kapi izinlerini (en yeni once) sayfalar.
func (s *Store) ListDoorGrantsPage(ctx context.Context, tenantID, tunnelID string, activeOnly bool, pg store.Page) ([]store.DoorGrant, int, error) {
	where := ` WHERE tenant_id=$1 AND tunnel_id=$2`
	if activeOnly {
		where += ` AND revoked_at IS NULL AND expires_at > now()`
	}
	total, err := s.countRows(ctx, `FROM tunnel_door_grants`+where, tenantID, tunnelID)
	if err != nil {
		return nil, 0, fmt.Errorf("kapi izinleri sayilamadi: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+grantCols+` FROM tunnel_door_grants`+where+` ORDER BY created_at DESC, id DESC`+limitClause(pg),
		tenantID, tunnelID)
	if err != nil {
		return nil, 0, fmt.Errorf("kapi izinleri listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.DoorGrant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if err := s.attachExtraIPs(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
