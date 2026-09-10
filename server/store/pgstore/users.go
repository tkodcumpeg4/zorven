package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// UpsertUserByGitHubID, github_id'ye gore kullaniciyi olusturur veya login'ini
// tazeler.
//
// ON CONFLICT (github_id) DO UPDATE ... RETURNING: cakisma halinde MEVCUT satir
// doner. Boylece ayni GitHub hesabi her giriste ayni kullaniciya duser; kisi
// kullanici adini degistirse bile kiracisi ve verisi korunur.
func (s *Store) UpsertUserByGitHubID(ctx context.Context, githubID int64, login string) (store.User, error) {
	u := store.User{
		ID:          newID("usr"),
		GitHubID:    githubID,
		GitHubLogin: login,
		CreatedAt:   time.Now().UTC(),
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, github_id, github_login, created_at)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (github_id) DO UPDATE SET github_login = EXCLUDED.github_login
		 RETURNING id, github_id, github_login, created_at`,
		u.ID, u.GitHubID, u.GitHubLogin, u.CreatedAt).
		Scan(&u.ID, &u.GitHubID, &u.GitHubLogin, &u.CreatedAt)
	if err != nil {
		return store.User{}, fmt.Errorf("kullanici kaydedilemedi: %w", err)
	}
	return u, nil
}

// GetTenantForUser, kullanicinin uye oldugu kiraciyi doner.
//
// Bir kullanici ileride birden fazla kiraciya uye olabilir; su an EN ESKI
// uyelik "birincil" kabul ediliyor. Kiraci secimi UI'si geldiginde burasi
// genisletilecek.
func (s *Store) GetTenantForUser(ctx context.Context, userID string) (store.Tenant, error) {
	var t store.Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT t.id, t.slug, t.created_at
		 FROM tenants t
		 JOIN tenant_members m ON m.tenant_id = t.id
		 WHERE m.user_id = $1
		 ORDER BY t.created_at
		 LIMIT 1`, userID).
		Scan(&t.ID, &t.Slug, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tenant{}, store.ErrTenantNotFound
	}
	if err != nil {
		return store.Tenant{}, err
	}
	return t, nil
}

// AddTenantMember, uyelik ekler. Idempotenttir: ayni cift yeniden eklenirse
// yalnizca rol guncellenir (giris akisi tekrar tekrar cagirabilsin diye).
func (s *Store) AddTenantMember(ctx context.Context, tenantID, userID, role string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		tenantID, userID, role)
	if err != nil {
		return fmt.Errorf("uyelik eklenemedi: %w", err)
	}
	return nil
}

// GetTenantBySlug, slug ile kiraci arar (buyuk/kucuk harf duyarsiz).
// Yeni kiraci acarken slug cakismasini kontrol etmek icin kullanilir.
func (s *Store) GetTenantBySlug(ctx context.Context, slug string) (store.Tenant, error) {
	var t store.Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT id, slug, created_at FROM tenants WHERE lower(slug) = lower($1)`, slug).
		Scan(&t.ID, &t.Slug, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Tenant{}, store.ErrTenantNotFound
	}
	if err != nil {
		return store.Tenant{}, err
	}
	return t, nil
}
