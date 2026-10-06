package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	projID, _ := s.projectFor(r)
	var list []store.Hostname
	var err error
	if projID != "" {
		list, err = s.Store.ListHostnamesByProject(r.Context(), tenantID, projID)
	} else {
		list, err = s.Store.ListHostnames(r.Context(), tenantID)
	}
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
	fqdn := name + "." + s.PlatformDomain
	// K1: reserved_names tablosu eksik kalsa bile platformun calisma zamanindaki
	// kendi hostlari (--control-host, analytics, ziyaretci callback'i) alinamaz.
	if !reserved && s.ReservedHost != nil && s.ReservedHost(fqdn) {
		reserved = true
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

	projID, _ := s.projectFor(r)
	h, err := s.Store.AddHostnameWithProject(r.Context(), tenantID, body.TunnelID, fqdn, store.HostTypeGlobal, projID)
	if errors.Is(err, store.ErrHostnameTaken) {
		writeJSONError(w, http.StatusConflict, "hostname_taken", fqdn+" zaten alinmis")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	s.tunnelsChanged()
	s.autoScanHostname(r.Context(), fqdn)
	writeJSON(w, http.StatusCreated, h)
}

type customHostnameRequest struct {
	TunnelID string `json:"tunnel_id"`
	FQDN     string `json:"fqdn"`
}

type customHostnameResponse struct {
	store.Hostname
	Instructions domain.VerificationInstructions `json:"instructions"`
	// AutoVerified, yeni FQDN'in ust zone'u ( or. example.net) bu kiracida zaten
	// dogrulanmis oldugu icin DNS dogrulamasi olmadan hemen verified edildiyse true.
	AutoVerified bool `json:"auto_verified,omitempty"`
	// ParentZone, AutoVerified true ise dogrulamayi devreden ust zone FQDN'i.
	ParentZone string `json:"parent_zone,omitempty"`
	// DNSPointed, AutoVerified iken adin zaten platforma yonlendirilip
	// yonlendirilmedigi. Sahiplik dogrulanmis olsa da CNAME kaydi yoksa trafik
	// gelmez; panel bu durumda yalnizca CNAME adimini gosterir.
	DNSPointed *bool `json:"dns_pointed,omitempty"`
}

// pushTunnelClient, bir hostname degisikliginden etkilenen tunelin istemcisine
// (ve replikalarina) guncel tunel+ad listesini iter. Tunel yoksa sessiz gecer.
func (s *Server) pushTunnelClient(ctx context.Context, tenantID, tunnelID string) {
	if tunnelID == "" {
		return
	}
	t, err := s.Store.GetTunnel(ctx, tenantID, tunnelID)
	if err != nil {
		return
	}
	s.pushClientConfig(t.ClientID)
	if reps, err := s.Store.ListTunnelReplicas(ctx, tenantID, tunnelID); err == nil {
		for _, c := range reps {
			s.pushClientConfig(c)
		}
	}
}

// dnsCheckHostname, POST /api/v1/hostnames/{id}/dns-check — adin platforma
// yonlendirilip yonlendirilmedigini canli DNS ile denetler.
func (s *Server) dnsCheckHostname(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	h, err := s.Store.GetHostnameByID(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	inst := domain.GetInstructions(h.FQDN, h.VerifyToken, s.PlatformDomain)
	writeJSON(w, http.StatusOK, map[string]any{
		"fqdn":         h.FQDN,
		"pointed":      domain.PointsToPlatform(ctx, nil, h.FQDN, s.PlatformDomain),
		"cname_target": inst.CNAMETarget,
	})
}

// verifiedParentZone, fqdn'in ust zone'u (or. api.example.net icin example.net) kiracida
// zaten dogrulanmis bir custom hostname mi diye bakar. Birden fazla ata varsa EN
// UZUN (en yakin) eslesmeyi doner. Eslesme yoksa "" doner.
func verifiedParentZone(hosts []store.Hostname, fqdn string) string {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	best := ""
	for _, h := range hosts {
		if h.Type != store.HostTypeCustom || !h.Verified {
			continue
		}
		zone := strings.ToLower(strings.TrimSuffix(h.FQDN, "."))
		if zone == "" || zone == fqdn {
			continue
		}
		if strings.HasSuffix(fqdn, "."+zone) && len(zone) > len(best) {
			best = zone
		}
	}
	return best
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

	// Ust zone otomatik dogrulama: kullanici example.net'yi bir kez dogruladiysa,
	// api.example.net gibi alt alan adlarini DNS dogrulamasi olmadan hemen aktif et.
	// Ust zone'un TXT/CNAME denetimi zaten alan adi sahipligini kanitliyor.
	parentZone := ""
	if existing, lerr := s.Store.ListHostnames(r.Context(), tenantID); lerr == nil {
		parentZone = verifiedParentZone(existing, fqdn)
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

	s.autoScanHostname(r.Context(), fqdn)

	if parentZone != "" {
		if vh, verr := s.Store.VerifyHostname(r.Context(), tenantID, h.ID); verr == nil {
			h = vh
			s.tunnelsChanged() // yeni verified route'u anlik snapshot'a al
			s.pushTunnelClient(r.Context(), tenantID, h.TunnelID)
			dctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			pointed := domain.PointsToPlatform(dctx, nil, h.FQDN, s.PlatformDomain)
			cancel()
			resp := customHostnameResponse{
				Hostname:     h,
				Instructions: domain.GetInstructions(h.FQDN, h.VerifyToken, s.PlatformDomain),
				AutoVerified: true,
				ParentZone:   parentZone,
				DNSPointed:   &pointed,
			}
			writeJSON(w, http.StatusCreated, resp)
			return
		}
		// Otomatik dogrulama basarisiz olursa normal DNS akisina dus.
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

	var oldTunnelID string
	if prev, perr := s.Store.GetHostnameByID(r.Context(), tenantID, id); perr == nil {
		oldTunnelID = prev.TunnelID
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
	s.pushTunnelClient(r.Context(), tenantID, h.TunnelID)
	if oldTunnelID != h.TunnelID {
		s.pushTunnelClient(r.Context(), tenantID, oldTunnelID)
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
	s.pushTunnelClient(r.Context(), tenantID, verifiedH.TunnelID)
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
	var tunnelID string
	if prev, perr := s.Store.GetHostnameByID(r.Context(), tenantID, r.PathValue("id")); perr == nil {
		tunnelID = prev.TunnelID
	}
	if err := s.Store.DeleteHostname(r.Context(), tenantID, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	s.pushTunnelClient(r.Context(), tenantID, tunnelID)
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
