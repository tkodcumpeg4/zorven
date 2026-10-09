package api

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.6 — mTLS (istemci sertifikası) uçları.
//
//	GET /api/v1/tunnels/{id}/mtls  — mTLS yapılandırması
//	PUT /api/v1/tunnels/{id}/mtls  — kaydet {enabled, ca_pem}
//
// enabled=true iken bu tünelin hostname'ine TLS el sıkışmasında, ca_pem ile
// imzalanmış bir istemci sertifikası zorunlu tutulur.

func (s *Server) getTunnelMTLS(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	m, err := s.Store.GetTunnelMTLS(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	// ca_pem'in var olup olmadığını bildir ama tam içeriği yine de döndür (kiracının kendi verisi).
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel_id": m.TunnelID,
		"enabled":   m.Enabled,
		"ca_pem":    m.CAPem,
		"has_ca":    strings.TrimSpace(m.CAPem) != "",
	})
}

func (s *Server) setTunnelMTLS(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	var body struct {
		Enabled bool   `json:"enabled"`
		CAPem   string `json:"ca_pem"`
	}
	if !decode(w, r, &body) {
		return
	}
	caPem := strings.TrimSpace(body.CAPem)
	// Etkinken geçerli bir CA sertifikası PEM'i şart.
	if body.Enabled {
		if caPem == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "missing_ca", "mTLS için imzalayan CA sertifikası (PEM) zorunlu")
			return
		}
		if !validCAPem(caPem) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_ca", "geçerli bir CA sertifikası (PEM) girin")
			return
		}
	}
	if err := s.Store.SetTunnelMTLS(r.Context(), tenantID, store.TunnelMTLS{
		TunnelID: r.PathValue("id"), Enabled: body.Enabled, CAPem: caPem,
	}); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged() // router snapshot'ı + TLS mTLS haritası tazelensin
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}

// validCAPem, PEM içinde en az bir parse edilebilir sertifika olup olmadığını kontrol eder.
func validCAPem(pemStr string) bool {
	rest := []byte(pemStr)
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err == nil {
			found = true
		}
	}
	return found
}
