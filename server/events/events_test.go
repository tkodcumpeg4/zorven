package events

import (
	"testing"
	"time"
)

func TestSubscribeAndPublish(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe()
	defer stop()

	b.Publish(TypeClientConnected, map[string]string{"client_id": "cli_1"})

	select {
	case e := <-ch:
		if e.Type != TypeClientConnected {
			t.Errorf("Type = %q, beklenen %q", e.Type, TypeClientConnected)
		}
	case <-time.After(time.Second):
		t.Fatal("olay alinamadi")
	}
}

func TestMultipleSubscribersAllReceive(t *testing.T) {
	b := New()
	ch1, stop1 := b.Subscribe()
	defer stop1()
	ch2, stop2 := b.Subscribe()
	defer stop2()

	if got := b.Count(); got != 2 {
		t.Fatalf("Count = %d, beklenen 2", got)
	}
	b.Publish(TypeTunnelCreated, nil)

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Errorf("abone %d olayi almadi", i+1)
		}
	}
}

// EN ONEMLI TEST: yavas bir abone Publish'i BLOKLAMAMALI.
// Aksi halde tek yavas dashboard tum ingress sicak yolunu yavaslatirdi.
func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	b := New()
	_, stop := b.Subscribe() // hic okunmuyor
	defer stop()

	done := make(chan struct{})
	go func() {
		// Tampondan cok daha fazla olay yayinla
		for range bufferPerSubscriber * 3 {
			b.Publish(TypeRequestCompleted, nil)
		}
		close(done)
	}()

	select {
	case <-done:
		if b.Dropped() == 0 {
			t.Error("tampon tasmasina ragmen hic olay dusurulmedi")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish yavas abone yuzunden BLOKLANDI")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	ch, stop := b.Subscribe()

	stop()
	if got := b.Count(); got != 0 {
		t.Errorf("Count = %d, abonelik dusurulmeliydi", got)
	}

	b.Publish(TypeClientConnected, nil) // panic etmemeli

	// Kanal kapali olmali
	if _, open := <-ch; open {
		t.Error("kanal kapatilmaliydi")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	b := New()
	_, stop := b.Subscribe()
	stop()
	stop() // ikinci cagri panic etmemeli (kapali kanali tekrar kapatma)
}
