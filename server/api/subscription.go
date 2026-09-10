package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// getSubscription, GET /api/v1/subscription
// Kiracinin mevcut abonelik planini, kaynak limitlerini ve anlik kullanimini doner.
func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
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

// adminUpdateTenantPlan, PUT /api/v1/admin/tenants/{id}/plan
// Platform adminin bir kiracinin planini (free, pro, team, enterprise) degistirmesini saglar.
func (s *Server) adminUpdateTenantPlan(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "Bu islem platform admin yetkisi gerektirir")
		return
	}

	tenantID := r.PathValue("id")
	if tenantID == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "Kiraci kimligi gerekli")
		return
	}

	// Kiraciyi dogrula
	if _, err := s.Store.GetTenant(r.Context(), tenantID); err != nil {
		if errors.Is(err, store.ErrTenantNotFound) {
			writeJSONError(w, http.StatusNotFound, "tenant_not_found", "Kiraci bulunamadi")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Kiraci sorgulanamadi")
		return
	}

	var body struct {
		Plan             string     `json:"plan"`
		Status           string     `json:"status"`
		CurrentPeriodEnd *time.Time `json:"current_period_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "Gecersiz istek govdesi")
		return
	}

	switch body.Plan {
	case store.PlanFree, store.PlanHobby, store.PlanPro, store.PlanTeam, store.PlanEnterprise:
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_plan", "Gecersiz plan. Gecerli planlar: free, hobby, pro, team, enterprise")
		return
	}

	status := body.Status
	if status == "" {
		status = store.SubStatusActive
	}

	if err := s.Store.UpdateTenantPlan(r.Context(), tenantID, body.Plan, status, body.CurrentPeriodEnd); err != nil {
		s.logger().Error("kiraci plani guncellenemedi", "tenant_id", tenantID, "hata", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Plan guncellenemedi")
		return
	}

	sub, err := s.Store.GetSubscription(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "db_error", "Guncel abonelik alinamadi")
		return
	}

	writeJSON(w, http.StatusOK, sub)
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
