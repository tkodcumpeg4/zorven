package api

// FAZ 3 / F14 — Cihaz (device) goruntusu.
//
//	GET /api/v1/devices        — cihazlari listele
//	GET /api/v1/devices/{id}   — tek cihazin detayi (tunelleri dahil)
//
// "Cihaz", istemci kaydinin kalici cihaz bilgileriyle (hostname, OS, IP,
// en son metrikler) ve CANLI baglanti durumuyla birlestirilmis halidir.
// Istemci uclari (/clients) kaldirilmadi: mevcut entegrasyonlar bozulmasin.

import (
	"net/http"
	"sort"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Device, panelin cihaz ekraninin beklediği goruntu.
type Device struct {
	store.Client
	// Tunnels yalnizca TEK cihaz detayinda doldurulur; listede N+1 sorgu
	// yapmamak icin bos birakilir.
	Tunnels []store.Tunnel `json:"tunnels,omitempty"`
}

// deviceOut, istemci kaydini cihaz goruntusune cevirir.
//
// Canli oturum varsa surum oradan gelir; yoksa en son BILINEN surum
// (agent_version) gosterilir. Ayni sey metrikler icin de gecerli: cihaz
// offline iken son bilinen metrikler gosterilir ama "canli" gibi sunulmaz.
func deviceOut(c store.Client) Device {
	if c.Version == "" {
		c.Version = c.AgentVersion
	}
	return Device{Client: c}
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	projID, _ := s.projectFor(r)

	var list []store.Client
	var err error
	if projID != "" {
		list, err = s.Store.ListClientsByProject(r.Context(), tenantID, projID)
	} else {
		list, err = s.Store.ListClients(r.Context(), tenantID)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	// F16: erisilemeyen cihazlar listede HIC gorunmez.
	if list, err = s.filterClientsForCaller(r, tenantID, list); err != nil {
		s.fail(w, err)
		return
	}

	out := make([]Device, 0, len(list))
	for _, c := range list {
		out = append(out, deviceOut(s.enrich(c)))
	}
	// Once online cihazlar, sonra en son gorulene gore: aranan cihaz
	// genellikle su an bagli olandir.
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := out[i].Status == "online", out[j].Status == "online"
		if oi != oj {
			return oi
		}
		switch {
		case out[i].LastSeenAt == nil && out[j].LastSeenAt == nil:
			return out[i].Name < out[j].Name
		case out[i].LastSeenAt == nil:
			return false
		case out[j].LastSeenAt == nil:
			return true
		default:
			return out[i].LastSeenAt.After(*out[j].LastSeenAt)
		}
	})

	if limit, cursor, ok := parsePage(r); ok {
		page, next, more := paginate(out, func(d Device) string { return d.ID }, limit, cursor)
		setPageHeaders(w, r, next, more)
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	c, err := s.Store.GetClient(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !s.requireDeviceAccess(w, r, tenantID, c.ID) {
		return
	}
	d := deviceOut(s.enrich(c))

	// Tunel listesi YALNIZCA detayda: listede her cihaz icin sorgu yapmak
	// N+1 olurdu.
	tuns, err := s.Store.ListTunnelsByClient(r.Context(), c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range tuns {
		// Adlar ayri tabloda; detay ekraninda gorunmeleri gerekiyor.
		if names, err := s.Store.ListHostnamesByTunnel(r.Context(), tenantID, tuns[i].ID); err == nil {
			tuns[i].Hostnames = names
		}
	}
	d.Tunnels = tuns
	writeJSON(w, http.StatusOK, d)
}
