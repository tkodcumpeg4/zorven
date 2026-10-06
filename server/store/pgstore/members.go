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
	// NOT: Platform admin'i burada filtrelemek YANLISTI — kendi kurdugu org'larda
	// da gizleniyordu. Hayalet uyelik artik hic olusmuyor (adminSwitchTenant
	// member yazmaz) ve eskileri 0051 ile silindi; liste gercek uyelikleri gosterir.
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM "member" WHERE "organizationId" = $1`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("ekip uye sayisi alinamadi: %w", err)
	}
	return count, nil
}

// CountPendingInvitations, kiracinin suresi dolmamis bekleyen davet sayisi.
// Uye limiti (aktif + bekleyen) hesabinda kullanilir: aksi halde limit dolu bir
// kiraci sinirsiz davet gonderip hepsini kabul ettirebilirdi.
func (s *Store) CountPendingInvitations(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM "invitation"
		 WHERE "organizationId" = $1 AND "status" = 'pending' AND "expiresAt" > now()`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("bekleyen davet sayisi alinamadi: %w", err)
	}
	return count, nil
}

// AddTeamMember, davet edilen e-posta icin HER ZAMAN bekleyen bir davet
// (invitation) olusturur — kullanici sistemde olsa da olmasa da. Uyelik ancak
// davetli e-postadaki linkten kendi hesabiyla KABUL edince olusur
// (AcceptInvitation). Onceden var olan kullanicilar onaysiz dogrudan ekleniyordu;
// kisi habersizce baska bir organizasyona dahil oluyordu.
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

	now := time.Now().UTC()

	// 1. Kullanici varsa ve zaten uyeyse davet anlamsiz.
	var existingMemberID string
	err = tx.QueryRow(ctx,
		`SELECT m."id" FROM "member" m JOIN "user" u ON u."id" = m."userId"
		 WHERE m."organizationId" = $1 AND lower(u."email") = $2`,
		tenantID, email).Scan(&existingMemberID)
	if err == nil {
		return store.TeamMember{}, fmt.Errorf("bu kullanici zaten ekibin bir uyesi")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.TeamMember{}, fmt.Errorf("uyelik kontrolu yapilamadi: %w", err)
	}

	// 2. Zaten bekleyen (suresi dolmamis) bir davet var mi? Suresi dolmus eski
	// davet yeni daveti engellemesin: iptal edilir.
	var existingInv string
	var existingExp time.Time
	e := tx.QueryRow(ctx,
		`SELECT "id", "expiresAt" FROM "invitation"
		 WHERE "organizationId" = $1 AND lower("email") = $2 AND "status" = 'pending'`,
		tenantID, email).Scan(&existingInv, &existingExp)
	if e == nil {
		if existingExp.After(now) {
			return store.TeamMember{}, fmt.Errorf("bu e-posta icin zaten bekleyen bir davet var")
		}
		if _, err := tx.Exec(ctx, `UPDATE "invitation" SET "status" = 'canceled' WHERE "id" = $1`, existingInv); err != nil {
			return store.TeamMember{}, fmt.Errorf("eski davet kapatilamadi: %w", err)
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return store.TeamMember{}, fmt.Errorf("davet kontrolu yapilamadi: %w", e)
	}

	// 3. Bekleyen davet olustur (7 gun gecerli).
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

const invitationSelect = `
	SELECT i."id", i."organizationId", COALESCE(o."name", i."organizationId"), i."email",
	       COALESCE(i."role", 'member'), i."status", COALESCE(u."name", ''), i."expiresAt"
	FROM "invitation" i
	LEFT JOIN "organization" o ON o."id" = i."organizationId"
	LEFT JOIN "user" u ON u."id" = i."inviterId"`

func scanInvitation(row pgx.Row, inv *store.TeamInvitation) error {
	return row.Scan(&inv.ID, &inv.OrganizationID, &inv.OrganizationName, &inv.Email,
		&inv.Role, &inv.Status, &inv.InviterName, &inv.ExpiresAt)
}

// GetInvitation, davet ayrintisini (org adi, davet eden) doner.
func (s *Store) GetInvitation(ctx context.Context, id string) (store.TeamInvitation, error) {
	var inv store.TeamInvitation
	err := scanInvitation(s.pool.QueryRow(ctx, invitationSelect+` WHERE i."id" = $1`, id), &inv)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.TeamInvitation{}, store.ErrNotFound
	}
	return inv, err
}

