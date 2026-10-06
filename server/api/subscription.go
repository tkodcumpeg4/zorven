package api

import (
	"net/http"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// getSubscription, GET /api/v1/subscription
// Kiracinin mevcut abonelik planini, kaynak limitlerini ve anlik kullanimini doner.
func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	sub, err := s.Store.GetSubscription(r.Context(), tenantID)
	if err != nil {
		s.logger().Error("abonelik bilgisi alinamadi", "tenant_id", tenantID, "hata", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Abonelik bilgisi alinamadi")
		return
	}

	usage, err := s.Store.GetTenantUsage(r.Context(), tenantID)
	if err != nil {
		s.logger().Error("kullanim bilgisi alinamadi", "tenant_id", tenantID, "hata", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Kullanim bilgisi alinamadi")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"subscription": sub,
		"usage":        usage,
	})
}

// planChanged, plan degisiminin calisma zamani etkilerini HEMEN uygular:
// bant genisligi kota/hiz durumu yeniden yuklenir ve ingress router'i
// (HostRoute.Plan -> interstitial karari) tazelenir.
func (s *Server) planChanged(tenantID string) {
	if s.OnPlanChange != nil {
		s.OnPlanChange(tenantID)
	}
	s.tunnelsChanged()
}

// listPlans, GET /api/v1/plans
// Sistemde tanimli tum planlari (fiyatlar, limitler, ozellikler) sirali olarak doner.
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.Store.ListPlans(r.Context())
	if err != nil {
		s.logger().Error("planlar listelenemedi", "hata", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Planlar listelenemedi")
		return
	}

	if plans == nil {
		plans = []store.Plan{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"plans": plans,
	})
}
