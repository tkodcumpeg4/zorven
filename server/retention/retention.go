// Package retention, istek loglarinin sabit sureli saklama temizligini arka
// planda calistirir (acik surum: plan mantigi yok).
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// Pruner, temizlik yapabilen depo (pgstore.Store).
type Pruner interface {
	PruneRetention(ctx context.Context) (pgstore.RetentionResult, error)
}

// Run, startDelay sonra bir kez, ardindan her interval'de temizlik yapar;
// ctx iptal edilince doner.
func Run(ctx context.Context, p Pruner, log *slog.Logger, startDelay, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	timer := time.NewTimer(startDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		res, err := p.PruneRetention(ctx)
		if err != nil {
			log.Warn("log saklama temizligi hata verdi", "hata", err)
		} else if res.RequestLogs > 0 || res.UDPStats > 0 {
			log.Info("log saklama temizligi tamamlandi",
				"gun", pgstore.RetentionDays(),
				"request_logs", res.RequestLogs, "udp_stats", res.UDPStats)
		}
		timer.Reset(interval)
	}
}
