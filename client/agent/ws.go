package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsDialTimeout, yerel servise WS icin baglanma/upgrade suresi.
const wsDialTimeout = 15 * time.Second

// handleWSOpen, sunucudan gelen bir WebSocket yukseltme istegini yerel servise
// tasir: yerel TCP baglantisi acar, upgrade el sikismasini oldugu gibi
// tekrarlar, 101 sonucunu sunucuya bildirir ve baglantiyi iki yonlu pipeler.
func (cs *clientSession) handleWSOpen(m protocol.WSOpen) {
	base := cs.targetFor(m.TunnelID)
	if base == "" {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "bu tunel icin yerel hedef tanimli degil")
		return
	}
	if base == zappUnavailable {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "uygulama calismiyor")
		return
	}
	u, err := url.Parse(base)
	if err != nil {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "gecersiz hedef: "+err.Error())
		return
	}
	if isBlockedTargetHost(u.Hostname()) {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "SSRF korumasi: hedef engellendi")
		return
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	// Sunucu yolu yuzde-kodlamasini koruyarak (EscapedPath) gonderir; HTTP
	// istekleriyle ayni dogrulama uygulanir ve yol ikinci kez kodlanmaz.
	if !validLocalPath(m.Path) {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "gecersiz istek yolu")
		return
	}
	reqURL := wsRequestURL(host, m.Path, m.Query)

	// Dial iptal edilebilir (ws_close dial surerken gelirse) ve SSRF korumali:
	// HTTP/akis yollariyla ayni guardedDial (cozumleme sonrasi IP denetimi + port listesi).
	dctx, endDial := cs.beginDial(m.ReqID, context.Background())
	defer endDial()
	conn, err := guardedDial(dctx, "tcp", host, true, wsDialTimeout)
	if err != nil {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "yerel servise baglanilamadi: "+err.Error())
		return
	}
	if u.Scheme == "https" || u.Scheme == "wss" {
		tconn := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		hctx, hcancel := context.WithTimeout(dctx, wsDialTimeout)
		herr := tconn.HandshakeContext(hctx)
		hcancel()
		if herr != nil {
			conn.Close()
			cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "yerel TLS el sikismasi basarisiz: "+herr.Error())
			return
		}
		conn = tconn
	}

	// Upgrade istegini yerel servise oldugu gibi yaz (Sec-WebSocket-Key vb.
	// tarayicidan gelenler korunur ki Accept degeri tarayicida dogrulansin).
	req := &http.Request{
		Method: http.MethodGet,
		URL:    reqURL,
		Host:   host,
		Header: http.Header(m.Headers),
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsDialTimeout))
	if err := req.Write(conn); err != nil {
		conn.Close()
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "upgrade istegi yazilamadi: "+err.Error())
		return
	}
	_ = conn.SetWriteDeadline(time.Time{})

	// 101 yanitini oku.
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(wsDialTimeout))
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "yerel yanit okunamadi: "+err.Error())
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	if resp.StatusCode != http.StatusSwitchingProtocols {
		cs.sendWSAccept(m.ReqID, resp.StatusCode, resp.Header, "not_upgraded", "yerel servis WebSocket yukseltmesini reddetti")
		resp.Body.Close()
		conn.Close()
		return
	}

	// Basari: ONCE kaydet, SONRA ws_accept gonder. Tarayici accept'i alir almaz
	// veri yollar; kayit sonra olsaydi ilk cerceveler sessizce dusurulurdu
	// (stream.go ile ayni yaris). Accept gonderilemezse kaydi geri al.
	// Kredi (sunucu onerdiyse): iki yonde de pencere; eski sunucu onermez.
	var caps []string
	var local net.Conn = conn
	if credit, _, accepted := cs.negotiateStreamCaps(m.Caps, false); credit {
		local = newFlowConn(conn, m.ReqID, true, false)
		caps = accepted
	}
	cs.registerWS(m.ReqID, local)
	cs.noteConn(m.ReqID, m.TunnelID)
	cs.tm.request(m.TunnelID)
	if err := cs.sendControl(context.Background(), protocol.WSAccept{
		Type: protocol.TypeWSAccept, ReqID: m.ReqID, Status: resp.StatusCode,
		Headers: resp.Header, Caps: caps,
	}, protocol.TypeWSAccept); err != nil {
		cs.closeWS(m.ReqID)
		return
	}
	if dctx.Err() != nil { // dial sirasinda ws_close geldi
		cs.closeWS(m.ReqID)
		return
	}

	// yerel -> sunucu (br: hijack sonrasi tamponlanmis baytlari da kapsar).
	go func() {
		defer cs.releaseSend(local, m.ReqID)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := br.Read(buf)
			if n > 0 {
				if cs.acquireSend(local, m.ReqID, n) != nil {
					break // akis kapandi
				}
				if serr := cs.sendFrame(context.Background(), protocol.BodyFrame{
					FrameType: protocol.FrameWSData, ReqID: m.ReqID, Payload: buf[:n],
				}); serr != nil {
					break
				}
				cs.tm.addOut(m.TunnelID, n)
			}
			if rerr != nil {
				break
			}
		}
		_ = cs.sendControl(context.Background(), protocol.WSClose{
			Type: protocol.TypeWSClose, ReqID: m.ReqID,
		}, protocol.TypeWSClose)
		cs.closeWS(m.ReqID)
	}()
}

