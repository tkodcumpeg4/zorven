// Package testdb, Postgres gerektiren testler icin ortak DSN yardimcisidir.
//
// Testler ZORVEN_TEST_PG_DSN ile calisir; ayarli degilse ATLANIR. Onceden bazi
// testler sabit "localhost:5432/rpshell_test" adresine baglaniyor ve baglanamayinca
// sessizce atlaniyordu: env ayarli bir ortamda (CI, izole test container'i) bile
// hic kosmuyorlardi.
package testdb

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

// DSN, test veritabani adresini doner. Ayarli degilse testi atlar; adres adi
// "test" icermeyen bir veritabanina isaret ediyorsa (yanlislikla prod) durdurur.
func DSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("ZORVEN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZORVEN_TEST_PG_DSN ayarli degil; Postgres testi atlaniyor")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		t.Fatalf("ZORVEN_TEST_PG_DSN bir test veritabanina isaret etmeli: %s", dsn)
	}
	return dsn
}
