package pgstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Sayfalama sozlesmesi testleri; ZORVEN_TEST_PG_DSN yoksa freshStore atlar.

func TestListTunnelsPage(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	c, err := s.CreateClient(ctx, store.DefaultTenantID, "dev", "tok1", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := 0; i < 7; i++ {
		tn, err := s.CreateTunnel(ctx, store.DefaultTenantID, c.ID, fmt.Sprintf("localhost:%d", 3000+i))
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, tn.ID)
	}

	// Sayfa sayfa gez: her kayit tam bir kez, toplam her sayfada 7.
	var got []string
	for off := 0; off < 7; off += 3 {
		page, total, err := s.ListTunnelsPage(ctx, store.DefaultTenantID, "", store.Page{Limit: 3, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if total != 7 {
			t.Fatalf("total=%d", total)
		}
		for _, tn := range page {
			got = append(got, tn.ID)
		}
	}
	if len(got) != 7 {
		t.Fatalf("toplam gezilen=%d", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Errorf("tekrar: %s", id)
		}
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("eksik: %s", id)
		}
	}

	// Ofset toplami asarsa bos sayfa ama dogru toplam.
	page, total, err := s.ListTunnelsPage(ctx, store.DefaultTenantID, "", store.Page{Limit: 3, Offset: 50})
	if err != nil || len(page) != 0 || total != 7 {
		t.Errorf("tasan ofset: len=%d total=%d err=%v", len(page), total, err)
	}
	// Sinirsiz (Limit=0): hepsi.
	page, total, err = s.ListTunnelsPage(ctx, store.DefaultTenantID, "", store.Page{})
	if err != nil || len(page) != 7 || total != 7 {
		t.Errorf("sinirsiz: len=%d total=%d err=%v", len(page), total, err)
	}
	// Baska kiracida hicbir sey yok.
	_, total, err = s.ListTunnelsPage(ctx, "ten_baska", "", store.Page{Limit: 10})
	if err != nil || total != 0 {
		t.Errorf("baska kiraci: total=%d err=%v", total, err)
	}
	// Eski ListTunnels ile ayni kume.
	all, err := s.ListTunnels(ctx, store.DefaultTenantID)
	if err != nil || len(all) != 7 {
		t.Fatalf("ListTunnels: %d %v", len(all), err)
	}
}

func TestAdminListAllClientsPage(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	for i := 0; i < 5; i++ {
		if _, err := s.CreateClient(ctx, store.DefaultTenantID, fmt.Sprintf("d%d", i), fmt.Sprintf("tk%d", i), "h"); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := s.AdminListAllClientsPage(ctx, store.Page{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 1 {
		t.Errorf("total=%d len=%d", total, len(page))
	}
}
