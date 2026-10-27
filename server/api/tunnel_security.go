package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunsec"
)

// ZTNA Z3 — tunel seviyesinde WAF + Geo ayari.
//
//	GET /api/v1/tunnels/{id}/security  — ayar + kural katalogu + Geo DB durumu
//	PUT /api/v1/tunnels/{id}/security  — kaydet {waf:{mode,body,disabled_rules}, geo:{mode,countries,unknown_is},
//	                                     bot:{mode,difficulty,ttl_minutes,exclude_paths,allow_verified_bots,block_empty_ua},
//	                                     rate:{enabled,requests,window_seconds,action,exclude_paths}}
//
// Engellenen/gozlenen istekler mevcut GET /api/v1/requests ucundan okunur:
//
//	?tunnel_id=<id>&reason=waf:*,waf_log:*,geo:*,challenge,bot:*,rate:*
//
// Plan kapisi (Pro+): WAF "block" kipi, Geo, bot.mode=always ve hiz siniri
// (bot.mode=suspicious tum planlarda). WAF "log" kipi herkese aciktir.
// Open core'da (Entitlements nil) kapi yoktur.

func (s *Server) getTunnelSecurity(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	ts, err := s.Store.GetTunnelSecurity(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := tunsec.Parse(ts.Config)
	if cfg.Bot.Mode == "" || cfg.Rate.Action == "" {
		// Kayit yok ya da bot/rate eklenmeden once kaydedilmis: varsayilanlarla doldur.
		d, _ := tunsec.Normalize(tunsec.Config{WAF: cfg.WAF, Geo: cfg.Geo})
		cfg.Bot, cfg.Rate = d.Bot, d.Rate
	}
	if cfg.WAF.Mode == "" {
		cfg.WAF.Mode = tunsec.WAFOff
	}
	if cfg.Geo.Mode == "" {
		cfg.Geo.Mode = tunsec.GeoOff
		cfg.Geo.UnknownIs = "allow"
	}
	pro := true
	if s.Entitlements != nil {
		pro = s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureTunnelSecurity) == nil
	}
	geoLoaded := false
	if s.GeoDBLoaded != nil {
		geoLoaded = s.GeoDBLoaded()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel_id":     ts.TunnelID,
		"waf":           cfg.WAF,
		"geo":           cfg.Geo,
		"bot":           cfg.Bot,
		"rate":          cfg.Rate,
		"updated_at":    ts.UpdatedAt,
		"rules":         tunsec.Rules(),
		"geo_db_loaded": geoLoaded,
		"plan_ok":       pro,
	})
}

func (s *Server) setTunnelSecurity(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	var body tunsec.Config
	if !decode(w, r, &body) {
		return
	}
	cfg, err := tunsec.Normalize(body)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_security", err.Error())
		return
	}
	// Plan kapisi: block kipi ve Geo Pro+; log kipi ve kapatma herkese acik.
	// Bot: suspicious tum planlar; always ve hiz siniri Pro+.
	needsPro := cfg.WAF.Mode == tunsec.WAFBlock || cfg.Geo.Mode != tunsec.GeoOff ||
		cfg.Bot.Mode == tunsec.BotAlways || cfg.Rate.Enabled
	if needsPro && s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureTunnelSecurity); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	raw, _ := json.Marshal(cfg)
	id := r.PathValue("id")
	if err := s.Store.SetTunnelSecurity(r.Context(), tenantID, store.TunnelSecurity{
		TunnelID: id, Config: raw,
	}); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "tunnel.security", id, "waf="+cfg.WAF.Mode+" geo="+cfg.Geo.Mode+" bot="+cfg.Bot.Mode+" rate="+strconv.FormatBool(cfg.Rate.Enabled))
	s.tunnelsChanged() // router snapshot'i + rawproxy Geo haritasi tazelensin
	// Z2: acik ham TCP/UDP akislari yeni ayara gore yeniden degerlendirilsin.
	s.notifyAuthzChange(tenantID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "waf": cfg.WAF, "geo": cfg.Geo, "bot": cfg.Bot, "rate": cfg.Rate})
}

// notifyAuthzChange, Z2 olay noktasi: ham TCP/UDP ve ziyaretci akislarinin
// yeniden degerlendirilmesini tetikler (OnAuthzChange main.go'da baglanir).
func (s *Server) notifyAuthzChange(tenantID string) {
	if s.OnAuthzChange == nil {
		return
	}
	go s.OnAuthzChange(tenantID)
}
