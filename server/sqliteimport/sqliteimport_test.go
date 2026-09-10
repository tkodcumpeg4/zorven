package sqliteimport

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Eski (kiracilik ONCESI) semali bir SQLite dosyasi olusturup okumayi dogrular.
func TestReadLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eski.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE clients (id TEXT PRIMARY KEY, name TEXT NOT NULL,
			token_id TEXT NOT NULL UNIQUE, token_hash TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE tunnels (id TEXT PRIMARY KEY, hostname TEXT NOT NULL UNIQUE,
			client_id TEXT NOT NULL, target TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL);
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO clients VALUES ('cli_1','ev-pc','tid1','argon2hash','2026-09-02T17:31:31Z');
		INSERT INTO tunnels VALUES ('tun_1','api.localhost','cli_1','http://localhost:8003',1,'2026-09-02T17:32:00Z');
		INSERT INTO settings VALUES ('admin_token_id','abc');
	`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	d, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(d.Clients) != 1 || d.Clients[0].ID != "cli_1" {
		t.Fatalf("istemciler: %+v", d.Clients)
	}
	// KRITIK: token bilgisi AYNEN korunmali, yoksa kurulu istemciler kirilir.
	if d.Clients[0].TokenHash != "argon2hash" || d.Clients[0].TokenID != "tid1" {
		t.Fatalf("token bilgisi bozuldu: %+v", d.Clients[0])
	}
	if d.Clients[0].CreatedAt.IsZero() {
		t.Fatalf("created_at cozulemedi: %+v", d.Clients[0])
	}

	if len(d.Tunnels) != 1 || d.Tunnels[0].Hostname != "api.localhost" {
		t.Fatalf("tuneller: %+v", d.Tunnels)
	}
	if !d.Tunnels[0].Tunnel.Enabled {
		t.Fatalf("enabled bayragi kayboldu: %+v", d.Tunnels[0])
	}
	// Hostname artik ayri alanda (store.Tunnel onu tasimiyor), ama tunelin
	// kendi kimligi de KAYBOLMAMALI: ad ona baglanacak.
	if d.Tunnels[0].Tunnel.ID == "" || d.Tunnels[0].Tunnel.Target == "" {
		t.Fatalf("tunel kimligi/hedefi kayboldu: %+v", d.Tunnels[0])
	}
	if d.Settings["admin_token_id"] != "abc" {
		t.Fatalf("ayarlar: %+v", d.Settings)
	}
}

// Olmayan dosya, panik degil duzgun hata dondurmeli.
func TestReadMissingFileFails(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "yok.db")); err == nil {
		t.Fatal("olmayan dosya icin hata bekleniyordu")
	}
}
