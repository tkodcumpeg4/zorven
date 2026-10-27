package pgstore

// Zorven CAPTCHA siteleri ve gunluk sayaclari (0102).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

var _ store.CaptchaStore = (*Store)(nil)

const captchaSiteCols = `id, tenant_id, name, site_key, secret_hash, domains, difficulty, pass_enabled, pass_ttl_minutes, created_at, updated_at`

func scanCaptchaSite(row pgx.Row) (store.CaptchaSite, error) {
	var s store.CaptchaSite
	err := row.Scan(&s.ID, &s.TenantID, &s.Name, &s.SiteKey, &s.SecretHash,
		&s.Domains, &s.Difficulty, &s.PassEnabled, &s.PassTTLMinutes, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.CaptchaSite{}, store.ErrNotFound
	}
	if err != nil {
		return store.CaptchaSite{}, err
	}
	if s.Domains == nil {
		s.Domains = []string{}
	}
	return s, nil
}

func (s *Store) ListCaptchaSites(ctx context.Context, tenantID string) ([]store.CaptchaSite, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+captchaSiteCols+` FROM captcha_sites WHERE tenant_id = $1 ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.CaptchaSite{}
	for rows.Next() {
		c, err := scanCaptchaSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCaptchaSite(ctx context.Context, tenantID, id string) (store.CaptchaSite, error) {
	return scanCaptchaSite(s.pool.QueryRow(ctx,
		`SELECT `+captchaSiteCols+` FROM captcha_sites WHERE id = $1 AND tenant_id = $2`, id, tenantID))
}

func (s *Store) GetCaptchaSiteByKey(ctx context.Context, siteKey string) (store.CaptchaSite, error) {
	return scanCaptchaSite(s.pool.QueryRow(ctx,
		`SELECT `+captchaSiteCols+` FROM captcha_sites WHERE site_key = $1`, siteKey))
}

func (s *Store) GetCaptchaSiteBySecretHash(ctx context.Context, secretHash string) (store.CaptchaSite, error) {
	return scanCaptchaSite(s.pool.QueryRow(ctx,
		`SELECT `+captchaSiteCols+` FROM captcha_sites WHERE secret_hash = $1`, secretHash))
}

func (s *Store) CreateCaptchaSite(ctx context.Context, in store.CaptchaSite) (store.CaptchaSite, error) {
	in.ID = newID("csite")
	in.CreatedAt = time.Now().UTC()
	in.UpdatedAt = in.CreatedAt
	if in.Domains == nil {
		in.Domains = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO captcha_sites (id, tenant_id, name, site_key, secret_hash, domains, difficulty, pass_enabled, pass_ttl_minutes, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.ID, in.TenantID, in.Name, in.SiteKey, in.SecretHash, in.Domains, in.Difficulty, in.PassEnabled, in.PassTTLMinutes, in.CreatedAt, in.UpdatedAt)
	if err != nil {
		return store.CaptchaSite{}, fmt.Errorf("captcha sitesi olusturulamadi: %w", err)
	}
	return in, nil
}

func (s *Store) UpdateCaptchaSite(ctx context.Context, tenantID, id string, p store.CaptchaSitePatch) (store.CaptchaSite, error) {
	cur, err := s.GetCaptchaSite(ctx, tenantID, id)
	if err != nil {
		return store.CaptchaSite{}, err
	}
	if p.Name != nil {
		cur.Name = *p.Name
	}
	if p.SetDomains {
		cur.Domains = p.Domains
		if cur.Domains == nil {
			cur.Domains = []string{}
		}
	}
	if p.Difficulty != nil {
		cur.Difficulty = *p.Difficulty
	}
	if p.PassEnabled != nil {
		cur.PassEnabled = *p.PassEnabled
	}
	if p.PassTTLMinutes != nil {
		cur.PassTTLMinutes = *p.PassTTLMinutes
	}
	cur.UpdatedAt = time.Now().UTC()
	_, err = s.pool.Exec(ctx,
		`UPDATE captcha_sites SET name=$3, domains=$4, difficulty=$5, pass_enabled=$7, pass_ttl_minutes=$8, updated_at=$6 WHERE id=$1 AND tenant_id=$2`,
		id, tenantID, cur.Name, cur.Domains, cur.Difficulty, cur.UpdatedAt, cur.PassEnabled, cur.PassTTLMinutes)
	if err != nil {
		return store.CaptchaSite{}, err
	}
	return cur, nil
}

func (s *Store) RotateCaptchaSiteSecret(ctx context.Context, tenantID, id, newSecretHash string) (store.CaptchaSite, error) {
	return scanCaptchaSite(s.pool.QueryRow(ctx,
		`UPDATE captcha_sites SET secret_hash=$3, updated_at=now() WHERE id=$1 AND tenant_id=$2
		 RETURNING `+captchaSiteCols, id, tenantID, newSecretHash))
}

func (s *Store) DeleteCaptchaSite(ctx context.Context, tenantID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `DELETE FROM captcha_sites WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM captcha_counters WHERE site_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddCaptchaCounters(ctx context.Context, day time.Time, deltas []store.CaptchaCounterDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	d := day.UTC().Format("2006-01-02")
	for _, x := range deltas {
		batch.Queue(`INSERT INTO captcha_counters (site_id, day, challenge, solve, verify_ok, verify_fail, renewals, unique_passes)
			VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (site_id, day) DO UPDATE SET
			  challenge = captcha_counters.challenge + EXCLUDED.challenge,
			  solve = captcha_counters.solve + EXCLUDED.solve,
			  verify_ok = captcha_counters.verify_ok + EXCLUDED.verify_ok,
			  verify_fail = captcha_counters.verify_fail + EXCLUDED.verify_fail,
			  renewals = captcha_counters.renewals + EXCLUDED.renewals,
			  unique_passes = captcha_counters.unique_passes + EXCLUDED.unique_passes`,
			x.SiteID, d, x.Challenge, x.Solve, x.VerifyOK, x.VerifyFail, x.Renewals, x.UniquePasses)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range deltas {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CaptchaCounters(ctx context.Context, siteIDs []string, since time.Time) ([]store.CaptchaCounterDay, error) {
	if len(siteIDs) == 0 {
		return []store.CaptchaCounterDay{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT site_id, day, challenge, solve, verify_ok, verify_fail, renewals, unique_passes FROM captcha_counters
		 WHERE site_id = ANY($1) AND day >= $2::date ORDER BY site_id, day`,
		siteIDs, since.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.CaptchaCounterDay{}
	for rows.Next() {
		var c store.CaptchaCounterDay
		if err := rows.Scan(&c.SiteID, &c.Day, &c.Challenge, &c.Solve, &c.VerifyOK, &c.VerifyFail, &c.Renewals, &c.UniquePasses); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) PruneCaptchaCounters(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM captcha_counters WHERE day < $1::date`, before.UTC().Format("2006-01-02"))
	return err
}
