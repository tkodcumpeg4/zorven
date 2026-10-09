package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// hostnameCols, tum hostname sorgularinin ortak kolon listesi.
const hostnameCols = `id, tenant_id, COALESCE(tunnel_id, ''), fqdn, type, verified, COALESCE(verify_token, ''), created_at, COALESCE(project_id, '')`

func (s *Store) AddHostnameWithProject(ctx context.Context, tenantID, tunnelID, fqdn, typ, projectID string) (store.Hostname, error) {
	if projectID == "" {
		if def, err := s.GetDefaultProject(ctx, tenantID); err == nil {
			projectID = def.ID
		}
	}
	var tunnelVal *string
	if tunnelID != "" {
		tunnelVal = &tunnelID
	}
	h := store.Hostname{
		ID:        newID("hst"),
		TenantID:  tenantID,
		ProjectID: projectID,
		TunnelID:  tunnelID,
		FQDN:      fqdn,
		Type:      typ,
		Verified:  true,
		CreatedAt: time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO hostnames (id, tenant_id, tunnel_id, fqdn, type, verified, created_at, project_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		h.ID, h.TenantID, tunnelVal, h.FQDN, h.Type, h.Verified, h.CreatedAt, h.ProjectID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Hostname{}, store.ErrHostnameTaken
		}
		return store.Hostname{}, fmt.Errorf("hostname eklenemedi: %w", err)
	}
	return h, nil
}

func (s *Store) AddHostname(ctx context.Context, tenantID, tunnelID, fqdn, typ string) (store.Hostname, error) {
	return s.AddHostnameWithProject(ctx, tenantID, tunnelID, fqdn, typ, "")
}

func (s *Store) AddCustomHostname(ctx context.Context, tenantID, tunnelID, fqdn, verifyToken string) (store.Hostname, error) {
	var projectID string
	if def, err := s.GetDefaultProject(ctx, tenantID); err == nil {
		projectID = def.ID
	}
	var tunnelVal *string
	if tunnelID != "" {
		tunnelVal = &tunnelID
	}
	h := store.Hostname{
		ID:          newID("hst"),
		TenantID:    tenantID,
		ProjectID:   projectID,
		TunnelID:    tunnelID,
		FQDN:        fqdn,
		Type:        store.HostTypeCustom,
		Verified:    false,
		VerifyToken: verifyToken,
		CreatedAt:   time.Now().UTC(),
	}
	// Dogrulanmamis ozel ad kismi unique indekse girmez (F-11); bu yuzden ayni
	// ad baska bir yerde platform adi ya da DOGRULANMIS ozel domain olarak
	// varsa eklemeyi burada reddediyoruz. Ayni kiracidaki kopyayi
	// idx_hostnames_custom_tenant_fqdn (0069) yakalar.
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO hostnames (id, tenant_id, tunnel_id, fqdn, type, verified, verify_token, created_at, project_id)
		 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		 WHERE NOT EXISTS (
		   SELECT 1 FROM hostnames
		   WHERE lower(fqdn) = lower($4) AND (type <> 'custom' OR verified))`,
		h.ID, h.TenantID, tunnelVal, h.FQDN, h.Type, h.Verified, h.VerifyToken, h.CreatedAt, h.ProjectID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Hostname{}, store.ErrHostnameTaken
		}
		return store.Hostname{}, fmt.Errorf("custom hostname eklenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.Hostname{}, store.ErrHostnameTaken
	}
	return h, nil
}

func (s *Store) AttachHostname(ctx context.Context, tenantID, hostnameID, tunnelID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE hostnames
		 SET tunnel_id = $3
		 WHERE id = $1 AND tenant_id = $2`,
		hostnameID, tenantID, tunnelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DetachHostname(ctx context.Context, tenantID, hostnameID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE hostnames
		 SET tunnel_id = NULL
		 WHERE id = $1 AND tenant_id = $2`,
		hostnameID, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) VerifyHostname(ctx context.Context, tenantID, id string) (store.Hostname, error) {
	var h store.Hostname
	err := s.pool.QueryRow(ctx,
		`UPDATE hostnames
		 SET verified = true
		 WHERE id = $1 AND tenant_id = $2
		 RETURNING `+hostnameCols,
		id, tenantID).
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt, &h.ProjectID)
	if err != nil {
		// Kismi unique index (0067): baska kiracida ayni FQDN zaten dogrulanmis.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Hostname{}, store.ErrHostnameTaken
		}
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}

func (s *Store) GetHostnameByID(ctx context.Context, tenantID, id string) (store.Hostname, error) {
	var h store.Hostname
	err := s.pool.QueryRow(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt, &h.ProjectID)
	if err != nil {
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}

// GetHostnameByFQDN, ACME sertifika kontrolu veya genel lookup icin global arama yapar.
// Dogrulanmamis ozel domain kopyalari birden cok kiracida olabildigi icin (F-11)
// yetkili satir (platform adi veya dogrulanmis ozel domain) once secilir.
func (s *Store) GetHostnameByFQDN(ctx context.Context, fqdn string) (store.Hostname, error) {
	var h store.Hostname
	err := s.pool.QueryRow(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE lower(fqdn) = lower($1)
		 ORDER BY (type <> 'custom' OR verified) DESC, created_at
		 LIMIT 1`,
		fqdn).
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt, &h.ProjectID)
	if err != nil {
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}