// AcceptInvitation, daveti kabul eder ve uyeligi olusturur. Davet YALNIZCA
// gonderildigi e-postanin sahibi tarafindan kabul edilebilir (link baskasinin
// eline gecse bile ise yaramaz). Idempotent: kisi zaten uyeyse hata vermez.
func (s *Store) AcceptInvitation(ctx context.Context, id, userID, userEmail string) (store.TeamInvitation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.TeamInvitation{}, fmt.Errorf("transaction baslatilamadi: %w", err)
	}
	defer tx.Rollback(ctx)

	var inv store.TeamInvitation
	err = scanInvitation(tx.QueryRow(ctx, invitationSelect+` WHERE i."id" = $1 FOR UPDATE OF i`, id), &inv)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.TeamInvitation{}, store.ErrNotFound
	}
	if err != nil {
		return store.TeamInvitation{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(inv.Email), strings.TrimSpace(userEmail)) {
		return store.TeamInvitation{}, store.ErrInvitationEmail
	}
	if inv.Status != "pending" {
		return store.TeamInvitation{}, store.ErrInvitationClosed
	}
	if !inv.ExpiresAt.After(time.Now()) {
		return store.TeamInvitation{}, store.ErrInvitationExpired
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
		 SELECT $1, $2, $3, $4, now()
		 WHERE NOT EXISTS (SELECT 1 FROM "member" WHERE "organizationId" = $2 AND "userId" = $3)`,
		newID("mem"), inv.OrganizationID, userID, inv.Role); err != nil {
		return store.TeamInvitation{}, fmt.Errorf("uyelik olusturulamadi: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE "invitation" SET "status" = 'accepted' WHERE "id" = $1`, id); err != nil {
		return store.TeamInvitation{}, fmt.Errorf("davet guncellenemedi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return store.TeamInvitation{}, fmt.Errorf("transaction tamamlanamadi: %w", err)
	}
	inv.Status = "accepted"
	return inv, nil
}

// DeclineInvitation, daveti reddeder (yalnizca davetli e-postanin sahibi).
func (s *Store) DeclineInvitation(ctx context.Context, id, userEmail string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE "invitation" SET "status" = 'rejected'
		 WHERE "id" = $1 AND lower("email") = lower($2) AND "status" = 'pending'`,
		id, strings.TrimSpace(userEmail))
	if err != nil {
		return fmt.Errorf("davet reddedilemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListInvitationsForEmail, e-postaya gelmis bekleyen ve suresi dolmamis davetler.
func (s *Store) ListInvitationsForEmail(ctx context.Context, email string) ([]store.TeamInvitation, error) {
	rows, err := s.pool.Query(ctx,
		invitationSelect+` WHERE lower(i."email") = lower($1) AND i."status" = 'pending' AND i."expiresAt" > now()
		 ORDER BY i."createdAt" DESC`, strings.TrimSpace(email))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TeamInvitation{}
	for rows.Next() {
		var inv store.TeamInvitation
		if err := scanInvitation(rows, &inv); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// FirstMembership, kullanicinin en eski uyeligi. Oturumdaki aktif org'da
// uyeligi kalmamis (cikarilmis) kullaniciyi kendi org'una dusurmek icin.
func (s *Store) FirstMembership(ctx context.Context, userID string) (string, string, error) {
	var orgID, role string
	err := s.pool.QueryRow(ctx,
		`SELECT "organizationId", "role" FROM "member" WHERE "userId" = $1 ORDER BY "createdAt" ASC LIMIT 1`,
		userID).Scan(&orgID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", store.ErrNotFound
	}
	return orgID, role, err
}

// UpdateTeamMemberRole, bir ekip uyesinin rolunu ('admin' veya 'member') gunceller.
func (s *Store) UpdateTeamMemberRole(ctx context.Context, tenantID, memberID, role string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE "member" SET "role" = $3 WHERE "id" = $1 AND "organizationId" = $2 AND "role" <> 'owner'`,
		memberID, tenantID, role)
	if err != nil {
		return fmt.Errorf("rol guncellenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Aktif uye degilse bekleyen davetin rolu olabilir (panel listede
		// davetleri de gosterir); kabulde bu rol kullanilir.
		tag, err = s.pool.Exec(ctx,
			`UPDATE "invitation" SET "role" = $3 WHERE "id" = $1 AND "organizationId" = $2 AND "status" = 'pending'`,
			memberID, tenantID, role)
		if err != nil {
			return fmt.Errorf("davet rolu guncellenemedi: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return store.ErrNotFound
		}
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

	// Uyenin kisisel API token'larini iptal et: aksi halde cikarilan kisi
	// token'iyla kiraciya erismeye devam ederdi.
	_, err = tx.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = now()
		 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		tenantID, userID)
	if err != nil {
		return fmt.Errorf("uyenin API token'lari iptal edilemedi: %w", err)
	}

	return tx.Commit(ctx)
}
