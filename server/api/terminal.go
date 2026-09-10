package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// issueTerminalTicket, POST /api/v1/terminal-ticket — admin middleware'inden
// gecer (bearer/cookie), dashboard'a WS icin tek kullanimlik bilet verir.
func (s *Server) issueTerminalTicket(w http.ResponseWriter, r *http.Request) {
	if s.Tickets == nil {
		s.Tickets = NewTicketStore()
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	var body struct {
		ClientID string `json:"client_id"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	}
	if body.ClientID == "" {
		body.ClientID = r.URL.Query().Get("client_id")
	}

	// İstemci belirtilmişse, istek sahibinin kiracısına ait olduğunu doğrula (IDOR önlemi)
	if body.ClientID != "" {
		if _, err := s.Store.GetClient(r.Context(), tenantID, body.ClientID); err != nil {
			writeJSONError(w, http.StatusNotFound, "client_not_found", "istemci bulunamadi")
			return
		}
	}

	ticket := s.Tickets.issue(tenantID, body.ClientID)
	s.logger().Info("terminal bileti verildi", "tenant_id", tenantID, "client_id", body.ClientID, "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"expires_in": int(ticketTTL.Seconds()),
	})
}

// terminalHandler, GET /api/v1/clients/{id}/terminal — dashboard'un uzak kabuk
// icin baglandigi WSS ucu.
//
// GUVENLIK: bu uc /api/v1/* altinda oldugu icin admin anahtari middleware'inden
// gecer (server/api/admin.go). Yalnizca admin anahtarina sahip dashboard bir
// istemcide kabuk acabilir; istemci token'i tek basina buraya erisemez.
//
// Koru: tarayici <-> (bu uc, WSS) <-> istemci oturumu <-> PTY
func (s *Server) terminalHandler(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	ticket := r.URL.Query().Get("ticket")

	// CSWSH Koruması: Origin doğrulaması
	if !s.isAllowedOrigin(r) {
		s.logger().Warn("terminal ws reddedildi: yetkisiz origin",
			"origin", r.Header.Get("Origin"), "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusForbidden, "forbidden_origin", "Gecersiz veya yetkisiz Origin.")
		return
	}

	if s.Tickets == nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid_ticket", "Bilet servisi aktif degil.")
		return
	}

	// Bilet tek kullanımlıktır ve veren kiracıyı kriptografik olarak taşır.
	reqTenant, _ := s.tenantFor(r)
	tenantID, valid := s.Tickets.redeem(ticket, reqTenant, clientID)
	if !valid || tenantID == "" {
		s.logger().Warn("terminal ws reddedildi: gecersiz veya suresi dolmus bilet",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusUnauthorized, "invalid_ticket",
			"Gecersiz veya suresi dolmus terminal bileti.")
		return
	}

	// İstemcinin biletteki kiracıya ait olduğunu doğrula (IDOR önlemi)
	if _, err := s.Store.GetClient(r.Context(), tenantID, clientID); err != nil {
		s.logger().Warn("terminal ws reddedildi: istemci bu kiraciya ait degil veya bulunamadi",
			"client_id", clientID, "tenant_id", tenantID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusForbidden, "forbidden", "Bu istemciye erisim yetkiniz yok.")
		return
	}

	sess, online := s.Hub.Get(clientID)
	if !online {
		s.logger().Warn("terminal ws baglanamadi: istemci cevrimdisi",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusBadGateway, "client_offline",
			"Istemci su anda bagli degil; terminal acilamaz.")
		return
	}

	// InsecureSkipVerify: true ile origin kontrolu atlanir.
	// Bu guvenlidir cunku kimlik dogrulamasi cookie/oturum degil, TEK KULLANIMLIK
	// 30 saniyelik bilet ile yapilmistir (bkz. tickets.go).
	// coder/websocket dökümantasyonu da herhangi bir origini kabul etmek icin
	// OriginPatterns:["*"] yerine InsecureSkipVerify:true kullanilmasini belirtir.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger().Warn("terminal ws upgrade basarisiz", "client_id", clientID, "hata", err)
		return // Accept zaten yaniti yazdi
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sessionID := "term_" + randomHex(6)
	s.logger().Info("terminal ws oturumu baslatildi", "client_id", clientID, "session_id", sessionID)

	out := make(chan protocol.TerminalOutput, 64)
	exit := make(chan protocol.TerminalExit, 1)

	if err := sess.OpenTerminal(ctx, sessionID, 80, 24, out, exit); err != nil {
		s.logger().Error("istemcide terminal oturumu acilamadi",
			"client_id", clientID, "session_id", sessionID, "hata", err)
		conn.Close(websocket.StatusInternalError, "terminal acilamadi")
		return
	}
	defer func() {
		s.logger().Info("terminal ws oturumu sonlandirildi", "client_id", clientID, "session_id", sessionID)
		sess.CloseTerminal(context.Background(), sessionID)
	}()

	// Tarayıcı bağlantısı kesilirse veya kilitlenirse oturumu hızlıca sonlandır.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Istemci -> dashboard: cikti ve exit'i WSS'e yaz.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-out:
				msg, _ := json.Marshal(map[string]any{"type": "output", "data": m.Data})
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					cancel()
					return
				}
			case m := <-exit:
				msg, _ := json.Marshal(map[string]any{"type": "exit", "code": m.Code, "message": m.Message})
				conn.Write(ctx, websocket.MessageText, msg)
				cancel()
				return
			}
		}
	}()

	// Dashboard -> istemci: gelen mesajlari coz ve oturuma ilet.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var msg struct {
			Type string `json:"type"`
			Data []byte `json:"data"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "input":
			sess.TerminalInput(ctx, sessionID, msg.Data)
		case "resize":
			sess.TerminalResize(ctx, sessionID, msg.Cols, msg.Rows)
		}
	}
}

// randomHex, oturum kimligi icin rastgele hex uretir.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