func (s *Store) ListHostnames(ctx context.Context, tenantID string) ([]store.Hostname, error) {
	return s.queryHostnames(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE tenant_id = $1 ORDER BY created_at`,
		tenantID)
}

func (s *Store) ListHostnamesByProject(ctx context.Context, tenantID, projectID string) ([]store.Hostname, error) {
	if projectID == "" {
		return s.ListHostnames(ctx, tenantID)
	}
	return s.queryHostnames(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE tenant_id = $1 AND project_id = $2 ORDER BY created_at`,
		tenantID, projectID)
}

func (s *Store) ListHostnamesByTunnel(ctx context.Context, tenantID, tunnelID string) ([]store.Hostname, error) {
	return s.queryHostnames(ctx,
		`SELECT `+hostnameCols+` FROM hostnames
		 WHERE tenant_id = $1 AND tunnel_id = $2 ORDER BY created_at`,
		tenantID, tunnelID)
}

// ListHostnamesByClient, istemci zaten dogrulanmis oldugu icin kiracidan
// bagimsiz (ListTunnelsByClient ile ayni gerekce).
func (s *Store) ListHostnamesByClient(ctx context.Context, clientID string) ([]store.Hostname, error) {
	return s.queryHostnames(ctx,
		`SELECT `+hostnameColsQualified+` FROM hostnames h
		 JOIN tunnels t ON t.id = h.tunnel_id
		 WHERE t.client_id = $1 ORDER BY h.created_at`,
		clientID)
}

// hostnameColsQualified, JOIN'li sorgularda "h." onekiyle kullanilan kolon
// listesi; hostnameCols ile ayni sirada olmali (Scan sirasi buna bagli).
const hostnameColsQualified = `h.id, h.tenant_id, h.tunnel_id, h.fqdn, h.type, h.verified, COALESCE(h.verify_token, ''), h.created_at, COALESCE(h.project_id, '')`

func (s *Store) queryHostnames(ctx context.Context, sql string, args ...any) ([]store.Hostname, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("hostname'ler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Hostname
	for rows.Next() {
		var h store.Hostname
		if err := rows.Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt, &h.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) DeleteHostname(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM hostnames WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// IsReservedName, adin platform icin ayrilip ayrilmadigini soyler.
//
// lower($1): tablo kucuk harfle tohumlandi, kullanicidan gelen "API" de
// yakalanmali.
func (s *Store) IsReservedName(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM reserved_names WHERE name = lower($1))`, name).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("rezerve ad kontrolu basarisiz: %w", err)
	}
	return exists, nil
}

// ListHostRoutes, ingress yonlendirme tablosunu TEK sorguda doner.
//
// YALNIZCA dogrulanmis custom domain'ler ve tum platform subdomainleri doner.
// Dogrulanmamis (verified=false) custom domainler tunele iletilmez.
func (s *Store) ListHostRoutes(ctx context.Context) ([]store.HostRoute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT h.fqdn, h.tenant_id, t.id, t.client_id, t.target, t.enabled,
		        t.proto, t.exposure,
		        COALESCE(ap.mode,'none'), COALESCE(ap.config,'{}'::jsonb), COALESCE(ap.enabled,false),
		        COALESCE(sub.plan,'free'), t.frozen,
		        COALESCE(rep.clients, '{}') AS replica_clients,
		        COALESCE(tp.config,'{}'::jsonb), COALESCE(tp.enabled,false),
		        COALESCE(mt.enabled,false), COALESCE(mt.ca_pem,'')
		 FROM hostnames h
		 JOIN tunnels t ON t.id = h.tunnel_id
		 LEFT JOIN tunnel_access_policies ap ON ap.tunnel_id = t.id
		 LEFT JOIN tunnel_traffic_policies tp ON tp.tunnel_id = t.id
		 LEFT JOIN tunnel_mtls mt ON mt.tunnel_id = t.id
		 LEFT JOIN subscriptions sub ON sub.tenant_id = t.tenant_id
		 LEFT JOIN (SELECT tunnel_id, array_agg(client_id) AS clients
		            FROM tunnel_replicas GROUP BY tunnel_id) rep ON rep.tunnel_id = t.id
		 WHERE (h.type != 'custom' OR h.verified = true)
		   -- Ozel kaynaklar (F17) internete ASLA yonlendirilmez: yanlislikla bir
		   -- ad baglansa bile burada elenir (savunma derinligi).
		   AND t.exposure != 'private'
		 ORDER BY h.created_at`)
	if err != nil {
		return nil, fmt.Errorf("yonlendirmeler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.HostRoute
	for rows.Next() {
		var r store.HostRoute
		if err := rows.Scan(&r.FQDN, &r.TenantID, &r.TunnelID, &r.ClientID,
			&r.Target, &r.Enabled, &r.Proto, &r.Exposure,
			&r.AccessMode, &r.AccessConfig, &r.AccessEnabled, &r.Plan, &r.Frozen,
			&r.ReplicaClientIDs, &r.TrafficConfig, &r.TrafficEnabled,
			&r.MTLSEnabled, &r.MTLSCAPem); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
