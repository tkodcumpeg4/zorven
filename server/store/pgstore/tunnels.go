package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// tunnelCols, tum tunel sorgularinin ortak kolon listesi.
//
// hostname YOK: adlar hostnames tablosunda tutulur (bkz. hostnames.go).
const tunnelCols = `id, tenant_id, client_id, target, enabled, created_at`

func (s *Store) CreateTunnel(ctx context.Context, tenantID, clientID, target string) (store.Tunnel, error) {
	tn := store.Tunnel{
		ID:        newID("tun"),
		TenantID:  tenantID,
		ClientID:  clientID,
		Target:    target,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnels (`+tunnelCols+`) VALUES ($1,$2,$3,$4,$5,$6)`,
		tn.ID, tn.TenantID, tn.ClientID, tn.Target, tn.Enabled, tn.CreatedAt)
	if err != nil {
		return store.Tunnel{}, fmt.Errorf("tunel olusturulamadi: %w", err)
	}
	return tn, nil
}

// ImportTunnel, satiri OLDUGU GIBI yazar (tek seferlik SQLite gocu icin).
func (s *Store) ImportTunnel(ctx context.Context, t store.Tunnel) error {
	if t.TenantID == "" {
		t.TenantID = store.DefaultTenantID
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tunnels (`+tunnelCols+`) VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO NOTHING`,
		t.ID, t.TenantID, t.ClientID, t.Target, t.Enabled, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("tunel ice aktarilamadi (%s): %w", t.ID, err)
	}
	return nil
}

func (s *Store) GetTunnel(ctx context.Context, tenantID, id string) (store.Tunnel, error) {
	var tn store.Tunnel
	err := s.pool.QueryRow(ctx,
		`SELECT `+tunnelCols+` FROM tunnels WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&tn.ID, &tn.TenantID, &tn.ClientID, &tn.Target, &tn.Enabled, &tn.CreatedAt)
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
		`SELECT `+tunnelCols+` FROM tunnels WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
}

// ListTunnelsByClient, istemci zaten dogrulanmis oldugu icin kiracidan bagimsiz.
func (s *Store) ListTunnelsByClient(ctx context.Context, clientID string) ([]store.Tunnel, error) {
	return s.queryTunnels(ctx,
		`SELECT `+tunnelCols+` FROM tunnels WHERE client_id = $1 ORDER BY created_at`, clientID)
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
		if err := rows.Scan(&tn.ID, &tn.TenantID, &tn.ClientID,
			&tn.Target, &tn.Enabled, &tn.CreatedAt); err != nil {
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
	err := s.pool.QueryRow(ctx,
		`UPDATE tunnels SET
			target  = COALESCE($3::text, target),
			enabled = COALESCE($4::boolean, enabled)
		 WHERE id = $1 AND tenant_id = $2
		 RETURNING `+tunnelCols,
		id, tenantID, patch.Target, patch.Enabled).
		Scan(&tn.ID, &tn.TenantID, &tn.ClientID, &tn.Target, &tn.Enabled, &tn.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tunnel{}, store.ErrNotFound
	}
	if err != nil {
		return store.Tunnel{}, err
	}
	return tn, nil
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
