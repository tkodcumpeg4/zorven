package pgstore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Web ile kapi acma (web door): tunnel_doors (ayar) + tunnel_door_grants (IP izinleri).

// GetTunnelDoor, tunelin kapi ayarini doner. Kayit yoksa varsayilan (kapali, 12 saat).
// Tunel bu kiraciya ait degilse ErrNotFound.
func (s *Store) GetTunnelDoor(ctx context.Context, tenantID, tunnelID string) (store.TunnelDoor, error) {
	d := store.TunnelDoor{TunnelID: tunnelID, TenantID: tenantID, DurationSec: store.DoorDuration12h}
	var enabled *bool
	var dur *int
	var host *string
	var upd *time.Time
	var planClosed *bool
	err := s.pool.QueryRow(ctx,
		`SELECT td.enabled, td.duration_sec, td.host, td.updated_at, td.plan_closed
		   FROM tunnels t LEFT JOIN tunnel_doors td ON td.tunnel_id = t.id
		  WHERE t.id=$1 AND t.tenant_id=$2`, tunnelID, tenantID).
		Scan(&enabled, &dur, &host, &upd, &planClosed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.TunnelDoor{}, store.ErrNotFound
		}
		return store.TunnelDoor{}, fmt.Errorf("kapi ayari okunamadi: %w", err)
	}
	if enabled != nil {
		d.Enabled = *enabled
	}
	if dur != nil && *dur > 0 {
		d.DurationSec = *dur
	}
	if host != nil {
		d.Host = *host
	}
	if upd != nil {
		d.UpdatedAt = *upd
	}
	if planClosed != nil {
		d.PlanClosed = *planClosed
	}
	return d, nil
}

// SetTunnelDoor, kapi ayarini upsert eder (kiraci sahipligi dogrulanir).
func (s *Store) SetTunnelDoor(ctx context.Context, tenantID string, d store.TunnelDoor) error {
	var owned bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunnels WHERE id=$1 AND tenant_id=$2)`,
		d.TunnelID, tenantID).Scan(&owned); err != nil {
		return fmt.Errorf("tunel dogrulanamadi: %w", err)
	}
	if !owned {
		return store.ErrNotFound
	}
	if d.DurationSec <= 0 {
		d.DurationSec = store.DoorDuration12h
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_doors (tunnel_id, tenant_id, enabled, duration_sec, host, updated_at, plan_closed)
		 VALUES ($1,$2,$3,$4,$5, now(), false)
		 ON CONFLICT (tunnel_id) DO UPDATE
		   SET enabled=EXCLUDED.enabled, duration_sec=EXCLUDED.duration_sec,
		       host=EXCLUDED.host, updated_at=now(), plan_closed=false`,
		d.TunnelID, tenantID, d.Enabled, d.DurationSec, d.Host); err != nil {
		return fmt.Errorf("kapi ayari kaydedilemedi: %w", err)
	}
	return nil
}

