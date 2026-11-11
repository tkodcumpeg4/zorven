package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"testing"
	"time"
)

// Y4: ajan govdeyi yarida birakip HIC bir sey yollamazsa, baglanan baglam
// iptal olunca okuma donmeli (eskiden oturum sonuna kadar asili kaliyordu).
func TestReadReturnsWhenBoundContextCanceled(t *testing.T) {
	e := newExchange(1)
	_ = e.writeBody([]byte("partial"))
	ctx, cancel := context.WithCancel(context.Background())
	e.BindContext(ctx)

	buf := make([]byte, 64)
	if n, err := e.Read(buf); err != nil || string(buf[:n]) != "partial" {
		t.Fatalf("ilk okuma: %q %v", buf[:n], err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.Read(buf); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("context.Canceled bekleniyordu: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("baglam iptalinden sonra Read donmedi")
	}
}

// FinishExchange (abandon) sonrasi da okuma beklemede kalmamali.
func TestReadReturnsOnAbandon(t *testing.T) {
	e := newExchange(1)
	done := make(chan error, 1)
	go func() { _, err := e.Read(make([]byte, 8)); done <- err }()
	time.Sleep(20 * time.Millisecond)
	e.abandon()
	select {
	case err := <-done:
		if !errors.Is(err, ErrReaderGone) {
			t.Fatalf("ErrReaderGone bekleniyordu: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("abandon sonrasi Read donmedi")
	}
}

// Sira korunur: kapanistan once kuyruga giren cerceveler hatadan/EOF'tan ONCE okunur.
func TestFramesDrainBeforeCloseError(t *testing.T) {
	e := newExchange(1)
	_ = e.writeBody([]byte("a"))
	_ = e.writeBody([]byte("b"))
	want := errors.New("kesildi")
	e.closeBody(want)
	got, err := io.ReadAll(e.Body())
	if string(got) != "ab" || !errors.Is(err, want) {
		t.Fatalf("got %q err %v", got, err)
	}
	// Kapanistan sonra gelen cerceve reddedilir (panik/sizinti yok).
	if err := e.writeBody([]byte("c")); err == nil {
		t.Fatal("kapanistan sonra yazim kabul edildi")
	}
}

// Ureten ve okuyan esz zamanli: tum baytlar sirayla gelir (uyandirma kaybi yok).
func TestConcurrentProducerConsumerNoLostWakeup(t *testing.T) {
	e := newExchange(1)
	var want bytes.Buffer
	go func() {
		for i := 0; i < 5000; i++ {
			p := []byte{byte(i), byte(i >> 8)}
			want.Write(p)
			for e.writeBody(p) != nil { // pencere doluysa okuyucuyu bekle
				runtime.Gosched()
			}
		}
		e.closeBody(nil)
	}()
	got, err := io.ReadAll(e.Body())
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("eksik/sirasiz veri: %d/%d bayt, hata %v", len(got), want.Len(), err)
	}
}

// O14: kucuk istek basina tahsis. Eski tasarimda her exchange 2048 elemanli
// kanal (~48 KB) ayiriyordu; simdi birkac yuz bayt.
func TestSmallExchangeAllocationIsTiny(t *testing.T) {
	var ms0, ms1 runtime.MemStats
	const n = 2000
	keep := make([]*Exchange, 0, n)
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	for i := 0; i < n; i++ {
		e := newExchange(uint64(i))
		_ = e.writeBody([]byte("ok"))
		e.closeBody(nil)
		keep = append(keep, e)
	}
	runtime.ReadMemStats(&ms1)
	per := (ms1.TotalAlloc - ms0.TotalAlloc) / n
	if per > 2048 {
		t.Fatalf("exchange basina %d bayt tahsis (beklenen < 2 KB)", per)
	}
	_ = keep
}

func BenchmarkSmallExchange(b *testing.B) {
	b.ReportAllocs()
	buf := make([]byte, 64)
	for i := 0; i < b.N; i++ {
		e := newExchange(uint64(i))
		_ = e.writeBody([]byte("ok"))
		e.closeBody(nil)
		for {
			if _, err := e.Read(buf); err != nil {
				break
			}
		}
	}
}
