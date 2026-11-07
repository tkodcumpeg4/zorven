package pgstore

// Hesap silme (kisisel veri).
//
// Akis: panel -> Better Auth /delete-user (parola, e-posta onayi) -> beforeDelete
// -> Go (bu dosya) -> Better Auth kullanici satirini siler (oturum, hesap, uyelik,
// 2FA, e-posta risk satirlari CASCADE ile gider).
//
// Neden Go'da: tek uyeli organizasyonun silinmesi tunelleri/istemcileri/alan
// adlarini/posta kutularini/uygulamalari da kaldirir ve bellekteki canli durumu
// (istemci baglantilari, yonlendirme) etkiler; bu zaten org_delete.go'da vardir ve
// BURADAN AYNEN yeniden kullanilir (deleteOrganizationTx).
//
// Siniflandirma (planAccountDeletion):
//   - Tek uyeli organizasyon (kisisel)          -> SIL (tum kaynaklariyla)
//   - Baska uyeli organizasyon, ben owner degil -> AYRIL (uyelik + istemci + kisisel API anahtari)
//   - Baska uyeli organizasyonun TEK sahibi     -> ENGEL (sahipligi devret ya da org'u sil)
//   - Platform organizasyonu (ten_default) yoneticisi -> ENGEL
//
// Denetim kaydi (audit_log) SILINMEZ (guvenlik kaydi): aktor e-posta/kimligi
// kararli bir takma ada cevrilir, metin alanlarindaki e-posta takma adla degisir,
// IP bosaltilir. Tablo ticari katmandadir; yoksa adim atlanir.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// pgQuerier, planin havuz ya da islem uzerinde calismasini saglar.
type pgQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Siniflandirma sonuclari.
const (
	acctDelete = "delete"
	acctLeave  = "leave"
	acctBlock  = "block"
)

// classifyAccountOrg, bir uyeligin hesap silmedeki akibetini belirler (saf islev).
//
//	members     organizasyondaki toplam uye sayisi (kullanici dahil)
//	otherOwners kullanici disindaki owner sayisi
func classifyAccountOrg(orgID, role string, members, otherOwners int) (kind, reason string) {
	privileged := role == "owner" || role == "admin"
	if orgID == store.DefaultTenantID {
		// Platform organizasyonu silinemez; yoneticisi hesabini panelden silemez
		// (platform sahipsiz kalirdi). Sade uye yalnizca ayrilir.
		if privileged || members <= 1 {
			return acctBlock, store.AccountBlockPlatform
		}
		return acctLeave, ""
	}
	if members <= 1 {
		return acctDelete, ""
	}
	if role == "owner" && otherOwners == 0 {
		return acctBlock, store.AccountBlockSoleOwner
	}
	return acctLeave, ""
}

// PlanAccountDeletion, salt okunur onizleme (panel metni ve engeller icin).
func (s *Store) PlanAccountDeletion(ctx context.Context, userID string) (store.AccountDeletionPlan, error) {
	return planAccountDeletion(ctx, s.pool, userID, true)
}

func planAccountDeletion(ctx context.Context, q pgQuerier, userID string, withCounts bool) (store.AccountDeletionPlan, error) {
	plan := store.AccountDeletionPlan{
		UserID:   userID,
		Delete:   []store.AccountOrg{},
		Leave:    []store.AccountOrg{},
		Blockers: []store.AccountOrg{},
	}
	rows, err := q.Query(ctx, `
		SELECT o."id", o."slug", o."name", m."role",
		       (SELECT count(*) FROM "member" x WHERE x."organizationId" = o."id")::int,
		       (SELECT count(*) FROM "member" x
		         WHERE x."organizationId" = o."id" AND x."role" = 'owner' AND x."userId" <> m."userId")::int
		  FROM "member" m
		  JOIN "organization" o ON o."id" = m."organizationId"
		 WHERE m."userId" = $1
		 ORDER BY m."createdAt", o."id"`, userID)
	if err != nil {
		return plan, fmt.Errorf("uyelikler okunamadi: %w", err)
	}
	type row struct {
		org         store.AccountOrg
		otherOwners int
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.org.ID, &r.org.Slug, &r.org.Name, &r.org.Role, &r.org.Members, &r.otherOwners); err != nil {
			rows.Close()
			return plan, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return plan, err
	}

	for _, r := range all {
		kind, reason := classifyAccountOrg(r.org.ID, r.org.Role, r.org.Members, r.otherOwners)
		switch kind {
		case acctBlock:
			r.org.Reason = reason
			plan.Blockers = append(plan.Blockers, r.org)
		case acctDelete:
			if withCounts {
				countOrgContents(ctx, q, &r.org)
			}
			plan.Delete = append(plan.Delete, r.org)
		default:
			plan.Leave = append(plan.Leave, r.org)
		}
	}
	return plan, nil
}

