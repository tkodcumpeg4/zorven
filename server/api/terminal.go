package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
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
		Shell    string `json:"shell"`
		Attach   string `json:"attach"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	}
	if body.ClientID == "" {
		body.ClientID = r.URL.Query().Get("client_id")
	}

	// İstemci belirtilmişse, istek sahibinin kiracısına ait olduğunu doğrula (IDOR önlemi)
	// F16: politikaya tabi cagiran cihaz ADI VERMEDEN bilet alamaz — adsiz
	// bilet kiracinin herhangi bir cihazina acilabilirdi.
	if !s.requireRemoteAccess(w, r, tenantID, body.ClientID) {
		return
	}
	if body.ClientID != "" {
		if _, err := s.Store.GetClient(r.Context(), tenantID, body.ClientID); err != nil {
			writeJSONError(w, http.StatusNotFound, "client_not_found", "istemci bulunamadi")
			return
		}
	}

	if len(body.Shell) > 32 {
		writeJSONError(w, http.StatusBadRequest, "invalid_shell", "gecersiz kabuk")
		return
	}
	owner := termOwner(r)
	if body.Attach != "" {
		// Yeniden baglanma: oturum hala yasiyor ve bu kimlige mi ait? Degilse
		// panel bunu "oturum bitti" olarak gosterir (yeni kabuk ACILMAZ).
		if body.ClientID == "" || s.termReg().lookup(body.Attach, tenantID, body.ClientID, owner) == nil {
			writeJSONError(w, http.StatusGone, "session_gone", "terminal oturumu artik yok")
			return
		}
	} else if body.ClientID != "" && s.termReg().countFor(tenantID, body.ClientID, owner) >= termMaxPerOwner {
		writeJSONError(w, http.StatusTooManyRequests, "too_many_terminals", "bu istemcide cok fazla acik terminal var")
		return
	}
	ticket := s.Tickets.issueTerminalWithShell(tenantID, body.ClientID, body.Shell, body.Attach, owner)
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
	tinfo := s.Tickets.peekTerminal(ticket) // redeem bileti tuketir; once oku
	ticketShell := tinfo.Shell
	tenantID, valid := s.Tickets.redeem(ticketKindTerminal, ticket, reqTenant, clientID)
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

	// Kabuk secimi (opsiyonel): ?shell=<id>. Istemci kabuk listesi bildirdiyse
	// ID o listede olmali; istemci ayrica kendi listesini tekrar denetler. Eski
	// istemcilerde (liste yok) alan yok sayilir, varsayilan kabuk acilir.
	shell := r.URL.Query().Get("shell")
	if shell == "" {
		shell = ticketShell
	}
	if shell != "" {
		if len(sess.Shells) == 0 {
			shell = ""
		} else if !sess.HasShell(shell) {
			writeJSONError(w, http.StatusBadRequest, "invalid_shell",
				"Bu istemcide boyle bir kabuk yok.")
			return
		}
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

	reg := s.termReg()
	var pt *persistTerm
	resumed := false
	if tinfo.Attach != "" {
		pt = reg.lookup(tinfo.Attach, tenantID, clientID, tinfo.Owner)
		if pt == nil || pt.tun != sess {
			conn.Close(websocket.StatusCode(4410), "session_gone")
			return
		}
		resumed = true
	} else {
		sessionID := "term_" + randomHex(12)
		pt, err = reg.open(sess, sessionID, tenantID, clientID, tinfo.Owner, shell)
		if err != nil {
			s.logger().Error("istemcide terminal oturumu acilamadi",
				"client_id", clientID, "session_id", sessionID, "hata", err)
			conn.Close(websocket.StatusInternalError, "terminal acilamadi")
			return
		}
	}
	s.logger().Info("terminal ws baglandi", "client_id", clientID, "session_id", pt.id, "resumed", resumed)

	replay, data, exitCh, exited := pt.attach()
	var killed atomic.Bool
	defer func() {
		if killed.Load() {
			reg.kill(pt)
		} else {
			pt.detach(data)
		}
		s.logger().Info("terminal ws ayrildi", "client_id", clientID, "session_id", pt.id, "kapatildi", killed.Load())
	}()

	writeJSONMsg := func(v any) error {
		msg, _ := json.Marshal(v)
		return conn.Write(ctx, websocket.MessageText, msg)
	}
	if writeJSONMsg(map[string]any{"type": "session", "id": pt.id, "resumed": resumed}) != nil {
		return
	}
	if len(replay) > 0 {
		if writeJSONMsg(map[string]any{"type": "output", "data": replay}) != nil {
			return
		}
	}
	if exited != nil {
		_ = writeJSONMsg(map[string]any{"type": "exit", "code": exited.Code, "message": exited.Message})
		killed.Store(true)
		return
	}

	// Tarayici baglantisi kesilirse veya kilitlenirse WS'i hizlica birak
	// (oturum yasamaya devam eder).
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

	// Oturum -> dashboard: cikti ve exit'i WSS'e yaz.
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case b, ok := <-data:
				if !ok {
					// Ayni oturuma baska bir sekme baglandi.
					conn.Close(websocket.StatusCode(4409), "baska sekmede acildi")
					return
				}
				if writeJSONMsg(map[string]any{"type": "output", "data": b}) != nil {
					return
				}
			case m := <-exitCh:
				_ = writeJSONMsg(map[string]any{"type": "exit", "code": m.Code, "message": m.Message})
				killed.Store(true)
				return
			}
		}
	}()

	// Dashboard -> istemci: gelen mesajlari coz ve oturuma ilet.
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var msg struct {
			Type string `json:"type"`
			Data []byte `json:"data"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "input":
			sess.TerminalInput(ctx, pt.id, msg.Data)
		case "resize":
			sess.TerminalResize(ctx, pt.id, msg.Cols, msg.Rows)
		case "close":
			// Sekme kapatildi / "Baglantiyi kes": kabugu gercekten sonlandir.
			killed.Store(true)
			return
		}
	}
}

// randomHex, oturum kimligi icin rastgele hex uretir.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
