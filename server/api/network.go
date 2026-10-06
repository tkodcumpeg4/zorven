package api

// FAZ 3 / F17 — Zorven Network, Asama 1 (SOCKS5 + WSS).
//
//	GET  /api/v1/network/resources   — ozel kaynaklari listele
//	POST /api/v1/network/resources   — ozel kaynak olustur (owner/admin)
//	GET  /api/v1/network/connect?target=ad:port  — WebSocket; ham TCP akisi
//
// Ozel kaynak = exposure='private' bir TCP tuneli. Hostname'i ve public portu
// yoktur; ingress sorgulari bu tunelleri dislar (internete hicbir yoldan
// cikamaz). Erisim yalnizca "zorven connect" ile, API token'la.
//
// GUVENLIK:
//   - Baglanma ucu YALNIZCA API token kabul eder (Authorization basligi).
//     Cerezle kabul etseydi, kotu niyetli bir web sayfasi giris yapmis
//     kullanicinin tarayicisi uzerinden ozel kaynaga WebSocket acabilirdi
//     (cross-site WebSocket hijacking). Tarayicilar WebSocket'e bu basligi
//     ekleyemez.
//   - Yetki F16 cihaz politikasidir: kaynaga erisim = onu yayinlayan cihaza
//     erisim. Yetkisiz, bulunmayan ve port uyusmayan istek AYNI 404'u alir:
//     kaynagin varligi sizdirilmaz.
//   - Ajan hedefi tunel TANIMINDAN alir, istekteki adresten degil: kullanici
//     ajan uzerinden LAN'da keyfi adres tarayamaz.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// networkAcceptTimeout, ajanin yerel hedefe baglanmasi icin beklenen sure.
const networkAcceptTimeout = 15 * time.Second

// privateNameRe: noktali DNS etiketleri, or. "db", "db.internal", "nas-1.ev".
var privateNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validPrivateName(n string) bool {
	return len(n) >= 1 && len(n) <= 253 && privateNameRe.MatchString(n)
}

// targetPort, tunel hedefinden ("tcp://h:p", "h:p") portu cikarir.
func targetPort(target string) (string, bool) {
	t := strings.TrimSpace(target)
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
	}
	t = strings.TrimSuffix(t, "/")
	_, port, err := net.SplitHostPort(t)
	if err != nil || port == "" {
		return "", false
	}
	return port, true
}

// parseConnectTarget, "ad:port" bicimini ayristirir ve dogrular.
func parseConnectTarget(v string) (name, port string, ok bool) {
	host, p, err := net.SplitHostPort(strings.TrimSpace(v))
	if err != nil {
		return "", "", false
	}
	host = strings.ToLower(host)
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return "", "", false
	}
	// IP literali (alt ag yonlendirmesi, F20) veya ozel kaynak adi.
	if _, ipErr := netip.ParseAddr(host); ipErr != nil && !validPrivateName(host) {
		return "", "", false
	}
	return host, p, true
}

func (s *Server) networkGate(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return "", false
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureZorvenNetwork); err != nil {
			writeEntitlementError(w, err)
			return "", false
		}
	}
	return tenantID, true
}

