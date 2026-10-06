package tunnel

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Hicbir okuyucu yokken bile pencere kadar yazmak ANINDA donmelidir.
//
// Eski io.Pipe tasariminda ilk writeBody, okuyucu baytlari tuketene kadar
// bloklardi; bu blok oturumun TEK okuma dongusunde gerceklestigi icin ayni
// istemcideki tum diger istekleri durduruyordu (head-of-line blocking).
func TestWriteBodyNeverBlocksWithinWindow(t *testing.T) {
	e := newExchange(1)
	chunk := make([]byte, protocol.BodyChunkSize)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for written := 0; written+len(chunk) <= protocol.InitialWindowBytes; written += len(chunk) {
			if err := e.writeBody(chunk); err != nil {
				t.Errorf("writeBody bloklamadan basarili olmaliydi: %v", err)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writeBody bloklandi — head-of-line blocking hala var")
	}
}

// Pencereyi asan yazim, bloklamak yerine protokol ihlali dondurmeli.
func TestWriteBodyRejectsWindowOverflow(t *testing.T) {
	e := newExchange(1)
	chunk := make([]byte, protocol.BodyChunkSize)
	for written := 0; written+len(chunk) <= protocol.InitialWindowBytes; written += len(chunk) {
		if err := e.writeBody(chunk); err != nil {
			t.Fatalf("pencere icinde hata: %v", err)
		}
	}
	if err := e.writeBody(chunk); !errors.Is(err, ErrFlowControlViolation) {
		t.Fatalf("ErrFlowControlViolation bekleniyordu, geldi: %v", err)
	}
}

// FAZ 8 regresyonu: KUCUK cerceveler (chunked/SSE) pencere kadar BAYT dolana
// kadar reddedilmemeli. Eski tampon cerceve-SAYISI (32) tabanliydi; kucuk
// cercevelerde pencereden cok once dolup buyuk chunked yanitlari kesiyordu.
func TestWriteBodySmallFramesFillByteWindow(t *testing.T) {
	e := newExchange(1)
	small := make([]byte, 1024) // 1 KB — 32 cerceveden cok daha fazlasi sigmali
	written := 0
	for written+len(small) <= protocol.InitialWindowBytes {
		if err := e.writeBody(small); err != nil {
			t.Fatalf("kucuk cerceve pencere icinde reddedildi (%d bayt sonra): %v", written, err)
		}
		written += len(small)
	}
	// Pencere kadar BAYT yazildi; en az 2000 kucuk cerceve (eski 32 tavaninin cok ustu).
	if frames := written / len(small); frames < 2000 {
		t.Fatalf("yalnizca %d kucuk cerceve sigdi; byte-tabanli tampon beklenirdi", frames)
	}
	// Bir sonraki yazim pencereyi asar -> ihlal.
	if err := e.writeBody(small); !errors.Is(err, ErrFlowControlViolation) {
		t.Fatalf("pencere doldu; ErrFlowControlViolation bekleniyordu, geldi: %v", err)
	}
}

// Tuketici okudukca kredi iadesi bildirilmeli (window_update'i bu tetikler).
func TestDrainCallbackReportsConsumedBytes(t *testing.T) {
	e := newExchange(1)
	var drained int
	e.onDrain = func(n int) { drained += n }

	payload := []byte("merhaba dunya")
	if err := e.writeBody(payload); err != nil {
		t.Fatalf("writeBody: %v", err)
	}
	e.closeBody(nil)

	got, err := io.ReadAll(e.Body())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("govde bozuldu: %q", got)
	}
	if drained != len(payload) {
		t.Fatalf("drained=%d, beklenen=%d", drained, len(payload))
	}
}

// Hata ile kapatilan govde, okuyan tarafa AYNI hatayi vermeli.
func TestCloseBodyWithErrorSurfacesToReader(t *testing.T) {
	e := newExchange(1)
	want := errors.New("istemci koptu")
	e.closeBody(want)

	if _, err := io.ReadAll(e.Body()); !errors.Is(err, want) {
		t.Fatalf("okuyucuya hata iletilmedi: %v", err)
	}
}

// Okuyucu vazgectiginde (abandon) uretici takilip kalmamali.
func TestWriteBodyAfterAbandonDoesNotHang(t *testing.T) {
	e := newExchange(1)
	chunk := make([]byte, protocol.BodyChunkSize)
	// Tamponu doldur ki "gonderilebilir" dal kapansin.
	for written := 0; written+len(chunk) <= protocol.InitialWindowBytes; written += len(chunk) {
		if err := e.writeBody(chunk); err != nil {
			t.Fatalf("pencere icinde hata: %v", err)
		}
	}
	e.abandon()

	done := make(chan error, 1)
	go func() { done <- e.writeBody(chunk) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("terk edilmis exchange'e yazim basarili olmamaliydi")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("abandon sonrasi writeBody bloklandi")
	}
}
