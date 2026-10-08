package api

// Web ile kapi acma (web door) - ham TCP/UDP tunelleri.
//
//	GET    /api/v1/tunnels/{id}/door                  - ayar + kapi adresi
//	PUT    /api/v1/tunnels/{id}/door                  - ac/kapat + sure
//	GET    /api/v1/tunnels/{id}/door/grants           - aktif IP izinleri
//	DELETE /api/v1/tunnels/{id}/door/grants/{grantId} - izni iptal et
//
// Kapi adresi: https://door--<ad>--<kiraci>.<platform>/ (platform altinda TEK etiket,
// *.<platform> wildcard sertifikasiyla uyumlu). Giris yontemi tunelin mevcut erisim
// politikasidir (basic | oauth).

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// doorTunnelFor, istekteki tuneli yukler; ham (tcp/udp, private olmayan) degilse 409 yazar.
func (s *Server) doorTunnelFor(w http.ResponseWriter, r *http.Request) (string, store.Tunnel, bool) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return "", store.Tunnel{}, false
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return "", store.Tunnel{}, false
	}
	if (tun.Proto != store.ProtoTCP && tun.Proto != store.ProtoUDP) || tun.Exposure == store.ExposurePrivate {
		writeJSONError(w, http.StatusConflict, "not_raw", "web ile kapı açma yalnızca TCP/UDP tünelleri içindir")
		return "", store.Tunnel{}, false
	}
	return tenantID, tun, true
}

// doorHostLabelOK, "door--" ile birlesince tek DNS etiketi (<=63) kalan etiket mi.
func doorHostLabelOK(label string) bool {
	return label != "" && !strings.Contains(label, ".") && len("door--"+label) <= 63
}

// deriveDoorHost, tunel icin kapi hostname'ini uretir:
//
//	door--<tunel-hostname-etiketi>.<platform>   (ornek: door--ssh--acme.zorven.app)
//
// Tunelin platform altinda adi yoksa (NoDomain) tunel kimliginden turetilir.
// Hostname'de "--" iceren bir kullanici adi olamayacagi icin (validateLabel) ve
// kapi adlari her zaman "door--" ile basladigindan gercek tunel adlariyla cakismaz.
func deriveDoorHost(hostnames []store.Hostname, tunnelID, platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return ""
	}
	suffix := "." + platform
	for _, h := range hostnames {
		fq := strings.ToLower(h.FQDN)
		if label, ok := strings.CutSuffix(fq, suffix); ok && doorHostLabelOK(label) {
			return "door--" + label + suffix
		}
	}
	var b strings.Builder
	for _, c := range strings.ToLower(tunnelID) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	id := b.String()
	if id == "" {
		return ""
	}
	return "door--id--" + id + suffix
}

// tunnelConnectAddress, tunelin ham baglanti adresi (host:port); belirsizse bos.
func (s *Server) tunnelConnectAddress(tun store.Tunnel, hostnames []store.Hostname) string {
	if tun.Exposure == store.ExposureSNI {
		for _, h := range hostnames {
			return h.FQDN + ":443"
		}
		return ""
	}
	if tun.PublicPort > 0 && s.PlatformDomain != "" {
		return s.PlatformDomain + ":" + strconv.Itoa(tun.PublicPort)
	}
	return ""
}

// doorView, GET/PUT yanitini kurar.
func (s *Server) doorView(r *http.Request, tenantID string, tun store.Tunnel, d store.TunnelDoor) (map[string]any, error) {
	hostnames, err := s.Store.ListHostnamesByTunnel(r.Context(), tenantID, tun.ID)
	if err != nil {
		return nil, err
	}
	pol, err := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, tun.ID)
	if err != nil {
		return nil, err
	}
	host := d.Host
	if host == "" {
		host = deriveDoorHost(hostnames, tun.ID, s.PlatformDomain)
	}
	url := ""
	if host != "" {
		url = "https://" + host + "/"
	}
	available := true
	if s.Entitlements != nil {
		available = s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureWebDoor) == nil
	}
	return map[string]any{
		"tunnel_id":       tun.ID,
		"enabled":         d.Enabled && available,
		"plan_closed":     d.PlanClosed,
		"duration_sec":    d.DurationSec,
		"durations":       store.DoorDurations,
		"available":       available,
		"host":            host,
		"url":             url,
		"connect_address": s.tunnelConnectAddress(tun, hostnames),
		"proto":           tun.Proto,
		"exposure":        tun.Exposure,
		"access_mode":     pol.Mode,
		"access_ready":    pol.Enabled && (pol.Mode == "basic" || pol.Mode == "oauth"),
	}, nil
}