// routeWSData, sunucudan gelen (tarayici -> yerel) baytlari yerel baglantinin
// SINIRLI yazma kuyruguna koyar. Okuma dongusunu BLOKLAMAZ; tasma/yazim zaman
// asiminda yalniz bu WS akisi kapatilir.
func (cs *clientSession) routeWSData(reqID uint64, payload []byte) {
	cs.wsMu.Lock()
	conn, ok := cs.wsConns[reqID]
	if !ok {
		cs.wsMu.Unlock()
		return
	}
	q := cs.wsQ[reqID]
	if q == nil {
		if cs.wsQ == nil {
			cs.wsQ = make(map[uint64]*writeQueue)
		}
		q = cs.newLocalWriteQueue(conn, func(err error) {
			cs.log.Warn("ws yazma kuyrugu basarisiz, akis kapatiliyor", "req_id", reqID, "hata", err)
			_ = cs.sendControl(context.Background(), protocol.WSClose{Type: protocol.TypeWSClose, ReqID: reqID}, protocol.TypeWSClose)
			cs.closeWS(reqID)
		})
		cs.wsQ[reqID] = q
	}
	cs.wsMu.Unlock()
	cs.tm.addIn(cs.tunnelOfConn(reqID), len(payload))
	_ = q.push(payload)
}

// handleWSClose, sunucu WS akisini kapattiginda yerel baglantiyi kapatir.
func (cs *clientSession) handleWSClose(reqID uint64) {
	cs.cancelDial(reqID)
	cs.closeWS(reqID)
}

func (cs *clientSession) sendWSAccept(reqID uint64, status int, headers map[string][]string, code, msg string) error {
	return cs.sendControl(context.Background(), protocol.WSAccept{
		Type: protocol.TypeWSAccept, ReqID: reqID, Status: status,
		Headers: headers, Code: code, Message: msg,
	}, protocol.TypeWSAccept)
}

func (cs *clientSession) registerWS(reqID uint64, conn net.Conn) {
	cs.wsMu.Lock()
	cs.wsConns[reqID] = conn
	cs.wsMu.Unlock()
}

func (cs *clientSession) closeWS(reqID uint64) {
	cs.wsMu.Lock()
	conn, ok := cs.wsConns[reqID]
	delete(cs.wsConns, reqID)
	q := cs.wsQ[reqID]
	delete(cs.wsQ, reqID)
	cs.wsMu.Unlock()
	if q != nil {
		q.stop()
	}
	if ok {
		cs.forgetConn(reqID)
		conn.Close()
	}
}

// closeAllWS, oturum kapaninca tum yerel WS baglantilarini kapatir.
func (cs *clientSession) closeAllWS() {
	cs.wsMu.Lock()
	conns := make([]net.Conn, 0, len(cs.wsConns))
	for _, c := range cs.wsConns {
		conns = append(conns, c)
	}
	cs.wsConns = make(map[uint64]net.Conn)
	qs := cs.wsQ
	cs.wsQ = nil
	cs.wsMu.Unlock()
	for _, q := range qs {
		q.stop()
	}
	for _, c := range conns {
		c.Close()
	}
}

// wsRequestURL, yerel upgrade isteginin URL'si. escapedPath kodlanmis yoldur
// (%2F, %40 korunur): Path cozulmus hali, RawPath ozgun kodlama olur; boylece
// istek satirina yol ikinci kez kodlanmadan yazilir. Cozulemeyen yol (eski
// sunucunun cozulmus yolu, ham '%' iceren) eski davranisa duser.
func wsRequestURL(host, escapedPath, query string) *url.URL {
	u := &url.URL{Scheme: "http", Host: host, RawQuery: query}
	if p, err := url.PathUnescape(escapedPath); err == nil {
		u.Path, u.RawPath = p, escapedPath
	} else {
		u.Path = escapedPath
	}
	return u
}
