package pgstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// lockMailFolders, ayni kutunun klasor degisikliklerini siralar (sinir/benzersizlik).
func lockMailFolders(ctx context.Context, tx pgx.Tx, tenantID, mailbox string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "mailfolders|"+tenantID+"|"+mailbox)
	if err != nil {
		return fmt.Errorf("mail klasoru kilitlenemedi: %w", err)
	}
	return nil
}

// folderNameMap, kutudaki klasor adlarini kucuk harf -> gercek ad olarak doner.
func folderNameMap(ctx context.Context, tx pgx.Tx, tenantID, mailbox string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT name FROM mail_folders WHERE tenant_id = $1 AND mailbox = $2`, tenantID, mailbox)
	if err != nil {
		return nil, fmt.Errorf("mail klasorleri okunamadi: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		m[strings.ToLower(n)] = n
	}
	return m, rows.Err()
}

// canonParents, adin ust klasor on ekini mevcut klasorlerin yazimiyla esler
// (kucuk/buyuk harf tutarliligi). Donus: ust klasorlerin gercek adlari ve
// adin kanonik hali.
func canonParents(name string, existing map[string]string) (parents []string, canon string) {
	segs := strings.Split(name, "/")
	cur := ""
	for i, seg := range segs {
		if i == 0 {
			cur = seg
		} else {
			cur += "/" + seg
		}
		if i == len(segs)-1 {
			break // son bilesen kullanicinin yazdigi gibi kalir
		}
		if got, ok := existing[strings.ToLower(cur)]; ok {
			cur = got
		}
		parents = append(parents, cur)
	}
	return parents, cur
}

// createMailFolderTx, klasoru (ve eksik ust klasorlerini) olusturur. ignoreExists
// true ise klasor zaten varsa hata vermez (panel tasima yolu).
func createMailFolderTx(ctx context.Context, tx pgx.Tx, tenantID, mailbox, name, specialUse string, ignoreExists bool) error {
	if err := store.ValidateMailFolderName(name); err != nil {
		return err
	}
	if err := lockMailFolders(ctx, tx, tenantID, mailbox); err != nil {
		return err
	}
	existing, err := folderNameMap(ctx, tx, tenantID, mailbox)
	if err != nil {
		return err
	}
	parents, canon := canonParents(name, existing)
	if _, ok := existing[strings.ToLower(canon)]; ok {
		if ignoreExists {
			return nil
		}
		return store.ErrMailFolderExists
	}
	var missing []string
	for _, p := range parents {
		if _, ok := existing[strings.ToLower(p)]; !ok {
			missing = append(missing, p)
		}
	}
	if len(existing)+len(missing)+1 > store.MaxMailFoldersPerMailbox {
		return store.ErrMailFolderLimit
	}
	for _, p := range missing {
		if _, err := tx.Exec(ctx,
			`INSERT INTO mail_folders (tenant_id, mailbox, name) VALUES ($1,$2,$3)`, tenantID, mailbox, p); err != nil {
			return fmt.Errorf("mail klasoru olusturulamadi: %w", err)
		}
	}
	var su any
	if specialUse != "" {
		su = specialUse
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO mail_folders (tenant_id, mailbox, name, special_use) VALUES ($1,$2,$3,$4)`,
		tenantID, mailbox, canon, su); err != nil {
		return fmt.Errorf("mail klasoru olusturulamadi: %w", err)
	}
	return nil
}

// CreateMailFolder, kullanici klasoru olusturur (eksik ust klasorlerle birlikte).
func (s *Store) CreateMailFolder(ctx context.Context, tenantID, mailbox, name, specialUse string) error {
	mailbox = mailboxKey(mailbox)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mail klasoru olusturulamadi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := createMailFolderTx(ctx, tx, tenantID, mailbox, name, specialUse, false); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail klasoru olusturulamadi: %w", err)
	}
	return nil
}

