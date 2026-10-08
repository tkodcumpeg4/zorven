package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// InsertMailMessage, bir gelen/giden e-posta kaydi ekler. m.ID bossa uretilir.
func (s *Store) InsertMailMessage(ctx context.Context, m store.MailMessage) (store.MailMessage, error) {
	if m.ID == "" {
		m.ID = newID("mail")
	}
	if m.Direction != "inbound" && m.Direction != "outbound" {
		return store.MailMessage{}, fmt.Errorf("gecersiz mail yonu: %q", m.Direction)
	}
	if m.Folder == "" {
		m.Folder = store.MailFolderInbox
		if m.Direction == "outbound" {
			m.Folder = store.MailFolderSent
		}
	}
	if !validMailFolder(m.Folder) {
		return store.MailMessage{}, fmt.Errorf("gecersiz mail klasoru: %q", m.Folder)
	}
	if m.Mailbox == "" {
		m.Mailbox = m.To
		if m.Direction == "outbound" {
			m.Mailbox = m.From
		}
	}
	m.Mailbox = mailboxKey(m.Mailbox)
	// Ham mesaj bytea'ya birebir yazilir (NUL/8-bit korunur); TEXT sutunu bos kalir.
	rawBytes := m.RawBytes
	if len(rawBytes) == 0 && m.Raw != "" {
		rawBytes = []byte(m.Raw)
	}
	m.Raw = ""
	if len(rawBytes) == 0 {
		rawBytes = nil
	}
	m.RawBytes = rawBytes

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.MailMessage{}, fmt.Errorf("mail kaydedilemedi: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	uid, err := nextMailUID(ctx, tx, m.TenantID, m.Mailbox, m.Folder)
	if err != nil {
		return store.MailMessage{}, err
	}
	m.UID = uid
	err = tx.QueryRow(ctx,
		`INSERT INTO mail_messages
		   (id, tenant_id, direction, from_addr, to_addr, subject, text_body, html_body,
		    message_id, in_reply_to, seen, raw, folder, mailbox, uid, flagged, answered, deleted, raw_bytes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		 RETURNING received_at`,
		m.ID, m.TenantID, m.Direction, m.From, m.To, m.Subject, m.TextBody, m.HTMLBody,
		m.MessageID, m.InReplyTo, m.Seen, m.Raw, m.Folder, m.Mailbox, int64(m.UID),
		m.Flagged, m.Answered, m.Deleted, rawBytes,
	).Scan(&m.ReceivedAt)
	if err != nil {
		return store.MailMessage{}, fmt.Errorf("mail kaydedilemedi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return store.MailMessage{}, fmt.Errorf("mail kaydedilemedi: %w", err)
	}
	return m, nil
}

// ListMailMessages, kiraciya ait belirli klasordeki (inbox/sent/trash) mesajlari
// en yeniden eskiye siralar. Klasor esas alinir (yon degil): IMAP ile tasinan
// mesajlar panelde de dogru listede gorunur. Govdeler haric hafif bir projeksiyon doner (liste
// icin); tam icerik GetMailMessage ile alinir. text_body onizleme icin gelir.
func (s *Store) ListMailMessages(ctx context.Context, tenantID, folder string, limit int) ([]store.MailMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if !validMailFolder(folder) {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, direction, from_addr, to_addr, subject,
		        left(text_body, 280), message_id, in_reply_to, seen, received_at, folder
		 FROM mail_messages
		 WHERE tenant_id = $1 AND folder = $2
		 ORDER BY received_at DESC
		 LIMIT $3`, tenantID, folder, limit)
	if err != nil {
		return nil, fmt.Errorf("mailler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.MailMessage
	for rows.Next() {
		var m store.MailMessage
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Direction, &m.From, &m.To, &m.Subject,
			&m.TextBody, &m.MessageID, &m.InReplyTo, &m.Seen, &m.ReceivedAt, &m.Folder); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMailMessage, tek bir mesajin tam icerigini (text + html) doner.
func (s *Store) GetMailMessage(ctx context.Context, tenantID, id string) (store.MailMessage, error) {
	var m store.MailMessage
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, direction, from_addr, to_addr, subject, text_body, html_body,
		        message_id, in_reply_to, seen, received_at
		 FROM mail_messages
		 WHERE tenant_id = $1 AND id = $2`, tenantID, id,
	).Scan(&m.ID, &m.TenantID, &m.Direction, &m.From, &m.To, &m.Subject, &m.TextBody, &m.HTMLBody,
		&m.MessageID, &m.InReplyTo, &m.Seen, &m.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MailMessage{}, store.ErrNotFound
	}
	if err != nil {
		return store.MailMessage{}, fmt.Errorf("mail alinamadi: %w", err)
	}
	return m, nil
}

// MarkMailSeen, mesaji okundu isaretler.
func (s *Store) MarkMailSeen(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE mail_messages SET seen = true WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("mail okundu isaretlenemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DeleteMailMessage, mesaji siler.
func (s *Store) DeleteMailMessage(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM mail_messages WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("mail silinemedi: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// CountUnseenMail, kiracinin okunmamis gelen mail sayisini doner.
func (s *Store) CountUnseenMail(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM mail_messages WHERE tenant_id = $1 AND folder = 'inbox' AND seen = false`,
		tenantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("okunmamis mail sayilamadi: %w", err)
	}
	return n, nil
}

// InsertMailAttachment, bir mesaja bagli ek dosya ekler. a.ID bossa uretilir.
func (s *Store) InsertMailAttachment(ctx context.Context, a store.MailAttachment) error {
	if a.ID == "" {
		a.ID = newID("matt")
	}
	if a.ContentType == "" {
		a.ContentType = "application/octet-stream"
	}
	if a.SizeBytes == 0 {
		a.SizeBytes = int64(len(a.Content))
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO mail_attachments (id, message_id, tenant_id, filename, content_type, size_bytes, content)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.MessageID, a.TenantID, a.Filename, a.ContentType, a.SizeBytes, a.Content)
	if err != nil {
		return fmt.Errorf("ek kaydedilemedi: %w", err)
	}
	return nil
}

// ListMailAttachments, bir mesajin eklerini (metadata; icerik HARIC) doner.
func (s *Store) ListMailAttachments(ctx context.Context, tenantID, messageID string) ([]store.MailAttachment, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, message_id, filename, content_type, size_bytes, created_at
		 FROM mail_attachments WHERE tenant_id = $1 AND message_id = $2 ORDER BY created_at ASC`,
		tenantID, messageID)
	if err != nil {
		return nil, fmt.Errorf("ekler listelenemedi: %w", err)
	}
	defer rows.Close()
	var out []store.MailAttachment
	for rows.Next() {
		var a store.MailAttachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetMailAttachment, tek bir ekin tam icerigini (indirme icin) doner.
func (s *Store) GetMailAttachment(ctx context.Context, tenantID, id string) (store.MailAttachment, error) {
	var a store.MailAttachment
	err := s.pool.QueryRow(ctx,
		`SELECT id, message_id, filename, content_type, size_bytes, content, created_at
		 FROM mail_attachments WHERE tenant_id = $1 AND id = $2`, tenantID, id,
	).Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.Content, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.MailAttachment{}, store.ErrNotFound
	}
	if err != nil {
		return store.MailAttachment{}, fmt.Errorf("ek alinamadi: %w", err)
	}
	return a, nil
}
