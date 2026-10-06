package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/abuse"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 4 — Kötüye kullanım uçları.
//
//	POST /api/v1/abuse              (PUBLIC)  — bir hostname'i bildir
//	GET  /api/v1/admin/abuse-reports (ADMIN)  — bildirimleri listele
//	POST /api/v1/admin/abuse/freeze  (ADMIN)  — tüneli dondur/çöz

// reportAbuse, ziyaretçilerin bir hostname'i kötüye kullanım olarak bildirmesi
// için PUBLIC uç. Kimlik doğrulama yok; main.go'da IP başına hız sınırlı.
func (s *Server) reportAbuse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FQDN   string `json:"fqdn"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	fqdn := strings.ToLower(strings.TrimSpace(body.FQDN))
	if fqdn == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "fqdn zorunlu")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > 2000 {
		reason = reason[:2000]
	}
	rep, err := s.Store.CreateAbuseReport(r.Context(), fqdn, reason, clientIP(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.Logger.Warn("kotuye kullanim bildirimi alindi", "fqdn", fqdn, "id", rep.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "id": rep.ID})
}

// autoScanHostname, yeni oluşturulan bir hostname'i sezgisel phishing tarayıcısından
// geçirir; şüpheli bulursa admin incelemesi için bir abuse raporu (bildiren=auto-scan)
// oluşturur. NON-BLOCKING: hostname oluşturmayı engellemez, yalnızca işaretler
// (yanlış-pozitif bir adın kilitlenmesini önlemek için karar admin'e bırakılır).
// Hata durumunda sessizce geçer — tarama, ana akışı asla bozmamalı.
func (s *Server) autoScanHostname(ctx context.Context, fqdn string) {
	res := abuse.ScanFQDN(fqdn, s.PlatformDomain)
	if !res.Suspicious {
		return
	}
	s.Logger.Warn("otomatik tarama supheli hostname isaretledi",
		"fqdn", fqdn, "score", res.Score, "reason", res.Reason)
	if _, err := s.Store.CreateAbuseReport(ctx, strings.ToLower(fqdn), res.Reason, "auto-scan"); err != nil {
		s.Logger.Error("otomatik tarama raporu olusturulamadi", "fqdn", fqdn, "err", err)
	}
}

// adminListAbuseReports, en yeni kötüye kullanım bildirimlerini döner (ADMIN).
func (s *Server) adminListAbuseReports(w http.ResponseWriter, r *http.Request) {
	// Platform geneli: tum kiracilarin raporlari / herhangi bir tunelin
	// dondurulmasi. Onceden giris yapmis HER kullaniciya acikti.
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca platform admin yetkisiyle kullanilabilir")
		return
	}
	reps, err := s.Store.ListAbuseReports(r.Context(), 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	if reps == nil {
		reps = []store.AbuseReport{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": reps, "count": len(reps)})
}

// adminFreeze, bir tüneli fqdn veya tunnel_id ile dondurur/çözer (ADMIN).
func (s *Server) adminFreeze(w http.ResponseWriter, r *http.Request) {
	// Platform geneli: tum kiracilarin raporlari / herhangi bir tunelin
	// dondurulmasi. Onceden giris yapmis HER kullaniciya acikti.
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca platform admin yetkisiyle kullanilabilir")
		return
	}
	var body struct {
		FQDN     string `json:"fqdn"`
		TunnelID string `json:"tunnel_id"`
		Frozen   bool   `json:"frozen"`
	}
	if !decode(w, r, &body) {
		return
	}
	var err error
	switch {
	case strings.TrimSpace(body.TunnelID) != "":
		err = s.Store.AdminSetTunnelFrozen(r.Context(), strings.TrimSpace(body.TunnelID), body.Frozen)
	case strings.TrimSpace(body.FQDN) != "":
		err = s.Store.AdminFreezeByFQDN(r.Context(), strings.ToLower(strings.TrimSpace(body.FQDN)), body.Frozen)
	default:
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "fqdn veya tunnel_id zorunlu")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	// Router snapshot'ını hemen tazele ki dondurma beklemeden etkin olsun.
	s.tunnelsChanged()
	target := strings.TrimSpace(body.FQDN)
	if target == "" {
		target = strings.TrimSpace(body.TunnelID)
	}
	action := "tunnel.freeze"
	if !body.Frozen {
		action = "tunnel.unfreeze"
	}
	s.audit(r, action, target, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "frozen": body.Frozen})
}
