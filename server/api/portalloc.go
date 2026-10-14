package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tkodcumpeg4/zorven/server/portpool"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Rezerve TCP/UDP port havuzu (Mod A, K3). Adres modeli (IP yuvasi, port)
// cifti; yapilandirma Server.PortPool (ZORVEN_TCP_* ortam degiskenleri).
// Bos ise eski davranis: tek yuva, 10000-10099, host = PlatformDomain.

// pool, etkin havuz yapilandirmasi (nil ise eski tek-yuvali davranis).
func (s *Server) pool() *portpool.Config {
	if s.PortPool != nil {
		return s.PortPool
	}
	return portpool.Legacy(s.PlatformDomain)
}

// portReserver, entitlements'in istege bagli port kotasi arayuzu. Acik
// surumde entitlements no-op oldugundan arayuz saglanmaz -> limitsiz.
type portReserver interface {
	CanReservePort(ctx context.Context, tenantID string) error
}

// checkPortQuota, kiracinin yeni bir rezerve port alabilmesini denetler.
func (s *Server) checkPortQuota(ctx context.Context, tenantID string) error {
	if pr, ok := s.Entitlements.(portReserver); ok && pr != nil {
		return pr.CanReservePort(ctx, tenantID)
	}
	return nil
}

// fillPublicHost, tunelin genel host adini doldurur (tunel adresi = host:port).
func (s *Server) fillPublicHost(t *store.Tunnel) {
	if t.PublicPort > 0 {
		t.PublicHost = s.pool().Host(t.PublicIP)
	} else {
		t.PublicHost = ""
	}
}

func (s *Server) fillPublicHosts(list []store.Tunnel) {
	for i := range list {
		s.fillPublicHost(&list[i])
	}
}

// portState, kullanimdaki ve karantinadaki (IP, port) kumelerini okur.
func (s *Server) portState(ctx context.Context) (used, quarantined map[portpool.Addr]bool, err error) {
	used = map[portpool.Addr]bool{}
	quarantined = map[portpool.Addr]bool{}
	list, err := s.Store.ListReservedPortTunnels(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, tn := range list {
		if tn.PublicPort > 0 {
			used[portpool.Addr{Key: tn.PublicIP, Port: tn.PublicPort}] = true
		}
	}
	if q := s.pool().Quarantine; q > 0 {
		rel, err := s.Store.ListReleasedPorts(ctx, time.Now().Add(-q))
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rel {
			quarantined[portpool.Addr{Key: r.IP, Port: r.Port}] = true
		}
	}
	return used, quarantined, nil
}

// allocateTunnelPort, havuzdan bos ve karantinada olmayan bir (IP, port)
// secip tunele yazar. Ilk dolu yuva birincil; sonra siradaki IP.
func (s *Server) allocateTunnelPort(ctx context.Context, tenantID, id string) (portpool.Addr, error) {
	s.portMu.Lock()
	defer s.portMu.Unlock()

	cfg := s.pool()
	used, quarantined, err := s.portState(ctx)
	if err != nil {
		return portpool.Addr{}, err
	}
	skip := map[portpool.Addr]bool{}
	for {
		a, ok := cfg.Pick(used, quarantined, skip)
		if !ok {
			s.Logger.Error("rezerve port havuzu DOLU", "kapasite", cfg.Capacity(),
				"karantinada", len(quarantined), "ipucu", "ZORVEN_TCP_BIND_IPS ile IP ekleyin veya ZORVEN_TCP_PORT_MAX'i artirin")
			return portpool.Addr{}, fmt.Errorf("bos rezerve port kalmadi (kapasite %d, karantinada %d)",
				cfg.Capacity(), len(quarantined))
		}
		if err := s.Store.SetTunnelAddr(ctx, tenantID, id, a.Key, a.Port); err != nil {
			// Yaris (unique ihlali) olabilir; sonraki adresi dene.
			skip[a] = true
			continue
		}
		used[a] = true
		s.warnPoolLow(cfg.Stats(used, quarantined))
		return a, nil
	}
}

// warnPoolLow, bos adres sayisi %10'un (en az 5) altina inince operatore uyari yazar.
func (s *Server) warnPoolLow(st portpool.Stats) {
	threshold := st.Capacity / 10
	if threshold < 5 {
		threshold = 5
	}
	if st.Free <= threshold {
		s.Logger.Warn("rezerve port havuzu dolmak uzere", "bos", st.Free, "kullanimda", st.Used,
			"karantinada", st.Quarantined, "kapasite", st.Capacity,
			"ipucu", "ZORVEN_TCP_BIND_IPS ile IP ekleyin veya ZORVEN_TCP_PORT_MAX'i artirin")
	}
}

// adminTCPPool, GET /api/v1/admin/tcp-pool — havuz doluluk ozeti (platform admin).
func (s *Server) adminTCPPool(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	used, quarantined, err := s.portState(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := s.pool()
	st := cfg.Stats(used, quarantined)
	writeJSON(w, http.StatusOK, map[string]any{
		"pool":               st,
		"port_min":           cfg.Min,
		"port_max":           cfg.Max,
		"quarantine_seconds": int(cfg.Quarantine / time.Second),
	})
}
