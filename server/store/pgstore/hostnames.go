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
const hostnameCols = `id, tenant_id, COALESCE(tunnel_id, ''), fqdn, type, verified, COALESCE(verify_token, ''), created_at`

func (s *Store) AddHostname(ctx context.Context, tenantID, tunnelID, fqdn, typ string) (store.Hostname, error) {
	var tunnelVal *string
	if tunnelID != "" {
		tunnelVal = &tunnelID
	}
	h := store.Hostname{
		ID:        newID("hst"),
		TenantID:  tenantID,
		TunnelID:  tunnelID,
		FQDN:      fqdn,
		Type:      typ,
		Verified:  true,
		CreatedAt: time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO hostnames (id, tenant_id, tunnel_id, fqdn, type, verified, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		h.ID, h.TenantID, tunnelVal, h.FQDN, h.Type, h.Verified, h.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Hostname{}, store.ErrHostnameTaken
		}
		return store.Hostname{}, fmt.Errorf("hostname eklenemedi: %w", err)
	}
	return h, nil
}

func (s *Store) AddCustomHostname(ctx context.Context, tenantID, tunnelID, fqdn, verifyToken string) (store.Hostname, error) {
	var tunnelVal *string
	if tunnelID != "" {
		tunnelVal = &tunnelID
	}
	h := store.Hostname{
		ID:          newID("hst"),
		TenantID:    tenantID,
		TunnelID:    tunnelID,
		FQDN:        fqdn,
		Type:        store.HostTypeCustom,
		Verified:    false,
		VerifyToken: verifyToken,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO hostnames (id, tenant_id, tunnel_id, fqdn, type, verified, verify_token, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		h.ID, h.TenantID, tunnelVal, h.FQDN, h.Type, h.Verified, h.VerifyToken, h.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Hostname{}, store.ErrHostnameTaken
		}
		return store.Hostname{}, fmt.Errorf("custom hostname eklenemedi: %w", err)
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
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt)
	if err != nil {
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}

func (s *Store) GetHostnameByID(ctx context.Context, tenantID, id string) (store.Hostname, error) {
	var h store.Hostname
	err := s.pool.QueryRow(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt)
	if err != nil {
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}

// GetHostnameByFQDN, ACME sertifika kontrolu veya genel lookup icin global arama yapar.
func (s *Store) GetHostnameByFQDN(ctx context.Context, fqdn string) (store.Hostname, error) {
	var h store.Hostname
	err := s.pool.QueryRow(ctx,
		`SELECT `+hostnameCols+` FROM hostnames WHERE lower(fqdn) = lower($1)`,
		fqdn).
		Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt)
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
const hostnameColsQualified = `h.id, h.tenant_id, h.tunnel_id, h.fqdn, h.type, h.verified, COALESCE(h.verify_token, ''), h.created_at`

func (s *Store) queryHostnames(ctx context.Context, sql string, args ...any) ([]store.Hostname, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("hostname'ler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Hostname
	for rows.Next() {
		var h store.Hostname
		if err := rows.Scan(&h.ID, &h.TenantID, &h.TunnelID, &h.FQDN, &h.Type, &h.Verified, &h.VerifyToken, &h.CreatedAt); err != nil {
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
		`SELECT h.fqdn, h.tenant_id, t.id, t.client_id, t.target, t.enabled
		 FROM hostnames h
		 JOIN tunnels t ON t.id = h.tunnel_id
		 WHERE h.type != 'custom' OR h.verified = true
		 ORDER BY h.created_at`)
	if err != nil {
		return nil, fmt.Errorf("yonlendirmeler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.HostRoute
	for rows.Next() {
		var r store.HostRoute
		if err := rows.Scan(&r.FQDN, &r.TenantID, &r.TunnelID, &r.ClientID,
			&r.Target, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
