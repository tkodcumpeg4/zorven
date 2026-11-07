package api

import (
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

func TestOfflineDiff(t *testing.T) {
	a := pgstore.OfflineClient{ID: "a", Name: "core"}
	b := pgstore.OfflineClient{ID: "b", Name: "ubuntu"}
	c := pgstore.OfflineClient{ID: "c", Name: "win"}

	down, up, next := offlineDiff(nil, []pgstore.OfflineClient{a, b})
	if len(down) != 2 || len(up) != 0 || len(next) != 2 {
		t.Fatalf("ilk tur: down=%d up=%d next=%d", len(down), len(up), len(next))
	}
	// Ayni kume: tekrar bildirim yok (spam onleme).
	down, up, next = offlineDiff(next, []pgstore.OfflineClient{a, b})
	if len(down) != 0 || len(up) != 0 {
		t.Fatalf("degismeyen kume bildirilmemeli: down=%d up=%d", len(down), len(up))
	}
	// b geri dondu, c koptu.
	down, up, _ = offlineDiff(next, []pgstore.OfflineClient{a, c})
	if len(down) != 1 || down[0].ID != "c" || len(up) != 1 || up[0].ID != "b" {
		t.Fatalf("degisim: down=%v up=%v", down, up)
	}
}

func TestAgentOfflineMail(t *testing.T) {
	ts := time.Date(2026, 11, 7, 14, 26, 0, 0, time.UTC)
	subj, body := agentOfflineMail(
		[]pgstore.OfflineClient{{ID: "cli_1", Name: "core", TenantSlug: "ornek-a", Version: "0.3.6", LastSeenAt: ts}},
		[]pgstore.OfflineClient{{ID: "cli_2", Name: "ubuntu", TenantSlug: "ornek-b"}},
		1, 10*time.Minute, "panel.zorven.app")
	for _, want := range []string{"1 ajan koptu", "1 geri dondu", "toplam kopuk: 1"} {
		if !strings.Contains(subj, want) {
			t.Errorf("konu %q icermeli: %s", want, subj)
		}
	}
	for _, want := range []string{"core", "ornek-a", "0.3.6", "2026-11-07 14:26", "ubuntu", "https://panel.zorven.app"} {
		if !strings.Contains(body, want) {
			t.Errorf("govde %q icermeli:\n%s", want, body)
		}
	}
}

func TestAgentOfflineWatchConnected(t *testing.T) {
	c := pgstore.OfflineClient{ID: "a", HasSession: true}
	if !(&AgentOfflineWatch{Cluster: true}).connected(c) {
		t.Fatal("kume kipinde oturumu olan bagli sayilmali")
	}
	if (&AgentOfflineWatch{Cluster: true}).connected(pgstore.OfflineClient{ID: "b"}) {
		t.Fatal("kume kipinde oturumsuz kopuk sayilmali")
	}
	w := &AgentOfflineWatch{LocalConnected: func(id string) bool { return id == "a" }}
	if !w.connected(pgstore.OfflineClient{ID: "a"}) || w.connected(pgstore.OfflineClient{ID: "b"}) {
		t.Fatal("standalone'da Hub'a bakilmali (HasSession yok sayilir)")
	}
}

func TestGroupUnnotifiedByTenant(t *testing.T) {
	order, m := groupUnnotifiedByTenant([]pgstore.OfflineClient{
		{ID: "1", TenantID: "t1"}, {ID: "2", TenantID: "t2"}, {ID: "3", TenantID: "t1"},
		{ID: "4", TenantID: "t3", OfflineNotified: true},
	})
	if strings.Join(order, ",") != "t1,t2" || len(m["t1"]) != 2 || len(m["t2"]) != 1 || len(m["t3"]) != 0 {
		t.Fatalf("gruplama: %v %v", order, m)
	}
}

// Dort dilde de konu/govde dolu, ajan adi, kurtarma komutu ve panel baglantisi var;
// baska kiracinin verisi icin alan yok (yalniz verilen ajanlar).
func TestAgentOfflineOwnerMailAllLanguages(t *testing.T) {
	ts := time.Date(2026, 11, 7, 14, 26, 0, 0, time.UTC)
	one := []pgstore.OfflineClient{{ID: "c1", Name: "core", Version: "0.3.6", LastSeenAt: ts}}
	two := append(one, pgstore.OfflineClient{ID: "c2", Name: "ubuntu", LastSeenAt: ts})
	for _, lang := range []string{"tr", "en"} {
		if _, ok := agentOfflineTexts[lang]; !ok {
			t.Fatalf("%s metni yok", lang)
		}
		s1, b1 := agentOfflineOwnerMail(lang, one, 10*time.Minute, "zorven.app")
		if !strings.Contains(s1, "core") {
			t.Errorf("%s: tek ajan konusu adi icermeli: %s", lang, s1)
		}
		for _, want := range []string{"core", "0.3.6", "2026-11-07 14:26", "10", "nodes.json", "zorven service restart", "https://panel.zorven.app/clients"} {
			if !strings.Contains(b1, want) {
				t.Errorf("%s: govde %q icermeli", lang, want)
			}
		}
		if strings.Contains(b1, "%!") || strings.Contains(s1, "%!") {
			t.Errorf("%s: bicim hatasi: %s\n%s", lang, s1, b1)
		}
		s2, b2 := agentOfflineOwnerMail(lang, two, 10*time.Minute, "zorven.app")
		if !strings.Contains(s2, "2") || !strings.Contains(b2, "ubuntu") || !strings.Contains(b2, "?") {
			t.Errorf("%s: coklu ajan: %s\n%s", lang, s2, b2)
		}
	}
	if s, _ := agentOfflineOwnerMail("xx", one, 10*time.Minute, "zorven.app"); !strings.Contains(s, "Ajan") {
		t.Errorf("bilinmeyen dil tr'ye dusmeli: %s", s)
	}
}
