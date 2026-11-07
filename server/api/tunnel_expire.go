package api

import (
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// TunnelsExpired, suresi dolup supurucu (main.go, Store.DeleteExpiredTunnels)
// tarafindan SILINMIS gecici tunellerin yan etkilerini yayar. deleteTunnel'in
// panel istegi disinda yaptiklarinin aynisidir:
//
//   - yonlendirme + ham TCP/UDP dinleyicileri tazelenir (OnTunnelChange);
//   - her tunel icin kiraciya tunnel.deleted olayi yayilir (panel canli
//     listeden dusurur);
//   - tunelin sahibi (birincil) istemcilere, tunel artik olmayan guncel liste
//     config_update ile itilir (istemci kendi tarafinda kaldirir).
//
// Gecici tuneller ozel kaynak (Z5) olamaz (network.go ozel kaynagi kalici
// tunelden uretir), bu yuzden kapi/netmap tazelemesi gerekmez. Denetim kaydi
// da yazilmaz: eylemi yapan bir kullanici yok, olay zaten TTL'den dogar.
//
// Bos liste hicbir sey yapmaz (bos supurme turlari yonlendiriciyi tazelemez).
func (s *Server) TunnelsExpired(deleted []store.Tunnel) {
	if len(deleted) == 0 {
		return
	}
	s.tunnelsChanged()
	pushed := make(map[string]struct{}, len(deleted))
	for _, t := range deleted {
		if s.Events != nil && t.TenantID != "" {
			s.Events.PublishTenant(t.TenantID, events.TypeTunnelDeleted, map[string]string{"id": t.ID})
		}
		// Ayni istemcinin birden cok tuneli ayni turda dolabilir: tek itme yeter
		// (liste sorgusu silme sonrasi durumu okur).
		if _, done := pushed[t.ClientID]; done || t.ClientID == "" {
			continue
		}
		pushed[t.ClientID] = struct{}{}
		s.pushClientConfig(t.ClientID)
	}
}

// TunnelsExpiredNow, suresi YENI dolan (trafige kapatilan, ama saklanan) gecici
// tunellerin yan etkilerini yayar: yonlendirme tazelenir (ziyaretciler "suresi
// doldu" yaniti alir, ham port kapanir), kiraciya tunnel.updated olayi gider
// (panel "suresi doldu" gosterir) ve istemciye tunelin artik olmadigi guncel
// yapilandirma itilir. Bos liste hicbir sey yapmaz.
func (s *Server) TunnelsExpiredNow(expired []store.Tunnel) {
	if len(expired) == 0 {
		return
	}
	s.tunnelsChanged()
	pushed := make(map[string]struct{}, len(expired))
	for _, t := range expired {
		if s.Events != nil && t.TenantID != "" {
			s.Events.PublishTenant(t.TenantID, events.TypeTunnelUpdated, t)
		}
		if _, done := pushed[t.ClientID]; done || t.ClientID == "" {
			continue
		}
		pushed[t.ClientID] = struct{}{}
		s.pushClientConfig(t.ClientID)
	}
}
