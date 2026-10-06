package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F04): Birlesik Policy motoru. Policy'ler (match -> action) tunele/hostname'e
// baglanir. Ingress router snapshot'i ListPolicyRoutes ile etkin policy'leri BIR KEZ
// okur; hot-path yalnizca parse edilmis kurallari uygular.

const policyCols = `id, tenant_id, project_id, name, config, enabled, priority, created_at, updated_at`

func scanPolicy(row pgx.Row, p *store.Policy) error {
	return row.Scan(&p.ID, &p.TenantID, &p.ProjectID, &p.Name, &p.Config, &p.Enabled, &p.Priority, &p.CreatedAt, &p.UpdatedAt)
}

func (s *Store) loadBindings(ctx context.Context, policyID string) ([]store.PolicyBinding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT policy_id, COALESCE(tunnel_id,''), COALESCE(hostname,'')
		 FROM policy_bindings WHERE policy_id = $1 ORDER BY created_at`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PolicyBinding
	for rows.Next() {
		var b store.PolicyBinding
		if err := rows.Scan(&b.PolicyID, &b.TunnelID, &b.Hostname); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) ListPolicies(ctx context.Context, tenantID, projectID string) ([]store.Policy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+policyCols+` FROM policies
		 WHERE tenant_id = $1 AND project_id = $2
		 ORDER BY priority ASC, created_at ASC`, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []store.Policy
	for rows.Next() {
		var p store.Policy
		if err := scanPolicy(rows, &p); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range list {
		b, err := s.loadBindings(ctx, list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].Bindings = b
	}
	if list == nil {
		list = []store.Policy{}
	}
	return list, nil
}

func (s *Store) GetPolicy(ctx context.Context, tenantID, id string) (store.Policy, error) {
	var p store.Policy
	err := scanPolicy(s.pool.QueryRow(ctx,
		`SELECT `+policyCols+` FROM policies WHERE tenant_id = $1 AND id = $2`, tenantID, id), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Policy{}, store.ErrNotFound
	}
	if err != nil {
		return store.Policy{}, err
	}
	b, err := s.loadBindings(ctx, p.ID)
	if err != nil {
		return store.Policy{}, err
	}
	p.Bindings = b
	return p, nil
}

func normalizePolicyConfig(config json.RawMessage) json.RawMessage {
	if len(config) == 0 {
		return json.RawMessage(`{}`)
	}
	return config
}

func (s *Store) CreatePolicy(ctx context.Context, tenantID, projectID, name string, config json.RawMessage, priority int) (store.Policy, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.Policy{}, errors.New("policy adı boş olamaz")
	}
	if priority == 0 {
		priority = 100
	}
	now := time.Now().UTC()
	p := store.Policy{
		ID:        newID("pol"),
		TenantID:  tenantID,
		ProjectID: projectID,
		Name:      name,
		Config:    normalizePolicyConfig(config),
		Enabled:   true,
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO policies (`+policyCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.TenantID, p.ProjectID, p.Name, p.Config, p.Enabled, p.Priority, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Policy{}, store.ErrPolicyNameTaken
		}
		return store.Policy{}, fmt.Errorf("policy oluşturulamadı: %w", err)
	}
	p.Bindings = []store.PolicyBinding{}
	return p, nil
}

func (s *Store) UpdatePolicy(ctx context.Context, tenantID, id, name string, config json.RawMessage, enabled bool, priority int) (store.Policy, error) {
	if priority == 0 {
		priority = 100
	}
	var p store.Policy
	err := scanPolicy(s.pool.QueryRow(ctx,
		`UPDATE policies SET name = $1, config = $2, enabled = $3, priority = $4, updated_at = now()
		 WHERE tenant_id = $5 AND id = $6
		 RETURNING `+policyCols,
		strings.TrimSpace(name), normalizePolicyConfig(config), enabled, priority, tenantID, id), &p)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Policy{}, store.ErrPolicyNameTaken
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Policy{}, store.ErrNotFound
	}
	if err != nil {
		return store.Policy{}, err
	}
	b, err := s.loadBindings(ctx, p.ID)
	if err != nil {
		return store.Policy{}, err
	}
	p.Bindings = b
	return p, nil
}

