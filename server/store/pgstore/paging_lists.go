package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Sayfali liste yardimcilari (sayfalama sozlesmesi: .planning/2026-10-27-sayfalama).
// Hepsi (sayfa, toplam) doner; siralama KARARLI (son anahtar id) ki sayfalar
// kaymasin. Toplam, ayni filtreyle COUNT'tur.

func (s *Store) ListTunnelsPage(ctx context.Context, tenantID, projectID string, pg store.Page) ([]store.Tunnel, int, error) {
	where, args := `WHERE tenant_id = $1`, []any{tenantID}
	if projectID != "" {
		where += ` AND project_id = $2`
		args = append(args, projectID)
	}
	total, err := s.countRows(ctx, `FROM tunnels `+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("tunel sayisi okunamadi: %w", err)
	}
	list, err := s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels `+where+` ORDER BY created_at, id`+limitClause(pg), args...)
	return list, total, err
}

func (s *Store) ListHostnamesPage(ctx context.Context, tenantID, projectID string, pg store.Page) ([]store.Hostname, int, error) {
	where, args := `WHERE tenant_id = $1`, []any{tenantID}
	if projectID != "" {
		where += ` AND project_id = $2`
		args = append(args, projectID)
	}
	total, err := s.countRows(ctx, `FROM hostnames `+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("hostname sayisi okunamadi: %w", err)
	}
	list, err := s.queryHostnames(ctx,
		`SELECT `+hostnameCols+` FROM hostnames `+where+` ORDER BY created_at, id`+limitClause(pg), args...)
	return list, total, err
}

func (s *Store) ListProjectsPage(ctx context.Context, tenantID string, pg store.Page) ([]store.Project, int, error) {
	total, err := s.countRows(ctx, `FROM projects WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+projectCols+` FROM projects WHERE tenant_id = $1 ORDER BY created_at ASC, id`+limitClause(pg), tenantID)
	if err != nil {
		return nil, 0, fmt.Errorf("projeler listelenemedi: %w", err)
	}
	defer rows.Close()
	list := []store.Project{}
	for rows.Next() {
		var p store.Project
		if err := scanProject(rows, &p); err != nil {
			return nil, 0, err
		}
		list = append(list, p)
	}
	return list, total, rows.Err()
}

