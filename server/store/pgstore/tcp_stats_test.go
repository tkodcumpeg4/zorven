package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// TCP dakikalik ozet: yazma, okuma, ayni dakikada birlestirme, kiraci izolasyonu, silme.
func TestTCPStatsInsertListMerge(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tn := store.DefaultTenantID
	c, err := s.CreateClient(ctx, tn, "tcp-st", "tid-tcp", "hash")
	if err != nil {
		t.Fatal(err)
	}
	tun, err := s.CreateTunnel(ctx, tn, c.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	m0 := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)

	if err := s.InsertTCPStats(ctx, []store.TCPStatMinute{
		{TunnelID: tun.ID, TenantID: tn, Minute: m0, ConnsNew: 2, ConnsPeak: 3, ConnsRejected: 1, RejectedIP: 1,
			BytesIn: 100, BytesOut: 200, DurSumMs: 1000, DurCount: 2, DurMaxMs: 800},
		{TunnelID: tun.ID, TenantID: tn, Minute: m0.Add(time.Minute), ConnsNew: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// Ayni dakikaya ikinci yazma: toplanir, tepe/en uzun buyuk olani alir.
	if err := s.InsertTCPStats(ctx, []store.TCPStatMinute{
		{TunnelID: tun.ID, TenantID: tn, Minute: m0, ConnsNew: 1, ConnsPeak: 2, ConnsRejected: 2, RejectedDoor: 2,
			BytesIn: 50, BytesOut: 50, DurSumMs: 3000, DurCount: 1, DurMaxMs: 3000},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListTCPStats(ctx, tn, tun.ID, m0.Add(-time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("liste: %v %+v", err, got)
	}
	a := got[0]
	if a.ConnsNew != 3 || a.ConnsPeak != 3 || a.ConnsRejected != 3 || a.RejectedIP != 1 || a.RejectedDoor != 2 ||
		a.BytesIn != 150 || a.BytesOut != 250 || a.DurCount != 3 || a.DurMaxMs != 3000 || a.DurAvgMs != 4000.0/3 {
		t.Fatalf("birlestirme: %+v", a)
	}
	// Baska kiracinin kimligiyle okuma bos doner.
	if other, err := s.ListTCPStats(ctx, "baska-kiraci", tun.ID, m0.Add(-time.Minute)); err != nil || len(other) != 0 {
		t.Fatalf("kiraci izolasyonu: %v %+v", err, other)
	}
	// Var olmayan tunel sessizce atlanir.
	if err := s.InsertTCPStats(ctx, []store.TCPStatMinute{{TunnelID: "yok", TenantID: tn, Minute: m0, ConnsNew: 1}}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneTCPStats(ctx, m0.Add(30*time.Second))
	if err != nil || n != 1 {
		t.Fatalf("prune: %v %d", err, n)
	}
}
