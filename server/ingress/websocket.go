package ingress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsAcceptTimeout, yerel servisin WS yukseltmesini kabul etmesi icin taninan sure.
const wsAcceptTimeout = 20 * time.Second

// wsWriteStall, tarayiciya tek bir yazimin ilerlemeden bekleyebilecegi sure
// (degisken: testler kisaltir).
var wsWriteStall = 30 * time.Second

// serveWebSocket, bir WebSocket yukseltme istegini tunel uzerinden yerel
// servise proxy'ler. Tarayici baglantisi hijack edilir; 101 el sikismasi ve
// sonraki ham baytlar cift yonlu olarak FrameWSData cerceveleriyle tasinir.
//
// Cagrilmadan once ServeHTTP tum kontrolleri (tunel aktif mi, IP izin listesi,
// hiz siniri, istemci cevrimici mi) yapmis olmalidir.
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID, tenantID string) {
	h.serveWebSocketLog(w, r, sess, tunnelID, tenantID, true)
}

// ServeClusterWS, baska bir kume dugumunden aktarilan WebSocket'i yerel oturuma kopruler
// (cluster.Manager.ServeWS). Istek logunu ZIYARETCIYI KARSILAYAN dugum yazar (bkz.
// serveViaCluster); burada yazilmaz.
func (h *Handler) ServeClusterWS(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID, tenantID string) {
	h.serveWebSocketLog(w, r, sess, tunnelID, tenantID, false)
}

func (h *Handler) serveWebSocketLog(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID, tenantID string, doLog bool) {
	// El sikismasi istek loguna yazilir (panel "Canli Istekler"): durum 101 ya da
	// yerel servisin/aginin hata durumu. Baglanti suresince akan veri loglanmaz.
	started := time.Now()
	zid, fwdID := wsRequestIDs(r, !doLog)
	// Hijack oncesi hata yanitlari (writeError) da kimligi tasisin.
	w.Header().Set(headerZorvenRequestID, zid)
	w.Header().Set(obs.HeaderRequestID, fwdID)
	logRec := &recorder{ResponseWriter: w, status: http.StatusSwitchingProtocols}
	logged := false
	logOnce := func() {
		if !logged {
			logged = true
			if !doLog {
				return
			}
			h.recordWithID(tunnelID, tenantID, false, r, logRec, started, zid)
		}
	}
	defer logOnce() // hata yollari; basarili el sikismasi asagida hemen yazilir

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, protocol.CodeWebSocketUnsup,
			"WebSocket icin baglanti hijack edilemiyor.")
		return
	}

	ctx := r.Context()
	// Istek basliklari OLDUGU GIBI iletilir (Upgrade, Connection, Sec-WebSocket-*);
	// ajan bu el sikismasini yerel servise aynen tekrarlar.
	wsHeaders := make(map[string][]string, len(r.Header))
	for k, v := range r.Header {
		wsHeaders[k] = v
	}
	// Zorven oturum cerezleri backend'e gitmez.
	if cv, ok := wsHeaders["Cookie"]; ok {
		if kept := stripAuthCookies(cv); len(kept) > 0 {
			wsHeaders["Cookie"] = kept
		} else {
			delete(wsHeaders, "Cookie")
		}
	}
	// HTTP istekleriyle ayni: gercek ziyaretci IP'si zincirin SONUNA eklenir
	// (istemcinin gonderdigi X-Forwarded-For tek basina backend'e ulasmaz).
	setForwardedHeaders(wsHeaders, r)
	// HTTP yoluyla ayni kural (requestid.go): hedef el sikismasinda kimligi gorur.
	setRequestIDHeaders(wsHeaders, zid, fwdID)
	stream, err := sess.OpenWS(ctx, tunnelID, agentPath(r.URL), r.URL.RawQuery, wsHeaders)
	if errors.Is(err, tunnel.ErrTooManyStreams) {
		logRec.status = http.StatusServiceUnavailable
		writeError(w, r, http.StatusServiceUnavailable, protocol.CodeRateLimited,
			"Bu tunel icin cok fazla acik WebSocket baglantisi var.")
		return
	}
	if err != nil {
		logRec.status = http.StatusBadGateway
		writeError(w, r, http.StatusBadGateway, protocol.CodeClientOffline,
			"WebSocket akisi acilamadi: "+err.Error())
		return
	}

	// Yerel servisin 101 (veya hata) yanitini bekle.
	var acc protocol.WSAccept
	select {
	case acc = <-stream.Accept():
	case <-time.After(wsAcceptTimeout):
		stream.Close("accept timeout")
		logRec.status = http.StatusGatewayTimeout
		writeError(w, r, http.StatusGatewayTimeout, protocol.CodeUpstreamTimeout,
			"Yerel servis WebSocket yukseltmesini zamaninda yanitlamadi.")
		return
	case <-ctx.Done():
		stream.Close("client gone")
		return
	}

	conn, brw, err := hj.Hijack()
	if err != nil {
		stream.Close("hijack failed")
		return
	}
	defer conn.Close()
	defer stream.Close("closed")

	// Yerel servis yukseltmeyi kabul etmediyse (101 disi) tarayiciya bir hata
	// durum satiri yazip kapat.
	if acc.Code != "" || acc.Status != http.StatusSwitchingProtocols {
		status := acc.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
		logRec.status = status
		fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n%s: %s\r\nConnection: close\r\n\r\n",
			status, http.StatusText(status), headerZorvenRequestID, zid)
		return
	}

	// 101 el sikismasini tarayiciya yaz.
	var sb strings.Builder
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	writeWSResponseHeaders(&sb, acc.Headers, zid, fwdID)
	sb.WriteString("\r\n")
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		return
	}
	logOnce() // el sikismasi tamam: baglanti kapanmasini beklemeden logla

	// Z2: acik WebSocket, erisim/Geo/IP ayari degisince yeniden degerlendirilir.
	defer h.registerWS(r, tunnelID, tenantID, func() {
		stream.Close("access revoked")
		conn.Close()
	})()

	// Akis iptal edilirse (kuyruk tasti, oturum dustu) asagidaki yazim okumayan
	// tarayiciya bloklu olabilir; baglantiyi kapatmak yazimi cozer (Y1).
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stream.Aborted():
			conn.Close()
		case <-done:
		}
	}()

	// tarayici -> yerel: hijack edilen baglantidan oku, ajana ilet.
	go func() {
		defer func() {
			stream.Close("browser closed")
			conn.Close()
		}()
		// B2: bu goroutine net/http'nin kurtarmasi disinda; panik sureci dusurmesin,
		// yalniz bu akis kapansin (yukaridaki defer).
		defer obs.Recover(h.Log, "ingress.ws_pump", nil)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := brw.Read(buf)
			if n > 0 {
				if serr := stream.Send(context.Background(), buf[:n]); serr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// yerel -> tarayici: ajandan gelen baytlari hijack edilen baglantiya yaz.
	// Her yazima ilerleme son tarihi: tarayici wsWriteStall boyunca hic okumazsa
	// akis kapatilir (eskiden zaman asimsiz bloklaniyordu ve kuyruk dolup
	// kapansa bile bu goroutine kapanisi goremiyordu).
	for {
		select {
		case data := <-stream.FromLocal():
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteStall))
			if _, werr := conn.Write(data); werr != nil {
				return
			}
		case <-stream.Closed():
			return
		case <-ctx.Done():
			return
		}
	}
}

