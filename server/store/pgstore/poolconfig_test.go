package pgstore

import (
	"context"
	"testing"
	"time"
)

func TestPoolConfigDefaults(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@localhost:5432/db?sslmode=disable", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 20 || cfg.MinConns != 2 {
		t.Fatalf("conns = %d/%d", cfg.MaxConns, cfg.MinConns)
	}
	if cfg.MaxConnIdleTime != 5*time.Minute || cfg.MaxConnLifetime != time.Hour || cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("sureler: %v %v %v", cfg.MaxConnIdleTime, cfg.MaxConnLifetime, cfg.HealthCheckPeriod)
	}
	if cfg.ConnConfig.ConnectTimeout != 5*time.Second {
		t.Fatalf("connect timeout = %v", cfg.ConnConfig.ConnectTimeout)
	}
	rp := cfg.ConnConfig.RuntimeParams
	if rp["statement_timeout"] != "15000" || rp["idle_in_transaction_session_timeout"] != "30000" || rp["application_name"] != "zorven" || rp["jit"] != "off" {
		t.Fatalf("runtime params = %v", rp)
	}
}

func TestPoolConfigEnv(t *testing.T) {
	env := map[string]string{"ZORVEN_DB_MAX_CONNS": "7", "ZORVEN_DB_STATEMENT_TIMEOUT_MS": "2500"}
	cfg, err := poolConfig("postgres://u:p@localhost/db", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 7 || cfg.ConnConfig.RuntimeParams["statement_timeout"] != "2500" {
		t.Fatalf("env uygulanmadi: %d %v", cfg.MaxConns, cfg.ConnConfig.RuntimeParams)
	}
	// Gecersiz degerler varsayilana duser.
	env = map[string]string{"ZORVEN_DB_MAX_CONNS": "abc", "ZORVEN_DB_STATEMENT_TIMEOUT_MS": "-5"}
	cfg, _ = poolConfig("postgres://u:p@localhost/db", func(k string) string { return env[k] })
	if cfg.MaxConns != 20 || cfg.ConnConfig.RuntimeParams["statement_timeout"] != "15000" {
		t.Fatalf("varsayilana dusmedi: %d %v", cfg.MaxConns, cfg.ConnConfig.RuntimeParams)
	}
	// Aralik disi (ust sinir) degerler de varsayilana duser.
	env = map[string]string{"ZORVEN_DB_MAX_CONNS": "1000000", "ZORVEN_DB_STATEMENT_TIMEOUT_MS": "99999999"}
	cfg, _ = poolConfig("postgres://u:p@localhost/db", func(k string) string { return env[k] })
	if cfg.MaxConns != 20 || cfg.ConnConfig.RuntimeParams["statement_timeout"] != "15000" {
		t.Fatalf("ust sinir uygulanmadi: %d %v", cfg.MaxConns, cfg.ConnConfig.RuntimeParams)
	}
	// MaxConns 1 iken MinConns tavanlanir.
	env = map[string]string{"ZORVEN_DB_MAX_CONNS": "1"}
	cfg, _ = poolConfig("postgres://u:p@localhost/db", func(k string) string { return env[k] })
	if cfg.MinConns > cfg.MaxConns {
		t.Fatalf("min %d > max %d", cfg.MinConns, cfg.MaxConns)
	}
}

// Havuzdan gelen baglanti statement_timeout'u uygular; execLong ise gevsetir.
func TestStatementTimeoutEffective(t *testing.T) {
	t.Setenv("ZORVEN_DB_STATEMENT_TIMEOUT_MS", "300")
	ctx := context.Background()
	s, err := Open(ctx, testDSN(t)) // migrasyon zaten uygulanmis; gevsetilmis oturumda calisir
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	var jit string
	if err := s.pool.QueryRow(ctx, `SHOW jit`).Scan(&jit); err != nil || jit != "off" {
		t.Fatalf("havuz oturumunda jit=off olmali: %q %v", jit, err)
	}
	if _, err := s.pool.Exec(ctx, `SELECT pg_sleep(2)`); err == nil {
		t.Fatal("statement_timeout calismadi: pg_sleep(2) hata vermeliydi")
	}
	// execLong: ayni sure asimi SET LOCAL ile gevsetilir.
	start := time.Now()
	if _, err := s.execLong(ctx, `SELECT pg_sleep(1)`); err != nil {
		t.Fatalf("execLong zaman asimina takildi: %v", err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatal("pg_sleep calismadi?")
	}
	// Migration oturumu: tekrar Open (migrasyon yok) hatasiz doner.
	s2, err := Open(ctx, testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}
