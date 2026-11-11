package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

const (
	// shutdownGrace, yoldaki isteklerin bitmesi icin taninan sure. Uzun omurlu
	// akislar (SSE, uzun indirmeler) bu sureyi doldurabilir; sonra zorla kapanir.
	shutdownGrace = 10 * time.Second
	// persistDrainTimeout, arka plan toplu yazicilarinin son turu icin ust sinir.
	// Persister'lar kendi 10 sn'lik yazim zaman asimina sahip; bu deger onun ustunde
	// kalir ki DB yavassa da kapanis sonsuza kadar uzamasin.
	persistDrainTimeout = 12 * time.Second
)

// serveGraceful, serve'u (ServeTLS/ListenAndServe) calistirir; sigCtx iptal olunca
// srv.Shutdown ile yeni baglanti kabulunu durdurur ve yoldaki isteklerin bitmesini
// BEKLER, sonra doner. Neden ayri yardimci: net/http'de Serve, Shutdown cagrildigi an
// ErrServerClosed ile doner (yoldaki istekler bitmeden); dogrudan donmek ertelenmis
// havuz kapanisini istekler hala calisirken tetikliyordu (Y6: "closed pool" 401/403/500).
//
// grace dolarsa kalan baglantilar srv.Close ile zorla kapatilir. Hijack edilmis
// baglantilar (ajan WebSocket'leri) Shutdown tarafindan izlenmez; surec cikisinda
// cekirdek tarafindan kapatilirlar ve ajanlar yeniden baglanir.
func serveGraceful(sigCtx context.Context, srv *http.Server, serve func() error, grace time.Duration, log *slog.Logger) error {
	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		<-sigCtx.Done()
		log.Info("kapatiliyor: yeni baglanti alinmiyor, yoldaki istekler bekleniyor", "sure_sn", grace.Seconds())
		sctx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		start := time.Now()
		if err := srv.Shutdown(sctx); err != nil {
			log.Warn("kapanis suresi doldu, kalan baglantilar zorla kapatiliyor", "hata", err)
			_ = srv.Close()
			return
		}
		log.Info("yoldaki istekler tamamlandi", "sure_ms", time.Since(start).Milliseconds())
	}()
	err := serve()
	if errors.Is(err, http.ErrServerClosed) {
		<-shutDone
		return nil
	}
	return err
}

// waitTimeout, wg'yi en fazla d kadar bekler; surede bitmezse false doner.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// closeAgentSessions, bagli ajan oturumlarini WebSocket kapanis cercevesiyle kapatir.
// Neden: ajan WS baglantilari hijack edildigi icin Shutdown onlari beklemez/kapatmaz;
// surec cikisinda cekirdek kapatirdi. Duzgun kapanis (1) oturum goroutine'leri havuz
// kapanmadan sonlansin ("closed pool" log gurultusu olmasin) ve (2) ajan kopmayi kesin
// gorup hemen yeniden baglanma dongusune girsin diye. Ajan normal kapanista (1000)
// yeniden baglanir (client/agent/conn.go). Her kapanis el sikismasi en fazla d bekler.
func closeAgentSessions(hub *tunnel.Hub, d time.Duration, log *slog.Logger) {
	var wg sync.WaitGroup
	n := 0
	for id := range hub.Statuses() {
		s, ok := hub.Get(id)
		if !ok {
			continue
		}
		n++
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Close("server shutting down")
		}()
	}
	if n == 0 {
		return
	}
	done := waitTimeout(&wg, d)
	log.Info("ajan oturumlari kapatildi", "adet", n, "zamaninda", done)
}

// httpServerErrors, net/http sunucusunun ErrorLog'a yazdigi satir sayisi (TLS el
// sikisma hatalari, kabul hatalari, panik kurtarmalari).
var httpServerErrors atomic.Int64

func init() {
	obs.RegisterCounter("zorven_http_server_errors_total",
		"net/http sunucu hata logu satir sayisi (TLS el sikisma hatalari dahil; artan sayac)",
		func() float64 { return float64(httpServerErrors.Load()) })
}

// rateLimitedErrorWriter, http.Server.ErrorLog icin yazici. Neden: net/http her bozuk
// TLS el sikismasini (tarayicilar, port tarayicilari) std log ile bir satir yaziyor;
// --log-level'i dinlemiyor ve log seli yaratiyordu (A5 #3: 250 bozuk el sikisma = 250
// satir). Her satir DEBUG'da ve sayacta; WARN ozeti en fazla dakikada bir.
type rateLimitedErrorWriter struct {
	log      *slog.Logger
	interval time.Duration

	mu         sync.Mutex
	last       time.Time
	suppressed int64
}

func (w *rateLimitedErrorWriter) Write(p []byte) (int, error) {
	httpServerErrors.Add(1)
	msg := string(bytes.TrimSpace(p))
	w.log.Debug("http sunucu hatasi", "hata", msg)
	w.mu.Lock()
	now := time.Now()
	if !w.last.IsZero() && now.Sub(w.last) < w.interval {
		w.suppressed++
		w.mu.Unlock()
		return len(p), nil
	}
	sup := w.suppressed
	w.suppressed = 0
	w.last = now
	w.mu.Unlock()
	w.log.Warn("http sunucu hatasi (ornek; ayrinti DEBUG'da)", "hata", msg, "bastirilan", sup)
	return len(p), nil
}

// newHTTPErrorLog, http.Server.ErrorLog icin slog'a bagli, hiz sinirli std logger.
func newHTTPErrorLog(l *slog.Logger) *log.Logger {
	return log.New(&rateLimitedErrorWriter{log: l, interval: time.Minute}, "", 0)
}
