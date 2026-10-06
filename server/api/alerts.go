package api

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.4 — metrik uyarı uçları.
//
//	GET /api/v1/tunnels/{id}/alert  — uyarı yapılandırması + durumu
//	PUT /api/v1/tunnels/{id}/alert  — kaydet {enabled, error_rate_pct, window_min, min_requests, notify_email}

func alertOut(a store.TunnelAlert) map[string]any {
	return map[string]any{
		"tunnel_id":      a.TunnelID,
		"enabled":        a.Enabled,
		"error_rate_pct": a.ErrorRatePct,
		"window_min":     a.WindowMin,
		"min_requests":   a.MinRequests,
		"notify_email":   a.NotifyEmail,
		"state":          a.State,
		"last_changed_at": func() any {
			if a.LastChangedAt != nil {
				return a.LastChangedAt
			}
			return nil
		}(),
	}
}

func (s *Server) getTunnelAlert(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	a, err := s.Store.GetTunnelAlert(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alertOut(a))
}

func (s *Server) setTunnelAlert(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	var body struct {
		Enabled      bool   `json:"enabled"`
		ErrorRatePct int    `json:"error_rate_pct"`
		WindowMin    int    `json:"window_min"`
		MinRequests  int    `json:"min_requests"`
		NotifyEmail  string `json:"notify_email"`
	}
	if !decode(w, r, &body) {
		return
	}
	// 0 = varsayilan (panel bos alani 0 gonderir; store 10/5/0'a tamamlar).
	// Acikca aralik disi deger ise SESSIZCE degistirmek yerine reddet: kullanici
	// %500 yazip %10'luk bir uyari kurdugunu sanmasin.
	switch {
	case body.ErrorRatePct < 0 || body.ErrorRatePct > 100:
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_error_rate", "error_rate_pct 1-100 arasinda olmali")
		return
	case body.WindowMin < 0 || body.WindowMin > 1440:
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_window", "window_min 1-1440 dakika arasinda olmali")
		return
	case body.MinRequests < 0:
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_min_requests", "min_requests negatif olamaz")
		return
	}
	email := strings.TrimSpace(body.NotifyEmail)
	if body.Enabled && email != "" && !strings.Contains(email, "@") {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_email", "gecerli bir e-posta adresi girin")
		return
	}
	if err := s.Store.SetTunnelAlert(r.Context(), tenantID, store.TunnelAlert{
		TunnelID:     r.PathValue("id"),
		Enabled:      body.Enabled,
		ErrorRatePct: body.ErrorRatePct,
		WindowMin:    body.WindowMin,
		MinRequests:  body.MinRequests,
		NotifyEmail:  email,
	}); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}
