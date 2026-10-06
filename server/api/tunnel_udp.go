package api

// FAZ 4 / F24 — UDP ileri + oyun sunucusu durumu.
//
//	GET /api/v1/tunnels/{id}/udp          — sinirlar + canli istatistik + son 60 dk
//	PUT /api/v1/tunnels/{id}/udp          — sinirlari yaz (dinleyici aninda guncellenir)
//	GET /api/v1/tunnels/{id}/game-status  — Minecraft Java/Bedrock durum sorgusu

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/gameprobe"
	"github.com/tkodcumpeg4/zorven/server/rawproxy"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

const (
	udpMinIdleSec   = 10
	udpMaxIdleSec   = 3600
	udpMinPacket    = 64
	udpMaxPacket    = 65507
	udpMaxPPSLimit  = 1_000_000
	udpMaxFlowLimit = 100_000

	gameProbeTimeout  = 6 * time.Second
	gameProbeCacheTTL = 10 * time.Second
	udpSeriesWindow   = time.Hour
)

// validateTunnelUDP, UDP sinirlarini dogrular.
func validateTunnelUDP(u store.TunnelUDP) string {
	switch {
	case u.IdleTimeoutSec < udpMinIdleSec || u.IdleTimeoutSec > udpMaxIdleSec:
		return "boşta kalma süresi 10 ile 3600 saniye arasında olmalı"
	case u.MaxPacketBytes < udpMinPacket || u.MaxPacketBytes > udpMaxPacket:
		return "paket boyu sınırı 64 ile 65507 bayt arasında olmalı"
	case u.MaxPPS < 0 || u.MaxPPS > udpMaxPPSLimit:
		return "tünel paket/sn sınırı 0 ile 1000000 arasında olmalı (0 = sınırsız)"
	case u.MaxFlowPPS < 0 || u.MaxFlowPPS > udpMaxPPSLimit:
		return "bağlantı başına paket/sn sınırı 0 ile 1000000 arasında olmalı (0 = sınırsız)"
	case u.MaxFlows < 1 || u.MaxFlows > udpMaxFlowLimit:
		return "eş zamanlı bağlantı sınırı 1 ile 100000 arasında olmalı"
	}
	return ""
}

// udpTunnelFor, istekteki tuneli yukler; UDP degilse 409 yazar.
func (s *Server) udpTunnelFor(w http.ResponseWriter, r *http.Request) (string, store.Tunnel, bool) {
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
	if tun.Proto != store.ProtoUDP {
		writeJSONError(w, http.StatusConflict, "not_udp", "bu ayarlar yalnızca UDP tünelleri içindir")
		return "", store.Tunnel{}, false
	}
	return tenantID, tun, true
}

func (s *Server) getTunnelUDP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, tun, ok := s.udpTunnelFor(w, r)
	if !ok {
		return
	}
	cfg, err := s.Store.GetTunnelUDP(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	series, err := s.Store.ListUDPStats(r.Context(), tenantID, tun.ID, time.Now().Add(-udpSeriesWindow))
	if err != nil {
		s.fail(w, err)
		return
	}
	var live *rawproxy.UDPLiveStats
	if s.UDPStats != nil {
		if ls, ok := s.UDPStats(tun.ID); ok {
			live = &ls
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":   cfg,
		"defaults": store.DefaultTunnelUDP(tun.ID),
		"live":     live,
		"series":   series,
	})
}

func (s *Server) setTunnelUDP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, tun, ok := s.udpTunnelFor(w, r)
	if !ok {
		return
	}
	var body store.TunnelUDP
	if !decode(w, r, &body) {
		return
	}
	body.TunnelID = tun.ID
	body.TenantID = ""
	if verr := validateTunnelUDP(body); verr != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_udp", verr)
		return
	}
	// Varsayilandan farkli sinirlar Pro+. Varsayilana donmek her planda serbest.
	if body != store.DefaultTunnelUDP(tun.ID) && s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureUDPAdvanced); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	if err := s.Store.SetTunnelUDP(r.Context(), tenantID, body); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged() // rawproxy yenilenir; dinleyici yeni sinirlari aninda uygular
	s.audit(r, "tunnel.udp.update", tun.ID, "")
	writeJSON(w, http.StatusOK, body)
}

