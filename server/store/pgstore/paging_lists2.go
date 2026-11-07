package pgstore

import (
	"context"
	"fmt"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Store) ListAbuseReportsPage(ctx context.Context, pg store.Page) ([]store.AbuseReport, int, error) {
	total, err := s.countRows(ctx, `FROM abuse_reports`)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+abuseCols+`
		 FROM abuse_reports ORDER BY created_at DESC, id DESC`+limitClause(pg))
	if err != nil {
		return nil, 0, fmt.Errorf("abuse bildirimleri listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.AbuseReport{}
	for rows.Next() {
		var r store.AbuseReport
		if err := scanAbuse(rows, &r); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// ListMailMessagesPage, klasordeki iletileri (en yeni once) sayfalar.
func (s *Store) ListMailMessagesPage(ctx context.Context, tenantID, folder string, pg store.Page) ([]store.MailMessage, int, error) {
	if !validMailFolder(folder) {
		return []store.MailMessage{}, 0, nil
	}
	total, err := s.countRows(ctx, `FROM mail_messages WHERE tenant_id = $1 AND folder = $2`, tenantID, folder)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, direction, from_addr, to_addr, subject,
		        left(text_body, 280), message_id, in_reply_to, seen, received_at, folder
		 FROM mail_messages
		 WHERE tenant_id = $1 AND folder = $2
		 ORDER BY received_at DESC, id DESC`+limitClause(pg), tenantID, folder)
	if err != nil {
		return nil, 0, fmt.Errorf("mailler listelenemedi: %w", err)
	}
	defer rows.Close()
	out := []store.MailMessage{}
	for rows.Next() {
		var m store.MailMessage
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Direction, &m.From, &m.To, &m.Subject,
			&m.TextBody, &m.MessageID, &m.InReplyTo, &m.Seen, &m.ReceivedAt, &m.Folder); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}
