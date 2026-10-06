package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F06): Secret Vault REST uclari. Deger yalnizca olusturma yanitinda BIR KEZ
// doner; liste/GET deger DONDURMEZ. Pro+ plan gerektirir.

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	projID, _ := s.projectFor(r)

	secrets, err := s.Store.ListSecrets(r.Context(), tenantID, projID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secrets": secrets,
		"count":   len(secrets),
	})
}

func (s *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Secret YAZMA owner/admin; okuma (yalniz adlar) member'a acik.
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureSecretVault); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	projID, _ := s.projectFor(r)

	var body struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "secret adı gerekli")
		return
	}
	if body.Value == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "secret değeri gerekli")
		return
	}

	sec, err := s.Store.CreateSecret(r.Context(), tenantID, projID, body.Name, body.Value)
	if err != nil {
		if errors.Is(err, store.ErrSecretKeyMissing) {
			writeJSONError(w, http.StatusServiceUnavailable, "secret_key_missing",
				"sunucuda secret şifreleme anahtarı yapılandırılmamış (ZORVEN_SECRET_KEY)")
			return
		}
		if errors.Is(err, store.ErrSecretNameTaken) {
			writeJSONError(w, http.StatusConflict, "secret_name_taken", "bu isimde bir secret zaten var")
			return
		}
		s.fail(w, err)
		return
	}

	s.audit(r, "secret.create", sec.ID, sec.Name)
	// Policy snapshot'i {{secret:ad}} referanslarini DERLEME zamaninda cozer;
	// secret degisince snapshot tazelenmezse eski deger kullanilmaya devam eder.
	s.tunnelsChanged()
	writeJSON(w, http.StatusCreated, map[string]any{
		"secret": sec,
		"value":  sec.Value, // yalnizca burada, bir kez
	})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "secret kimliği gerekli")
		return
	}
	if err := s.Store.DeleteSecret(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "secret.delete", id, "")
	s.tunnelsChanged()
	w.WriteHeader(http.StatusNoContent)
}
