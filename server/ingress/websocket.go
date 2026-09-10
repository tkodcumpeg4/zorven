package ingress

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsAcceptTimeout, yerel servisin WS yukseltmesini kabul etmesi icin taninan sure.
const wsAcceptTimeout = 20 * time.Second

// serveWebSocket, bir WebSocket yukseltme istegini tunel uzerinden yerel
// servise proxy'ler. Tarayici baglantisi hijack edilir; 101 el sikismasi ve
// sonraki ham baytlar cift yonlu olarak FrameWSData cerceveleriyle tasinir.
//
// Cagrilmadan once ServeHTTP tum kontrolleri (tunel aktif mi, IP izin listesi,
// hiz siniri, istemci cevrimici mi) yapmis olmalidir.
func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, protocol.CodeWebSocketUnsup,
			"WebSocket icin baglanti hijack edilemiyor.")
		return
	}

	ctx := r.Context()
	// Istek basliklari OLDUGU GIBI iletilir (Upgrade, Connection, Sec-WebSocket-*);
	// ajan bu el sikismasini yerel servise aynen tekrarlar.
	stream, err := sess.OpenWS(ctx, tunnelID, r.URL.Path, r.URL.RawQuery, map[string][]string(r.Header))
	if err != nil {
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
		fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nConnection: close\r\n\r\n",
			status, http.StatusText(status))
		return
	}

	// 101 el sikismasini tarayiciya yaz.
	var sb strings.Builder
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	for k, vals := range acc.Headers {
		for _, v := range vals {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	sb.WriteString("\r\n")
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		return
	}

	// tarayici -> yerel: hijack edilen baglantidan oku, ajana ilet.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := brw.Read(buf)
			if n > 0 {
				if serr := stream.Send(context.Background(), buf[:n]); serr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		stream.Close("browser closed")
		conn.Close()
	}()

	// yerel -> tarayici: ajandan gelen baytlari hijack edilen baglantiya yaz.
	for {
		select {
		case data := <-stream.FromLocal():
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
