package ingress

import (
	"context"
	"log/slog"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// MCPLogger, MCP cagri loglarini (yalniz ust veri) toplu yazar. Enqueue
// bloklamaz; kuyruk dolarsa kayit duser (log kalicilligi best-effort).
type MCPLogger struct {
	ch    chan store.MCPCall
	flush func(ctx context.Context, batch []store.MCPCall) error
	log   *slog.Logger
}

// NewMCPLogger, flush geri cagrimi genelde store.InsertMCPCalls'a baglanir.
func NewMCPLogger(flush func(ctx context.Context, batch []store.MCPCall) error, log *slog.Logger) *MCPLogger {
	if log == nil {
		log = slog.Default()
	}
	return &MCPLogger{ch: make(chan store.MCPCall, 4096), flush: flush, log: log}
}

// Enqueue, kaydi kuyruga ekler (nil guvenli, bloklamaz).
func (l *MCPLogger) Enqueue(c store.MCPCall) {
	if l == nil {
		return
	}
	select {
	case l.ch <- c:
	default:
	}
}

// Run, kuyrugu tuketir; ctx iptalinde kalani yazar.
func (l *MCPLogger) Run(ctx context.Context) {
	const batchSize = 200
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	batch := make([]store.MCPCall, 0, batchSize)
	doFlush := func() {
		if len(batch) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := l.flush(fctx, batch); err != nil {
			l.log.Warn("mcp cagri loglari yazilamadi", "adet", len(batch), "hata", err)
		}
		cancel()
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case c := <-l.ch:
					batch = append(batch, c)
					if len(batch) >= batchSize {
						doFlush()
					}
				default:
					doFlush()
					return
				}
			}
		case c := <-l.ch:
			batch = append(batch, c)
			if len(batch) >= batchSize {
				doFlush()
			}
		case <-t.C:
			doFlush()
		}
	}
}
