package agent

import (
	"bufio"
	"context"
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

	conn, err := (&net.Dialer{Timeout: wsDialTimeout}).Dial("tcp", host)
	if err != nil {
		cs.sendWSAccept(m.ReqID, 0, nil, protocol.CodeLocalUnreachable, "yerel servise baglanilamadi: "+err.Error())
		return
	}

	// Upgrade istegini yerel servise oldugu gibi yaz (Sec-WebSocket-Key vb.
	// tarayicidan gelenler korunur ki Accept degeri tarayicida dogrulansin).
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: host, Path: m.Path, RawQuery: m.Query},
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

	// Basari: 101 el sikismasini sunucuya bildir, baglantiyi kaydet.
	cs.sendWSAccept(m.ReqID, resp.StatusCode, resp.Header, "", "")
	cs.registerWS(m.ReqID, conn)

	// yerel -> sunucu (br: hijack sonrasi tamponlanmis baytlari da kapsar).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := br.Read(buf)
			if n > 0 {
				if serr := cs.sendFrame(context.Background(), protocol.BodyFrame{
					FrameType: protocol.FrameWSData, ReqID: m.ReqID, Payload: buf[:n],
				}); serr != nil {
					break
				}
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

// routeWSData, sunucudan gelen (tarayici -> yerel) baytlari yerel baglantiya yazar.
func (cs *clientSession) routeWSData(reqID uint64, payload []byte) {
	cs.wsMu.Lock()
	conn, ok := cs.wsConns[reqID]
	cs.wsMu.Unlock()
	if !ok {
		return
	}
	if _, err := conn.Write(payload); err != nil {
		cs.closeWS(reqID)
	}
}

// handleWSClose, sunucu WS akisini kapattiginda yerel baglantiyi kapatir.
func (cs *clientSession) handleWSClose(reqID uint64) {
	cs.closeWS(reqID)
}

func (cs *clientSession) sendWSAccept(reqID uint64, status int, headers map[string][]string, code, msg string) {
	_ = cs.sendControl(context.Background(), protocol.WSAccept{
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
	cs.wsMu.Unlock()
	if ok {
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
	cs.wsMu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}
