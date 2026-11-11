package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Y6: sinyal geldiginde yoldaki yavas istek 200 ile tamamlanmali ve "havuz" (burada
// sahte store) ancak ondan SONRA kapanmali. Eskiden Serve, Shutdown cagrildigi an
// donuyor, ertelenmis st.Close istekler bitmeden calisiyordu.
func TestServeGracefulYoldakiIstekBitmedenStoreKapanmaz(t *testing.T) {
	var storeClosed atomic.Bool
	var handlerSawClosed atomic.Bool
	var handlerDoneAt, storeClosedAt atomic.Int64
	started := make(chan struct{})

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(400 * time.Millisecond) // yavas DB islemi
		if storeClosed.Load() {
			handlerSawClosed.Store(true)
			http.Error(w, "closed pool", http.StatusInternalServerError)
			return
		}
		handlerDoneAt.Store(time.Now().UnixNano())
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "tamam")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sigCtx, sig := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		// RunE'nin sirasini taklit eder: serveGraceful doner, sonra store kapanir.
		err := serveGraceful(sigCtx, srv, func() error { return srv.Serve(ln) }, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
		storeClosedAt.Store(time.Now().UnixNano())
		storeClosed.Store(true)
		runDone <- err
	}()

	type res struct {
		code int
		body string
		err  error
	}
	resCh := make(chan res, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			resCh <- res{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resCh <- res{code: resp.StatusCode, body: string(b)}
	}()
	<-started
	sig() // SIGTERM
	r := <-resCh
	if r.err != nil || r.code != http.StatusOK || r.body != "tamam" {
		t.Fatalf("yoldaki istek 200 tamamlanmali: %+v", r)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("serveGraceful hata: %v", err)
	}
	if handlerSawClosed.Load() {
		t.Fatal("isleyici kapanmis store gordu")
	}
	if storeClosedAt.Load() < handlerDoneAt.Load() {
		t.Fatal("store, yoldaki istek bitmeden kapandi")
	}
	// Kapanistan sonra yeni baglanti kabul edilmez.
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("kapanistan sonra dinleyici acik kaldi")
	}
}

// Sure dolunca uzun omurlu istek zorla kapatilir; serveGraceful sonsuza kadar beklemez.
func TestServeGracefulSureDolunceZorlaKapatir(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // SSE benzeri: istemci ya da sunucu kapatana kadar
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sigCtx, sig := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveGraceful(sigCtx, srv, func() error { return srv.Serve(ln) }, 300*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started
	t0 := time.Now()
	sig()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hata: %v", err)
		}
		if d := time.Since(t0); d > 3*time.Second {
			t.Fatalf("zorla kapanis cok uzun: %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveGraceful donmedi")
	}
}

// A5 #3: ErrorLog satirlari DEBUG'da hepsi, WARN'da en fazla aralik basina bir ornek.
func TestHTTPErrorLogHizSinirli(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	before := httpServerErrors.Load()
	el := newHTTPErrorLog(l)
	for i := 0; i < 250; i++ {
		el.Printf("http: TLS handshake error from 1.2.3.4:%d: client sent an HTTP request to an HTTPS server", i)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("250 satirda 1 WARN bekleniyordu, gelen %d", n)
	}
	if got := httpServerErrors.Load() - before; got != 250 {
		t.Fatalf("sayac 250 olmali: %d", got)
	}
}

func TestWaitTimeout(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	if waitTimeout(&wg, 50*time.Millisecond) {
		t.Fatal("bitmemis is icin true dondu")
	}
	wg.Done()
	if !waitTimeout(&wg, time.Second) {
		t.Fatal("bitmis is icin false dondu")
	}
}
