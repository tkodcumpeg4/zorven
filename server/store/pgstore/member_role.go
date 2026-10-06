package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetMemberRole, kullanicinin kiracidaki rolunu doner (owner | admin | member).
// Uyelik yoksa ("", nil) doner: rol bilinmiyorsa ayricalik da YOKTUR.
//
// Neden gerekli: API token'lari kontekste "api_token" rolu ile gelir. Gercek
// rol cozulmezse varsayilan DENY altinda token sahibi (owner bile) cihazlarina
// erisemezdi.
func (s *Store) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT "role" FROM "member" WHERE "organizationId" = $1 AND "userId" = $2 LIMIT 1`,
		tenantID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}
