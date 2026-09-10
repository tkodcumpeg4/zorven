package agent

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Baslangic penceresi kadar veri BEKLEMEDEN gonderilebilmeli.
func TestAcquireWithinInitialWindow(t *testing.T) {
	w := newWindowTable()
	if err := w.acquire(context.Background(), 1, protocol.InitialWindowBytes); err != nil {
		t.Fatalf("baslangic penceresi icinde beklememeliydi: %v", err)
	}
}

// Pencere bittiginde beklemeli, kredi gelince devam etmeli.
func TestAcquireBlocksUntilCredit(t *testing.T) {
	w := newWindowTable()
	ctx := context.Background()
	if err := w.acquire(ctx, 1, protocol.InitialWindowBytes); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- w.acquire(ctx, 1, 1024) }()

	select {
	case <-done:
		t.Fatal("kredi yokken devam etti")
	case <-time.After(100 * time.Millisecond):
	}

	w.add(1, 4096) // sunucu kredi verdi
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("kredi sonrasi hata: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kredi geldigi halde devam etmedi")
	}
}

// Bir akisin beklemesi DIGER akisi durdurmamali — HOL blocking'in ozu budur.
func TestOneStreamStallDoesNotBlockAnother(t *testing.T) {
	w := newWindowTable()
	ctx := context.Background()
	if err := w.acquire(ctx, 1, protocol.InitialWindowBytes); err != nil {
		t.Fatal(err)
	}
	// req 1 kredisiz kaldi; req 2 hala akmali.
	done := make(chan error, 1)
	go func() { done <- w.acquire(ctx, 2, protocol.BodyChunkSize) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ikinci akis hata verdi: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bir akisin beklemesi digerini durdurdu")
	}
}

// Iptal edilen baglam beklemeyi sonlandirmali (goroutine sizmasin).
func TestAcquireHonoursContext(t *testing.T) {
	w := newWindowTable()
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.acquire(ctx, 1, protocol.InitialWindowBytes); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := w.acquire(ctx, 1, 1); err == nil {
		t.Fatal("iptal edilen baglamda hata bekleniyordu")
	}
}

// release, istek bitince bekleyeni serbest birakmali ve kaydi temizlemeli.
func TestReleaseWakesWaiterAndResets(t *testing.T) {
	w := newWindowTable()
	ctx := context.Background()
	if err := w.acquire(ctx, 1, protocol.InitialWindowBytes); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- w.acquire(ctx, 1, 1024)
	}()
	<-started
	time.Sleep(50 * time.Millisecond)

	w.release(1) // kayit silinir -> yeniden baslangic penceresi gecerli olur
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("release sonrasi hata: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release bekleyeni uyandirmadi")
	}
}
