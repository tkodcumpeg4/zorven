package pgstore

// Organizasyon (kiraci) silme.
//
// NEDEN BURADA, Better Auth'ta DEGIL: organization <-> tenants senkronizasyonu
// yalnizca INSERT/UPDATE icin var. Better Auth'un kendi silmesi yalnizca
// "organization" satirini ve uyelikleri siler; tenants satiri, istemciler ve
// tuneller KALIR — ajanlar token'la baglanmaya devam eder ve tuneller
// internette yayinda kalirdi. Bu yuzden Better Auth'ta silme kapatildi
// (disableOrganizationDeletion) ve silme burada tek islemde yapilir.

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// CountOtherOrganizations, kullanicinin verilen organizasyon DISINDA uye oldugu
// organizasyon sayisi. Silme sonrasi kullanicinin gidecek yeri kalmali.
func (s *Store) CountOtherOrganizations(ctx context.Context, userID, exceptOrgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM "member" WHERE "userId" = $1 AND "organizationId" <> $2`,
		userID, exceptOrgID).Scan(&n)
	return n, err
}

// DeleteOrganization, kiraciyi ve TUM verisini tek islemde siler.
// Donen liste, baglantisi kesilmesi gereken istemci kimlikleridir.
//
// Silinenler: tenants satiri (istemciler, tuneller, adlar, projeler, secret'lar,
// policy'ler, token'lar, abonelik... CASCADE ile), istek loglari (FK yok, elle),
// organization satiri (uyelikler ve davetler CASCADE ile).
func (s *Store) DeleteOrganization(ctx context.Context, tenantID string) ([]string, error) {
	if tenantID == "" || tenantID == store.DefaultTenantID {
		return nil, fmt.Errorf("bu organizasyon silinemez")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Commit sonrasi etkisiz

	// Once istemci kimliklerini topla: silindikten sonra canli oturumlari
	// koparmak icin gerekecek.
	rows, err := tx.Query(ctx, `SELECT id FROM clients WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	var clientIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		clientIDs = append(clientIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	steps := []struct{ what, sql string }{
		{"istek loglari", `DELETE FROM request_logs WHERE tenant_id = $1`},
		// Bu organizasyonu aktif secmis oturumlar gecersiz kiraciya bakmasin.
		{"oturumlar", `UPDATE "session" SET "activeOrganizationId" = NULL WHERE "activeOrganizationId" = $1`},
		{"kiraci", `DELETE FROM tenants WHERE id = $1`},
		{"organizasyon", `DELETE FROM "organization" WHERE id = $1`},
	}
	for _, st := range steps {
		if _, err := tx.Exec(ctx, st.sql, tenantID); err != nil {
			return nil, fmt.Errorf("%s silinemedi: %w", st.what, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return clientIDs, nil
}
