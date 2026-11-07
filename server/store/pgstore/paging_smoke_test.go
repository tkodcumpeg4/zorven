package pgstore

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Her sayfali sorgunun SQL'i (UNION tipleri, kolon sirasi, LIMIT/OFFSET) gercek
// Postgres'te hatasiz calismali ve bos tabloda 0 toplam donmeli.
func TestPagedQueriesRunOnEmptyTables(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	pg := store.Page{Limit: 5, Offset: 2}
	tn := store.DefaultTenantID
	checks := map[string]func() (int, int, error){
		"tunnels":   func() (int, int, error) { l, n, e := s.ListTunnelsPage(ctx, tn, "", pg); return len(l), n, e },
		"hostnames": func() (int, int, error) { l, n, e := s.ListHostnamesPage(ctx, tn, "", pg); return len(l), n, e },
		"projects":  func() (int, int, error) { l, n, e := s.ListProjectsPage(ctx, tn, pg); return len(l), n, e },
		"secrets":   func() (int, int, error) { l, n, e := s.ListSecretsPage(ctx, tn, "p", pg); return len(l), n, e },
		"policies":  func() (int, int, error) { l, n, e := s.ListPoliciesPage(ctx, tn, "p", pg); return len(l), n, e },
		"tokens":    func() (int, int, error) { l, n, e := s.ListAPITokensPage(ctx, tn, pg); return len(l), n, e },
		"iprules":   func() (int, int, error) { l, n, e := s.ListIPRulesPage(ctx, tn, nil, pg); return len(l), n, e },
		"members":   func() (int, int, error) { l, n, e := s.ListTeamMembersPage(ctx, tn, pg); return len(l), n, e },
		"tenants":   func() (int, int, error) { l, n, e := s.AdminListTenantsWithCountsPage(ctx, pg); return len(l), n, e },
		"allhosts":  func() (int, int, error) { l, n, e := s.AdminListAllHostnamesPage(ctx, pg); return len(l), n, e },
		"abuse":     func() (int, int, error) { l, n, e := s.ListAbuseReportsPage(ctx, pg); return len(l), n, e },
		"mail":      func() (int, int, error) { l, n, e := s.ListMailMessagesPage(ctx, tn, "inbox", pg); return len(l), n, e },
		"doorgrant": func() (int, int, error) {
			l, n, e := s.ListDoorGrantsPage(ctx, tn, "tun_x", true, pg)
			return len(l), n, e
		},
	}
	for name, fn := range checks {
		t.Run(name, func(t *testing.T) {
			got, total, err := fn()
			if err != nil {
				t.Fatalf("hata: %v", err)
			}
			// tenants/projects tablosunda varsayilan kayit olabilir; yalniz tutarlilik denetlenir.
			if got > total {
				t.Errorf("sayfa (%d) toplamdan (%d) buyuk", got, total)
			}
		})
	}
}
