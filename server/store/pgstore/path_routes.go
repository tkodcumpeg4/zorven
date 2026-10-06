package pgstore

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.5 — yol tabanlı yönlendirme deposu.

// ListPathRouteEntries, tüm yol kurallarını router snapshot'ı için döner. Her
// satır, kuralın HEDEF tünelinin tam HostRoute verisini taşır (ListHostRoutes ile
// aynı kolonlar) + fqdn ve path_prefix. Böylece ingress, yola göre seçtiği tünelin
// erişim/trafik/replika bilgisine de sahip olur.
func (s *Store) ListPathRouteEntries(ctx context.Context) ([]store.HostRoute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT pr.fqdn, t.tenant_id, t.id, t.client_id, t.target, t.enabled,
		        t.proto, t.exposure,
		        COALESCE(ap.mode,'none'), COALESCE(ap.config,'{}'::jsonb), COALESCE(ap.enabled,false),
		        COALESCE(sub.plan,'free'), t.frozen,
		        COALESCE(rep.clients, '{}') AS replica_clients,
		        COALESCE(tp.config,'{}'::jsonb), COALESCE(tp.enabled,false),
		        COALESCE(mt.enabled,false), COALESCE(mt.ca_pem,''),
		        pr.path_prefix
		 FROM tunnel_path_routes pr
		 JOIN tunnels t ON t.id = pr.tunnel_id AND t.tenant_id = pr.tenant_id
		 -- O3: yol kurali YALNIZCA kiracinin kendi, yonlendirilebilir adinda
		 -- calisir. Dogrulanmamis custom domain (sahipligi kanitlanmamis) uzerinden
		 -- yol yonlendirmesiyle trafik alinamaz (ListHostRoutes ile ayni kural).
		 JOIN hostnames h ON lower(h.fqdn) = pr.fqdn AND h.tenant_id = pr.tenant_id
		                 AND (h.type != 'custom' OR h.verified = true)
		 LEFT JOIN tunnel_access_policies ap ON ap.tunnel_id = t.id
		 LEFT JOIN tunnel_traffic_policies tp ON tp.tunnel_id = t.id
		 LEFT JOIN tunnel_mtls mt ON mt.tunnel_id = t.id
		 LEFT JOIN subscriptions sub ON sub.tenant_id = t.tenant_id
		 LEFT JOIN (SELECT tunnel_id, array_agg(client_id) AS clients
		            FROM tunnel_replicas GROUP BY tunnel_id) rep ON rep.tunnel_id = t.id
		 -- Ozel kaynaklar (F17) yol yonlendirmesiyle de internete cikamaz.
		 WHERE t.exposure != 'private'
		 ORDER BY pr.fqdn, length(pr.path_prefix) DESC`)
	if err != nil {
		return nil, fmt.Errorf("yol kurallari listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.HostRoute
	for rows.Next() {
		var r store.HostRoute
		if err := rows.Scan(&r.FQDN, &r.TenantID, &r.TunnelID, &r.ClientID,
			&r.Target, &r.Enabled, &r.Proto, &r.Exposure,
			&r.AccessMode, &r.AccessConfig, &r.AccessEnabled, &r.Plan, &r.Frozen,
			&r.ReplicaClientIDs, &r.TrafficConfig, &r.TrafficEnabled,
			&r.MTLSEnabled, &r.MTLSCAPem, &r.PathPrefix); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListPathRoutes(ctx context.Context, tenantID, fqdn string) ([]store.PathRoute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, fqdn, path_prefix, tunnel_id, created_at
		 FROM tunnel_path_routes
		 WHERE tenant_id=$1 AND fqdn=$2
		 ORDER BY length(path_prefix) DESC, path_prefix`,
		tenantID, strings.ToLower(fqdn))
	if err != nil {
		return nil, fmt.Errorf("yol kurallari listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.PathRoute
	for rows.Next() {
		var p store.PathRoute
		if err := rows.Scan(&p.ID, &p.FQDN, &p.PathPrefix, &p.TunnelID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddPathRoute(ctx context.Context, tenantID, fqdn, pathPrefix, tunnelID string) (store.PathRoute, error) {
	fqdn = strings.ToLower(strings.TrimSpace(fqdn))
	pathPrefix = strings.TrimSpace(pathPrefix)
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	// Eslesme normalize edilmis yolla yapilir; kayit da ayni bicimde olsun
	// ('//api', '/api/', '/a/../api' -> '/api'), yoksa kural hic eslesmezdi.
	pathPrefix = path.Clean(strings.ReplaceAll(pathPrefix, "\\", "/"))
	// Hostname bu kiraciya ait mi?
	var ownsHost bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM hostnames WHERE lower(fqdn)=$1 AND tenant_id=$2)`,
		fqdn, tenantID).Scan(&ownsHost); err != nil {
		return store.PathRoute{}, fmt.Errorf("hostname dogrulanamadi: %w", err)
	}
	if !ownsHost {
		return store.PathRoute{}, store.ErrNotFound
	}
	// Tünel bu kiraciya ait mi?
	owns, err := s.tunnelOwnedBy(ctx, tenantID, tunnelID)
	if err != nil {
		return store.PathRoute{}, err
	}
	if !owns {
		return store.PathRoute{}, store.ErrNotFound
	}

	id := newID("pr")
	var p store.PathRoute
	err = s.pool.QueryRow(ctx,
		`INSERT INTO tunnel_path_routes (id, tenant_id, fqdn, path_prefix, tunnel_id)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (fqdn, path_prefix) DO UPDATE SET tunnel_id=EXCLUDED.tunnel_id
		 RETURNING id, fqdn, path_prefix, tunnel_id, created_at`,
		id, tenantID, fqdn, pathPrefix, tunnelID).
		Scan(&p.ID, &p.FQDN, &p.PathPrefix, &p.TunnelID, &p.CreatedAt)
	if err != nil {
		return store.PathRoute{}, fmt.Errorf("yol kurali eklenemedi: %w", err)
	}
	return p, nil
}

func (s *Store) DeletePathRoute(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tunnel_path_routes WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("yol kurali silinemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