func (s *Store) ListSecretsPage(ctx context.Context, tenantID, projectID string, pg store.Page) ([]store.Secret, int, error) {
	total, err := s.countRows(ctx, `FROM secrets WHERE tenant_id = $1 AND project_id = $2`, tenantID, projectID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+secretMetaCols+` FROM secrets
		 WHERE tenant_id = $1 AND project_id = $2
		 ORDER BY created_at DESC, id DESC`+limitClause(pg), tenantID, projectID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []store.Secret{}
	for rows.Next() {
		var sec store.Secret
		if err := scanSecretMeta(rows, &sec); err != nil {
			return nil, 0, err
		}
		list = append(list, sec)
	}
	return list, total, rows.Err()
}

func (s *Store) ListPoliciesPage(ctx context.Context, tenantID, projectID string, pg store.Page) ([]store.Policy, int, error) {
	total, err := s.countRows(ctx, `FROM policies WHERE tenant_id = $1 AND project_id = $2`, tenantID, projectID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+policyCols+` FROM policies
		 WHERE tenant_id = $1 AND project_id = $2
		 ORDER BY priority ASC, created_at ASC, id ASC`+limitClause(pg), tenantID, projectID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []store.Policy{}
	for rows.Next() {
		var p store.Policy
		if err := scanPolicy(rows, &p); err != nil {
			return nil, 0, err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	for i := range list {
		b, err := s.loadBindings(ctx, list[i].ID)
		if err != nil {
			return nil, 0, err
		}
		list[i].Bindings = b
	}
	return list, total, nil
}

func (s *Store) ListAPITokensPage(ctx context.Context, tenantID string, pg store.Page) ([]store.APIToken, int, error) {
	total, err := s.countRows(ctx, `FROM api_tokens WHERE tenant_id = $1 AND revoked_at IS NULL`, tenantID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+apiTokenCols+`
		 FROM api_tokens
		 WHERE tenant_id = $1 AND revoked_at IS NULL
		 ORDER BY created_at DESC, id DESC`+limitClause(pg), tenantID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	tokens := []store.APIToken{}
	for rows.Next() {
		var t store.APIToken
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.TokenID, &t.TokenHash,
			&t.TokenPrefix, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		tokens = append(tokens, t)
	}
	return tokens, total, rows.Err()
}

func (s *Store) ListIPRulesPage(ctx context.Context, tenantID string, tunnelID *string, pg store.Page) ([]store.IPAllowlistRule, int, error) {
	where, args := `WHERE tenant_id = $1`, []any{tenantID}
	if tunnelID != nil && *tunnelID != "" {
		where += ` AND (tunnel_id = $2 OR tunnel_id IS NULL)`
		args = append(args, *tunnelID)
	}
	total, err := s.countRows(ctx, `FROM ip_allowlist_rules `+where, args...)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+ipRuleCols+` FROM ip_allowlist_rules `+where+` ORDER BY created_at DESC, id DESC`+limitClause(pg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []store.IPAllowlistRule{}
	for rows.Next() {
		var r store.IPAllowlistRule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.TunnelID, &r.CIDR, &r.Description,
			&r.Enabled, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		list = append(list, r)
	}
	return list, total, rows.Err()
}

// ListTeamMembersPage, aktif uyeler (once) + bekleyen davetler (sonra) birlesik
// listesini sayfalar; toplam ikisinin toplamidir.
func (s *Store) ListTeamMembersPage(ctx context.Context, tenantID string, pg store.Page) ([]store.TeamMember, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM "member" WHERE "organizationId" = $1)
		      + (SELECT count(*) FROM "invitation" WHERE "organizationId" = $1 AND "status" = 'pending')`,
		tenantID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ekip uyeleri sayilamadi: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, email, name, role, created_at, tokens_count, status FROM (
		  SELECT m."id" AS id, m."userId" AS user_id, u."email" AS email, u."name" AS name,
		         m."role" AS role, m."createdAt" AS created_at,
		         COALESCE(c.token_count, 0)::int AS tokens_count, 'active' AS status, 0 AS ord
		  FROM "member" m
		  JOIN "user" u ON m."userId" = u."id"
		  LEFT JOIN (
		    SELECT user_id, count(*) AS token_count FROM clients
		    WHERE tenant_id = $1 AND user_id IS NOT NULL GROUP BY user_id
		  ) c ON c.user_id = m."userId"
		  WHERE m."organizationId" = $1
		  UNION ALL
		  SELECT i."id", '', i."email", split_part(i."email", '@', 1),
		         COALESCE(i."role", 'member'), i."createdAt", 0, 'pending', 1
		  FROM "invitation" i
		  WHERE i."organizationId" = $1 AND i."status" = 'pending'
		) x ORDER BY ord, created_at ASC, id`+limitClause(pg), tenantID)
	if err != nil {
		return nil, 0, fmt.Errorf("ekip uyeleri listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.TeamMember{}
	for rows.Next() {
		var m store.TeamMember
		if err := rows.Scan(&m.ID, &m.UserID, &m.Email, &m.Name, &m.Role, &m.CreatedAt, &m.TokensCount, &m.Status); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *Store) AdminListTenantsWithCountsPage(ctx context.Context, pg store.Page) ([]store.TenantWithCounts, int, error) {
	total, err := s.countRows(ctx, `FROM tenants`)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.slug, t.created_at,
		       (SELECT count(*) FROM clients WHERE tenant_id = t.id),
		       (SELECT count(*) FROM tunnels WHERE tenant_id = t.id),
		       (SELECT count(*) FROM hostnames WHERE tenant_id = t.id),
		       COALESCE(s.plan, 'free')
		FROM tenants t
		LEFT JOIN subscriptions s ON s.tenant_id = t.id
		ORDER BY t.created_at DESC, t.id DESC`+limitClause(pg))
	if err != nil {
		return nil, 0, fmt.Errorf("kiracilar ve sayilari listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.TenantWithCounts{}
	for rows.Next() {
		var item store.TenantWithCounts
		if err := rows.Scan(
			&item.ID, &item.Slug, &item.CreatedAt,
			&item.ClientsCount, &item.TunnelsCount, &item.HostnamesCount,
			&item.Plan,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (s *Store) AdminListAllClientsPage(ctx context.Context, pg store.Page) ([]store.ClientWithTenant, int, error) {
	total, err := s.countRows(ctx, `FROM clients c JOIN tenants t ON t.id = c.tenant_id`)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.tenant_id, c.name, c.token_id, c.token_hash, c.created_at, t.slug
		FROM clients c
		JOIN tenants t ON t.id = c.tenant_id
		ORDER BY c.created_at DESC, c.id DESC`+limitClause(pg))
	if err != nil {
		return nil, 0, fmt.Errorf("tum istemciler listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.ClientWithTenant{}
	for rows.Next() {
		var item store.ClientWithTenant
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Name, &item.TokenID, &item.TokenHash, &item.CreatedAt,
			&item.TenantSlug,
		); err != nil {
			return nil, 0, err
		}
		item.Status = "offline"
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (s *Store) AdminListAllHostnamesPage(ctx context.Context, pg store.Page) ([]store.HostnameWithTenant, int, error) {
	total, err := s.countRows(ctx, `FROM hostnames h JOIN tenants t ON t.id = h.tenant_id`)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT h.id, h.tenant_id, COALESCE(h.tunnel_id, ''), h.fqdn, h.type, h.verified, COALESCE(h.verify_token, ''), h.created_at,
		       t.slug, COALESCE(tu.target, '')
		FROM hostnames h
		JOIN tenants t ON t.id = h.tenant_id
		LEFT JOIN tunnels tu ON tu.id = h.tunnel_id
		ORDER BY h.created_at DESC, h.id DESC`+limitClause(pg))
	if err != nil {
		return nil, 0, fmt.Errorf("tum domainler listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.HostnameWithTenant{}
	for rows.Next() {
		var item store.HostnameWithTenant
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.TunnelID, &item.FQDN, &item.Type, &item.Verified, &item.VerifyToken, &item.CreatedAt,
			&item.TenantSlug, &item.Target,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}
