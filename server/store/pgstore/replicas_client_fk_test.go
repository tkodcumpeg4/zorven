package pgstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Hicbir migration UTF-8 BOM ile baslamamali: Postgres BOM'u gecersiz bir
// belirteç sayar ve acilisi cokertir (Postgres gerekmez).
func TestMigrationsHaveNoBOM(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("migration bulunamadi")
	}
	for _, e := range entries {
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("%s UTF-8 BOM iceriyor", e.Name())
		}
	}
}

// 0115: istemci silinince replika satirlari da gider (eskiden FK yoktu, satir
// kaliyor ve olmayan istemci hala replika sayiliyordu).
func TestDeleteClientCascadesReplicas(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	prim, err := s.CreateClient(ctx, tenant, "primary", "tok_p", "hash_p")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.CreateClient(ctx, tenant, "rep", "tok_r", "hash_r")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.CreateTunnel(ctx, tenant, prim.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddTunnelReplica(ctx, tenant, tn.ID, rep.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListAllTunnelReplicas(ctx); !reflect.DeepEqual(got, map[string][]string{tn.ID: {rep.ID}}) {
		t.Fatalf("replika eklenmeli: %v", got)
	}

	if err := s.DeleteClient(ctx, tenant, rep.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListAllTunnelReplicas(ctx); err != nil || len(got) != 0 {
		t.Fatalf("silinen istemcinin replika satiri kalmamali: %v %v", got, err)
	}
	if got, err := s.ListTunnelReplicas(ctx, tenant, tn.ID); err != nil || len(got) != 0 {
		t.Fatalf("tunelin replika listesi bos olmali: %v %v", got, err)
	}
	// Tunel ve birincil istemci etkilenmez.
	if _, err := s.GetTunnel(ctx, tenant, tn.ID); err != nil {
		t.Fatalf("tunel yerinde kalmali: %v", err)
	}

	// FK artik var olmayan istemciyi reddeder.
	_, err = s.pool.Exec(ctx, `INSERT INTO tunnel_replicas (tunnel_id, client_id) VALUES ($1, 'cli_yok')`, tn.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("FK ihlali (23503) bekleniyordu: %v", err)
	}
}

// 0115'in kendisi: FK'dan onceki semada kalmis artik satirlar silinir, gecerli
// satirlar korunur, sonra FK + indeks eklenir. Migration yeniden calistirilabilir.
func TestMigration0115CleansOrphanReplicasAndAddsFK(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	prim, err := s.CreateClient(ctx, tenant, "primary", "tok_p", "hash_p")
	if err != nil {
		t.Fatal(err)
	}
	good, err := s.CreateClient(ctx, tenant, "good", "tok_g", "hash_g")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.CreateTunnel(ctx, tenant, prim.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}

	// 0115 oncesi sema: FK ve indeks yok.
	for _, q := range []string{
		`ALTER TABLE tunnel_replicas DROP CONSTRAINT tunnel_replicas_client_id_fkey`,
		`DROP INDEX idx_tunnel_replicas_client`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tunnel_replicas (tunnel_id, client_id) VALUES ($1,$2), ($1,'cli_silinmis')`,
		tn.ID, good.ID); err != nil {
		t.Fatal(err)
	}

	sqlBytes, err := migrationFS.ReadFile("migrations/0115_tunnel_replicas_client_fk.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // ikinci calistirma da hatasiz (idempotent)
		if _, err := s.pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("migration calistirma %d: %v", i+1, err)
		}
	}

	got, err := s.ListAllTunnelReplicas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{tn.ID: {good.ID}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("artik satir silinmeli, gecerli korunmali: %v (istenen %v)", got, want)
	}
	var fkDef string
	if err := s.pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'tunnel_replicas_client_id_fkey'`).
		Scan(&fkDef); err != nil {
		t.Fatalf("FK olusmali: %v", err)
	}
	if !strings.Contains(fkDef, "REFERENCES clients(id)") || !strings.Contains(fkDef, "ON DELETE CASCADE") {
		t.Fatalf("FK clients(id) ON DELETE CASCADE olmali: %s", fkDef)
	}
	var idx int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_tunnel_replicas_client'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("client_id indeksi olusmali: %d %v", idx, err)
	}
}
