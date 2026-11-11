package reqlog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func testPersister(flush func(context.Context, []Entry) error) *Persister {
	p := NewPersister(flush, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.interval = 10 * time.Millisecond
	return p
}

// A4 #8: DB kesintisinde topluluk atilmaz; sinirli tamponda yeniden denenir ve DB
// donunce yazilir. Disa aktarim (OnNew) her kaydi bir kez alir.
func TestPersisterKesintidenSonraYazar(t *testing.T) {
	var mu sync.Mutex
	fails := 3
	stored := map[string]int{}
	p := testPersister(func(_ context.Context, b []Entry) error {
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return errors.New("closed pool")
		}
		for _, e := range b {
			stored[e.ID]++
		}
		return nil
	})
	exported := map[string]int{}
	p.OnNew = func(b []Entry) {
		for _, e := range b {
			exported[e.ID]++ // Run goroutine'inde cagrilir; ayri kilit gerekmez
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	for i := 0; i < 50; i++ {
		p.Enqueue(Entry{ID: NewID()})
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(stored)
		mu.Unlock()
		if n == 50 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if len(stored) != 50 || p.Dropped() != 0 {
		t.Fatalf("yazilan=%d atilan=%d", len(stored), p.Dropped())
	}
	for id, c := range exported {
		if c != 1 {
			t.Fatalf("%s disa aktarima %d kez verildi", id, c)
		}
	}
	if len(exported) != 50 {
		t.Fatalf("disa aktarilan %d", len(exported))
	}
}

// Kalici kesintide yeniden denemeler tukenince atilir ve sayilir; tampon sinirli kalir.
func TestPersisterKaliciKesintideSinirliVeSayar(t *testing.T) {
	var maxBatch int
	var mu sync.Mutex
	p := testPersister(func(_ context.Context, b []Entry) error {
		mu.Lock()
		if len(b) > maxBatch {
			maxBatch = len(b)
		}
		mu.Unlock()
		return errors.New("db kapali")
	})
	p.maxRetain = 100
	p.maxAttempts = 3
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	for i := 0; i < 1000; i++ {
		p.Enqueue(Entry{ID: NewID()})
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	if p.Dropped() != 1000 {
		t.Fatalf("1000 kaydin tamami atilmis sayilmali: %d", p.Dropped())
	}
	if maxBatch > 100 {
		t.Fatalf("tampon siniri asildi: %d", maxBatch)
	}
}

// A5 #13: yol/host/metot icindeki CR/LF ve diger kontrol karakterleri kacislanir.
func TestSanitizeEntryCRLF(t *testing.T) {
	r := New(10)
	e := r.Add(Entry{Path: "/a\r\nX-Sahte: 1\x00\x7f", Method: "GET\n", Hostname: "h\r.x"})
	if e.Path != "/a%0D%0AX-Sahte: 1%00%7F" || e.Method != "GET%0A" || e.Hostname != "h%0D.x" {
		t.Fatalf("kacislanmadi: %+v", e)
	}
	if got := r.List(1, "")[0].Path; got != e.Path {
		t.Fatalf("halkadaki kayit kacislanmadi: %q", got)
	}
	if s := escapeControl("/normal/yol?x=1"); s != "/normal/yol?x=1" {
		t.Fatalf("normal yol degisti: %q", s)
	}
}
