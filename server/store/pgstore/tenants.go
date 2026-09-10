package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Store) CreateTenant(ctx context.Context, slug string) (store.Tenant, error) {
	t := store.Tenant{ID: newID("ten"), Slug: slug, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenants (id, slug, created_at) VALUES ($1,$2,$3)`,
		t.ID, t.Slug, t.CreatedAt)
	if err != nil {
		return store.Tenant{}, fmt.Errorf("kiraci olusturulamadi: %w", err)
	}
	// Yeni kiraciya otomatik Free aboneligi ata.
	sub := DefaultSubscriptionForPlan(t.ID, store.PlanFree)
	_ = s.UpsertSubscription(ctx, sub)

	return t, nil
}

func (s *Store) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	var t store.Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT id, slug, created_at FROM tenants WHERE id = $1`, id).
		Scan(&t.ID, &t.Slug, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tenant{}, store.ErrTenantNotFound
	}
	if err != nil {
		return store.Tenant{}, err
	}
	return t, nil
}

func (s *Store) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, slug, created_at FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("kiracilar listelenemedi: %w", err)
	}
	defer rows.Close()

	var out []store.Tenant
	for rows.Next() {
		var t store.Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
