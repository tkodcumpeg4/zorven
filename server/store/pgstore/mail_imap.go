package pgstore

import (
	"context"
	"errors"
	"fmt"
	stdmail "net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// maxMailAppPasswords, bir kutu icin ayni anda tutulabilecek aktif uygulama
// parolasi sayisinin ust sinirdir (giris dogrulamasi hepsini dener).
const maxMailAppPasswords = 20

// rawSizeExpr, saklanan ham mesajin bayt uzunlugu (raw_bytes oncelikli).
const rawSizeExpr = `(CASE WHEN raw_bytes IS NOT NULL THEN octet_length(raw_bytes) ELSE octet_length(raw) END)`

func validMailFolder(f string) bool {
	switch f {
	case store.MailFolderInbox, store.MailFolderSent, store.MailFolderTrash, store.MailFolderDrafts:
		return true
	}
	if n, ok := store.MailUserFolderName(f); ok {
		return store.ValidateMailFolderName(n) == nil
	}
	return false
}

// mailboxKey, bir adresi kutu anahtarina (kucuk harf, yalin adres) cevirir.
func mailboxKey(addr string) string {
	addr = strings.TrimSpace(addr)
	if a, err := stdmail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	return strings.ToLower(strings.TrimSpace(addr))
}

// nextMailUID, (tenant, mailbox, folder) icin bir sonraki UID'yi atomik verir.
// Ayni satirin guncellenmesi eszamanli eklemeleri siralar.
func nextMailUID(ctx context.Context, tx pgx.Tx, tenantID, mailbox, folder string) (uint32, error) {
	var uid int64
	err := tx.QueryRow(ctx,
		`INSERT INTO mail_uid_state (tenant_id, mailbox, folder, last_uid)
		 VALUES ($1,$2,$3,1)
		 ON CONFLICT (tenant_id, mailbox, folder)
		 DO UPDATE SET last_uid = mail_uid_state.last_uid + 1
		 RETURNING last_uid`, tenantID, mailbox, folder).Scan(&uid)
	if err != nil {
		return 0, fmt.Errorf("mail uid atanamadi: %w", err)
	}
	return uint32(uid), nil
}

// MailFolderState, klasorun UIDVALIDITY ve UIDNEXT degerini doner (yoksa olusturur).
func (s *Store) MailFolderState(ctx context.Context, tenantID, mailbox, folder string) (store.MailFolderState, error) {
	mailbox = mailboxKey(mailbox)
	if !validMailFolder(folder) {
		return store.MailFolderState{}, store.ErrNotFound
	}
	var validity, last int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO mail_uid_state (tenant_id, mailbox, folder, last_uid)
		 VALUES ($1,$2,$3,0)
		 ON CONFLICT (tenant_id, mailbox, folder)
		 DO UPDATE SET last_uid = mail_uid_state.last_uid
		 RETURNING uid_validity, last_uid`, tenantID, mailbox, folder).Scan(&validity, &last)
	if err != nil {
		return store.MailFolderState{}, fmt.Errorf("mail klasor durumu alinamadi: %w", err)
	}
	return store.MailFolderState{UIDValidity: uint32(validity), UIDNext: uint32(last) + 1}, nil
}

// ListMailIndex, klasordeki mesajlarin UID sirali dizinini doner (govde yok).
func (s *Store) ListMailIndex(ctx context.Context, tenantID, mailbox, folder string) ([]store.MailIndexEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, uid, seen, flagged, answered, deleted,
		        `+rawSizeExpr+`, received_at, message_id
		   FROM mail_messages
		  WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3
		  ORDER BY uid`, tenantID, mailboxKey(mailbox), folder)
	if err != nil {
		return nil, fmt.Errorf("mail dizini alinamadi: %w", err)
	}
	defer rows.Close()
	var out []store.MailIndexEntry
	for rows.Next() {
		var e store.MailIndexEntry
		var uid int64
		if err := rows.Scan(&e.ID, &uid, &e.Seen, &e.Flagged, &e.Answered, &e.Deleted,
			&e.Size, &e.ReceivedAt, &e.MessageID); err != nil {
			return nil, err
		}
		e.UID = uint32(uid)
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetMailMessageFull, ham RFC822 dahil tam mesaj satirini doner.
func (s *Store) GetMailMessageFull(ctx context.Context, tenantID, id string) (store.MailMessage, error) {
	var m store.MailMessage
	var uid int64
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, direction, from_addr, to_addr, subject, text_body, html_body,
		        message_id, in_reply_to, seen, received_at, raw, folder, mailbox, uid,
		        flagged, answered, deleted, raw_bytes
		   FROM mail_messages WHERE tenant_id = $1 AND id = $2`, tenantID, id,
	).Scan(&m.ID, &m.TenantID, &m.Direction, &m.From, &m.To, &m.Subject, &m.TextBody, &m.HTMLBody,
		&m.MessageID, &m.InReplyTo, &m.Seen, &m.ReceivedAt, &m.Raw, &m.Folder, &m.Mailbox, &uid,
		&m.Flagged, &m.Answered, &m.Deleted, &m.RawBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MailMessage{}, store.ErrNotFound
	}
	if err != nil {
		return store.MailMessage{}, fmt.Errorf("mail alinamadi: %w", err)
	}
	m.UID = uint32(uid)
	return m, nil
}

// SetMailRaw, mesajin ham RFC822 icerigini (yeniden olusturulmus) raw_bytes'a saklar.
func (s *Store) SetMailRaw(ctx context.Context, tenantID, id, raw string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE mail_messages SET raw_bytes = $3, raw = '' WHERE tenant_id = $1 AND id = $2`,
		tenantID, id, []byte(raw))
	if err != nil {
		return fmt.Errorf("mail ham icerigi kaydedilemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// SetMailFlags, nil olmayan bayraklari gunceller. Seen panelin "okundu"
// durumuyla ayni sutundur.
func (s *Store) SetMailFlags(ctx context.Context, tenantID, id string, u store.MailFlagUpdate) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE mail_messages SET
		    seen     = COALESCE($3, seen),
		    flagged  = COALESCE($4, flagged),
		    answered = COALESCE($5, answered),
		    deleted  = COALESCE($6, deleted)
		  WHERE tenant_id = $1 AND id = $2`,
		tenantID, id, u.Seen, u.Flagged, u.Answered, u.Deleted)
	if err != nil {
		return fmt.Errorf("mail bayraklari guncellenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// MoveMailMessage, mesaji baska klasore tasir ve hedefte yeni UID atar.
func (s *Store) MoveMailMessage(ctx context.Context, tenantID, id, folder string) (uint32, error) {
	if !validMailFolder(folder) {
		return 0, store.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("mail tasinamadi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var mailbox, cur string
	var curUID int64
	err = tx.QueryRow(ctx,
		`SELECT mailbox, folder, uid FROM mail_messages WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID, id).Scan(&mailbox, &cur, &curUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("mail tasinamadi: %w", err)
	}
	if cur == folder {
		return uint32(curUID), nil
	}
	// Panelden tasima: hedef kullanici klasoru bu kutuda yoksa olusturulur.
	if n, ok := store.MailUserFolderName(folder); ok {
		if err := createMailFolderTx(ctx, tx, tenantID, mailbox, n, "", true); err != nil {
			return 0, err
		}
	}
	uid, err := nextMailUID(ctx, tx, tenantID, mailbox, folder)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE mail_messages SET folder = $3, uid = $4, deleted = false
		  WHERE tenant_id = $1 AND id = $2`, tenantID, id, folder, int64(uid)); err != nil {
		return 0, fmt.Errorf("mail tasinamadi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("mail tasinamadi: %w", err)
	}
	return uid, nil
}

// ExpungeMail, Deleted isaretli mesajlari kalici siler (ekler CASCADE ile gider).
func (s *Store) ExpungeMail(ctx context.Context, tenantID, mailbox, folder string, uids []uint32) ([]uint32, error) {
	var filter []int64
	for _, u := range uids {
		filter = append(filter, int64(u))
	}
	rows, err := s.pool.Query(ctx,
		`DELETE FROM mail_messages
		  WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3 AND deleted
		    AND ($4::bigint[] IS NULL OR uid = ANY($4::bigint[]))
		  RETURNING uid`, tenantID, mailboxKey(mailbox), folder, filter)
	if err != nil {
		return nil, fmt.Errorf("mail silinemedi: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var u int64
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, uint32(u))
	}
	return out, rows.Err()
}

// FindMailByMessageID, klasorde ayni Message-ID'li mesaji arar.
func (s *Store) FindMailByMessageID(ctx context.Context, tenantID, mailbox, folder, messageID string) (store.MailIndexEntry, bool, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return store.MailIndexEntry{}, false, nil
	}
	var e store.MailIndexEntry
	var uid int64
	err := s.pool.QueryRow(ctx,
		`SELECT id, uid, seen, flagged, answered, deleted,
		        `+rawSizeExpr+`, received_at, message_id
		   FROM mail_messages
		  WHERE tenant_id = $1 AND mailbox = $2 AND folder = $3 AND message_id = $4
		  ORDER BY uid LIMIT 1`, tenantID, mailboxKey(mailbox), folder, messageID,
	).Scan(&e.ID, &uid, &e.Seen, &e.Flagged, &e.Answered, &e.Deleted, &e.Size, &e.ReceivedAt, &e.MessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MailIndexEntry{}, false, nil
	}
	if err != nil {
		return store.MailIndexEntry{}, false, fmt.Errorf("mail aranamadi: %w", err)
	}
	e.UID = uint32(uid)
	return e, true, nil
}

// --- uygulama parolalari ---------------------------------------------------

const appPwCols = `id, tenant_id, mailbox, label, password_hash, created_at, last_used_at, last_used_ip, revoked_at`

func scanAppPw(row pgx.Row) (store.MailAppPassword, error) {
	var p store.MailAppPassword
	err := row.Scan(&p.ID, &p.TenantID, &p.Mailbox, &p.Label, &p.PasswordHash, &p.CreatedAt,
		&p.LastUsedAt, &p.LastUsedIP, &p.RevokedAt)
	return p, err
}

// CreateMailAppPassword, yeni uygulama parolasi kaydi ekler (hash'i cagiran uretir).
func (s *Store) CreateMailAppPassword(ctx context.Context, p store.MailAppPassword) (store.MailAppPassword, error) {
	if p.ID == "" {
		p.ID = newID("mapw")
	}
	p.Mailbox = mailboxKey(p.Mailbox)
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM mail_app_passwords WHERE mailbox = $1 AND revoked_at IS NULL`,
		p.Mailbox).Scan(&n); err != nil {
		return store.MailAppPassword{}, fmt.Errorf("uygulama parolasi sayilamadi: %w", err)
	}
	if n >= maxMailAppPasswords {
		return store.MailAppPassword{}, fmt.Errorf("%w: bir kutu icin en fazla %d aktif uygulama parolasi olabilir",
			store.ErrMailAppPasswordLimit, maxMailAppPasswords)
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO mail_app_passwords (id, tenant_id, mailbox, label, password_hash)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+appPwCols,
		p.ID, p.TenantID, p.Mailbox, p.Label, p.PasswordHash)
	out, err := scanAppPw(row)
	if err != nil {
		return store.MailAppPassword{}, fmt.Errorf("uygulama parolasi kaydedilemedi: %w", err)
	}
	return out, nil
}

// ListMailAppPasswords, kiracinin parolalarini (mailbox doluysa yalniz o kutu) listeler.
func (s *Store) ListMailAppPasswords(ctx context.Context, tenantID, mailbox string) ([]store.MailAppPassword, error) {
	q := `SELECT ` + appPwCols + ` FROM mail_app_passwords WHERE tenant_id = $1`
	args := []any{tenantID}
	if mailbox != "" {
		q += ` AND mailbox = $2`
		args = append(args, mailboxKey(mailbox))
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("uygulama parolalari listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []store.MailAppPassword
	for rows.Next() {
		p, err := scanAppPw(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RevokeMailAppPassword, parolayi iptal eder (idempotent; yoksa ErrNotFound).
func (s *Store) RevokeMailAppPassword(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE mail_app_passwords SET revoked_at = COALESCE(revoked_at, now())
		  WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("uygulama parolasi iptal edilemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListActiveMailAppPasswords, kutunun iptal edilmemis parolalarini doner.
func (s *Store) ListActiveMailAppPasswords(ctx context.Context, mailbox string) ([]store.MailAppPassword, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+appPwCols+` FROM mail_app_passwords
		  WHERE mailbox = $1 AND revoked_at IS NULL ORDER BY created_at`, mailboxKey(mailbox))
	if err != nil {
		return nil, fmt.Errorf("uygulama parolalari alinamadi: %w", err)
	}
	defer rows.Close()
	var out []store.MailAppPassword
	for rows.Next() {
		p, err := scanAppPw(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TouchMailAppPassword, son kullanim zamani ve IP'sini gunceller.
func (s *Store) TouchMailAppPassword(ctx context.Context, id, ip string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE mail_app_passwords SET last_used_at = $2, last_used_ip = $3 WHERE id = $1`,
		id, time.Now(), ip)
	if err != nil {
		return fmt.Errorf("uygulama parolasi guncellenemedi: %w", err)
	}
	return nil
}
