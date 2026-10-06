package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// tunnelSelectCols, SELECT sorgularinda kullanilan kolon listesi (public_port
// NULL olabilir; COALESCE ile 0'a duser). tunnelInsertCols INSERT icindir.
//
// hostname YOK: adlar hostnames tablosunda tutulur (bkz. hostnames.go).
const tunnelSelectCols = `id, tenant_id, client_id, target, enabled, created_at, proto, exposure, COALESCE(public_port, 0), frozen, COALESCE(project_id, ''), ephemeral, expires_at, COALESCE(private_name, '')`
const tunnelInsertCols = `id, tenant_id, client_id, target, enabled, created_at, proto, exposure, project_id`

// scanTunnel, tunnelSelectCols sirasiyla bir satiri okur.
func scanTunnel(row pgx.Row, tn *store.Tunnel) error {
	var expires *time.Time
	if err := row.Scan(&tn.ID, &tn.TenantID, &tn.ClientID, &tn.Target, &tn.Enabled,
		&tn.CreatedAt, &tn.Proto, &tn.Exposure, &tn.PublicPort, &tn.Frozen, &tn.ProjectID,
		&tn.Ephemeral, &expires, &tn.PrivateName); err != nil {
		return err
	}
	if expires != nil {
		u := expires.UTC()
		tn.ExpiresAt = &u
	} else {
		tn.ExpiresAt = nil
	}
	return nil
}

