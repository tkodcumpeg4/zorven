package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
)

// listAPITokens, kiraciya ait programatik REST API tokenlarini listeler.
func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	tokens, err := s.Store.ListAPITokens(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tokens": tokens,
		"count":  len(tokens),
	})
}

// createAPIToken, kiraci icin yeni bir REST API erisim anahtari uretir.
func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	// Plan yetki kontrolu (Pro, Team, Enterprise)
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureAPIAccess); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	var body struct {
		Name      string    `json:"name"`
		Scopes    []string  `json:"scopes"`
		ExpiresIn *int      `json:"expires_in_days"` // gun cinsinden, null = suresiz
	}
	if !decode(w, r, &body) {
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "API Key"
	}

	scopes := body.Scopes
	if len(scopes) == 0 {
		scopes = []string{"read", "write"}
	}

	var expiresAt *time.Time
	if body.ExpiresIn != nil && *body.ExpiresIn > 0 {
		exp := time.Now().UTC().AddDate(0, 0, *body.ExpiresIn)
		expiresAt = &exp
	}

	full, tokenID, tokenHash, err := auth.GenerateAPIKey()
	if err != nil {
		s.fail(w, err)
		return
	}

	var userID *string
	if u, ok := userFromContext(r.Context()); ok && u != nil && u.ID != "" {
		userID = &u.ID
	}

	tok, err := s.Store.CreateAPIToken(r.Context(), tenantID, userID, name, tokenID, tokenHash, auth.PrefixAPI, scopes, expiresAt)
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     full,
		"api_token": tok,
	})
}

// revokeAPIToken, API tokenini iptal eder.
func (s *Server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "token kimligi gerekli")
		return
	}

	if err := s.Store.RevokeAPIToken(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
