package reqlog

import (
	"context"
	"log/slog"
	"time"
)

// Persister, istek kayitlarini bellek ici halkadan bagimsiz olarak KALICI
// depoya (Postgres) toplu (batch) yazar. Amac: ingress sicak yolunu asla
// bloklamamak. Enqueue non-blocking'dir; tampon dolarsa kayit DUSER (log
// kalicilligi best-effort'tur, istek islemeyi asla yavaslatmaz).
type Persister struct {
	ch    chan Entry
	flush func(ctx context.Context, batch []Entry) error
	log   *slog.Logger

	batchSize int
	interval  time.Duration
}

// NewPersister, flush geri cagrimi ile bir persister olusturur. flush genelde
// store.InsertRequestLogs'a baglanir.
func NewPersister(flush func(ctx context.Context, batch []Entry) error, log *slog.Logger) *Persister {
	return &Persister{
		ch:        make(chan Entry, 4096),
		flush:     flush,
		log:       log,
		batchSize: 200,
		interval:  3 * time.Second,
	}
}

// Enqueue, kaydi yazma kuyruguna ekler. Non-blocking: kuyruk doluysa dusurur.
func (p *Persister) Enqueue(e Entry) {
	if p == nil {
		return
	}
	select {
	case p.ch <- e:
	default:
		// Kuyruk dolu — sicak yolu bloklamak yerine dusur.
	}
}

// Run, kuyrugu tuketir ve batchSize'a ulasinca ya da interval dolunca flush'lar.
// ctx iptal edilince kalan tamponu bir kez daha yazip doner.
func (p *Persister) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	batch := make([]Entry, 0, p.batchSize)
	doFlush := func() {
		if len(batch) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := p.flush(fctx, batch); err != nil {
			p.log.Warn("istek loglari kalici yazilamadi", "adet", len(batch), "hata", err)
		}
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Kalan kuyrugu bosalt.
			for {
				select {
				case e := <-p.ch:
					batch = append(batch, e)
					if len(batch) >= p.batchSize {
						doFlush()
					}
				default:
					doFlush()
					return
				}
			}
		case e := <-p.ch:
			batch = append(batch, e)
			if len(batch) >= p.batchSize {
				doFlush()
			}
		case <-ticker.C:
			doFlush()
		}
	}
}