// MakeTunnelPrivate, tuneli ozel kaynak yapar (FAZ 3 / F17): TCP, exposure
// private, public port YOK. Ad kiraci icinde tekildir (kismi benzersiz indeks).
func (s *Store) MakeTunnelPrivate(ctx context.Context, tenantID, id, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET proto = $3, exposure = $4, private_name = $5, public_port = NULL
		 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, store.ProtoTCP, store.ExposurePrivate, name)
	if err != nil {
		return fmt.Errorf("ozel kaynak ayarlanamadi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// GetPrivateTunnel, ozel kaynagi adiyla bulur (buyuk/kucuk duyarsiz).
// Yalnizca exposure='private' ve ETKIN tuneller doner: devre disi birakilan
// kaynaga baglanilamamali.
func (s *Store) GetPrivateTunnel(ctx context.Context, tenantID, name string) (store.Tunnel, error) {
	var tn store.Tunnel
	err := scanTunnel(s.pool.QueryRow(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels
		 WHERE tenant_id = $1 AND lower(private_name) = lower($2)
		   AND exposure = $3 AND enabled`,
		tenantID, name, store.ExposurePrivate), &tn)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tunnel{}, store.ErrNotFound
	}
	return tn, err
}

// ListPrivateSubnets, kiracinin etkin alt ag kaynaklarini doner (F20).
func (s *Store) ListPrivateSubnets(ctx context.Context, tenantID string) ([]store.Tunnel, error) {
	return s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels
		 WHERE tenant_id = $1 AND exposure = $2 AND enabled AND target LIKE 'subnet:%'
		 ORDER BY created_at`, tenantID, store.ExposurePrivate)
}

func (s *Store) CreateTunnelWithProject(ctx context.Context, tenantID, clientID, target, projectID string) (store.Tunnel, error) {
	if projectID == "" {
		if def, err := s.GetDefaultProject(ctx, tenantID); err == nil {
			projectID = def.ID
		}
	}
	tn := store.Tunnel{
		ID:        newID("tun"),
		TenantID:  tenantID,
		ProjectID: projectID,
		ClientID:  clientID,
		Target:    target,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		Proto:     store.ProtoHTTP,
		Exposure:  store.ExposureAuto,
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnels (`+tunnelInsertCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		tn.ID, tn.TenantID, tn.ClientID, tn.Target, tn.Enabled, tn.CreatedAt, tn.Proto, tn.Exposure, tn.ProjectID)
	if err != nil {
		return store.Tunnel{}, fmt.Errorf("tunel olusturulamadi: %w", err)
	}
	return tn, nil
}

func (s *Store) CreateTunnel(ctx context.Context, tenantID, clientID, target string) (store.Tunnel, error) {
	return s.CreateTunnelWithProject(ctx, tenantID, clientID, target, "")
}

// CreateEphemeralTunnel, TTL'li gecici tunel olusturur (FAZ 2 / F07).
// expiresAt gecmiste ise kayit acilmaz: suresi dolmus bir tunel yaratmak,
// supurucunun bir sonraki turunda temizlenecek olu satir birakmaktir.
func (s *Store) CreateEphemeralTunnel(ctx context.Context, tenantID, clientID, target, projectID string, expiresAt time.Time) (store.Tunnel, error) {
	if !expiresAt.After(time.Now().UTC()) {
		return store.Tunnel{}, fmt.Errorf("gecici tunel: bitis zamani gelecekte olmali")
	}
	tn, err := s.CreateTunnelWithProject(ctx, tenantID, clientID, target, projectID)
	if err != nil {
		return store.Tunnel{}, err
	}
	exp := expiresAt.UTC()
	if _, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET ephemeral = true, expires_at = $2 WHERE id = $1`, tn.ID, exp); err != nil {
		// Isaretleme basarisizsa yarim kalmis KALICI tunel birakmayalim.
		_ = s.DeleteTunnel(ctx, tenantID, tn.ID)
		return store.Tunnel{}, fmt.Errorf("gecici tunel isaretlenemedi: %w", err)
	}
	tn.Ephemeral = true
	tn.ExpiresAt = &exp
	return tn, nil
}

// DeleteExpiredTunnels, suresi dolmus gecici tunelleri temizler ve temizlenen
// satir sayisini doner. Hostname ve binding satirlari CASCADE ile gider.
func (s *Store) DeleteExpiredTunnels(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tunnels WHERE expires_at IS NOT NULL AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ImportTunnel, satiri OLDUGU GIBI yazar (tek seferlik SQLite gocu icin).
func (s *Store) ImportTunnel(ctx context.Context, t store.Tunnel) error {
	if t.TenantID == "" {
		t.TenantID = store.DefaultTenantID
	}
	if t.Proto == "" {
		t.Proto = store.ProtoHTTP
	}
	if t.Exposure == "" {
		t.Exposure = store.ExposureAuto
	}
	if t.ProjectID == "" {
		if def, err := s.GetDefaultProject(ctx, t.TenantID); err == nil {
			t.ProjectID = def.ID
		}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnels (`+tunnelInsertCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (id) DO NOTHING`,
		t.ID, t.TenantID, t.ClientID, t.Target, t.Enabled, t.CreatedAt, t.Proto, t.Exposure, t.ProjectID)
	if err != nil {
		return fmt.Errorf("tunel ice aktarilamadi (%s): %w", t.ID, err)
	}
	return nil
}

func (s *Store) GetTunnel(ctx context.Context, tenantID, id string) (store.Tunnel, error) {
	var tn store.Tunnel
	err := scanTunnel(s.pool.QueryRow(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels WHERE id = $1 AND tenant_id = $2`, id, tenantID), &tn)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tunnel{}, store.ErrNotFound
	}
	if err != nil {
		return store.Tunnel{}, err
	}
	return tn, nil
}

func (s *Store) ListTunnels(ctx context.Context, tenantID string) ([]store.Tunnel, error) {
	return s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
}

func (s *Store) ListTunnelsByProject(ctx context.Context, tenantID, projectID string) ([]store.Tunnel, error) {
	if projectID == "" {
		return s.ListTunnels(ctx, tenantID)
	}
	return s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels WHERE tenant_id = $1 AND project_id = $2 ORDER BY created_at`, tenantID, projectID)
}

// ListTunnelsByClient, istemci zaten dogrulanmis oldugu icin kiracidan bagimsiz.
// FAZ 5 / HA: istemcinin BIRINCIL olarak sahip oldugu tunellere EK olarak,
// replika (tunnel_replicas) olarak atandigi tunelleri de dondurur — boylece
// hello_ack/config_update replika istemciye o tuneli (hedefiyle) bildirir ve
// replika istekleri servis edebilir. UNION ile tekrarlar ayiklanir.
func (s *Store) ListTunnelsByClient(ctx context.Context, clientID string) ([]store.Tunnel, error) {
	return s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels t
		 WHERE t.client_id = $1
		    OR t.id IN (SELECT tunnel_id FROM tunnel_replicas WHERE client_id = $1)
		 ORDER BY t.created_at`, clientID)
}

func (s *Store) queryTunnels(ctx context.Context, sql string, args ...any) ([]store.Tunnel, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("tuneller listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Tunnel
	for rows.Next() {
		var tn store.Tunnel
		if err := scanTunnel(rows, &tn); err != nil {
			return nil, err
		}
		out = append(out, tn)
	}
	return out, rows.Err()
}

// UpdateTunnel, kismi guncelleme yapar: patch'te nil olan alan DEGISMEZ.
//
// COALESCE + acik tip donusumu ($3::text): pgx, NULL parametrenin tipini
// baglamdan cikaramadiginda hata verir; cast bunu kesin cozer.
func (s *Store) UpdateTunnel(ctx context.Context, tenantID, id string, patch store.TunnelPatch) (store.Tunnel, error) {
	var tn store.Tunnel
	err := scanTunnel(s.pool.QueryRow(ctx,
		`UPDATE tunnels SET
			target      = COALESCE($3::text, target),
			enabled     = COALESCE($4::boolean, enabled),
			proto       = COALESCE($5::text, proto),
			exposure    = COALESCE($6::text, exposure),
			public_port = COALESCE($7::int, public_port),
			client_id   = COALESCE($8::text, client_id)
		 WHERE id = $1 AND tenant_id = $2
		 RETURNING `+tunnelSelectCols,
		id, tenantID, patch.Target, patch.Enabled, patch.Proto, patch.Exposure, patch.PublicPort, patch.ClientID), &tn)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tunnel{}, store.ErrNotFound
	}
	if err != nil {
		return store.Tunnel{}, err
	}
	return tn, nil
}

// ListReservedPortTunnels, public_port atanmis tum tunelleri doner (kiracidan bagimsiz).
func (s *Store) ListReservedPortTunnels(ctx context.Context) ([]store.Tunnel, error) {
	return s.queryTunnels(ctx,
		`SELECT `+tunnelSelectCols+` FROM tunnels WHERE public_port IS NOT NULL AND exposure != 'private' ORDER BY public_port`)
}

// SetTunnelPort, rezerve portu ayarlar (port>0) veya birakir (port=0 -> NULL).
func (s *Store) SetTunnelPort(ctx context.Context, tenantID, id string, port int) error {
	var val any
	if port > 0 {
		val = port
	} else {
		val = nil
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET public_port = $3 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, val)
	if err != nil {
		return fmt.Errorf("rezerve port ayarlanamadi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// AdminSetTunnelFrozen, bir tuneli kiracidan bagimsiz dondurur/cozer (FAZ 4).
func (s *Store) AdminSetTunnelFrozen(ctx context.Context, tunnelID string, frozen bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tunnels SET frozen = $2 WHERE id = $1`, tunnelID, frozen)
	if err != nil {
		return fmt.Errorf("tunel dondurma durumu ayarlanamadi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// AdminFreezeByFQDN, bir hostname'e bagli tuneli dondurur/cozer (FAZ 4).
func (s *Store) AdminFreezeByFQDN(ctx context.Context, fqdn string, frozen bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET frozen = $2
		 WHERE id = (SELECT tunnel_id FROM hostnames WHERE lower(fqdn) = lower($1) LIMIT 1)`,
		fqdn, frozen)
	if err != nil {
		return fmt.Errorf("hostname ile dondurma basarisiz: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteTunnel(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tunnels WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
