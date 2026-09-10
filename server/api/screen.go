package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// issueScreenTicket, POST /api/v1/screen-ticket — admin middleware'inden gecer,
// dashboard'a ekran WS'i icin tek kullanimlik bilet verir (terminal ile ayni
// bilet deposu; bilet opak ve tek kullanimlik).
func (s *Server) issueScreenTicket(w http.ResponseWriter, r *http.Request) {
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

	var release func()
	var spec entitlements.ScreenStreamSpec
	if s.Entitlements != nil {
		var err error
		release, spec, err = s.Entitlements.AcquireScreenSlot(r.Context(), tenantID)
		if err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	ticket := s.Tickets.issueWithRelease(tenantID, body.ClientID, release)
	s.logger().Info("ekran bileti verildi", "tenant_id", tenantID, "client_id", body.ClientID, "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":         ticket,
		"expires_in":     int(ticketTTL.Seconds()),
		"max_fps":        spec.MaxFPS,
		"max_resolution": spec.MaxResolution,
	})
}

// screenHandler, GET /api/v1/clients/{id}/screen — dashboard'un uzak ekran icin
// baglandigi WSS ucu. Guvenlik terminal ile ayni: admin middleware + tek
// kullanimlik bilet (bkz. terminal.go).
//
// Akis:
//
//	istemci ekran/ffmpeg  --frame-->  sunucu  --WSS-->  dashboard (canvas/MSE)
//	dashboard fare/klavye --input-->  sunucu  --tunnel-> istemci (SendInput)
func (s *Server) screenHandler(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	ticket := r.URL.Query().Get("ticket")

	// CSWSH Koruması: Origin doğrulaması
	if !s.isAllowedOrigin(r) {
		s.logger().Warn("ekran ws reddedildi: yetkisiz origin",
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
	tenantID, releaseSlot, valid := s.Tickets.redeemWithRelease(ticket, reqTenant, clientID)
	if !valid || tenantID == "" {
		s.logger().Warn("ekran ws reddedildi: gecersiz veya suresi dolmus bilet",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusUnauthorized, "invalid_ticket",
			"Gecersiz veya suresi dolmus ekran bileti.")
		return
	}
	if releaseSlot != nil {
		defer releaseSlot()
	}

	// İstemcinin biletteki kiracıya ait olduğunu doğrula (IDOR önlemi)
	if _, err := s.Store.GetClient(r.Context(), tenantID, clientID); err != nil {
		s.logger().Warn("ekran ws reddedildi: istemci bu kiraciya ait degil veya bulunamadi",
			"client_id", clientID, "tenant_id", tenantID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusForbidden, "forbidden", "Bu istemciye erisim yetkiniz yok.")
		return
	}

	sess, online := s.Hub.Get(clientID)
	if !online {
		s.logger().Warn("ekran ws baglanamadi: istemci cevrimdisi",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusBadGateway, "client_offline",
			"Istemci su anda bagli degil; ekran acilamaz.")
		return
	}

	// Istege bagli parametreler (query): fps, quality, max_width, mode.
	q := r.URL.Query()
	fps := atoiDefault(q.Get("fps"), 0)
	quality := atoiDefault(q.Get("quality"), 0)
	maxWidth := atoiDefault(q.Get("max_width"), 0)
	mode := q.Get("mode") // auto|mjpeg|h264

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger().Warn("ekran ws upgrade basarisiz", "client_id", clientID, "hata", err)
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sessionID := "scr_" + randomHex(6)
	s.logger().Info("ekran ws oturumu baslatildi",
		"client_id", clientID, "session_id", sessionID, "mode", mode, "max_width", maxWidth)

	frames := make(chan protocol.ScreenFrame, 8)
	errs := make(chan protocol.ScreenError, 1)

	if err := sess.OpenScreen(ctx, sessionID, fps, quality, maxWidth, mode, frames, errs); err != nil {
		s.logger().Error("istemcide ekran oturumu acilamadi",
			"client_id", clientID, "session_id", sessionID, "hata", err)
		conn.Close(websocket.StatusInternalError, "ekran acilamadi")
		return
	}
	defer func() {
		s.logger().Info("ekran ws oturumu sonlandirildi", "client_id", clientID, "session_id", sessionID)
		sess.CloseScreen(context.Background(), sessionID)
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

	// Istemci -> dashboard: kareleri ve hatalari WSS'e yaz.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-frames:
				msg, _ := json.Marshal(map[string]any{
					"type":   "frame",
					"codec":  f.Codec,
					"data":   f.Data, // JSON'da base64
					"width":  f.Width,
					"height": f.Height,
					"seq":    f.Seq,
					// Uzak ekranin gercek boyutu: dashboard cozunurluk
					// seceneklerini buna gore uretir.
					"screen_w": f.ScreenW,
					"screen_h": f.ScreenH,
				})
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					cancel()
					return
				}
			case e := <-errs:
				msg, _ := json.Marshal(map[string]any{"type": "error", "message": e.Message})
				conn.Write(ctx, websocket.MessageText, msg)
				cancel()
				return
			}
		}
	}()

	// Dashboard -> istemci: fare/klavye olaylarini oturuma ilet.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in protocol.ScreenInput
		if json.Unmarshal(data, &in) != nil {
			continue
		}
		in.SessionID = sessionID
		sess.ScreenInput(ctx, in)
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
