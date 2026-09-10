package reqlog

import (
	"testing"
	"time"
)

func TestAddAndListNewestFirst(t *testing.T) {
	r := New(10)
	for _, m := range []string{"GET", "POST", "PUT"} {
		r.Add(Entry{TunnelID: "tun_1", Method: m})
	}

	got := r.List(0, "")
	if len(got) != 3 {
		t.Fatalf("List = %d kayit, beklenen 3", len(got))
	}
	// En yeni basta olmali
	if got[0].Method != "PUT" || got[2].Method != "GET" {
		t.Errorf("siralama yanlis: %s, %s, %s", got[0].Method, got[1].Method, got[2].Method)
	}
}

// Kapasite asilinca en eski kayitlar dusmeli; tampon buyumemeli.
func TestRingDropsOldest(t *testing.T) {
	r := New(3)
	for _, m := range []string{"A", "B", "C", "D", "E"} {
		r.Add(Entry{Method: m})
	}

	if got := r.Len(); got != 3 {
		t.Fatalf("Len = %d, kapasite 3 olmaliydi", got)
	}
	got := r.List(0, "")
	want := []string{"E", "D", "C"} // en yeni 3, yeniden eskiye
	for i, w := range want {
		if got[i].Method != w {
			t.Errorf("kayit %d = %s, beklenen %s", i, got[i].Method, w)
		}
	}
}

func TestFilterByTunnel(t *testing.T) {
	r := New(10)
	r.Add(Entry{TunnelID: "tun_a", Method: "GET"})
	r.Add(Entry{TunnelID: "tun_b", Method: "POST"})
	r.Add(Entry{TunnelID: "tun_a", Method: "PUT"})

	got := r.List(0, "tun_a")
	if len(got) != 2 {
		t.Fatalf("tun_a icin %d kayit, beklenen 2", len(got))
	}
	for _, e := range got {
		if e.TunnelID != "tun_a" {
			t.Errorf("sizinti: %s", e.TunnelID)
		}
	}
}

func TestLimitIsRespected(t *testing.T) {
	r := New(100)
	for range 50 {
		r.Add(Entry{Method: "GET"})
	}
	if got := len(r.List(10, "")); got != 10 {
		t.Errorf("limit=10 icin %d kayit dondu", got)
	}
}

func TestIDAndTimestampAreFilled(t *testing.T) {
	r := New(5)
	e := r.Add(Entry{Method: "GET"})
	if e.ID == "" {
		t.Error("ID otomatik doldurulmaliydi")
	}
	if e.TS.IsZero() {
		t.Error("TS otomatik doldurulmaliydi")
	}

	// Acikca verilen degerler korunmali
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e2 := r.Add(Entry{ID: "req_ozel", TS: ts})
	if e2.ID != "req_ozel" || !e2.TS.Equal(ts) {
		t.Errorf("acikca verilen ID/TS ezildi: %+v", e2)
	}
}

func TestEmptyRingReturnsEmptySlice(t *testing.T) {
	if got := New(10).List(0, ""); got == nil || len(got) != 0 {
		t.Errorf("bos tampon bos dilim donmeli (JSON'da []), aldim: %v", got)
	}
}