func (s *Server) listNetworkResources(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.networkGate(w, r)
	if !ok {
		return
	}
	all, err := s.Store.ListTunnels(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Cagiranin erisebildigi cihazlarin kaynaklari (F16).
	clients, err := s.Store.ListClients(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	allowed, err := s.filterClientsForCaller(r, tenantID, clients)
	if err != nil {
		s.fail(w, err)
		return
	}
	reachable := make(map[string]bool, len(allowed))
	for _, c := range allowed {
		reachable[c.ID] = true
	}
	out := []store.Tunnel{}
	for _, t := range all {
		if t.Exposure == store.ExposurePrivate && reachable[t.ClientID] {
			out = append(out, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": out})
}

func (s *Server) createNetworkResource(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.networkGate(w, r)
	if !ok {
		return
	}
	// Ozel kaynak yayinlamak kurum agina kapi acmaktir: owner/admin.
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	var body struct {
		Name     string `json:"name"`
		ClientID string `json:"client_id"`
		Target   string `json:"target"`
		// Subnet (F20): tekil hedef yerine bir adres araligi yayinlar.
		Subnet string `json:"subnet"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	if !validPrivateName(name) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name",
			"ad küçük harf, rakam, tire ve noktadan oluşmalı (ör. db.internal)")
		return
	}
	target := strings.TrimSpace(body.Target)
	if strings.TrimSpace(body.Subnet) != "" {
		if target != "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_target",
				"target ve subnet birlikte verilemez")
			return
		}
		p, verr := validPublishableSubnet(body.Subnet)
		if verr != "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_subnet", verr)
			return
		}
		target = subnetTargetPrefix + p.String()
	} else if !validRawTarget(target) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_target",
			"hedef host:port biçiminde olmalı (ör. localhost:5432)")
		return
	}
	if _, err := s.Store.GetClient(r.Context(), tenantID, body.ClientID); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "client_not_found", "belirtilen client_id bulunamadı")
		return
	}
	if _, err := s.Store.GetPrivateTunnel(r.Context(), tenantID, name); err == nil {
		writeJSONError(w, http.StatusConflict, "name_taken", "bu adda bir özel kaynak zaten var")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateTunnel(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	projID, _ := s.projectFor(r)
	t, err := s.Store.CreateTunnelWithProject(r.Context(), tenantID, body.ClientID, target, projID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.MakeTunnelPrivate(r.Context(), tenantID, t.ID, name); err != nil {
		// Yarim kalmis (public olabilecek) tunel birakma.
		_ = s.Store.DeleteTunnel(r.Context(), tenantID, t.ID)
		s.fail(w, err)
		return
	}
	t, _ = s.Store.GetTunnel(r.Context(), tenantID, t.ID)

	s.tunnelsChanged()
	s.pushClientConfig(body.ClientID)
	s.audit(r, "network.resource.create", t.ID, name)
	writeJSON(w, http.StatusCreated, t)
}

// connectNetwork, SOCKS istemcisinin tek bir TCP baglantisini ozel kaynaga
// kopruler. Tum kontroller WebSocket yukseltmesinden ONCE yapilir ki hata
// HTTP durum koduyla donsun ve istemci SOCKS'a dogru yaniti verebilsin.
func (s *Server) connectNetwork(w http.ResponseWriter, r *http.Request) {
	if _, isToken := apiScopesFromContext(r.Context()); !isToken {
		writeJSONError(w, http.StatusForbidden, "token_required",
			"özel ağa yalnızca API token ile bağlanılabilir (zorven connect)")
		return
	}
	// Ozel aga yalnizca bir KISI adina baglanilir: kullaniciya bagli OLMAYAN
	// (kiraci geneli) token ile baglanilamaz.
	if _, hasUser := userFromContext(r.Context()); !hasUser {
		writeJSONError(w, http.StatusForbidden, "identity_required",
			"özel ağa bağlanmak için bir kullanıcıya ait API token gerekir")
		return
	}
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.networkGate(w, r)
	if !ok {
		return
	}
	name, port, ok := parseConnectTarget(r.URL.Query().Get("target"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid_target", "target ad:port biçiminde olmalı")
		return
	}

	notFound := func() {
		writeJSONError(w, http.StatusNotFound, "not_found", "özel kaynak bulunamadı")
	}
	var tun store.Tunnel
	dest := "" // yalnizca alt ag icin: ajana iletilecek ip:port
	if ip, ipErr := netip.ParseAddr(name); ipErr == nil {
		// F20: IP literali — onu iceren en spesifik alt ag kaynagi.
		subnets, err := s.Store.ListPrivateSubnets(r.Context(), tenantID)
		if err != nil {
			s.fail(w, err)
			return
		}
		t, found := pickSubnet(ip, subnets)
		if !found {
			notFound()
			return
		}
		tun = t
		dest = net.JoinHostPort(ip.Unmap().String(), port)
	} else {
		t, err := s.Store.GetPrivateTunnel(r.Context(), tenantID, name)
		if errors.Is(err, store.ErrNotFound) {
			notFound()
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		// Ad ile alt ag kaynagina baglanilamaz (hangi IP'ye gidilecegi belli degil).
		if _, isSubnet := subnetOf(t.Target); isSubnet {
			notFound()
			return
		}
		if tp, ok := targetPort(t.Target); !ok || tp != port {
			notFound()
			return
		}
		tun = t
	}
	// Cihaz erisimi uye rollerine baglidir (acik surum): kiracinin uyesi erisir.
	if !s.requireDeviceAccess(w, r, tenantID, tun.ClientID) {
		return
	}

	sess, online := s.Hub.Get(tun.ClientID)
	if !online {
		writeJSONError(w, http.StatusServiceUnavailable, "device_offline",
			"kaynağı yayınlayan cihaz çevrimdışı")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := sess.OpenStreamTo(ctx, tun.ID, protocol.ProtoTCP, clientIP(r), dest)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "stream_failed", "akış açılamadı")
		return
	}
	if _, err := tunnel.WaitAccept(ctx, stream, networkAcceptTimeout); err != nil {
		stream.Close("rejected")
		writeJSONError(w, http.StatusBadGateway, "target_unreachable",
			"cihaz yerel hedefe bağlanamadı")
		return
	}

	// Origin kontrolu ACIK birakildi (InsecureSkipVerify yok): CLI Origin
	// gondermez ve gecer; tarayicidan gelen capraz kaynakli istek reddedilir.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		stream.Close("upgrade failed")
		return // Accept yaniti yazdi
	}
	c.SetReadLimit(protocol.WSReadLimit)
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer conn.Close()

	s.audit(r, "network.connect", tun.ID, name+":"+port)
	tunnel.Pipe(ctx, conn, stream)
}
