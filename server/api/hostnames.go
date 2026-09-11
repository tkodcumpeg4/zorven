package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/domain"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// labelRe, tek bir DNS etiketi: harf/rakamla baslar ve biter, arada tire olur.
// RFC 1123 geregi en fazla 63 karakter.
var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validateLabel, kullanicinin sectigi kisa adi dogrular.
//
// "--" YASAK: kiraci ayraci odur (<tunel>--<kiraci>). Serbest birakilirsa bir
// kullanici "api--baskakiraci" alip baska kiracinin kapsamli adini TAKLIT
// edebilirdi.
func validateLabel(label string) error {
	if !labelRe.MatchString(label) {
		return errors.New("ad yalnizca kucuk harf, rakam ve tire icerebilir; " +
			"harf veya rakamla baslayip bitmeli (en fazla 63 karakter)")
	}
	if strings.Contains(label, "--") {
		return errors.New("ad ardisik tire (--) iceremez; " +
			"bu ayrac kiraci adlari icin ayrilmistir")
	}
	return nil
}

// scopedFQDN, kiraci kapsamli adi uretir: <tunel>--<kiraci>.<platform>
//
// TEK ETIKET olmasi kritik: "api.acme.rpshell.app" gibi noktali bir bicim
// kiraci basina AYRI wildcard sertifika gerektirir ve Let's Encrypt'in
// kayitli-domain basina haftalik sinirina takilir. "--" ayraciyla tek bir
// *.<platform> sertifikasi hepsini kapsar.
func scopedFQDN(tunnelName, tenantSlug, platform string) string {
	return tunnelName + "--" + tenantSlug + "." + platform
}

// --- hostnames -------------------------------------------------------------

