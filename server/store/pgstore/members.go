package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// ListTeamMembers, belirtilen kiraci/organizasyona ait tum uyeleri ve sahip olduklari istemci jeton sayisini doner.
func (s *Store) ListTeamMembers(ctx context.Context, tenantID string) ([]store.TeamMember, error) {
	query := `
		SELECT m."id", m."userId", u."email", u."name", m."role", m."createdAt",
		       COALESCE(c.token_count, 0) as tokens_count
		FROM "member" m
		JOIN "user" u ON m."userId" = u."id"
		LEFT JOIN (
			SELECT user_id, count(*) as token_count
			FROM clients
			WHERE tenant_id = $1 AND user_id IS NOT NULL
			GROUP BY user_id
		) c ON c.user_id = m."userId"
		WHERE m."organizationId" = $1
		ORDER BY m."createdAt" ASC
	`
	rows, err := s.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ekip uyeleri listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.TeamMember
	for rows.Next() {
		var m store.TeamMember
		if err := rows.Scan(&m.ID, &m.UserID, &m.Email, &m.Name, &m.Role, &m.CreatedAt, &m.TokensCount); err != nil {
			return nil, err
		}
		m.Status = "active"
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Bekleyen davetleri (henuz kayit olmamis e-postalar) de "pending" olarak ekle.
	invRows, err := s.pool.Query(ctx,
		`SELECT "id", "email", "role", "createdAt" FROM "invitation"
		 WHERE "organizationId" = $1 AND "status" = 'pending'
		 ORDER BY "createdAt" ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("bekleyen davetler listelenemedi: %w", err)
	}
	defer invRows.Close()
	for invRows.Next() {
		var m store.TeamMember
		var role *string
		if err := invRows.Scan(&m.ID, &m.Email, &role, &m.CreatedAt); err != nil {
			return nil, err
		}
		if role != nil {
			m.Role = *role
		} else {
			m.Role = "member"
		}
		m.Name = strings.Split(m.Email, "@")[0]
		m.Status = "pending"
		m.TokensCount = 0
		out = append(out, m)
	}
	return out, invRows.Err()
}

// CountTeamMembers, kiraciya ait toplam uye sayisini dondurur.
func (s *Store) CountTeamMembers(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM "member" WHERE "organizationId" = $1`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("ekip uye sayisi alinamadi: %w", err)
	}
	return count, nil
}

// AddTeamMember, davet edilen e-posta icin:
//   - Kullanici ZATEN varsa: dogrudan organizasyona uye (active) olarak ekler.
//   - Kullanici YOKSA: bir bekleyen davet (invitation) kaydi olusturur. Boylece
//     Better Auth "user" tablosuna sifresiz/credential'siz placeholder satir
//     EKLENMEZ; kisi normal sekilde kayit olabilir (kayit olunca signup hook'u
//     daveti otomatik kabul edip uyeligi olusturur).
//
// inviterID: daveti yapan Better Auth kullanici id'si (varsa). Bilinmiyorsa ""
// gecilebilir; invitation.inviterId NULL olarak kaydedilir.
func (s *Store) AddTeamMember(ctx context.Context, tenantID, email, name, role, inviterID string) (store.TeamMember, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return store.TeamMember{}, errors.New("gecersiz e-posta adresi")
	}
	if name == "" {
		parts := strings.Split(email, "@")
		name = parts[0]
	}
	if role == "" {
		role = "member"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.TeamMember{}, fmt.Errorf("transaction baslatilamadi: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Kullanici mevcut mu?
	var userID string
	var userName string
	err = tx.QueryRow(ctx, `SELECT "id", "name" FROM "user" WHERE lower("email") = $1`, email).Scan(&userID, &userName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return store.TeamMember{}, fmt.Errorf("kullanici aranamadi: %w", err)
	}
	now := time.Now().UTC()

	// 2a. Kullanici YOKSA → bekleyen davet olustur.
	if errors.Is(err, pgx.ErrNoRows) {
		// Zaten bekleyen bir davet var mi?
		var existingInv string
		e := tx.QueryRow(ctx,
			`SELECT "id" FROM "invitation" WHERE "organizationId" = $1 AND lower("email") = $2 AND "status" = 'pending'`,
			tenantID, email).Scan(&existingInv)
		if e == nil {
			return store.TeamMember{}, fmt.Errorf("bu e-posta icin zaten bekleyen bir davet var")
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return store.TeamMember{}, fmt.Errorf("davet kontrolu yapilamadi: %w", e)
		}

		invID := newID("inv")
		var inviter *string
		if strings.TrimSpace(inviterID) != "" {
			inviter = &inviterID
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO "invitation" ("id", "organizationId", "email", "role", "status", "expiresAt", "inviterId", "createdAt")
			 VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)`,
			invID, tenantID, email, role, now.Add(7*24*time.Hour), inviter, now)
		if err != nil {
			return store.TeamMember{}, fmt.Errorf("davet olusturulamadi: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return store.TeamMember{}, fmt.Errorf("transaction tamamlanamadi: %w", err)
		}
		return store.TeamMember{
			ID:          invID,
			UserID:      "",
			Email:       email,
			Name:        name,
			Role:        role,
			CreatedAt:   now,
			TokensCount: 0,
			Status:      "pending",
		}, nil
	}

	// 2b. Kullanici VARSA → zaten uye mi kontrol et.
	var existingMemberID string
	err = tx.QueryRow(ctx,
		`SELECT "id" FROM "member" WHERE "organizationId" = $1 AND "userId" = $2`,
		tenantID, userID).Scan(&existingMemberID)
	if err == nil {
		return store.TeamMember{}, fmt.Errorf("bu kullanici zaten ekibin bir uyesi")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.TeamMember{}, fmt.Errorf("uyelik kontrolu yapilamadi: %w", err)
	}

	// 3. Member tablosuna ekle.
	memberID := newID("mem")
	_, err = tx.Exec(ctx,
		`INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt") VALUES ($1, $2, $3, $4, $5)`,
		memberID, tenantID, userID, role, now)
	if err != nil {
		return store.TeamMember{}, fmt.Errorf("ekip uyesi eklenemedi: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return store.TeamMember{}, fmt.Errorf("transaction tamamlanamadi: %w", err)
	}

	return store.TeamMember{
		ID:          memberID,
		UserID:      userID,
		Email:       email,
		Name:        userName,
		Role:        role,
		CreatedAt:   now,
		TokensCount: 0,
		Status:      "active",
	}, nil
}

// UpdateTeamMemberRole, bir ekip uyesinin rolunu ('admin' veya 'member') gunceller.
func (s *Store) UpdateTeamMemberRole(ctx context.Context, tenantID, memberID, role string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE "member" SET "role" = $3 WHERE "id" = $1 AND "organizationId" = $2`,
		memberID, tenantID, role)
	if err != nil {
		return fmt.Errorf("rol guncellenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// RemoveTeamMember, uyeyi organizasyondan cikarir ve bagli tum istemcilerini/tokenlarini temizler.
func (s *Store) RemoveTeamMember(ctx context.Context, tenantID, memberID string) error {
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT "userId" FROM "member" WHERE "id" = $1 AND "organizationId" = $2`,
		memberID, tenantID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Aktif uye degil — bekleyen bir davet olabilir (id davet id'si). Iptal et.
		tag, delErr := s.pool.Exec(ctx,
			`DELETE FROM "invitation" WHERE "id" = $1 AND "organizationId" = $2 AND "status" = 'pending'`,
			memberID, tenantID)
		if delErr != nil {
			return fmt.Errorf("davet iptal edilemedi: %w", delErr)
		}
		if tag.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("uye bulunamadi: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("transaction baslatilamadi: %w", err)
	}
	defer tx.Rollback(ctx)

	// Uyeligi sil
	_, err = tx.Exec(ctx, `DELETE FROM "member" WHERE "id" = $1 AND "organizationId" = $2`, memberID, tenantID)
	if err != nil {
		return fmt.Errorf("uye silinemedi: %w", err)
	}

	// Uyenin istemcilerini temizle
	_, err = tx.Exec(ctx, `DELETE FROM clients WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	if err != nil {
		return fmt.Errorf("uyeye bagli istemciler silinemedi: %w", err)
	}

	return tx.Commit(ctx)
}
