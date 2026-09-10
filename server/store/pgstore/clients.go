package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// clientCols, tum istemci sorgularinin ortak kolon listesi.
const clientCols = `id, tenant_id, COALESCE(user_id, ''), name, token_id, token_hash, created_at`

func (s *Store) CreateClient(ctx context.Context, tenantID, name, tokenID, tokenHash string) (store.Client, error) {
	c := store.Client{
		ID:        newID("cli"),
		TenantID:  tenantID,
		Name:      name,
		TokenID:   tokenID,
		TokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
		Status:    "offline",
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO clients (id, tenant_id, name, token_id, token_hash, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		c.ID, c.TenantID, c.Name, c.TokenID, c.TokenHash, c.CreatedAt)
	if err != nil {
		return store.Client{}, fmt.Errorf("istemci olusturulamadi: %w", err)
	}
	return c, nil
}

// CreateMemberClient, belirli bir ekip uyesine bagli istemci olusturur.
func (s *Store) CreateMemberClient(ctx context.Context, tenantID, userID, name, tokenID, tokenHash string) (store.Client, error) {
	c := store.Client{
		ID:        newID("cli"),
		TenantID:  tenantID,
		UserID:    userID,
		Name:      name,
		TokenID:   tokenID,
		TokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
		Status:    "offline",
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO clients (id, tenant_id, user_id, name, token_id, token_hash, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.TenantID, c.UserID, c.Name, c.TokenID, c.TokenHash, c.CreatedAt)
	if err != nil {
		return store.Client{}, fmt.Errorf("uye istemcisi olusturulamadi: %w", err)
	}
	return c, nil
}

// ListMemberClients, belirli bir ekip uyesine bagli tum istemcileri doner.
func (s *Store) ListMemberClients(ctx context.Context, tenantID, userID string) ([]store.Client, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+clientCols+` FROM clients WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at DESC`,
		tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("uye istemcileri listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Client
	for rows.Next() {
		var c store.Client
		if err := rows.Scan(&c.ID, &c.TenantID, &c.UserID, &c.Name, &c.TokenID, &c.TokenHash, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Status = "offline"
		out = append(out, c)
	}
	return out, rows.Err()
}

// ImportClient, satiri OLDUGU GIBI yazar (tek seferlik SQLite gocu icin):
// id, token hash ve created_at korunur, yeni id URETILMEZ. Ayni id yeniden
// aktarilirsa sessizce atlanir, boylece goc tekrar calistirilabilir.
func (s *Store) ImportClient(ctx context.Context, c store.Client) error {
	if c.TenantID == "" {
		c.TenantID = store.DefaultTenantID
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO clients (id, tenant_id, name, token_id, token_hash, created_at) VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO NOTHING`,
		c.ID, c.TenantID, c.Name, c.TokenID, c.TokenHash, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("istemci ice aktarilamadi (%s): %w", c.ID, err)
	}
	return nil
}

func (s *Store) GetClient(ctx context.Context, tenantID, id string) (store.Client, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+clientCols+` FROM clients WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	return scanClientRow(row)
}

// GetClientByTokenID, KIRACIDAN BAGIMSIZDIR: istemci baglanirken hangi kiraciya
// ait oldugu bilinmez, kimligi token'in kendisidir. Donen Client.TenantID
// kiraciyi belirler.
func (s *Store) GetClientByTokenID(ctx context.Context, tokenID string) (store.Client, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+clientCols+` FROM clients WHERE token_id = $1`, tokenID)
	return scanClientRow(row)
}

func scanClientRow(row pgx.Row) (store.Client, error) {
	var c store.Client
	err := row.Scan(&c.ID, &c.TenantID, &c.UserID, &c.Name, &c.TokenID, &c.TokenHash, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Baska kiracinin kaydi da "yok" gorunur: VARLIGINI bile sizdirma.
		return store.Client{}, store.ErrNotFound
	}
	if err != nil {
		return store.Client{}, err
	}
	c.Status = "offline"
	return c, nil
}

func (s *Store) ListClients(ctx context.Context, tenantID string) ([]store.Client, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+clientCols+` FROM clients WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("istemciler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Client
	for rows.Next() {
		var c store.Client
		if err := rows.Scan(&c.ID, &c.TenantID, &c.UserID, &c.Name, &c.TokenID, &c.TokenHash, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Status = "offline"
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteClient(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM clients WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) RotateClientToken(ctx context.Context, tenantID, id, tokenID, tokenHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE clients SET token_id = $3, token_hash = $4
		 WHERE id = $1 AND tenant_id = $2`, id, tenantID, tokenID, tokenHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