// ListMailFolders, kutunun kullanici klasorlerini ada gore listeler.
func (s *Store) ListMailFolders(ctx context.Context, tenantID, mailbox string) ([]store.MailFolder, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, coalesce(special_use,''), subscribed, created_at
		   FROM mail_folders WHERE tenant_id = $1 AND mailbox = $2 ORDER BY name`,
		tenantID, mailboxKey(mailbox))
	if err != nil {
		return nil, fmt.Errorf("mail klasorleri listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []store.MailFolder
	for rows.Next() {
		var f store.MailFolder
		if err := rows.Scan(&f.Name, &f.SpecialUse, &f.Subscribed, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListTenantMailFolderNames, kiracinin tum kutularindaki ayri klasor adlarini doner.
func (s *Store) ListTenantMailFolderNames(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT name FROM mail_folders WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("mail klasorleri listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteMailFolder, yaprak klasoru ve icindeki mesajlari siler. UIDVALIDITY
// artirilir: ayni adla yeniden olusturulan klasor eski istemci onbellegiyle karismaz.
func (s *Store) DeleteMailFolder(ctx context.Context, tenantID, mailbox, name string) error {
	mailbox = mailboxKey(mailbox)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMailFolders(ctx, tx, tenantID, mailbox); err != nil {
		return err
	}
	var one int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM mail_folders WHERE tenant_id = $1 AND mailbox = $2 AND name = $3`,
		tenantID, mailbox, name).Scan(&one)
	if err == pgx.ErrNoRows {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	var kids int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM mail_folders WHERE tenant_id = $1 AND mailbox = $2 AND starts_with(name, $3 || '/')`,
		tenantID, mailbox, name).Scan(&kids); err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	if kids > 0 {
		return store.ErrMailFolderHasChildren
	}
	key := store.MailUserFolderKey(name)
	if _, err := tx.Exec(ctx,
		`DELETE FROM mail_messages WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3`,
		tenantID, mailbox, key); err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE mail_uid_state
		    SET last_uid = 0,
		        uid_validity = greatest(uid_validity + 1, extract(epoch FROM now())::bigint)
		  WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3`, tenantID, mailbox, key); err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM mail_folders WHERE tenant_id = $1 AND mailbox = $2 AND name = $3`,
		tenantID, mailbox, name); err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail klasoru silinemedi: %w", err)
	}
	return nil
}

// RenameMailFolder, klasoru ve alt klasorlerini yeniden adlandirir; mesajlar UID'leriyle tasinir.
func (s *Store) RenameMailFolder(ctx context.Context, tenantID, mailbox, oldName, newName string) error {
	mailbox = mailboxKey(mailbox)
	if err := store.ValidateMailFolderName(newName); err != nil {
		return err
	}
	if newName == oldName || strings.HasPrefix(newName, oldName+"/") {
		return store.ErrMailFolderInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMailFolders(ctx, tx, tenantID, mailbox); err != nil {
		return err
	}
	existing, err := folderNameMap(ctx, tx, tenantID, mailbox)
	if err != nil {
		return err
	}
	if existing[strings.ToLower(oldName)] != oldName {
		return store.ErrNotFound
	}
	parents, canon := canonParents(newName, existing)
	// Hedef (buyuk/kucuk harf duyarsiz) varsa ve kendi alt agacimizin disindaysa cakisir.
	if got, ok := existing[strings.ToLower(canon)]; ok && !strings.EqualFold(got, oldName) {
		return store.ErrMailFolderExists
	}
	// Hedefin ust klasoru kaynagin alt agacinda olamaz.
	for _, p := range parents {
		if p == oldName || strings.HasPrefix(p, oldName+"/") {
			return store.ErrMailFolderInvalid
		}
	}
	var moving []string
	for _, n := range existing {
		if n == oldName || strings.HasPrefix(n, oldName+"/") {
			moving = append(moving, n)
		}
	}
	var missing []string
	for _, p := range parents {
		if _, ok := existing[strings.ToLower(p)]; !ok {
			missing = append(missing, p)
		}
	}
	if len(existing)+len(missing) > store.MaxMailFoldersPerMailbox {
		return store.ErrMailFolderLimit
	}
	for _, p := range missing {
		if _, err := tx.Exec(ctx,
			`INSERT INTO mail_folders (tenant_id, mailbox, name) VALUES ($1,$2,$3)`, tenantID, mailbox, p); err != nil {
			return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
		}
	}
	for _, n := range moving {
		dst := canon + n[len(oldName):]
		oldKey, newKey := store.MailUserFolderKey(n), store.MailUserFolderKey(dst)
		if _, err := tx.Exec(ctx,
			`UPDATE mail_folders SET name = $4 WHERE tenant_id = $1 AND mailbox = $2 AND name = $3`,
			tenantID, mailbox, n, dst); err != nil {
			return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE mail_messages SET folder = $4 WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3`,
			tenantID, mailbox, oldKey, newKey); err != nil {
			return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
		}
		// UID sayaci ve UIDVALIDITY hedefe tasinir; hedefte eski bir iz varsa
		// UIDVALIDITY ondan buyuk tutulur. Kaynakta iz birakilir (yeniden
		// olusturulursa farkli UIDVALIDITY alsin).
		if _, err := tx.Exec(ctx,
			`INSERT INTO mail_uid_state (tenant_id, mailbox, folder, last_uid, uid_validity)
			 SELECT tenant_id, mailbox, $4::text, last_uid,
			        greatest(uid_validity, coalesce((SELECT uid_validity + 1 FROM mail_uid_state
			                                          WHERE tenant_id = $1 AND mailbox = $2 AND folder = $4), 0))
			   FROM mail_uid_state WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3
			 ON CONFLICT (tenant_id, mailbox, folder)
			 DO UPDATE SET last_uid = EXCLUDED.last_uid, uid_validity = EXCLUDED.uid_validity`,
			tenantID, mailbox, oldKey, newKey); err != nil {
			return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE mail_uid_state
			    SET last_uid = 0,
			        uid_validity = greatest(uid_validity + 1, extract(epoch FROM now())::bigint)
			  WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3`, tenantID, mailbox, oldKey); err != nil {
			return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail klasoru yeniden adlandirilamadi: %w", err)
	}
	return nil
}

// SetMailFolderSubscribed, klasorun abonelik bayragini ayarlar.
func (s *Store) SetMailFolderSubscribed(ctx context.Context, tenantID, mailbox, name string, subscribed bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE mail_folders SET subscribed = $4 WHERE tenant_id = $1 AND mailbox = $2 AND name = $3`,
		tenantID, mailboxKey(mailbox), name, subscribed)
	if err != nil {
		return fmt.Errorf("mail klasoru aboneligi guncellenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}
