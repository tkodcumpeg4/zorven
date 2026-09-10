package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const ipRuleCols = `id, tenant_id, tunnel_id, cidr, description, enabled, created_at, updated_at`

func (s *Store) CreateIPRule(ctx context.Context, tenantID string, tunnelID *string, cidr, description string) (store.IPAllowlistRule, error) {
	now := time.Now().UTC()
	rule := store.IPAllowlistRule{
		ID:          newID("ipr"),
		TenantID:    tenantID,
		TunnelID:    tunnelID,
		CIDR:        cidr,
		Description: description,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO ip_allowlist_rules (id, tenant_id, tunnel_id, cidr, description, enabled, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rule.ID, rule.TenantID, rule.TunnelID, rule.CIDR, rule.Description, rule.Enabled, rule.CreatedAt, rule.UpdatedAt)
	if err != nil {
		return store.IPAllowlistRule{}, fmt.Errorf("ip izin kurali olusturulamadi: %w", err)
	}
	return rule, nil
}

func (s *Store) ListIPRules(ctx context.Context, tenantID string, tunnelID *string) ([]store.IPAllowlistRule, error) {
	var rows pgx.Rows
	var err error
	if tunnelID != nil && *tunnelID != "" {
		rows, err = s.pool.Query(ctx,
			`SELECT `+ipRuleCols+`
			 FROM ip_allowlist_rules
			 WHERE tenant_id = $1 AND (tunnel_id = $2 OR tunnel_id IS NULL)
			 ORDER BY created_at DESC`, tenantID, *tunnelID)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT `+ipRuleCols+`
			 FROM ip_allowlist_rules
			 WHERE tenant_id = $1
			 ORDER BY created_at DESC`, tenantID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []store.IPAllowlistRule
	for rows.Next() {
		var r store.IPAllowlistRule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.TunnelID, &r.CIDR, &r.Description,
			&r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if list == nil {
		list = []store.IPAllowlistRule{}
	}
	return list, rows.Err()
}

func (s *Store) ListAllActiveIPRules(ctx context.Context) ([]store.IPAllowlistRule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+ipRuleCols+`
		 FROM ip_allowlist_rules
		 WHERE enabled = true
		 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []store.IPAllowlistRule
	for rows.Next() {
		var r store.IPAllowlistRule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.TunnelID, &r.CIDR, &r.Description,
			&r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if list == nil {
		list = []store.IPAllowlistRule{}
	}
	return list, rows.Err()
}

func (s *Store) UpdateIPRule(ctx context.Context, tenantID, id string, enabled *bool, description *string) (store.IPAllowlistRule, error) {
	var current store.IPAllowlistRule
	err := s.pool.QueryRow(ctx,
		`SELECT `+ipRuleCols+`
		 FROM ip_allowlist_rules
		 WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&current.ID, &current.TenantID, &current.TunnelID, &current.CIDR,
			&current.Description, &current.Enabled, &current.CreatedAt, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.IPAllowlistRule{}, store.ErrNotFound
	}
	if err != nil {
		return store.IPAllowlistRule{}, err
	}

	if enabled != nil {
		current.Enabled = *enabled
	}
	if description != nil {
		current.Description = *description
	}
	current.UpdatedAt = time.Now().UTC()

	_, err = s.pool.Exec(ctx,
		`UPDATE ip_allowlist_rules
		 SET enabled = $1, description = $2, updated_at = $3
		 WHERE id = $4 AND tenant_id = $5`,
		current.Enabled, current.Description, current.UpdatedAt, id, tenantID)
	if err != nil {
		return store.IPAllowlistRule{}, err
	}
	return current, nil
}

func (s *Store) DeleteIPRule(ctx context.Context, tenantID, id string) error {
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM ip_allowlist_rules WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
