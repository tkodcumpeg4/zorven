package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

var _ store.UpdatesStore = (*Store)(nil)

const updatePostCols = `id, slug, kind, version, title_tr, body_tr, title_en, body_en, title_ja, body_ja,
	maintenance_start, maintenance_end, published_at, created_by, created_at, updated_at, emailed_at, email_stats`

func scanUpdatePost(row pgx.Row) (store.UpdatePost, error) {
	var p store.UpdatePost
	var stats []byte
	err := row.Scan(&p.ID, &p.Slug, &p.Kind, &p.Version, &p.TitleTR, &p.BodyTR, &p.TitleEN, &p.BodyEN,
		&p.TitleJA, &p.BodyJA, &p.MaintenanceStart, &p.MaintenanceEnd, &p.PublishedAt, &p.CreatedBy,
		&p.CreatedAt, &p.UpdatedAt, &p.EmailedAt, &stats)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, store.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if len(stats) > 0 {
		_ = json.Unmarshal(stats, &p.EmailStats)
	}
	return p, nil
}

func mapUpdateErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return store.ErrUpdateSlugTaken
	}
	return err
}

func (s *Store) ListUpdatePosts(ctx context.Context, onlyPublished bool, limit, offset int) ([]store.UpdatePost, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := ""
	if onlyPublished {
		where = ` WHERE published_at IS NOT NULL AND published_at <= now()`
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM updates_posts`+where).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+updatePostCols+` FROM updates_posts`+where+
			` ORDER BY COALESCE(published_at, created_at) DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []store.UpdatePost{}
	for rows.Next() {
		p, err := scanUpdatePost(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func (s *Store) GetUpdatePost(ctx context.Context, id string) (store.UpdatePost, error) {
	return scanUpdatePost(s.pool.QueryRow(ctx, `SELECT `+updatePostCols+` FROM updates_posts WHERE id = $1`, id))
}

func (s *Store) GetUpdatePostBySlug(ctx context.Context, slug string, onlyPublished bool) (store.UpdatePost, error) {
	q := `SELECT ` + updatePostCols + ` FROM updates_posts WHERE lower(slug) = lower($1)`
	if onlyPublished {
		q += ` AND published_at IS NOT NULL AND published_at <= now()`
	}
	return scanUpdatePost(s.pool.QueryRow(ctx, q, slug))
}

func (s *Store) CreateUpdatePost(ctx context.Context, p store.UpdatePost) (store.UpdatePost, error) {
	p.ID = newID("upd")
	row := s.pool.QueryRow(ctx,
		`INSERT INTO updates_posts (id, slug, kind, version, title_tr, body_tr, title_en, body_en, title_ja, body_ja,
			maintenance_start, maintenance_end, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING `+updatePostCols,
		p.ID, p.Slug, p.Kind, p.Version, p.TitleTR, p.BodyTR, p.TitleEN, p.BodyEN, p.TitleJA, p.BodyJA,
		p.MaintenanceStart, p.MaintenanceEnd, p.CreatedBy)
	out, err := scanUpdatePost(row)
	if err != nil {
		return out, mapUpdateErr(err)
	}
	return out, nil
}

func (s *Store) UpdateUpdatePost(ctx context.Context, p store.UpdatePost) (store.UpdatePost, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE updates_posts SET slug=$2, kind=$3, version=$4, title_tr=$5, body_tr=$6, title_en=$7, body_en=$8,
			title_ja=$9, body_ja=$10, maintenance_start=$11, maintenance_end=$12, updated_at=now()
		 WHERE id=$1 RETURNING `+updatePostCols,
		p.ID, p.Slug, p.Kind, p.Version, p.TitleTR, p.BodyTR, p.TitleEN, p.BodyEN, p.TitleJA, p.BodyJA,
		p.MaintenanceStart, p.MaintenanceEnd)
	out, err := scanUpdatePost(row)
	if err != nil {
		return out, mapUpdateErr(err)
	}
	return out, nil
}

func (s *Store) DeleteUpdatePost(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM updates_posts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetUpdatePostPublished(ctx context.Context, id string, publish bool) (store.UpdatePost, error) {
	q := `UPDATE updates_posts SET published_at = NULL, updated_at = now() WHERE id = $1 RETURNING ` + updatePostCols
	if publish {
		// Ilk yayin zamani korunur (yeniden yayinlama tarihi oynatmaz).
		q = `UPDATE updates_posts SET published_at = COALESCE(published_at, now()), updated_at = now()
			 WHERE id = $1 RETURNING ` + updatePostCols
	}
	return scanUpdatePost(s.pool.QueryRow(ctx, q, id))
}

func (s *Store) ClaimUpdatePostEmail(ctx context.Context, id string, force bool, total int) (bool, error) {
	stats, _ := json.Marshal(store.UpdateEmailStats{Total: total})
	tag, err := s.pool.Exec(ctx,
		`UPDATE updates_posts SET emailed_at = now(), email_stats = $3::jsonb, updated_at = now()
		 WHERE id = $1 AND published_at IS NOT NULL AND published_at <= now() AND ($2 OR emailed_at IS NULL)`,
		id, force, string(stats))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) SetUpdatePostEmailStats(ctx context.Context, id string, st store.UpdateEmailStats) error {
	b, _ := json.Marshal(st)
	_, err := s.pool.Exec(ctx, `UPDATE updates_posts SET email_stats = $2::jsonb WHERE id = $1`, id, string(b))
	return err
}

// recipientWhere: dogrulanmis e-posta + ACIK RIZA (opt-in; tercih satiri yok = abone degil).
const recipientWhere = `u."emailVerified" = true AND u."email" <> ''
	AND EXISTS (SELECT 1 FROM announcement_prefs p WHERE p.user_id = u."id" AND p.subscribed = true)`

func (s *Store) ListAnnouncementRecipients(ctx context.Context, afterUserID string, limit int) ([]store.AnnouncementRecipient, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`SELECT u."id", u."email", COALESCE(p.lang, ''), u."emailVerified"
		 FROM "user" u LEFT JOIN announcement_prefs p ON p.user_id = u."id"
		 WHERE u."id" > $1 AND `+recipientWhere+`
		 ORDER BY u."id" LIMIT $2`, afterUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AnnouncementRecipient
	for rows.Next() {
		var r store.AnnouncementRecipient
		if err := rows.Scan(&r.UserID, &r.Email, &r.Lang, &r.Verified); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CountAnnouncementRecipients(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM "user" u WHERE `+recipientWhere).Scan(&n)
	return n, err
}

func (s *Store) GetAnnouncementPref(ctx context.Context, userID string) (store.AnnouncementPref, error) {
	var pref store.AnnouncementPref
	err := s.pool.QueryRow(ctx,
		`SELECT subscribed, lang, source FROM announcement_prefs WHERE user_id = $1`, userID).
		Scan(&pref.Subscribed, &pref.Lang, &pref.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.AnnouncementPref{}, nil
	}
	pref.Asked = err == nil
	return pref, err
}

func (s *Store) SetAnnouncementLang(ctx context.Context, userID, lang string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE announcement_prefs SET lang = $2 WHERE user_id = $1 AND lang IS DISTINCT FROM $2`, userID, lang)
	return err
}

func (s *Store) SetAnnouncementPref(ctx context.Context, userID string, subscribed bool, lang, source string) error {
	if userID == "" {
		return fmt.Errorf("kullanici kimligi bos")
	}
	var unsub *time.Time
	if !subscribed {
		now := time.Now().UTC()
		unsub = &now
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO announcement_prefs (user_id, subscribed, unsubscribed_at, lang, source, consented_at)
		 VALUES ($1, $2, $3, $4, $5, CASE WHEN $2 THEN now() END)
		 ON CONFLICT (user_id) DO UPDATE SET subscribed = EXCLUDED.subscribed,
			unsubscribed_at = CASE WHEN EXCLUDED.subscribed THEN NULL
				ELSE COALESCE(announcement_prefs.unsubscribed_at, EXCLUDED.unsubscribed_at) END,
			consented_at = CASE WHEN EXCLUDED.subscribed THEN now() ELSE announcement_prefs.consented_at END,
			source = CASE WHEN EXCLUDED.source = '' THEN announcement_prefs.source ELSE EXCLUDED.source END,
			lang = CASE WHEN EXCLUDED.lang = '' THEN announcement_prefs.lang ELSE EXCLUDED.lang END,
			updated_at = now()`,
		userID, subscribed, unsub, lang, source)
	return err
}

func (s *Store) IsAnnouncementSubscribed(ctx context.Context, userID string) (bool, error) {
	p, err := s.GetAnnouncementPref(ctx, userID)
	return p.Subscribed, err
}

func (s *Store) ClaimUpdatePostResume(ctx context.Context, id, mode string, total int) (bool, error) {
	var q string
	switch mode {
	case store.SendModeResume:
		q = `UPDATE updates_posts SET
				email_stats = jsonb_set(jsonb_set(email_stats, '{interrupted}', 'false'::jsonb), '{total}', to_jsonb($2::int)),
				updated_at = now()
			 WHERE id = $1 AND emailed_at IS NOT NULL
			   AND COALESCE((email_stats->>'done')::boolean, false) = false`
	case store.SendModeRetryFailed:
		q = `UPDATE updates_posts SET
				email_stats = jsonb_set(jsonb_set(jsonb_set(email_stats, '{interrupted}', 'false'::jsonb), '{done}', 'false'::jsonb), '{total}', to_jsonb($2::int)),
				updated_at = now()
			 WHERE id = $1 AND emailed_at IS NOT NULL
			   AND COALESCE((email_stats->>'done')::boolean, false) = true
			   AND COALESCE((email_stats->>'failed')::int, 0) > 0`
	default:
		return false, fmt.Errorf("gecersiz gonderim kipi: %q", mode)
	}
	tag, err := s.pool.Exec(ctx, q, id, total)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) MarkInterruptedUpdateSends(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE updates_posts SET email_stats = jsonb_set(email_stats, '{interrupted}', 'true'::jsonb), updated_at = now()
		 WHERE emailed_at IS NOT NULL
		   AND COALESCE((email_stats->>'done')::boolean, false) = false
		   AND COALESCE((email_stats->>'interrupted')::boolean, false) = false`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) RecordUpdateDelivery(ctx context.Context, postID, userID, status, errMsg string) error {
	if status != store.DeliverySent && status != store.DeliveryFailed {
		return fmt.Errorf("gecersiz gonderim durumu: %q", status)
	}
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO updates_email_deliveries (post_id, user_id, status, attempts, error, sent_at)
		 VALUES ($1, $2, $3, 1, $4, CASE WHEN $3 = 'sent' THEN now() END)
		 ON CONFLICT (post_id, user_id) DO UPDATE SET status = EXCLUDED.status,
			attempts = updates_email_deliveries.attempts + 1, error = EXCLUDED.error,
			sent_at = CASE WHEN EXCLUDED.status = 'sent' THEN now() ELSE updates_email_deliveries.sent_at END,
			updated_at = now()`,
		postID, userID, status, errMsg)
	return err
}

func (s *Store) UpdateDeliveryStatuses(ctx context.Context, postID string, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT user_id, status FROM updates_email_deliveries WHERE post_id = $1 AND user_id = ANY($2)`,
		postID, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u, st string
		if err := rows.Scan(&u, &st); err != nil {
			return nil, err
		}
		out[u] = st
	}
	return out, rows.Err()
}

func (s *Store) CountUpdateDeliveries(ctx context.Context, postID string) (sent, failed int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'sent'), count(*) FILTER (WHERE status = 'failed')
		 FROM updates_email_deliveries WHERE post_id = $1`, postID).Scan(&sent, &failed)
	return
}

func (s *Store) ResetUpdateDeliveries(ctx context.Context, postID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM updates_email_deliveries WHERE post_id = $1`, postID)
	return err
}
