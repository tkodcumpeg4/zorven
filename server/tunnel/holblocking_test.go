package tunnel

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Bir exchange'in tuketicisi HIC okumasa bile, baska bir exchange'e yazmak
// gecikmemelidir.
//
// Bu, oturum okuma dongusunun bloklanmadiginin kanitidir. Eski io.Pipe
// tasariminda ilk writeBody okuyucu gelene kadar bloklardi ve gercek olcumde
// tek bir 100 KB/s'lik tuketici throughput'u 7681 rps'den 15.3 rps'e
// dusuruyordu (p99 gecikme 1.8 ms -> 7030 ms).
func TestSlowConsumerDoesNotStallOtherExchange(t *testing.T) {
	slow := newExchange(1) // tuketicisi hic okumayacak
	fast := newExchange(2)

	chunk := make([]byte, protocol.BodyChunkSize)

	// Yavas akisin penceresini tamamen doldur.
	for w := 0; w+len(chunk) <= protocol.InitialWindowBytes; w += len(chunk) {
		if err := slow.writeBody(chunk); err != nil {
			t.Fatalf("yavas akisa yazim: %v", err)
		}
	}
	// Pencere doldu: bir sonraki yazim BLOKLAMAK yerine hata donmeli.
	if err := slow.writeBody(chunk); err == nil {
		t.Fatal("pencere dolu iken hata bekleniyordu (bloklanmis olabilir)")
	}

	// Hizli akis bundan hic etkilenmemeli.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := fast.writeBody([]byte("ping")); err != nil {
			t.Errorf("hizli akis yazimi hata verdi: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("yavas tuketici hizli akisi durdurdu — HOL blocking hala var")
	}
}

// Cok sayida es zamanli akista, birinin tikanmasi digerlerinin TAMAMINI
// etkilememeli. Gercek yuk desenine daha yakin bir kontrol.
func TestManyStreamsUnaffectedByOneStalledStream(t *testing.T) {
	stalled := newExchange(1)
	chunk := make([]byte, protocol.BodyChunkSize)
	for w := 0; w+len(chunk) <= protocol.InitialWindowBytes; w += len(chunk) {
		if err := stalled.writeBody(chunk); err != nil {
			t.Fatalf("tikanan akisa yazim: %v", err)
		}
	}

	const others = 50
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 2; i < 2+others; i++ {
			e := newExchange(uint64(i))
			if err := e.writeBody([]byte("x")); err != nil {
				t.Errorf("akis %d yazim hatasi: %v", i, err)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("tikanan tek akis %d diger akisi durdurdu", others)
	}
}
