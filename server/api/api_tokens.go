package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
)

// maxTokenLifetimeDays, expires_in_days ust siniri (~10 yil).
const maxTokenLifetimeDays = 3650

// knownTokenScopes, effectiveScopes'un anladigi kapsamlar. Bilinmeyen bir
// dizge sessizce kaydedilir ama hicbir yetki vermezdi (kullanici yazim
// hatasini fark etmezdi).
var knownTokenScopes = map[string]bool{
	"*": true, "all": true, "admin": true, "read": true, "write": true,
	ScopeClientsRead: true, ScopeClientsWrite: true,
	ScopeTunnelsRead: true, ScopeTunnelsWrite: true,
	ScopeHostnamesRead: true, ScopeHostnamesWrite: true,
	ScopeIPAllowRead: true, ScopeIPAllowWrite: true,
	ScopeAnalyticsRead: true,
}

// normalizeTokenScopes, kapsamlari kirpar/kucultur, tekrarlari atar. Bilinmeyen
// ilk kapsam ikinci donus degeridir ("" = hepsi gecerli).
func normalizeTokenScopes(in []string) ([]string, string) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, sc := range in {
		sc = strings.ToLower(strings.TrimSpace(sc))
		if sc == "" || seen[sc] {
			continue
		}
		if !knownTokenScopes[sc] {
			return nil, sc
		}
		seen[sc] = true
		out = append(out, sc)
	}
	return out, ""
}

// authorizeTokenChange, rotate/iptal yetkisi: kisiye bagli token'i yalnizca
// SAHIBI ya da owner/admin degistirebilir; servis hesabi (veya sahipsiz)
// token'lari organizasyona aittir, yalnizca owner/admin. Onceden kiracidaki
// her member baskasinin (owner dahil) token'ini rotate edip yeni sirri
// alabiliyordu. Reddedilirse yanit yazilir ve false doner.
func (s *Server) authorizeTokenChange(w http.ResponseWriter, r *http.Request, tenantID, id string) bool {
	tok, err := s.Store.GetAPIToken(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return false
	}
	if tok.UserID != nil {
		if u, ok := userFromContext(r.Context()); ok && u != nil && u.ID != "" && u.ID == *tok.UserID {
			return true
		}
	}
	return s.requirePrivileged(w, r, tenantID)
}

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
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresIn *int       `json:"expires_in_days"` // gun cinsinden, null = suresiz
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if !decode(w, r, &body) {
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "API Key"
	}

	scopes, bad := normalizeTokenScopes(body.Scopes)
	if bad != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_scope", "bilinmeyen kapsam: "+bad)
		return
	}
	if len(scopes) == 0 {
		scopes = []string{"read", "write"}
	}

	var expiresAt *time.Time
	if body.ExpiresAt != nil {
		// Gecmis tarih olusturulur olusturulmaz kullanilamayan bir token uretirdi.
		if !body.ExpiresAt.After(time.Now()) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_expires_at", "son kullanma tarihi gelecekte olmali")
			return
		}
		exp := body.ExpiresAt.UTC()
		expiresAt = &exp
	} else if body.ExpiresIn != nil {
		if *body.ExpiresIn < 0 || *body.ExpiresIn > maxTokenLifetimeDays {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_expires_in", "gecerlilik suresi 0-3650 gun arasinda olmali")
			return
		}
		if *body.ExpiresIn > 0 {
			exp := time.Now().UTC().AddDate(0, 0, *body.ExpiresIn)
			expiresAt = &exp
		}
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

	s.audit(r, "token.create", tok.ID, tok.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     full,
		"api_token": tok,
	})
}

// rotateAPIToken, API tokenini dondurur ve yeni gizli uretir (FAZ 0 / F0A).
func (s *Server) rotateAPIToken(w http.ResponseWriter, r *http.Request) {
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

	if !s.authorizeTokenChange(w, r, tenantID, id) {
		return
	}

	full, newTokenID, newTokenHash, err := auth.GenerateAPIKey()
	if err != nil {
		s.fail(w, err)
		return
	}

	tok, err := s.Store.RotateAPIToken(r.Context(), tenantID, id, newTokenID, newTokenHash)
	if err != nil {
		s.fail(w, err)
		return
	}

	s.audit(r, "token.rotate", tok.ID, tok.Name)
	writeJSON(w, http.StatusOK, map[string]any{
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

	if !s.authorizeTokenChange(w, r, tenantID, id) {
		return
	}

	if err := s.Store.RevokeAPIToken(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}

	s.audit(r, "token.revoke", id, "")
	w.WriteHeader(http.StatusNoContent)
}
