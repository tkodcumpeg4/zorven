package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func fp(v float64) *float64 { return &v }

// UDP olcum kolonlari: yazma, okuma, NULL korunumu ve ayni dakikada birlestirme.
func TestUDPStatsProbeColumns(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tn := store.DefaultTenantID
	c, err := s.CreateClient(ctx, tn, "udp-pc", "tid-udp", "hash")
	if err != nil {
		t.Fatal(err)
	}
	tun, err := s.CreateTunnel(ctx, tn, c.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	m0 := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)

	rows := []store.UDPStatMinute{
		{TunnelID: tun.ID, TenantID: tn, Minute: m0, PacketsIn: 1,
			RTTP50Ms: fp(12.5), RTTP95Ms: fp(40), ProbeLossPct: fp(20), DroppedDatagrams: 3},
		// Olcumsuz dakika (eski istemci): NULL kalmali.
		{TunnelID: tun.ID, TenantID: tn, Minute: m0.Add(time.Minute), PacketsIn: 2},
	}
	if err := s.InsertUDPStats(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// Ayni dakikaya ikinci yazma: sayac toplanir, nil olcum eskisini silmez.
	if err := s.InsertUDPStats(ctx, []store.UDPStatMinute{
		{TunnelID: tun.ID, TenantID: tn, Minute: m0, PacketsIn: 1, DroppedDatagrams: 2},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListUDPStats(ctx, tn, tun.ID, m0.Add(-time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("liste: %v %+v", err, got)
	}
	a, b := got[0], got[1]
	if a.RTTP50Ms == nil || *a.RTTP50Ms != 12.5 || *a.RTTP95Ms != 40 || *a.ProbeLossPct != 20 {
		t.Fatalf("olcum degerleri: %+v", a)
	}
	if a.PacketsIn != 2 || a.DroppedDatagrams != 5 {
		t.Fatalf("birlestirme: %+v", a)
	}
	if b.RTTP50Ms != nil || b.RTTP95Ms != nil || b.ProbeLossPct != nil || b.DroppedDatagrams != 0 {
		t.Fatalf("olcumsuz dakika NULL olmali: %+v", b)
	}
}
