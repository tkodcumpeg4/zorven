package reqlog

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Persister, istek kayitlarini bellek ici halkadan bagimsiz olarak KALICI
// depoya (Postgres) toplu (batch) yazar. Amac: ingress sicak yolunu asla
// bloklamamak. Enqueue non-blocking'dir; tampon dolarsa kayit DUSER (log
// kalicilligi best-effort'tur, istek islemeyi asla yavaslatmaz).
//
// Yazim basarisiz olursa (A4 #8: DB kesintisinde topluluk hemen atiliyordu) toplu
// kayit sinirli bir tamponda tutulur ve sonraki tiklarda maxAttempts kez yeniden
// denenir; tampon maxRetain'i asarsa en eskiler atilir. Bellek her durumda
// kuyruk (4096) + maxRetain kayitla sinirlidir. Atilan kayitlar Dropped ile sayilir.
type Persister struct {
	ch    chan Entry
	flush func(ctx context.Context, batch []Entry) error
	log   *slog.Logger

	batchSize   int
	interval    time.Duration
	maxRetain   int // yazilamayan kayitlar icin tampon tavani
	maxAttempts int // ayni tampon icin en fazla yazim denemesi

	// OnNew (opsiyonel), her kaydi TAM BIR KEZ, DB yazimindan once alir (log disa
	// aktarimi). Yeniden denemelerde tekrar cagrilmaz; DB'den bagimsiz calisir.
	// Run baslamadan atanmali.
	OnNew func(batch []Entry)

	dropped atomic.Int64
}

// NewPersister, flush geri cagrimi ile bir persister olusturur. flush genelde
// store.InsertRequestLogs'a baglanir.
func NewPersister(flush func(ctx context.Context, batch []Entry) error, log *slog.Logger) *Persister {
	return &Persister{
		ch:          make(chan Entry, 4096),
		flush:       flush,
		log:         log,
		batchSize:   200,
		interval:    3 * time.Second,
		maxRetain:   2000,
		maxAttempts: 5, // 3 sn aralikla ~15 sn: kisa DB yeniden baslatmalarini kapsar
	}
}

// Dropped, kalici yazilamadan atilan (kuyruk dolu ya da yeniden denemeler tukendi)
// kayit sayisi. Metrik olarak yayinlanir.
func (p *Persister) Dropped() int64 {
	if p == nil {
		return 0
	}
	return p.dropped.Load()
}

// Enqueue, kaydi yazma kuyruguna ekler. Non-blocking: kuyruk doluysa dusurur.
func (p *Persister) Enqueue(e Entry) {
	if p == nil {
		return
	}
	select {
	case p.ch <- sanitizeEntry(e):
	default:
		// Kuyruk dolu — sicak yolu bloklamak yerine dusur.
		p.dropped.Add(1)
	}
}

// Run, kuyrugu tuketir ve batchSize'a ulasinca ya da interval dolunca flush'lar.
// ctx iptal edilince kalan tamponu bir kez daha yazip doner.
func (p *Persister) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	batch := make([]Entry, 0, p.batchSize)
	failures := 0 // mevcut tampon icin ardisik basarisiz deneme
	sent := 0     // batch'in OnNew'e zaten verilmis bas kismi

	drop := func(n int, reason string, err error) {
		p.dropped.Add(int64(n))
		p.log.Warn("istek loglari kalici yazilamadi, atildi", "adet", n, "neden", reason,
			"toplam_atilan", p.dropped.Load(), "hata", err)
	}
	doFlush := func(final bool) {
		if len(batch) == 0 {
			return
		}
		if p.OnNew != nil && sent < len(batch) {
			p.OnNew(batch[sent:])
		}
		sent = len(batch)
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := p.flush(fctx, batch)
		cancel()
		if err == nil {
			if failures > 0 {
				p.log.Info("istek loglari yeniden yazildi", "adet", len(batch), "deneme", failures+1)
			}
			failures, sent = 0, 0
			batch = batch[:0]
			return
		}
		failures++
		if final || failures >= p.maxAttempts {
			drop(len(batch), "yeniden_deneme_tukendi", err)
			failures, sent = 0, 0
			batch = batch[:0]
			return
		}
		p.log.Warn("istek loglari kalici yazilamadi, yeniden denenecek", "adet", len(batch),
			"deneme", failures, "en_fazla", p.maxAttempts, "hata", err)
	}
	add := func(e Entry) {
		batch = append(batch, e)
		if len(batch) > p.maxRetain {
			// Bellek siniri: en eski kayitlar gider (yeni trafik daha degerli).
			n := len(batch) - p.maxRetain
			p.dropped.Add(int64(n))
			batch = append(batch[:0], batch[n:]...)
			sent = max(0, sent-n)
		}
		// Basarisizlik surerken her yeni kayitta DB'yi yoklamak yerine tiki bekle.
		if failures == 0 && len(batch) >= p.batchSize {
			doFlush(false)
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Kalan kuyrugu bosalt.
			for {
				select {
				case e := <-p.ch:
					add(e)
				default:
					doFlush(true)
					return
				}
			}
		case e := <-p.ch:
			add(e)
		case <-ticker.C:
			doFlush(false)
		}
	}
}
