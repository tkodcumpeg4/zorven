package ingress

// FAZ 4 / F21 — Aktif saglik denetleyicisi.
//
// Her replikaya TUNEL UZERINDEN gercek bir istek atilir: HTTP tunellerinde
// "GET <yol>", TCP tunellerinde yerel hedefe baglanti. Boylece "ajan bagli"
// degil "yerel servis gercekten yanit veriyor" olculur. Sonuclar esiklerle
// durum makinesine yazilir; ingress secimi yalnizca bu durumu okur.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	// healthTick, denetleyicinin "zamani gelen var mi" diye bakma araligi.
	healthTick = time.Second
	// minHealthInterval / maxHealthTimeout: kullanici ayarinin sinirlari.
	minHealthInterval = 2 * time.Second
	maxHealthTimeout  = 30 * time.Second
	// healthBodyLimit, saglik yanitindan okunacak azami govde.
	healthBodyLimit = 64 << 10
)

// HealthChecker, yuk dengeleme kaydi olan tunellerin replikalarini denetler.
type HealthChecker struct {
	Router *Router
	Hub    *tunnel.Hub
	Log    *slog.Logger

	mu       sync.Mutex
	lastRun  map[string]time.Time // tunnelID -> son denetim
	inFlight map[string]bool      // tunnelID+"|"+clientID -> denetim suruyor
}

// Run, ctx iptal edilene kadar calisir.
func (c *HealthChecker) Run(ctx context.Context) {
	c.lastRun = map[string]time.Time{}
	c.inFlight = map[string]bool{}
	t := time.NewTicker(healthTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			c.tick(ctx, now)
		}
	}
}

func (c *HealthChecker) tick(ctx context.Context, now time.Time) {
	for id, e := range c.Router.lbSnapshot() {
		if !e.cfg.HealthEnabled {
			continue
		}
		interval := time.Duration(e.cfg.IntervalSec) * time.Second
		if interval < minHealthInterval {
			interval = minHealthInterval
		}
		c.mu.Lock()
		due := now.Sub(c.lastRun[id]) >= interval
		if due {
			c.lastRun[id] = now
		}
		c.mu.Unlock()
		if !due {
			continue
		}
		for _, cid := range e.clients {
			sess, ok := c.Hub.Get(cid)
			if !ok {
				continue // cevrimdisi: secimde zaten elenir; durumu degistirme
			}
			key := healthKey(id, cid)
			c.mu.Lock()
			busy := c.inFlight[key]
			if !busy {
				c.inFlight[key] = true
			}
			c.mu.Unlock()
			if busy {
				continue // onceki denetim hala suruyor: ust uste yigma
			}
			go func(id, cid string, e *lbEntry, sess *tunnel.Session) {
				defer func() {
					c.mu.Lock()
					delete(c.inFlight, healthKey(id, cid))
					c.mu.Unlock()
				}()
				c.probe(ctx, id, cid, e, sess)
			}(id, cid, e, sess)
		}
	}
}

func (c *HealthChecker) probe(ctx context.Context, tunnelID, clientID string, e *lbEntry, sess *tunnel.Session) {
	timeout := time.Duration(e.cfg.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > maxHealthTimeout {
		timeout = 3 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var err error
	if e.proto == store.ProtoTCP {
		err = probeTCP(pctx, sess, tunnelID, timeout)
	} else {
		err = probeHTTP(pctx, sess, tunnelID, e.cfg.HealthPath)
	}
	latency := time.Since(start)

	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed, healthy := c.Router.lb.record(tunnelID, clientID, err == nil, latency, msg, e.cfg)
	if changed && c.Log != nil {
		if healthy {
			c.Log.Info("backend saglikli", "tunnel", tunnelID, "client", clientID, "gecikme", latency.Round(time.Millisecond))
		} else {
			c.Log.Warn("backend sagliksiz", "tunnel", tunnelID, "client", clientID, "hata", msg)
		}
	}
}

// probeHTTP, tunel uzerinden GET atar; 2xx/3xx saglikli sayilir.
func probeHTTP(ctx context.Context, sess *tunnel.Session, tunnelID, path string) error {
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	ex, err := sess.SendRequest(ctx, protocol.HTTPRequest{
		TunnelID: tunnelID, Method: "GET", Path: path,
		Headers: map[string][]string{"User-Agent": {"Zorven-HealthCheck/1.0"}},
	})
	if err != nil {
		return fmt.Errorf("istek gonderilemedi: %w", err)
	}
	defer sess.FinishExchange(ex.ReqID())

	select {
	case <-ctx.Done():
		return fmt.Errorf("zaman asimi")
	case head := <-ex.Head():
		if head.ErrCode != "" {
			return fmt.Errorf("yerel servise ulasilamadi (%s)", head.ErrCode)
		}
		// Govdeyi sinirli ve zaman asimli bosalt: ajanin akisi yarim kalmasin.
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, io.LimitReader(ex.Body(), healthBodyLimit))
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
		}
		if head.Status < 200 || head.Status > 399 {
			return fmt.Errorf("HTTP %d", head.Status)
		}
		return nil
	}
}

// probeTCP, ajanin yerel hedefe baglanabildigini dogrular ve hemen kapatir.
func probeTCP(ctx context.Context, sess *tunnel.Session, tunnelID string, timeout time.Duration) error {
	stream, err := sess.OpenStream(ctx, tunnelID, protocol.ProtoTCP, "health-check")
	if err != nil {
		return fmt.Errorf("akis acilamadi: %w", err)
	}
	defer stream.Close("health-check")
	if _, err := tunnel.WaitAccept(ctx, stream, timeout); err != nil {
		return fmt.Errorf("yerel hedefe baglanilamadi")
	}
	return nil
}
