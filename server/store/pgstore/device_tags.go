package pgstore

// Cihaz etiketleri (FAZ 3 / F16) + uyelik rolu. ACIK SURUM: kaynak politikasi
// (etiket+rol varsayilan-DENY) ticari katmandadir; burada yalnizca etiket
// meta verisi ve rol sorgusu vardir.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// GetDeviceTags, tek cihazin etiketleri.
func (s *Store) GetDeviceTags(ctx context.Context, tenantID, clientID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM device_tags WHERE tenant_id = $1 AND client_id = $2`, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ListDeviceTagsByTenant, kiracidaki TUM cihazlarin etiketleri (client_id -> etiketler).
// Liste filtrelemesinde her cihaz icin ayri sorgu (N+1) atmamak icin.
func (s *Store) ListDeviceTagsByTenant(ctx context.Context, tenantID string) (map[string]map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT client_id, key, value FROM device_tags WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var c, k, v string
		if err := rows.Scan(&c, &k, &v); err != nil {
			return nil, err
		}
		if out[c] == nil {
			out[c] = map[string]string{}
		}
		out[c][k] = v
	}
	return out, rows.Err()
}

// SetDeviceTags, cihazin etiketlerini TAMAMEN degistirir (tek islemde).
// Anahtarlar kucuk harfe cevrilir: eslesme anahtar duyarsizdir.
func (s *Store) SetDeviceTags(ctx context.Context, tenantID, clientID string, tags map[string]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Commit sonrasi etkisiz

	if _, err := tx.Exec(ctx,
		`DELETE FROM device_tags WHERE tenant_id = $1 AND client_id = $2`, tenantID, clientID); err != nil {
		return err
	}
	for k, v := range tags {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO device_tags (client_id, tenant_id, key, value) VALUES ($1,$2,$3,$4)`,
			clientID, tenantID, k, strings.TrimSpace(v)); err != nil {
			return fmt.Errorf("etiket yazilamadi: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// GetMemberRole, kullanicinin kiracidaki rolunu doner (owner | admin | member).
// Uyelik yoksa ("", nil) doner: rol bilinmiyorsa ayricalik da YOKTUR.
//
// Neden gerekli: API token'lari kontekste "api_token" rolu ile gelir. Gercek
// rol cozulmezse varsayilan DENY altinda token sahibi (owner bile) cihazlarina
// erisemezdi.
func (s *Store) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT "role" FROM "member" WHERE "organizationId" = $1 AND "userId" = $2 LIMIT 1`,
		tenantID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}