// wsRequestIDs, WebSocket el sikismasinin istek kimlikleri (HTTP yoluyla ayni
// kural, bkz. requestid.go). fromCluster: istek kume anahtariyla dogrulanmis bir
// kenar dugumden geldi; o dugum kimligi uretip loga yazdi, burada AYNISI
// kullanilir ki ziyaretcinin 101'de gordugu deger request_logs.id ile eslessin.
// Ziyaretciden dogrudan gelen X-Zorven-Request-Id ise asla benimsenmez.
func wsRequestIDs(r *http.Request, fromCluster bool) (zid, fwd string) {
	if fromCluster {
		if v := r.Header.Get(headerZorvenRequestID); obs.ValidRequestID(v) {
			zid = v
		}
	}
	if zid == "" {
		zid = reqlog.NewID()
	}
	return zid, forwardRequestID(r, zid)
}

// writeWSResponseHeaders, yerel servisin 101 basliklarini ziyaretciye yazar ve
// kimlikleri ekler: X-Zorven-Request-Id her zaman Zorven'inki (hedefinki atilir);
// X-Request-Id hedef verdiyse ona dokunulmaz, yoksa hedefe giden deger
// yankilanir. Ajan basliklari kanonik olmayabilir: karsilastirma harf duyarsiz.
func writeWSResponseHeaders(sb *strings.Builder, hdr map[string][]string, zid, fwd string) {
	hasReqID := false
	for k, vals := range hdr {
		if strings.EqualFold(k, headerZorvenRequestID) {
			continue
		}
		if strings.EqualFold(k, obs.HeaderRequestID) && len(vals) > 0 {
			hasReqID = true
		}
		for _, v := range vals {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	sb.WriteString(headerZorvenRequestID + ": " + zid + "\r\n")
	if !hasReqID {
		sb.WriteString(obs.HeaderRequestID + ": " + fwd + "\r\n")
	}
}