func (s *Server) listHostnames(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	list, err := s.Store.ListHostnames(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Hostname{}
	}
	if limit, cursor, ok := parsePage(r); ok {
		page, next, more := paginate(list, func(h store.Hostname) string { return h.ID }, limit, cursor)
		setPageHeaders(w, r, next, more)
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	var body struct {
		TunnelID string `json:"tunnel_id"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields",
			"name zorunlu")
		return
	}
	// Platform domaini yoksa ad uretemeyiz: tek etiket kendi basina bir adres
	// degildir. Sessizce yanlis bir ad kaydetmektense acikca soyluyoruz.
	if s.PlatformDomain == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "no_platform_domain",
			"sunucuda subdomain tahsisi kapali (--platform-domain verilmemis)")
		return
	}

	name := strings.ToLower(strings.TrimSpace(body.Name))
	if err := validateLabel(name); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name", err.Error())
		return
	}

	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}

	reserved, err := s.Store.IsReservedName(r.Context(), name)
	if err != nil {
		s.fail(w, err)
		return
	}
	if reserved {
		writeJSONError(w, http.StatusConflict, "name_reserved",
			"bu ad platform icin ayrilmis")
		return
	}

	// Tunel belirtilmisse bu kiraciya ait mi?
	if body.TunnelID != "" {
		if _, err := s.Store.GetTunnel(r.Context(), tenantID, body.TunnelID); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "tunnel_not_found",
				"belirtilen tunnel_id bulunamadi")
			return
		}
	}

	fqdn := name + "." + s.PlatformDomain
	h, err := s.Store.AddHostname(r.Context(), tenantID, body.TunnelID, fqdn, store.HostTypeGlobal)
	if errors.Is(err, store.ErrHostnameTaken) {
		writeJSONError(w, http.StatusConflict, "hostname_taken", fqdn+" zaten alinmis")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	s.tunnelsChanged()
	writeJSON(w, http.StatusCreated, h)
}

type customHostnameRequest struct {
	TunnelID string `json:"tunnel_id"`
	FQDN     string `json:"fqdn"`
}

type customHostnameResponse struct {
	store.Hostname
	Instructions domain.VerificationInstructions `json:"instructions"`
}

func (s *Server) createCustomHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	var body customHostnameRequest
	if !decode(w, r, &body) {
		return
	}
	if body.FQDN == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields",
			"fqdn zorunlu")
		return
	}

	fqdn := strings.ToLower(strings.TrimSpace(body.FQDN))
	if err := domain.ValidateDomain(fqdn, s.PlatformDomain); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_domain", err.Error())
		return
	}

	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}

	if s.Entitlements != nil {
		if err := s.Entitlements.CanAddCustomDomain(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	// Tunel belirtilmisse bu kiraciya ait mi?
	if body.TunnelID != "" {
		if _, err := s.Store.GetTunnel(r.Context(), tenantID, body.TunnelID); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "tunnel_not_found",
				"belirtilen tunnel_id bulunamadi")
			return
		}
	}

	verifyToken := "rpsh-verify-" + randomHex(8)
	h, err := s.Store.AddCustomHostname(r.Context(), tenantID, body.TunnelID, fqdn, verifyToken)
	if errors.Is(err, store.ErrHostnameTaken) {
		writeJSONError(w, http.StatusConflict, "hostname_taken", fqdn+" zaten alinmis")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	resp := customHostnameResponse{
		Hostname:     h,
		Instructions: domain.GetInstructions(h.FQDN, h.VerifyToken, s.PlatformDomain),
	}
	writeJSON(w, http.StatusCreated, resp)
}

type patchHostnameRequest struct {
	TunnelID *string `json:"tunnel_id"`
}

func (s *Server) patchHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	var body patchHostnameRequest
	if !decode(w, r, &body) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_id", "hostname id eksik")
		return
	}

	if body.TunnelID == nil || *body.TunnelID == "" {
		// Detach
		if err := s.Store.DetachHostname(r.Context(), tenantID, id); err != nil {
			s.fail(w, err)
			return
		}
	} else {
		// Attach - check tunnel exists and belongs to tenant
		if _, err := s.Store.GetTunnel(r.Context(), tenantID, *body.TunnelID); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "tunnel_not_found", "belirtilen tunnel_id bulunamadi")
			return
		}
		if err := s.Store.AttachHostname(r.Context(), tenantID, id, *body.TunnelID); err != nil {
			s.fail(w, err)
			return
		}
	}

	s.tunnelsChanged()
	h, err := s.Store.GetHostnameByID(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) verifyHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}

	id := r.PathValue("id")
	h, err := s.Store.GetHostnameByID(r.Context(), tenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "alan adi bulunamadi")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	if h.Type != store.HostTypeCustom {
		writeJSONError(w, http.StatusBadRequest, "not_custom_domain",
			"yalnizca ozel alan adlari dogrulanabilir")
		return
	}

	if h.Verified {
		writeJSON(w, http.StatusOK, h)
		return
	}

	verifier := s.DNSVerifier
	if verifier == nil {
		verifier = domain.NewNetDNSVerifier()
	}

	if err := domain.VerifyCustomDomain(r.Context(), h.FQDN, h.VerifyToken, s.PlatformDomain, verifier); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "verification_failed", err.Error())
		return
	}

	verifiedH, err := s.Store.VerifyHostname(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}

	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, verifiedH)
}

func (s *Server) deleteHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if err := s.Store.DeleteHostname(r.Context(), tenantID, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	w.WriteHeader(http.StatusNoContent)
}

// withHostnames, tunellere adlarini ekler.
//
// Adlar tunel satirinda tutulmaz; yonetim uclarinda cevabi zenginlestirmek
// icin ayrica okunur. Tek sorgu + gruplama: tunel basina sorgu (N+1) degil.
func (s *Server) withHostnames(r *http.Request, tenantID string, tunnels []store.Tunnel) ([]store.Tunnel, error) {
	if len(tunnels) == 0 {
		return tunnels, nil
	}
	names, err := s.Store.ListHostnames(r.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	byTunnel := make(map[string][]store.Hostname, len(tunnels))
	for _, n := range names {
		byTunnel[n.TunnelID] = append(byTunnel[n.TunnelID], n)
	}
	for i := range tunnels {
		tunnels[i].Hostnames = byTunnel[tunnels[i].ID]
	}
	return tunnels, nil
}