// countOrgContents, silinecek organizasyonun icerik sayilarini doldurur (yalniz
// onizleme metni icin; EN IYI CABA: tablo yoksa ya da sorgu basarisizsa 0 kalir).
func countOrgContents(ctx context.Context, q pgQuerier, o *store.AccountOrg) {
	o.Tunnels = countTenantRows(ctx, q, "tunnels", "", o.ID)
	o.Clients = countTenantRows(ctx, q, "clients", "", o.ID)
	o.Domains = countTenantRows(ctx, q, "hostnames", "AND type = 'custom'", o.ID)
	o.Apps = countTenantRows(ctx, q, "apps", "", o.ID)
	o.Secrets = countTenantRows(ctx, q, "secrets", "", o.ID)
}

// countTenantRows: table/extra yalniz kod sabitleridir (disaridan gelmez).
func countTenantRows(ctx context.Context, q pgQuerier, table, extra, tenantID string) int {
	if !tableExists(ctx, q, table) {
		return 0
	}
	var n int
	if err := q.QueryRow(ctx,
		fmt.Sprintf(`SELECT count(*)::int FROM %s WHERE tenant_id = $1 %s`, table, extra), tenantID).Scan(&n); err != nil {
		return 0
	}
	return n
}

// tableExists, tablo bu surumde var mi (ticari katman tablolari acik surumde yoktur).
func tableExists(ctx context.Context, q pgQuerier, table string) bool {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT to_regclass($1::text) IS NOT NULL`, table).Scan(&ok); err != nil {
		return false
	}
	return ok
}

// PurgeAccount, hesabin organizasyon uyeliklerini ve kisisel verisini TEK islemde
// temizler (kullanici satirina dokunmaz; onu Better Auth siler). Engel varsa
// *store.AccountBlockedError doner ve hicbir sey degismez. Idempotenttir: ikinci
// cagrida yapilacak is kalmaz.
func (s *Store) PurgeAccount(ctx context.Context, userID string) (store.AccountPurgeResult, error) {
	res := store.AccountPurgeResult{
		Pseudonym:   store.AccountPseudonym(userID),
		DeletedOrgs: []store.AccountOrg{},
		LeftOrgs:    []store.AccountOrg{},
		ClientIDs:   []string{},
	}
	if userID == "" {
		return res, fmt.Errorf("kullanici kimligi bos")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Commit sonrasi etkisiz
	if err := relaxTimeout(ctx, tx); err != nil {
		return res, err
	}

	// Kullanicinin organizasyon satirlarini kilitle: FK denetimi (member INSERT)
	// FOR KEY SHARE alir ve FOR UPDATE ile cakisir; bu yuzden plan ile silme
	// arasinda eszamanli bir davet kabulu yeni uye ekleyemez. Sabit sirayla
	// (id) kilitlenir: iki eszamanli silme birbirini kilitlemez.
	if _, err := tx.Exec(ctx, `
		SELECT o."id" FROM "organization" o
		  JOIN "member" m ON m."organizationId" = o."id"
		 WHERE m."userId" = $1
		 ORDER BY o."id"
		   FOR UPDATE OF o`, userID); err != nil {
		return res, fmt.Errorf("organizasyonlar kilitlenemedi: %w", err)
	}

	plan, err := planAccountDeletion(ctx, tx, userID, false)
	if err != nil {
		return res, err
	}
	if !plan.CanDelete() {
		return res, &store.AccountBlockedError{Plan: plan}
	}

	// E-posta: denetim kaydindaki aktoru/metni takma ada cevirmek ve kullaniciya
	// gelen davetleri silmek icin. Satir yoksa (onceki bir deneme sildi) bos kalir.
	var email string
	if err := tx.QueryRow(ctx, `SELECT lower("email") FROM "user" WHERE "id" = $1`, userID).Scan(&email); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, fmt.Errorf("kullanici okunamadi: %w", err)
	}

	// Kullanicinin aktif kisisel API anahtari sayisi (rapor icin; org silme/ayrilma
	// adimlari bunlari iptal eder ya da kiracilariyla birlikte siler).
	if err := tx.QueryRow(ctx,
		`SELECT count(*)::int FROM api_tokens WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&res.RevokedTokens); err != nil {
		return res, fmt.Errorf("API anahtarlari sayilamadi: %w", err)
	}

	auditOK := tableExists(ctx, tx, "audit_log")
	audit := func(tenantID, action, target, detail string) error {
		if !auditOK {
			return nil
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO audit_log (id, tenant_id, actor, action, target, detail, ip) VALUES ($1,$2,$3,$4,$5,$6,'')`,
			newID("aud"), tenantID, res.Pseudonym, action, target, detail)
		return err
	}

	// 1. Tek uyeli organizasyonlar: "Organizasyonu sil" ile ayni adimlar.
	for _, o := range plan.Delete {
		ids, err := deleteOrganizationTx(ctx, tx, o.ID)
		if err != nil {
			return res, err
		}
		res.ClientIDs = append(res.ClientIDs, ids...)
		res.DeletedOrgs = append(res.DeletedOrgs, o)
		// Kiraci gitti; kayit yine de tutulur (audit_log'da FK yok).
		if err := audit(o.ID, "organization.delete", o.ID, o.Slug+" (hesap silme)"); err != nil {
			return res, fmt.Errorf("denetim kaydi yazilamadi: %w", err)
		}
	}

	// 2. Baska uyeli organizasyonlar: uyelik, istemciler, kisisel API anahtarlari.
	for _, o := range plan.Leave {
		if _, err := tx.Exec(ctx,
			`DELETE FROM "member" WHERE "organizationId" = $1 AND "userId" = $2`, o.ID, userID); err != nil {
			return res, fmt.Errorf("uyelik silinemedi: %w", err)
		}
		ids, err := removeMemberResourcesTx(ctx, tx, o.ID, userID)
		if err != nil {
			return res, err
		}
		res.ClientIDs = append(res.ClientIDs, ids...)
		res.LeftOrgs = append(res.LeftOrgs, o)
		if err := audit(o.ID, "account.delete", res.Pseudonym, "role="+o.Role); err != nil {
			return res, fmt.Errorf("denetim kaydi yazilamadi: %w", err)
		}
	}

	// 3. Artan istemciler (uyeligi onceden dusmus kiracilarda kalmis olabilir) ve
	// kisisel API anahtarlari. Servis hesabi anahtarlari organizasyona aittir.
	leftover, err := tx.Query(ctx, `DELETE FROM clients WHERE user_id = $1 RETURNING id`, userID)
	if err != nil {
		return res, fmt.Errorf("istemciler silinemedi: %w", err)
	}
	for leftover.Next() {
		var id string
		if err := leftover.Scan(&id); err != nil {
			leftover.Close()
			return res, err
		}
		res.ClientIDs = append(res.ClientIDs, id)
	}
	leftover.Close()
	if err := leftover.Err(); err != nil {
		return res, fmt.Errorf("istemciler silinemedi: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = now()
		  WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return res, fmt.Errorf("API anahtarlari iptal edilemedi: %w", err)
	}

	// 4. Davetler: kullanicinin gonderdikleri kalir (davet eden bos), kendisine
	// gelenler (e-postasi) silinir.
	if _, err := tx.Exec(ctx, `UPDATE "invitation" SET "inviterId" = NULL WHERE "inviterId" = $1`, userID); err != nil {
		return res, fmt.Errorf("davetler guncellenemedi: %w", err)
	}
	if email != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM "invitation" WHERE lower("email") = $1`, email); err != nil {
			return res, fmt.Errorf("davetler silinemedi: %w", err)
		}
	}

	// 5. Kullanici kimligine bagli FK'siz kayitlar (tablolar surume gore yoktur).
	optional := []struct{ table, sql string }{
		{"oauth_codes", `DELETE FROM oauth_codes WHERE user_id = $1`},
		{"oauth_grants", `DELETE FROM oauth_grants WHERE user_id = $1`}, // yenileme jetonlari CASCADE
		{"announcement_prefs", `DELETE FROM announcement_prefs WHERE user_id = $1`},
		{"updates_email_deliveries", `DELETE FROM updates_email_deliveries WHERE user_id = $1`},
		{"user_idp_groups", `DELETE FROM user_idp_groups WHERE user_id = $1`},
		// subject = tenant_id || ':' || user_id
		{"session_epochs", `DELETE FROM session_epochs WHERE right(subject, length($1::text) + 1) = ':' || $1::text`},
	}
	for _, st := range optional {
		if !tableExists(ctx, tx, st.table) {
			continue
		}
		if _, err := tx.Exec(ctx, st.sql, userID); err != nil {
			return res, fmt.Errorf("%s temizlenemedi: %w", st.table, err)
		}
	}
	// Tek kullanimlik dogrulama/sifirlama/silme jetonlari (deger = kullanici kimligi).
	if _, err := tx.Exec(ctx, `DELETE FROM "verification" WHERE "value" = $1`, userID); err != nil {
		return res, fmt.Errorf("dogrulama kayitlari silinemedi: %w", err)
	}

	// 6. Denetim kaydi: SILINMEZ, kisisel alanlar anonimlestirilir.
	if auditOK {
		idents := []string{userID}
		if email != "" {
			idents = append(idents, email)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE audit_log SET actor = $1, ip = '' WHERE actor = ANY($2::text[])`, res.Pseudonym, idents)
		if err != nil {
			return res, fmt.Errorf("denetim kaydi anonimlestirilemedi: %w", err)
		}
		res.AuditRows = int(tag.RowsAffected())
		if email != "" {
			// strpos: LIKE ozel karakterleri (e-postadaki '_') kacis gerektirmez.
			for _, col := range []string{"target", "detail"} {
				if _, err := tx.Exec(ctx,
					fmt.Sprintf(`UPDATE audit_log SET %[1]s = replace(%[1]s, $1::text, $2::text) WHERE strpos(%[1]s, $1::text) > 0`, col),
					email, res.Pseudonym); err != nil {
					return res, fmt.Errorf("denetim kaydi metni anonimlestirilemedi: %w", err)
				}
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.ClientIDs = dedupStrings(res.ClientIDs)
	return res, nil
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