// --- Oyun sunucusu durumu -------------------------------------------------------

type gameProbeEntry struct {
	at     time.Time
	status gameprobe.Status
}

var (
	gameProbeMu    sync.Mutex
	gameProbeCache = map[string]gameProbeEntry{}
)

// streamConn, bir tunel akisini gameprobe.Conn'a uyarlar.
type streamConn struct{ st *tunnel.RawStream }

func (c streamConn) Write(ctx context.Context, b []byte) error { return c.st.Send(ctx, b) }

func (c streamConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-c.st.FromLocal():
		return b, nil
	case <-c.st.Closed():
		return nil, errors.New("akis kapandi")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) getGameStatus(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = gameprobe.KindJava
		if tun.Proto == store.ProtoUDP {
			kind = gameprobe.KindBedrock
		}
	}
	switch {
	case kind == gameprobe.KindJava && tun.Proto == store.ProtoTCP:
	case kind == gameprobe.KindBedrock && tun.Proto == store.ProtoUDP:
	default:
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_kind",
			"Minecraft Java TCP, Bedrock UDP tüneli gerektirir")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureUDPAdvanced); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	key := tun.ID + "|" + kind
	gameProbeMu.Lock()
	if e, ok := gameProbeCache[key]; ok && time.Since(e.at) < gameProbeCacheTTL {
		gameProbeMu.Unlock()
		writeJSON(w, http.StatusOK, e.status)
		return
	}
	gameProbeMu.Unlock()

	st := s.probeGame(r.Context(), tenantID, tun, kind)

	gameProbeMu.Lock()
	// Eski kayitlari da temizle (harita sinirsiz buyumesin).
	for k, e := range gameProbeCache {
		if time.Since(e.at) > time.Minute {
			delete(gameProbeCache, k)
		}
	}
	gameProbeCache[key] = gameProbeEntry{at: time.Now(), status: st}
	gameProbeMu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

// probeGame, tunelin cevrimici bir istemcisi uzerinden sorguyu calistirir.
func (s *Server) probeGame(parent context.Context, tenantID string, tun store.Tunnel, kind string) gameprobe.Status {
	down := gameprobe.Status{Kind: kind}
	candidates := []string{tun.ClientID}
	if reps, err := s.Store.ListTunnelReplicas(parent, tenantID, tun.ID); err == nil {
		candidates = append(candidates, reps...)
	}
	var sess *tunnel.Session
	for _, cid := range candidates {
		if ss, ok := s.Hub.Get(cid); ok {
			sess = ss
			break
		}
	}
	if sess == nil {
		down.Error = "tünel istemcisi çevrimdışı"
		return down
	}

	ctx, cancel := context.WithTimeout(parent, gameProbeTimeout)
	defer cancel()
	stream, err := sess.OpenStream(ctx, tun.ID, tun.Proto, "game-probe")
	if err != nil {
		down.Error = "akış açılamadı"
		return down
	}
	defer stream.Close("game-probe")
	if _, err := tunnel.WaitAccept(ctx, stream, gameProbeTimeout); err != nil {
		down.Error = "yerel sunucuya bağlanılamadı"
		return down
	}

	conn := streamConn{st: stream}
	if kind == gameprobe.KindBedrock {
		return gameprobe.MinecraftBedrock(ctx, conn)
	}
	host, port := "localhost", uint16(25565)
	if h, p, err := net.SplitHostPort(tun.Target); err == nil {
		if h != "" {
			host = h
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
			port = uint16(n)
		}
	}
	return gameprobe.MinecraftJava(ctx, conn, host, port)
}
