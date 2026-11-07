package pgstore

import (
	"context"
	"fmt"
	"time"
)

// OfflineClient, yakin zamanda bagli olup su an oturumu olmayan bir ajan (operator alarmi).
type OfflineClient struct {
	ID         string
	Name       string
	TenantID   string
	TenantSlug string
	Version    string
	LastSeenAt time.Time
	// HasSession: acik surumde kume yoktur; her zaman false (baglilik bellekteki
	// Hub'dan okunur, bkz. api.AgentOfflineWatch.LocalConnected).
	HasSession bool
	// OfflineNotified: bu kopus icin kiraci owner'larina bildirim gitti (yeniden baglaninca temizlenir).
	OfflineNotified bool
}

// TenantOwner, bir kiracinin owner rolundeki kullanicisi (bildirim alicisi).
type TenantOwner struct {
	UserID string
	Email  string
}

// ListRecentlyOfflineClients, kopuk ajan ADAYLARINI doner: son `within` icinde gorulmus
// ama `olderThan`dan beri gorulmeyenler (2026-11-07 olayi: kopuk ajanlari kullanicidan
// once fark etmek icin). last_seen_at yalniz el sikisma ve kopusta yazilir; bagli bir
// ajanin degeri eski olabilir, bu yuzden baglilik HasSession (kume) ya da cagiranin
// Hub'i ile ayiklanir. Uzun suredir kapali ajanlar (within disinda) dahil edilmez.
func (s *Store) ListRecentlyOfflineClients(ctx context.Context, within, olderThan time.Duration) ([]OfflineClient, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.tenant_id, COALESCE(t.slug, ''), COALESCE(c.agent_version, ''), c.last_seen_at,
		       false, (c.offline_notified_at IS NOT NULL)
		FROM clients c
		LEFT JOIN tenants t ON t.id = c.tenant_id
		WHERE c.last_seen_at IS NOT NULL
		  AND c.last_seen_at > NOW() - make_interval(secs => $1)
		  AND c.last_seen_at < NOW() - make_interval(secs => $2)
		ORDER BY c.last_seen_at ASC
		LIMIT 500`, within.Seconds(), olderThan.Seconds())
	if err != nil {
		return nil, fmt.Errorf("kopuk ajanlar listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []OfflineClient
	for rows.Next() {
		var c OfflineClient
		if err := rows.Scan(&c.ID, &c.Name, &c.TenantID, &c.TenantSlug, &c.Version, &c.LastSeenAt, &c.HasSession, &c.OfflineNotified); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkClientsOfflineNotified, verilen ajanlarin kopus bildiriminin gittigini isaretler.
func (s *Store) MarkClientsOfflineNotified(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clients SET offline_notified_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("kopus bildirimi isaretlenemedi: %w", err)
	}
	return nil
}

// TenantOwners, kiracinin (organizasyon kimligi = kiraci kimligi) owner rolundeki ve
// e-postasi olan kullanicilarini doner.
func (s *Store) TenantOwners(ctx context.Context, tenantID string) ([]TenantOwner, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u."id", u."email"
		  FROM "member" m
		  JOIN "user" u ON u."id" = m."userId"
		 WHERE m."organizationId" = $1 AND m."role" = 'owner' AND COALESCE(u."email", '') <> ''
		 ORDER BY m."createdAt", u."id"`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("kiraci owner'lari okunamadi: %w", err)
	}
	defer rows.Close()
	var out []TenantOwner
	for rows.Next() {
		var o TenantOwner
		if err := rows.Scan(&o.UserID, &o.Email); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
