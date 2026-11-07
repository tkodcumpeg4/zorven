package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// limitClause, sorguya eklenecek " LIMIT n OFFSET m" ekini uretir. Sayilar
// int oldugundan enjeksiyon riski yoktur; negatif ofset 0'a kelepcelenir.
func limitClause(pg store.Page) string {
	if pg.Limit <= 0 {
		return ""
	}
	off := pg.Offset
	if off < 0 {
		off = 0
	}
	return fmt.Sprintf(" LIMIT %d OFFSET %d", pg.Limit, off)
}

// countRows, ayni FROM/WHERE ile satir sayisini doner ("SELECT count(*) " + tail).
func (s *Store) countRows(ctx context.Context, tail string, args ...any) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) "+tail, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