func (s *Server) getTunnelDoor(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, tun, ok := s.doorTunnelFor(w, r)
	if !ok {
		return
	}
	d, err := s.Store.GetTunnelDoor(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	view, err := s.doorView(r, tenantID, tun, d)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) setTunnelDoor(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, tun, ok := s.doorTunnelFor(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled     bool `json:"enabled"`
		DurationSec int  `json:"duration_sec"`
	}
	if !decode(w, r, &body) {
		return
	}
	cur, err := s.Store.GetTunnelDoor(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.DurationSec == 0 {
		body.DurationSec = cur.DurationSec
	}
	if !store.ValidDoorDuration(body.DurationSec) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_duration",
			"duration_sec 3600, 43200, 86400 veya 604800 olmalı")
		return
	}

	next := store.TunnelDoor{TunnelID: tun.ID, Enabled: body.Enabled, DurationSec: body.DurationSec, Host: cur.Host}
	if body.Enabled {
		// Plan kapisi (acikta her zaman acik); kapatmak her zaman serbesttir.
		if s.Entitlements != nil {
			if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureWebDoor); err != nil {
				writeEntitlementError(w, err)
				return
			}
		}
		if s.PlatformDomain == "" {
			writeJSONError(w, http.StatusConflict, "door_unavailable", "bu sunucuda platform alan adı tanımlı değil")
			return
		}
		pol, err := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, tun.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !pol.Enabled || (pol.Mode != "basic" && pol.Mode != "oauth") {
			writeJSONError(w, http.StatusUnprocessableEntity, "access_not_configured",
				"kapı için önce giriş yöntemi (kullanıcı adı/parola veya OAuth) ayarlanmalı")
			return
		}
		hostnames, err := s.Store.ListHostnamesByTunnel(r.Context(), tenantID, tun.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if next.Host = deriveDoorHost(hostnames, tun.ID, s.PlatformDomain); next.Host == "" {
			writeJSONError(w, http.StatusConflict, "door_unavailable", "kapı adresi üretilemedi")
			return
		}
	}
	if err := s.Store.SetTunnelDoor(r.Context(), tenantID, next); err != nil {
		s.fail(w, err)
		return
	}
	if s.Door != nil {
		s.Door.Changed(tun.ID)
	}
	s.tunnelsChanged() // router kapi hostname'ini ekler/kaldirir
	s.audit(r, "tunnel.door", tun.ID, "enabled="+strconv.FormatBool(body.Enabled)+" duration="+strconv.Itoa(body.DurationSec))

	d, err := s.Store.GetTunnelDoor(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	view, err := s.doorView(r, tenantID, tun, d)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listDoorGrants(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, tun, ok := s.doorTunnelFor(w, r)
	if !ok {
		return
	}
	grants, err := s.Store.ListDoorGrants(r.Context(), tenantID, tun.ID, true, 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grants)
}

func (s *Server) revokeDoorGrant(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, tun, ok := s.doorTunnelFor(w, r)
	if !ok {
		return
	}
	grantID := r.PathValue("grantId")
	var err error
	if s.Door != nil {
		_, err = s.Door.Revoke(r.Context(), tenantID, tun.ID, grantID)
	} else {
		_, err = s.Store.RevokeDoorGrant(r.Context(), tenantID, tun.ID, grantID)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "aktif kapı izni bulunamadı")
			return
		}
		s.fail(w, err)
		return
	}
	s.audit(r, "tunnel.door.revoke", tun.ID, grantID)
	w.WriteHeader(http.StatusNoContent)
}
