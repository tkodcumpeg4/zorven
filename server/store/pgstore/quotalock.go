package pgstore

import (
	"context"
	"time"
)

// LockTenantQuota, (kiraci, kaynak) icin oturum seviyesinde Postgres danisma kilidi
// alir (entitlements.QuotaLocker; O1 plan limiti yarisi). Kilit bir havuz baglantisinda
// tutulur ve unlock ile birakilir. Neden islem (xact) kilidi degil: limit denetimi ve
// ekleme farkli depo cagrilarinda (farkli baglantilarda) yapiliyor; ikisini tek isleme
// tasimak tum olusturma yollarini (tunel, ad, proje, istemci) yeniden yazmayi gerektirirdi.
//
// Anahtar uzayi: hashtextextended("zv_quota|..."). Lider/migration kilitleri sabit
// tamsayi anahtar kullanir; 64 bitlik ozet ile cakisma pratikte olanaksiz.
func (s *Store) LockTenantQuota(ctx context.Context, tenantID, resource string) (func(), error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := "zv_quota|" + resource + "|" + tenantID
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
		// Bekleme iptal edildiyse kilidin alinip alinmadigi belirsiz olabilir: baglanti
		// havuza geri donmez, kapatilir (oturum kilidi onunla birlikte duser).
		_ = conn.Conn().Close(context.Background())
		conn.Release()
		return nil, err
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key); err != nil {
			// Birakilamayan kilit havuzdaki baglantida kalirsa o kiraci kalici kilitlenir;
			// baglantiyi kapatmak kilidi sunucu tarafinda dusurur.
			_ = conn.Conn().Close(context.Background())
		}
		conn.Release()
	}, nil
}
