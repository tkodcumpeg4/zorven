package api

// FAZ 3 / F15 — Uzaktan ajan yapilandirmasi.
//
//	GET /api/v1/devices/{id}/config  — kayitli ayarlar
//	PUT /api/v1/devices/{id}/config  — ayarlari yaz ve cihaza it
//
// GUVENLIK NOTU: burada yazilan "allow_terminal: true" bir GARANTI DEGILDIR.
// Ajan bunu yerel bayraklariyla kesistirir; yerelde kapatilmis bir izin
// uzaktan acilamaz (bkz. client/agent/settings.go). Panelde de boyle anlatilir.

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ayar sinirlari. Uc degerler ajani ya bosuna mesgul eder (cok sik metrik)
// ya da fiilen kullanilamaz hale getirir (cok uzun backoff).
const (
	minMetricsIntervalSec = 5
	maxMetricsIntervalSec = 3600
	minBackoffSec         = 1
	maxBackoffSec         = 3600
)

func validLogLevel(s string) bool {
	switch s {
	case "", "debug", "info", "warn", "error":
		return true
	}
	return false
}

// validateAgentSettings, kabul edilebilir bir ayar seti mi. Bos dizge = hata yok.
func validateAgentSettings(s protocol.AgentSettings) string {
	if !validLogLevel(s.LogLevel) {
		return "log_level yalnizca debug, info, warn veya error olabilir"
	}
	if s.MetricsIntervalSec != 0 &&
		(s.MetricsIntervalSec < minMetricsIntervalSec || s.MetricsIntervalSec > maxMetricsIntervalSec) {
		return "metrics_interval_sec 5 ile 3600 saniye arasinda olmali"
	}
	if s.ReconnectMaxBackoffSec != 0 &&
		(s.ReconnectMaxBackoffSec < minBackoffSec || s.ReconnectMaxBackoffSec > maxBackoffSec) {
		return "reconnect_max_backoff_sec 1 ile 3600 saniye arasinda olmali"
	}
	return ""
}

func (s *Server) deviceConfigGate(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return "", false
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureAgentConfig); err != nil {
			writeEntitlementError(w, err)
			return "", false
		}
	}
	return tenantID, true
}

func (s *Server) getDeviceConfig(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
	tenantID, ok := s.deviceConfigGate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	// Cihazin bu kiraciya ait oldugunu DOGRULA: yoksa baska kiracinin
	// ayarlari okunabilirdi.
	if _, err := s.Store.GetClient(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	if !s.requireDeviceAccess(w, r, tenantID, id) {
		return
	}
	cfg, err := s.Store.GetDeviceConfig(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if cfg == nil {
		// Ayar yok: bos bir set doner, 404 DEGIL. Panel formu doldurabilsin.
		cfg = &protocol.AgentSettings{}
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) setDeviceConfig(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsWrite) {
		return
	}
	tenantID, ok := s.deviceConfigGate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := s.Store.GetClient(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	if !s.requireDeviceAccess(w, r, tenantID, id) {
		return
	}
	var body protocol.AgentSettings
	if !decode(w, r, &body) {
		return
	}
	body.LogLevel = strings.ToLower(strings.TrimSpace(body.LogLevel))
	if verr := validateAgentSettings(body); verr != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_settings", verr)
		return
	}
	if err := s.Store.SetDeviceConfig(r.Context(), tenantID, id, body); err != nil {
		s.fail(w, err)
		return
	}
	// Cihaz bagliysa aninda it; degilse bir sonraki hello_ack'te gider.
	s.pushClientConfig(id)
	s.audit(r, "device_config.update", id, "")
	writeJSON(w, http.StatusOK, body)
}
