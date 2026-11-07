package api

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
)

// PLAN KAPISI (karar 2026-10-30, Ek 1 madde 3): kapi olusturmada (createIPRule) ve kurali yeniden
// etkinlestirmede (updateIPRule enabled=true) uygulanir. Plan dusunce MEVCUT kurallar uygulanmaya
// DEVAM eder (ipfilter.CheckAllowed plan sormaz; kapatmak tuneli sessizce acardi); yeni kural ve
// yeniden etkinlestirme kapali kalir (pasife alma ve aciklama duzenleme plansiz serbesttir);
// SILME (deleteIPRule) HER ZAMAN acik kalir. Test: plan_gate_test.go.

// listIPRules, kiraciya ait IP izin listesi kurallarini listeler.
func (s *Server) listIPRules(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeIPAllowRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	var tunnelID *string
	if q := strings.TrimSpace(r.URL.Query().Get("tunnel_id")); q != "" {
		tunnelID = &q
	}

	pg, ok := pageOf(w, r)
	if !ok {
		return
	}
	rules, total, err := s.Store.ListIPRulesPage(r.Context(), tenantID, tunnelID, pg)
	if err != nil {
		s.fail(w, err)
		return
	}
	setPageTotals(w, pg, len(rules), total)

	writeJSON(w, http.StatusOK, map[string]any{
		"rules": rules,
		"count": len(rules),
	})
}

// createIPRule, yeni bir IP / CIDR izin kurali tanimlar.
func (s *Server) createIPRule(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeIPAllowWrite) {
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

	// Plan yetki kontrolu (Pro, Team, Enterprise)
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureIPAllowlist); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	var body struct {
		TunnelID    *string `json:"tunnel_id"` // null veya bos ise tenant-wide
		CIDR        string  `json:"cidr"`
		Description string  `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}

	cidrStr := strings.TrimSpace(body.CIDR)
	if cidrStr == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_cidr", "IP veya CIDR adresi zorunludur")
		return
	}

	// CIDR formatini dogrula ve normalize et
	_, ipNet, err := ipfilter.ParseCIDR(cidrStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_cidr", "Geçersiz IP veya CIDR formatı: "+err.Error())
		return
	}
	normalizedCIDR := ipNet.String()

	var tid *string
	if body.TunnelID != nil && strings.TrimSpace(*body.TunnelID) != "" {
		t := strings.TrimSpace(*body.TunnelID)
		// Tunelin bu kiraciya ait oldugunu dogrula
		if _, err := s.Store.GetTunnel(r.Context(), tenantID, t); err != nil {
			writeJSONError(w, http.StatusNotFound, "tunnel_not_found", "Belirtilen tünel bulunamadı")
			return
		}
		tid = &t
	}

	rule, err := s.Store.CreateIPRule(r.Context(), tenantID, tid, normalizedCIDR, strings.TrimSpace(body.Description))
	if err != nil {
		s.fail(w, err)
		return
	}

	// Ingress bellek-ici filtresini aninda guncelle
	if s.IPFilter != nil {
		_ = s.IPFilter.InvalidateTenant(r.Context(), tenantID)
	}

	writeJSON(w, http.StatusCreated, rule)
}

// updateIPRule, IP izin kuralini aktif/pasif yapar veya aciklamasini gunceller.
func (s *Server) updateIPRule(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeIPAllowWrite) {
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

	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "kural kimligi gerekli")
		return
	}

	var body struct {
		Enabled     *bool   `json:"enabled"`
		Description *string `json:"description"`
	}
	if !decode(w, r, &body) {
		return
	}

	// Kurali (yeniden) etkinlestirmek yeni kural eklemekle esdeger: plan yetkisi gerekir.
	// Pasife alma ve aciklama guncelleme plan ozelligi olmadan da serbesttir.
	if body.Enabled != nil && *body.Enabled && s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureIPAllowlist); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	rule, err := s.Store.UpdateIPRule(r.Context(), tenantID, id, body.Enabled, body.Description)
	if err != nil {
		s.fail(w, err)
		return
	}

	if s.IPFilter != nil {
		_ = s.IPFilter.InvalidateTenant(r.Context(), tenantID)
	}

	writeJSON(w, http.StatusOK, rule)
}

// deleteIPRule, IP izin kuralini siler. Plan ozelligi KONTROL EDILMEZ: plani dusen kiraci
// kurallarini her zaman silebilmeli (aksi halde artik duzenleyemedigi kuralda takili kalir).
func (s *Server) deleteIPRule(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeIPAllowWrite) {
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

	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "kural kimligi gerekli")
		return
	}

	if err := s.Store.DeleteIPRule(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}

	if s.IPFilter != nil {
		_ = s.IPFilter.InvalidateTenant(r.Context(), tenantID)
	}

	w.WriteHeader(http.StatusNoContent)
}
