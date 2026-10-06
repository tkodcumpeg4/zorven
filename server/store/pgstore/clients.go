package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// clientCols, tum istemci sorgularinin ortak kolon listesi.
const clientCols = `id, tenant_id, COALESCE(user_id, ''), name, token_id, token_hash, created_at, COALESCE(project_id, ''), ` +
	// Cihaz alanlari (FAZ 3 / F14). Hepsi NULL olabilir: eski ajanlar
	// gondermez ve gondermemeleri hata degildir.
	`COALESCE(hostname, ''), COALESCE(os, ''), COALESCE(arch, ''), ips, COALESCE(agent_version, ''), last_metrics, last_seen_at`

func (s *Store) CreateClientWithProject(ctx context.Context, tenantID, name, tokenID, tokenHash, projectID string) (store.Client, error) {
	if projectID == "" {
		if def, err := s.GetDefaultProject(ctx, tenantID); err == nil {
			projectID = def.ID
		}
	}
	c := store.Client{
		ID:        newID("cli"),
		TenantID:  tenantID,
		ProjectID: projectID,
		Name:      name,
		TokenID:   tokenID,
		TokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
		Status:    "offline",
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO clients (id, tenant_id, name, token_id, token_hash, created_at, project_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.TenantID, c.Name, c.TokenID, c.TokenHash, c.CreatedAt, c.ProjectID)
	if err != nil {
		return store.Client{}, fmt.Errorf("istemci olusturulamadi: %w", err)
	}
	return c, nil
}

func (s *Store) CreateClient(ctx context.Context, tenantID, name, tokenID, tokenHash string) (store.Client, error) {
	return s.CreateClientWithProject(ctx, tenantID, name, tokenID, tokenHash, "")
}

// CreateMemberClient, belirli bir ekip uyesine bagli istemci olusturur.
func (s *Store) CreateMemberClient(ctx context.Context, tenantID, userID, name, tokenID, tokenHash string) (store.Client, error) {
	var projectID string
	if def, err := s.GetDefaultProject(ctx, tenantID); err == nil {
		projectID = def.ID
	}
	c := store.Client{
		ID:        newID("cli"),
		TenantID:  tenantID,
		ProjectID: projectID,
		UserID:    userID,
		Name:      name,
		TokenID:   tokenID,
		TokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
		Status:    "offline",
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO clients (id, tenant_id, user_id, name, token_id, token_hash, created_at, project_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.TenantID, c.UserID, c.Name, c.TokenID, c.TokenHash, c.CreatedAt, c.ProjectID)
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
		// scanClientRow ile ayni yerden okunur: kolon listesi degistiginde
		// tek yerde guncellenir, listeler sessizce bozulmaz.
		c, err := scanClientRow(rows)
		if err != nil {
			return nil, err
		}
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
	var ips []string
	var metrics []byte
	var lastSeen *time.Time
	err := row.Scan(&c.ID, &c.TenantID, &c.UserID, &c.Name, &c.TokenID, &c.TokenHash, &c.CreatedAt, &c.ProjectID,
		&c.Hostname, &c.OS, &c.Arch, &ips, &c.AgentVersion, &metrics, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		// Baska kiracinin kaydi da "yok" gorunur: VARLIGINI bile sizdirma.
		return store.Client{}, store.ErrNotFound
	}
	if err != nil {
		return store.Client{}, err
	}
	c.IPs = ips
	if len(metrics) > 0 {
		var m protocol.Metrics
		if json.Unmarshal(metrics, &m) == nil {
			c.LastMetrics = &m
		}
		// Bozuk metrik kaydi tum satiri dusurmemeli; metriksiz devam ederiz.
	}
	if lastSeen != nil {
		u := lastSeen.UTC()
		c.LastSeenAt = &u
	}
	c.Status = "offline"
	return c, nil
}

// UpdateDeviceInfo, el sikismasinda gelen cihaz bilgilerini kalicilastirir
// (FAZ 3 / F14). Bos gelen alanlar MEVCUDU EZMEZ: eski bir ajan baglandiginda
// daha once toplanmis bilgiyi silmemeli.
func (s *Store) UpdateDeviceInfo(ctx context.Context, clientID string, d store.DeviceInfo) error {
	var metrics any
	if d.Metrics != nil {
		b, err := json.Marshal(d.Metrics)
		if err == nil {
			metrics = b
		}
	}
	var ips any
	if len(d.IPs) > 0 {
		ips = d.IPs
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE clients SET
			hostname      = COALESCE(NULLIF($2, ''), hostname),
			os            = COALESCE(NULLIF($3, ''), os),
			arch          = COALESCE(NULLIF($4, ''), arch),
			ips           = COALESCE($5::text[], ips),
			agent_version = COALESCE(NULLIF($6, ''), agent_version),
			last_metrics  = COALESCE($7::jsonb, last_metrics),
			last_seen_at  = now()
		 WHERE id = $1`,
		clientID, d.Hostname, d.OS, d.Arch, ips, d.AgentVersion, metrics)
	return err
}

// TouchDeviceSeen, yalnizca "en son gorulme" zamanini gunceller.
// Baglanti kopusunda cagrilir: cihaz offline olsa da ne zaman gorundugu bilinsin.
func (s *Store) TouchDeviceSeen(ctx context.Context, clientID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE clients SET last_seen_at = now() WHERE id = $1`, clientID)
	return err
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
		// scanClientRow ile ayni yerden okunur: kolon listesi degistiginde
		// tek yerde guncellenir, listeler sessizce bozulmaz.
		c, err := scanClientRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListClientsByProject(ctx context.Context, tenantID, projectID string) ([]store.Client, error) {
	if projectID == "" {
		return s.ListClients(ctx, tenantID)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+clientCols+` FROM clients WHERE tenant_id = $1 AND project_id = $2 ORDER BY created_at`,
		tenantID, projectID)
	if err != nil {
		return nil, fmt.Errorf("istemciler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Client
	for rows.Next() {
		// scanClientRow ile ayni yerden okunur: kolon listesi degistiginde
		// tek yerde guncellenir, listeler sessizce bozulmaz.
		c, err := scanClientRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []store.Client{}
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
