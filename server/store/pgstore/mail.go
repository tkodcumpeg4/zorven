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
	err := s.pool.QueryRow(ctx,
		`INSERT INTO mail_messages
		   (id, tenant_id, direction, from_addr, to_addr, subject, text_body, html_body,
		    message_id, in_reply_to, seen, raw)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING received_at`,
		m.ID, m.TenantID, m.Direction, m.From, m.To, m.Subject, m.TextBody, m.HTMLBody,
		m.MessageID, m.InReplyTo, m.Seen, m.Raw,
	).Scan(&m.ReceivedAt)
	if err != nil {
		return store.MailMessage{}, fmt.Errorf("mail kaydedilemedi: %w", err)
	}
	return m, nil
}

// ListMailMessages, kiraciya ait belirli yondeki (inbound/outbound) mesajlari
// en yeniden eskiye siralar. Govdeler haric hafif bir projeksiyon doner (liste
// icin); tam icerik GetMailMessage ile alinir. text_body onizleme icin gelir.
func (s *Store) ListMailMessages(ctx context.Context, tenantID, direction string, limit int) ([]store.MailMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, direction, from_addr, to_addr, subject,
		        left(text_body, 280), message_id, in_reply_to, seen, received_at
		 FROM mail_messages
		 WHERE tenant_id = $1 AND direction = $2
		 ORDER BY received_at DESC
		 LIMIT $3`, tenantID, direction, limit)
	if err != nil {
		return nil, fmt.Errorf("mailler listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.MailMessage
	for rows.Next() {
		var m store.MailMessage
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Direction, &m.From, &m.To, &m.Subject,
			&m.TextBody, &m.MessageID, &m.InReplyTo, &m.Seen, &m.ReceivedAt); err != nil {
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
		`SELECT count(*) FROM mail_messages WHERE tenant_id = $1 AND direction = 'inbound' AND seen = false`,
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
