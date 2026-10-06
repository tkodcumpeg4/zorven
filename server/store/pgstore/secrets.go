package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F06): Secret Vault. Degerler AES-256-GCM ile sifreli saklanir (bkz.
// secretcrypto.go). Liste ve GET degeri DONDURMEZ; yalnizca ResolveSecret cozer.

const secretMetaCols = `id, tenant_id, project_id, name, key_version, created_at, updated_at`

func scanSecretMeta(row pgx.Row, s *store.Secret) error {
	return row.Scan(&s.ID, &s.TenantID, &s.ProjectID, &s.Name, &s.KeyVersion, &s.CreatedAt, &s.UpdatedAt)
}

func (s *Store) CreateSecret(ctx context.Context, tenantID, projectID, name, plaintext string) (store.Secret, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.Secret{}, errors.New("secret adı boş olamaz")
	}
	enc, keyVersion, err := encryptSecret([]byte(plaintext))
	if err != nil {
		return store.Secret{}, err
	}
	now := time.Now().UTC()
	sec := store.Secret{
		ID:         newID("sec"),
		TenantID:   tenantID,
		ProjectID:  projectID,
		Name:       name,
		KeyVersion: keyVersion,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO secrets (id, tenant_id, project_id, name, value_enc, key_version, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		sec.ID, sec.TenantID, sec.ProjectID, sec.Name, enc, sec.KeyVersion, sec.CreatedAt, sec.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return store.Secret{}, store.ErrSecretNameTaken
		}
		return store.Secret{}, fmt.Errorf("secret oluşturulamadı: %w", err)
	}
	sec.Value = plaintext // yalnizca bu yanitta bir kez doner
	return sec, nil
}

func (s *Store) ListSecrets(ctx context.Context, tenantID, projectID string) ([]store.Secret, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+secretMetaCols+` FROM secrets
		 WHERE tenant_id = $1 AND project_id = $2
		 ORDER BY created_at DESC`, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []store.Secret
	for rows.Next() {
		var sec store.Secret
		if err := scanSecretMeta(rows, &sec); err != nil {
			return nil, err
		}
		list = append(list, sec)
	}
	if list == nil {
		list = []store.Secret{}
	}
	return list, rows.Err()
}

func (s *Store) DeleteSecret(ctx context.Context, tenantID, id string) error {
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM secrets WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("secret silinemedi: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ResolveSecret(ctx context.Context, tenantID, projectID, name string) (string, error) {
	var enc []byte
	var keyVersion int
	err := s.pool.QueryRow(ctx,
		`SELECT value_enc, key_version FROM secrets
		 WHERE tenant_id = $1 AND project_id = $2 AND name = $3`,
		tenantID, projectID, strings.TrimSpace(name)).Scan(&enc, &keyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	plain, err := decryptSecret(enc, keyVersion)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
