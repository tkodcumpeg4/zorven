package api

import (
	"context"
	"time"
)

// projectTunnelSet, projenin tunel kimlikleri kumesini dondurur (istek
// kayitlarini projeye gore suzmek icin). Store yoksa bos kume doner.
func (s *Server) projectTunnelSet(ctx context.Context, tenantID, projectID string) (map[string]bool, error) {
	set := map[string]bool{}
	if s.Store == nil {
		return set, nil
	}
	list, err := s.Store.ListTunnelsByProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		set[t.ID] = true
	}
	return set, nil
}

const (
	projectTunnelTTL         = 30 * time.Second // tam yenileme araligi
	projectTunnelMissRefresh = 3 * time.Second  // bilinmeyen tunelde en sik yenileme
)

// projectTunnelCache, bir SSE akisinin projeye ait tunel kimlikleri onbellegi.
// Tek goroutine (akis dongusu) kullanir; kilit gerekmez. Onbellekte olmayan
// tunel gelirse (yeni olusturulmus olabilir) en gec 3 sn'de bir yenilenir.
// Tunelsiz kayit (platform duzeyi) proje gorunumunde gosterilmez.
type projectTunnelCache struct {
	s       *Server
	tenant  string
	project string
	set     map[string]bool
	loaded  time.Time
}

func (c *projectTunnelCache) has(ctx context.Context, tunnelID string) bool {
	if tunnelID == "" {
		return false
	}
	now := time.Now()
	if c.set == nil || now.Sub(c.loaded) > projectTunnelTTL {
		c.refresh(ctx, now)
	}
	if c.set[tunnelID] {
		return true
	}
	if now.Sub(c.loaded) > projectTunnelMissRefresh {
		c.refresh(ctx, now)
		return c.set[tunnelID]
	}
	return false
}

func (c *projectTunnelCache) refresh(ctx context.Context, now time.Time) {
	set, err := c.s.projectTunnelSet(ctx, c.tenant, c.project)
	c.loaded = now
	if err != nil {
		if c.set == nil {
			c.set = map[string]bool{} // fail-closed
		}
		return
	}
	c.set = set
}