func (s *Store) DeletePolicy(ctx context.Context, tenantID, id string) error {
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM policies WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("policy silinemedi: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ensurePolicyOwned, policy'nin kiraciya ait oldugunu dogrular (IDOR koruması).
func (s *Store) ensurePolicyOwned(ctx context.Context, tenantID, policyID string) error {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM policies WHERE tenant_id = $1 AND id = $2)`,
		tenantID, policyID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) BindPolicy(ctx context.Context, tenantID, policyID string, b store.PolicyBinding) error {
	if err := s.ensurePolicyOwned(ctx, tenantID, policyID); err != nil {
		return err
	}
	tunnelID := nullIfEmpty(b.TunnelID)
	hostname := nullIfEmpty(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(b.Hostname), ".")))
	if tunnelID == nil && hostname == nil {
		return errors.New("bağlama için tunnel_id veya hostname gerekli")
	}
	// Kiraci izolasyonu (K3): baglanan tunel ve hostname de CAGIRAN kiraciya ait
	// olmali. Aksi halde bir kiraci kendi policy'sini baskasinin hostuna baglayip
	// o trafige deny/redirect/set_header uygulayabilirdi. Varligi sizdirmamak
	// icin yabanci kayit da "bulunamadi" (404) doner.
	if tunnelID != nil {
		owns, err := s.tunnelOwnedBy(ctx, tenantID, *tunnelID)
		if err != nil {
			return err
		}
		if !owns {
			return store.ErrNotFound
		}
	}
	if hostname != nil {
		var owns bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM hostnames WHERE lower(fqdn) = $1 AND tenant_id = $2)`,
			*hostname, tenantID).Scan(&owns); err != nil {
			return fmt.Errorf("hostname dogrulanamadi: %w", err)
		}
		if !owns {
			return store.ErrNotFound
		}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO policy_bindings (policy_id, tunnel_id, hostname)
		 VALUES ($1, COALESCE($2,''), COALESCE($3,''))
		 ON CONFLICT (policy_id, tunnel_id, hostname) DO NOTHING`,
		policyID, tunnelID, hostname)
	if err != nil {
		return fmt.Errorf("policy bağlanamadı: %w", err)
	}
	return nil
}

func (s *Store) UnbindPolicy(ctx context.Context, tenantID, policyID string, b store.PolicyBinding) error {
	if err := s.ensurePolicyOwned(ctx, tenantID, policyID); err != nil {
		return err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM policy_bindings
		 WHERE policy_id = $1 AND tunnel_id = $2 AND lower(hostname) = $3`,
		policyID, strings.TrimSpace(b.TunnelID),
		strings.ToLower(strings.TrimSuffix(strings.TrimSpace(b.Hostname), ".")))
	if err != nil {
		return fmt.Errorf("policy bağı kaldırılamadı: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListPolicyRoutes, TUM kiracilarin etkin policy'lerini host cozumuyle doner.
// hostname bagli ise dogrudan; tunnel_id bagli ise o tunelin tum FQDN'leri.
//
// Kiraci kilidi (K3): host YALNIZCA policy'nin kendi kiracisina ait bir hostnames
// kaydindan cozulur. Gecmiste denetimsiz eklenmis yabanci bir bag (baska kiracinin
// FQDN'i / tuneli) burada sessizce elenir; ingress'e hic ulasmaz.
// TunnelID, bag tunele yapilmissa doludur (yol yonlendirmesinde hedef tunelin
// policy'lerini bulmak icin).
func (s *Store) ListPolicyRoutes(ctx context.Context) ([]store.PolicyRoute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+prefixCols("p", policyCols)+`, h.fqdn AS host,
		        CASE WHEN b.hostname = '' THEN b.tunnel_id ELSE '' END AS tunnel_id
		 FROM policy_bindings b
		 JOIN policies p ON p.id = b.policy_id AND p.enabled = true
		 JOIN hostnames h ON h.tenant_id = p.tenant_id AND (
		        (b.hostname <> '' AND lower(h.fqdn) = lower(b.hostname))
		     OR (b.hostname = '' AND b.tunnel_id <> '' AND h.tunnel_id = b.tunnel_id))
		 LEFT JOIN tunnels t ON b.tunnel_id <> '' AND t.id = b.tunnel_id
		 WHERE b.tunnel_id = '' OR t.tenant_id = p.tenant_id
		 ORDER BY p.priority ASC, p.created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("policy yonlendirmeleri listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.PolicyRoute
	for rows.Next() {
		var pr store.PolicyRoute
		if err := rows.Scan(&pr.Policy.ID, &pr.Policy.TenantID, &pr.Policy.ProjectID, &pr.Policy.Name,
			&pr.Policy.Config, &pr.Policy.Enabled, &pr.Policy.Priority, &pr.Policy.CreatedAt, &pr.Policy.UpdatedAt,
			&pr.Host, &pr.TunnelID); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	if out == nil {
		out = []store.PolicyRoute{}
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// prefixCols, "a, b, c" listesini "p.a, p.b, p.c" haline getirir.
func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
