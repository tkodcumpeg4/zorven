package obs

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// Go: kisa omurlu is; panik sayilir, yeniden baslatilmaz, normal is calisir.
func TestGoOncePanicCounted(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var runs atomic.Int32
	Go(log, "test_once", func() {
		runs.Add(1)
		panic("tek seferlik")
	})
	done := make(chan struct{})
	Go(nil, "test_once_ok", func() { close(done) })
	deadline := time.Now().Add(3 * time.Second)
	for Panics("test_once") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("panik sayilmadi")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("normal is calismadi")
	}
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 1 || Panics("test_once_ok") != 0 {
		t.Fatalf("tur=%d (yeniden baslatilmamali)", runs.Load())
	}
}
