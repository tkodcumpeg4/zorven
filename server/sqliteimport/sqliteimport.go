// Package sqliteimport, eski SQLite veritabanini TEK SEFERLIK Postgres'e
// tasimak icindir.
//
// store.Store implementasyonu DEGILDIR ve sqlitestore paketine BAGLI DEGILDIR:
// o paket kaldirildiktan sonra da calismaya devam etmeli. Herkes gectikten
// sonra bu paket ve modernc.org/sqlite bagimliligi da silinebilir.
//
// Dosyayi SALT OKUR; hicbir sey degistirmez.
package sqliteimport

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	_ "modernc.org/sqlite"
)

// Dump, eski veritabanindan okunan her sey.
type Dump struct {
	Clients  []store.Client
	Tunnels  []LegacyTunnel
	Settings map[string]string
}

// LegacyTunnel, eski semadaki tunel satiri.
//
// store.Tunnel artik hostname TASIMAZ (adlar ayri tabloda), ama eski semada
// hostname tunel satirinin kendisindeydi. Ice aktarma once tuneli, sonra adi
// 'legacy' tipiyle yazar.
type LegacyTunnel struct {
	Tunnel   store.Tunnel
	Hostname string
}

// Read, verilen SQLite dosyasini okur.
//
// Zaman damgalari ve token hash'leri OLDUGU GIBI tasinir: kurulu istemcilerin
// yeniden yapilandirma gerektirmemesi buna bagli.
func Read(path string) (Dump, error) {
	// sql.Open tembeldir ve olmayan dosyayi hata saymaz; acikca kontrol et.
	if _, err := os.Stat(path); err != nil {
		return Dump{}, fmt.Errorf("eski veritabani bulunamadi (%s): %w", path, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return Dump{}, fmt.Errorf("eski veritabani acilamadi: %w", err)
	}
	defer db.Close()

	d := Dump{Settings: map[string]string{}}

	if err := readClients(db, &d); err != nil {
		return Dump{}, err
	}
	if err := readTunnels(db, &d); err != nil {
		return Dump{}, err
	}
	if err := readSettings(db, &d); err != nil {
		return Dump{}, err
	}
	return d, nil
}

func readClients(db *sql.DB, d *Dump) error {
	rows, err := db.Query(
		`SELECT id, name, token_id, token_hash, created_at FROM clients ORDER BY created_at`)
	if err != nil {
		return fmt.Errorf("istemciler okunamadi: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var c store.Client
		var ts string
		if err := rows.Scan(&c.ID, &c.Name, &c.TokenID, &c.TokenHash, &ts); err != nil {
			return err
		}
		c.CreatedAt = parseTime(ts)
		d.Clients = append(d.Clients, c)
	}
	return rows.Err()
}

func readTunnels(db *sql.DB, d *Dump) error {
	rows, err := db.Query(
		`SELECT id, hostname, client_id, target, enabled, created_at FROM tunnels ORDER BY created_at`)
	if err != nil {
		return fmt.Errorf("tuneller okunamadi: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var lt LegacyTunnel
		var ts string
		if err := rows.Scan(&lt.Tunnel.ID, &lt.Hostname, &lt.Tunnel.ClientID,
			&lt.Tunnel.Target, &lt.Tunnel.Enabled, &ts); err != nil {
			return err
		}
		lt.Tunnel.CreatedAt = parseTime(ts)
		d.Tunnels = append(d.Tunnels, lt)
	}
	return rows.Err()
}

func readSettings(db *sql.DB, d *Dump) error {
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("ayarlar okunamadi: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		d.Settings[k] = v
	}
	return rows.Err()
}

// parseTime, sqlitestore'un yazdigi RFC3339Nano damgasini cozer.
// Cozulemezse sifir zaman doner — goc, tek bir bozuk damga yuzunden durmamali.
func parseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