// ListEnabledDoors, etkin tum kapilari doner.
func (s *Store) ListEnabledDoors(ctx context.Context) ([]store.TunnelDoor, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.tunnel_id, d.tenant_id, d.enabled, d.duration_sec, d.host, d.updated_at
		   FROM tunnel_doors d JOIN tunnels t ON t.id = d.tunnel_id
		  WHERE d.enabled AND t.proto IN ('tcp','udp') AND t.exposure != 'private'`)
	if err != nil {
		return nil, fmt.Errorf("kapilar listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []store.TunnelDoor
	for rows.Next() {
		var d store.TunnelDoor
		if err := rows.Scan(&d.TunnelID, &d.TenantID, &d.Enabled, &d.DurationSec, &d.Host, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDoorRoutes, etkin kapilarin ingress yonlendirme kayitlarini doner. Erisim
// politikasi (Basic/OAuth) tunelin mevcut tunnel_access_policies kaydindan gelir.
func (s *Store) ListDoorRoutes(ctx context.Context) ([]store.HostRoute, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.host, d.tenant_id, t.id, t.client_id, t.enabled, t.frozen, t.proto, t.exposure,
		        COALESCE(t.public_port,0), t.public_ip, d.duration_sec,
		        COALESCE((SELECT h.fqdn FROM hostnames h WHERE h.tunnel_id = t.id
		                   ORDER BY h.created_at LIMIT 1), ''),
		        COALESCE(ap.mode,'none'), COALESCE(ap.config,'{}'::jsonb), COALESCE(ap.enabled,false),
		        COALESCE(sub.plan,'free'), NOT d.enabled
		   FROM tunnel_doors d
		   JOIN tunnels t ON t.id = d.tunnel_id
		   LEFT JOIN tunnel_access_policies ap ON ap.tunnel_id = t.id
		   LEFT JOIN subscriptions sub ON sub.tenant_id = t.tenant_id
		  WHERE (d.enabled OR d.plan_closed) AND d.host <> '' AND t.proto IN ('tcp','udp') AND t.exposure != 'private'`)
	if err != nil {
		return nil, fmt.Errorf("kapi yonlendirmeleri listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []store.HostRoute
	for rows.Next() {
		var r store.HostRoute
		var dr store.DoorRoute
		if err := rows.Scan(&r.FQDN, &r.TenantID, &r.TunnelID, &r.ClientID, &r.Enabled, &r.Frozen,
			&dr.Proto, &dr.Exposure, &dr.PublicPort, &dr.PublicIP, &dr.DurationSec, &dr.TunnelHost,
			&r.AccessMode, &r.AccessConfig, &r.AccessEnabled, &r.Plan, &dr.Disabled); err != nil {
			return nil, err
		}
		r.Proto, r.Exposure = dr.Proto, dr.Exposure
		r.Door = &dr
		out = append(out, r)
	}
	return out, rows.Err()
}

const grantCols = `id, tenant_id, tunnel_id, ip, identity, method, expires_at, created_at, revoked_at`

func scanGrant(row pgx.Row) (store.DoorGrant, error) {
	var g store.DoorGrant
	err := row.Scan(&g.ID, &g.TenantID, &g.TunnelID, &g.IP, &g.Identity, &g.Method, &g.ExpiresAt, &g.CreatedAt, &g.RevokedAt)
	return g, err
}

// attachExtraIPs, grant'lerin ek (diger aile) adreslerini tek sorguyla doldurur.
func (s *Store) attachExtraIPs(ctx context.Context, gs []store.DoorGrant) error {
	if len(gs) == 0 {
		return nil
	}
	ids := make([]string, len(gs))
	idx := make(map[string]int, len(gs))
	for i := range gs {
		ids[i] = gs[i].ID
		idx[gs[i].ID] = i
	}
	rows, err := s.pool.Query(ctx,
		`SELECT grant_id, ip FROM tunnel_door_grant_addrs WHERE grant_id = ANY($1) ORDER BY family`, ids)
	if err != nil {
		return fmt.Errorf("kapi ek adresleri okunamadi: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, ip string
		if err := rows.Scan(&id, &ip); err != nil {
			return err
		}
		if i, ok := idx[id]; ok {
			gs[i].ExtraIPs = append(gs[i].ExtraIPs, ip)
		}
	}
	return rows.Err()
}

// AddDoorGrantAddr, aktif bir grant'e diger IP ailesinden ek adres baglar (aile basina
// tek adres; yenisi eskisini degistirir). Grant aktif degilse ErrNotFound. ip grant'in
// ANA adresiyle ayni ailedeyse bir sey yapmaz (added=false). Guncel grant doner.
func (s *Store) AddDoorGrantAddr(ctx context.Context, grantID, ip string) (store.DoorGrant, bool, error) {
	pip := net.ParseIP(ip)
	if pip == nil {
		return store.DoorGrant{}, false, errors.New("gecersiz ip")
	}
	fam := 6
	if pip.To4() != nil {
		fam = 4
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.DoorGrant{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	g, err := scanGrant(tx.QueryRow(ctx,
		`SELECT `+grantCols+` FROM tunnel_door_grants
		  WHERE id=$1 AND revoked_at IS NULL AND expires_at > now() FOR UPDATE`, grantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.DoorGrant{}, false, store.ErrNotFound
		}
		return store.DoorGrant{}, false, err
	}
	if pg := net.ParseIP(g.IP); pg != nil && (pg.To4() != nil) == (fam == 4) {
		return g, false, nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tunnel_door_grant_addrs (grant_id, family, ip) VALUES ($1,$2,$3)
		 ON CONFLICT (grant_id, family) DO UPDATE SET ip=EXCLUDED.ip, created_at=now()`,
		grantID, fam, ip); err != nil {
		return store.DoorGrant{}, false, fmt.Errorf("kapi ek adresi yazilamadi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return store.DoorGrant{}, false, err
	}
	gs := []store.DoorGrant{g}
	if err := s.attachExtraIPs(ctx, gs); err != nil {
		return g, true, nil
	}
	return gs[0], true, nil
}

// DoorGrantAddrsByIP, ip'nin ana ya da ek adresi oldugu aktif grant'lerin TUM adreslerini
// (ana + ek) tekrarsiz doner. Kapi kapatilirken tum acik baglantilari kesmek icin.
func (s *Store) DoorGrantAddrsByIP(ctx context.Context, tenantID, tunnelID, ip string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT g.ip FROM tunnel_door_grants g
		  WHERE g.tenant_id=$1 AND g.tunnel_id=$2 AND g.revoked_at IS NULL AND g.expires_at > now()
		    AND (g.ip=$3 OR g.id IN (SELECT grant_id FROM tunnel_door_grant_addrs WHERE ip=$3))
		 UNION
		 SELECT a.ip FROM tunnel_door_grant_addrs a JOIN tunnel_door_grants g ON g.id=a.grant_id
		  WHERE g.tenant_id=$1 AND g.tunnel_id=$2 AND g.revoked_at IS NULL AND g.expires_at > now()
		    AND (g.ip=$3 OR g.id IN (SELECT grant_id FROM tunnel_door_grant_addrs WHERE ip=$3))`,
		tenantID, tunnelID, ip)
	if err != nil {
		return nil, fmt.Errorf("kapi adresleri okunamadi: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenDoorGrant, (tunel, ip) icin aktif grant varsa onu doner, yoksa olusturur.
func (s *Store) OpenDoorGrant(ctx context.Context, g store.DoorGrant) (store.DoorGrant, bool, error) {
	if g.ID == "" {
		g.ID = newID("dg")
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.DoorGrant{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Ayni (tunel, ip) icin eszamanli iki giris cift satir uretmesin.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, g.TunnelID+"|"+g.IP); err != nil {
		return store.DoorGrant{}, false, err
	}
	cur, err := scanGrant(tx.QueryRow(ctx,
		`SELECT `+grantCols+` FROM tunnel_door_grants
		  WHERE tunnel_id=$1 AND ip=$2 AND revoked_at IS NULL AND expires_at > now()
		  ORDER BY expires_at DESC LIMIT 1`, g.TunnelID, g.IP))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return store.DoorGrant{}, false, err
		}
		gs := []store.DoorGrant{cur}
		_ = s.attachExtraIPs(ctx, gs)
		return gs[0], false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.DoorGrant{}, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tunnel_door_grants (id, tenant_id, tunnel_id, ip, identity, method, expires_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		g.ID, g.TenantID, g.TunnelID, g.IP, pgText(g.Identity), g.Method, g.ExpiresAt, g.CreatedAt); err != nil {
		return store.DoorGrant{}, false, fmt.Errorf("kapi izni yazilamadi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return store.DoorGrant{}, false, err
	}
	return g, true, nil
}

// ListDoorGrants, tunelin grant'lerini en yeniden eskiye doner.
func (s *Store) ListDoorGrants(ctx context.Context, tenantID, tunnelID string, activeOnly bool, limit int) ([]store.DoorGrant, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + grantCols + ` FROM tunnel_door_grants WHERE tenant_id=$1 AND tunnel_id=$2`
	if activeOnly {
		q += ` AND revoked_at IS NULL AND expires_at > now()`
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, tenantID, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("kapi izinleri listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.DoorGrant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.attachExtraIPs(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActiveDoorGrantsByTunnel, rawproxy onbellegi icin aktif grant'ler (kiracisiz).
func (s *Store) ListActiveDoorGrantsByTunnel(ctx context.Context, tunnelID string) ([]store.DoorGrant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+grantCols+` FROM tunnel_door_grants
		  WHERE tunnel_id=$1 AND revoked_at IS NULL AND expires_at > now()`, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("aktif kapi izinleri okunamadi: %w", err)
	}
	defer rows.Close()
	var out []store.DoorGrant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.attachExtraIPs(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeDoorGrant, tek grant'i iptal eder; iptal edilen kaydi doner.
func (s *Store) RevokeDoorGrant(ctx context.Context, tenantID, tunnelID, grantID string) (store.DoorGrant, error) {
	g, err := scanGrant(s.pool.QueryRow(ctx,
		`UPDATE tunnel_door_grants SET revoked_at = now()
		  WHERE id=$1 AND tenant_id=$2 AND tunnel_id=$3 AND revoked_at IS NULL AND expires_at > now()
		  RETURNING `+grantCols, grantID, tenantID, tunnelID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.DoorGrant{}, store.ErrNotFound
		}
		return store.DoorGrant{}, fmt.Errorf("kapi izni iptal edilemedi: %w", err)
	}
	gs := []store.DoorGrant{g}
	_ = s.attachExtraIPs(ctx, gs)
	return gs[0], nil
}

// RevokeDoorGrantsByIP, (tunel, ip) icin aktif tum grant'leri iptal eder.
func (s *Store) RevokeDoorGrantsByIP(ctx context.Context, tenantID, tunnelID, ip string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tunnel_door_grants SET revoked_at = now()
		  WHERE tenant_id=$1 AND tunnel_id=$2 AND revoked_at IS NULL AND expires_at > now()
		    AND (ip=$3 OR id IN (SELECT grant_id FROM tunnel_door_grant_addrs WHERE ip=$3))`,
		tenantID, tunnelID, ip)
	if err != nil {
		return 0, fmt.Errorf("kapi izinleri iptal edilemedi: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PruneDoorGrants, bitisi (sure dolumu veya iptal, hangisi once ise) before'dan eski
// grant'leri siler.
func (s *Store) PruneDoorGrants(ctx context.Context, before time.Time) (int64, error) {
	return s.batchDelete(ctx,
		`DELETE FROM tunnel_door_grants WHERE id IN (
		   SELECT id FROM tunnel_door_grants
		    WHERE LEAST(expires_at, COALESCE(revoked_at, expires_at)) < $1 LIMIT $2)`,
		before)
}

// RecordDoorRevocation, (tunel, kimlik) icin son iptal zamanini yazar; yalniz ileri gider.
func (s *Store) RecordDoorRevocation(ctx context.Context, tunnelID, identity string, at time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_door_revocations (tunnel_id, identity, revoked_at) VALUES ($1,$2,$3)
		 ON CONFLICT (tunnel_id, identity) DO UPDATE
		   SET revoked_at = GREATEST(tunnel_door_revocations.revoked_at, EXCLUDED.revoked_at)`,
		tunnelID, identity, at.UTC()); err != nil {
		return fmt.Errorf("kapi oturum iptali yazilamadi: %w", err)
	}
	return nil
}

// ListDoorRevocations, tunelin kimlik -> son iptal zamani haritasini doner.
func (s *Store) ListDoorRevocations(ctx context.Context, tunnelID string) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT identity, revoked_at FROM tunnel_door_revocations WHERE tunnel_id=$1`, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("kapi oturum iptalleri okunamadi: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = at
	}
	return out, rows.Err()
}

// CloseDoorsForTenant, plan Pro altina dusen kiracinin kapilarini kalici kapatir:
// enabled=false + plan_closed=true, aktif grant'ler iptal, kimlikler icin oturum iptali.
func (s *Store) CloseDoorsForTenant(ctx context.Context, tenantID string, at time.Time) ([]string, []store.DoorGrant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var tunnelIDs []string
	rows, err := tx.Query(ctx,
		`UPDATE tunnel_doors SET enabled=false, plan_closed=true, updated_at=now()
		  WHERE tenant_id=$1 AND enabled RETURNING tunnel_id`, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("kapilar kapatilamadi: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		tunnelIDs = append(tunnelIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var revoked []store.DoorGrant
	grows, err := tx.Query(ctx,
		`WITH rv AS (
		   UPDATE tunnel_door_grants SET revoked_at = $2
		    WHERE tenant_id=$1 AND revoked_at IS NULL AND expires_at > now()
		    RETURNING `+grantCols+`),
		 ins AS (
		   INSERT INTO tunnel_door_revocations (tunnel_id, identity, revoked_at)
		   SELECT tunnel_id, lower(identity), max($2::timestamptz) FROM rv GROUP BY tunnel_id, lower(identity)
		   ON CONFLICT (tunnel_id, identity) DO UPDATE
		     SET revoked_at = GREATEST(tunnel_door_revocations.revoked_at, EXCLUDED.revoked_at))
		 SELECT `+grantCols+` FROM rv`, tenantID, at.UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("kapi izinleri iptal edilemedi: %w", err)
	}
	for grows.Next() {
		g, err := scanGrant(grows)
		if err != nil {
			grows.Close()
			return nil, nil, err
		}
		revoked = append(revoked, g)
	}
	grows.Close()
	if err := grows.Err(); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	_ = s.attachExtraIPs(ctx, revoked)
	return tunnelIDs, revoked, nil
}
