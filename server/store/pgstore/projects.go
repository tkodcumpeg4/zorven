package pgstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

var slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

const projectCols = `id, tenant_id, name, slug, created_at`

func scanProject(row pgx.Row, p *store.Project) error {
	return row.Scan(&p.ID, &p.TenantID, &p.Name, &p.Slug, &p.CreatedAt)
}

func (s *Store) ListProjects(ctx context.Context, tenantID string) ([]store.Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+projectCols+` FROM projects WHERE tenant_id = $1 ORDER BY created_at ASC`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("projeler listelenemedi: %w", err)
	}
	defer rows.Close()

	var list []store.Project
	for rows.Next() {
		var p store.Project
		if err := scanProject(rows, &p); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if list == nil {
		list = []store.Project{}
	}
	return list, rows.Err()
}

func (s *Store) CreateProject(ctx context.Context, tenantID, name, slug string) (store.Project, error) {
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))

	if name == "" {
		return store.Project{}, errors.New("proje adı boş olamaz")
	}
	if !slugRegex.MatchString(slug) {
		return store.Project{}, errors.New("proje slug yalnızca küçük harf, rakam ve tire içerebilir")
	}

	p := store.Project{
		ID:        newID("prj"),
		TenantID:  tenantID,
		Name:      name,
		Slug:      slug,
		CreatedAt: time.Now().UTC(),
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO projects (`+projectCols+`) VALUES ($1,$2,$3,$4,$5)`,
		p.ID, p.TenantID, p.Name, p.Slug, p.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return store.Project{}, store.ErrProjectSlugTaken
		}
		return store.Project{}, fmt.Errorf("proje oluşturulamadı: %w", err)
	}
	return p, nil
}

func (s *Store) GetProjectBySlug(ctx context.Context, tenantID, slug string) (store.Project, error) {
	var p store.Project
	err := scanProject(s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE tenant_id = $1 AND slug = $2`,
		tenantID, strings.ToLower(strings.TrimSpace(slug))), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Project{}, store.ErrNotFound
	}
	if err != nil {
		return store.Project{}, err
	}
	return p, nil
}

func (s *Store) GetProjectByID(ctx context.Context, tenantID, id string) (store.Project, error) {
	var p store.Project
	err := scanProject(s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE tenant_id = $1 AND id = $2`,
		tenantID, strings.TrimSpace(id)), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Project{}, store.ErrNotFound
	}
	if err != nil {
		return store.Project{}, err
	}
	return p, nil
}

func (s *Store) GetDefaultProject(ctx context.Context, tenantID string) (store.Project, error) {
	p, err := s.GetProjectBySlug(ctx, tenantID, "default")
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Project{}, err
	}

	// Varsayılan proje henüz yoksa otomatik oluştur
	defID := newID("prj")
	newP := store.Project{
		ID:        defID,
		TenantID:  tenantID,
		Name:      "Default",
		Slug:      "default",
		CreatedAt: time.Now().UTC(),
	}
	_, insErr := s.pool.Exec(ctx,
		`INSERT INTO projects (`+projectCols+`) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id, slug) DO NOTHING`,
		newP.ID, newP.TenantID, newP.Name, newP.Slug, newP.CreatedAt)
	if insErr != nil {
		return store.Project{}, insErr
	}
	return s.GetProjectBySlug(ctx, tenantID, "default")
}

func (s *Store) DeleteProject(ctx context.Context, tenantID, id string) error {
	p, err := s.GetProjectByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if p.Slug == "default" {
		return store.ErrProjectDefaultDelete
	}

	// Secret ve policy varsayilan projeye TASINMAZ: secret ve
	// policy adlari proje icinde tekildir, tasima ad cakismasi yaratabilir.
	// Bu kayitlarin projeye yabanci anahtari da yok; proje silinseydi hicbir
	// projede listelenmeyip GORUNMEZ olurlardi. Bu yuzden silme reddedilir.
	var nSecrets, nPolicies int
	if err := s.pool.QueryRow(ctx,
		`SELECT
			(SELECT count(*) FROM secrets   WHERE tenant_id = $1 AND project_id = $2),
			(SELECT count(*) FROM policies  WHERE tenant_id = $1 AND project_id = $2)`,
		tenantID, id).Scan(&nSecrets, &nPolicies); err != nil {
		return fmt.Errorf("proje icerigi okunamadi: %w", err)
	}
	if nSecrets+nPolicies > 0 {
		return fmt.Errorf("%w: %d secret, %d policy",
			store.ErrProjectNotEmpty, nSecrets, nPolicies)
	}

	// Tuneller, adlar ve istemciler varsayilan projeye aktarilir. Varsayilan
	// proje yoksa SILINMEZ: kaynaklar var olmayan bir projeye bakar kalirdi.
	def, err := s.GetDefaultProject(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("varsayilan proje bulunamadi, silme iptal: %w", err)
	}

	// Aktarim ve silme TEK islemde: yarida kalirsa kaynaklar iki proje
	// arasinda bolunmus kalmasin.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Commit sonrasi etkisiz

	for _, table := range []string{"tunnels", "hostnames", "clients"} {
		if _, err := tx.Exec(ctx,
			`UPDATE `+table+` SET project_id = $1 WHERE tenant_id = $2 AND project_id = $3`,
			def.ID, tenantID, id); err != nil {
			return fmt.Errorf("%s aktarilamadi: %w", table, err)
		}
	}
	ct, err := tx.Exec(ctx,
		`DELETE FROM projects WHERE tenant_id = $1 AND id = $2 AND slug != 'default'`,
		tenantID, id)
	if err != nil {
		return fmt.Errorf("proje silinemedi: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return tx.Commit(ctx)
}

// RenameProject, projenin GORUNEN adini degistirir. Slug DEGISMEZ: aktif proje
// basligi (X-Zorven-Project) ve tarayicida saklanan secim slug'a bakar.
func (s *Store) RenameProject(ctx context.Context, tenantID, id, name string) (store.Project, error) {
	var p store.Project
	err := s.pool.QueryRow(ctx,
		`UPDATE projects SET name = $3 WHERE tenant_id = $1 AND id = $2
		 RETURNING id, tenant_id, name, slug, created_at`,
		tenantID, id, name).Scan(&p.ID, &p.TenantID, &p.Name, &p.Slug, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Project{}, store.ErrNotFound
	}
	return p, err
}

// CountProjects, kiracinin proje sayisi (varsayilan proje dahil).
func (s *Store) CountProjects(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE tenant_id = $1`, tenantID).Scan(&n)
	return n, err
}
