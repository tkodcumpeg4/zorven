package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
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

	var release func()
	var spec entitlements.ScreenStreamSpec
	if s.Entitlements != nil {
		var err error
		if body.ClientID != "" && s.screen2Hubs.active(body.ClientID) {
			// Bu istemcinin canli paylasimli yayini zaten bir slot tutuyor:
			// ek izleyici yeni slot harcamaz (slot akis basina sayilir).
			var te entitlements.TenantEntitlements
			te, err = s.Entitlements.GetTenantEntitlements(r.Context(), tenantID)
			spec = te.ScreenSpec
		} else {
			release, spec, err = s.Entitlements.AcquireScreenSlot(r.Context(), tenantID)
		}
		if err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	// Denetim yetkisi BILET VERILIRKEN (kimligi dogrulanmis istekte) kararlastirilir:
	// WS acilisinda kullanici baglami yoktur.
	controlAllowed := s.screenControlAllowed(r, tenantID)
	ticket := s.Tickets.issueFull(ticketKindScreen, tenantID, body.ClientID, release, controlAllowed)
	s.logger().Info("ekran bileti verildi", "tenant_id", tenantID, "client_id", body.ClientID, "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":          ticket,
		"expires_in":      int(ticketTTL.Seconds()),
		"max_fps":         spec.MaxFPS,
		"max_resolution":  spec.MaxResolution,
		"control_allowed": controlAllowed,
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
	tinfo, valid := s.Tickets.redeemInfo(ticketKindScreen, ticket, reqTenant, clientID)
	tenantID, releaseSlot := tinfo.TenantID, tinfo.Release
	locked := !tinfo.ControlAllowed
	if !valid || tenantID == "" {
		s.logger().Warn("ekran ws reddedildi: gecersiz veya suresi dolmus bilet",
			"client_id", clientID, "remote_addr", r.RemoteAddr)
		writeJSONError(w, http.StatusUnauthorized, "invalid_ticket",
			"Gecersiz veya suresi dolmus ekran bileti.")
		return
	}
	if releaseSlot != nil {
		// screen2 paylasimli yayina devredilirse releaseSlot nil'lenir.
		defer func() {
			if releaseSlot != nil {
				releaseSlot()
			}
		}()
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
	mode := q.Get("mode") // auto|screen2|mjpeg|h264
	display := atoiDefault(q.Get("display"), 0)

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

	// Kare kuyrugu: H.264'te parcalar DUSURULEMEZ (fMP4 sureklidir), o yuzden
	// kisa ag dalgalanmalarini yutacak kadar derin tutuyoruz. 8 kare 5 fps'te
	// yalnizca 1.6 saniyeydi ve normal bir gecikmede bile tasiyordu.
	frames := make(chan protocol.ScreenFrame, 64)
	errs := make(chan protocol.ScreenError, 1)

	// screen2: ham ikili mesajlar. Mod secimi saf fonksiyonda (test edilebilir).
	activeMode := chooseScreenMode(mode, sess.SupportsScreen2())

	// screen2: paylasimli yayin (istemci basina tek yakalama, cok izleyici).
	if activeMode == screenModeScreen2 {
		handled, handed := s.serveScreen2Shared(ctx, cancel, conn, sess, clientID,
			tunnel.Screen2Options{FPS: fps, Quality: quality, MaxWidth: maxWidth, Display: display},
			q.Get("view_only") == "1" || locked, locked, releaseSlot)
		if handed {
			releaseSlot = nil
		}
		if handled {
			return
		}
	}

	// Kilitli (salt-izleme) izleyiciye modu bildirmek icin bu mesaj panele gider.
	// Girdi durumu: istemci destekliyorsa ondan gelenleri aktar; desteklemiyorsa
	// (eski istemci) bir kez "unknown" bildir.
	inpStatus := make(chan protocol.ScreenInputStatus, 4)
	inputStatusSupported := sess.SubscribeScreenInputStatus(sessionID, inpStatus)

	var raw chan []byte // screen2 ham mesaj kuyrugu (yalniz screen2 modunda)
	if activeMode == screenModeScreen2 {
		raw = make(chan []byte, 64)
		_, err := sess.OpenScreen2(ctx, sessionID, tunnel.Screen2Options{
			FPS: fps, Quality: quality, MaxWidth: maxWidth, Display: display,
		}, raw, errs)
		if errors.Is(err, tunnel.ErrScreen2Unsupported) {
			// Yaris: istemci arada degismis olabilir; eski yola dus.
			activeMode, raw, err = screenModeLegacy(mode), nil, nil
		}
		if err != nil {
			s.logger().Error("istemcide screen2 oturumu acilamadi",
				"client_id", clientID, "session_id", sessionID, "hata", err)
			conn.Close(websocket.StatusInternalError, "ekran acilamadi")
			return
		}
	}
	if raw == nil {
		legacy := mode
		if legacy == "auto" || legacy == screenModeScreen2 {
			legacy = ""
		}
		if err := sess.OpenScreen(ctx, sessionID, fps, quality, maxWidth, legacy, frames, errs); err != nil {
			s.logger().Error("istemcide ekran oturumu acilamadi",
				"client_id", clientID, "session_id", sessionID, "hata", err)
			conn.Close(websocket.StatusInternalError, "ekran acilamadi")
			return
		}
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

	// screen2: istemci -> panel ham mesajlar. Yazma yavassa kuyruk dolar; dolunca
	// anahtar kareye kadar mesajlar dusurulur ve anahtar kare istenir (en fazla
	// saniyede bir). Boylece ne goruntu bozulur ne de okuma tikanir.
	var out chan []byte
	if raw != nil {
		out = make(chan []byte, 32)
		go pumpScreen2(ctx, raw, out, func() {
			_ = sess.Screen2Keyframe(ctx, sessionID)
		})
	}

	// Istemci -> dashboard: kareleri ve hatalari WSS'e yaz.
	go func() {
		// Panel hangi modun aktif oldugunu ilk mesajdan ogrenir.
		{
			shown := activeMode
			if shown != screenModeScreen2 && shown != "h264" {
				shown = "mjpeg"
			}
			msg, _ := json.Marshal(map[string]any{"type": "mode", "mode": shown})
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				cancel()
				return
			}
		}
		if locked {
			msg, _ := json.Marshal(map[string]any{"type": "view_only", "value": true, "locked": true})
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				cancel()
				return
			}
		}
		if !inputStatusSupported {
			msg, _ := json.Marshal(map[string]any{"type": "input_status", "state": protocol.InputStateUnknown})
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				cancel()
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-out: // out nil ise (eski mod) bu dal hic secilmez
				wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
				err := conn.Write(wctx, websocket.MessageBinary, b)
				wcancel()
				if err != nil {
					cancel()
					return
				}
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
					// Metrik paneli: darbogazin yakalama mi, kodlama mi,
					// yoksa ag mi oldugunu dashboard ancak boyle ayirir.
					"capture_ms": f.CaptureMS,
					"encode_ms":  f.EncodeMS,
				})
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					cancel()
					return
				}
			case st := <-inpStatus:
				msg, _ := json.Marshal(map[string]any{
					"type":   "input_status",
					"state":  st.State,
					"code":   st.Code,
					"detail": st.Detail,
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
		if raw != nil {
			if c, ok := parseScreenControl(data); ok {
				switch c.kind {
				case "keyframe":
					_ = sess.Screen2Keyframe(ctx, sessionID)
				case "settings":
					_ = sess.Screen2Settings(ctx, sessionID, c.opts)
				}
				continue
			}
		}
		forwardLegacyInput(ctx, sess, sessionID, data, locked)
	}
}

// screenInputSink, eski (mjpeg/h264) yolda girdinin iletildigi yuzey.
type screenInputSink interface {
	ScreenInput(ctx context.Context, in protocol.ScreenInput) error
}

// forwardLegacyInput, panelden gelen girdi mesajini istemciye iletir. locked
// (salt-izleme) ise TUM girdi (fare/klavye/kisayol/SAS) sunucuda dusurulur;
// iletildiyse true doner.
func forwardLegacyInput(ctx context.Context, sink screenInputSink, sessionID string, data []byte, locked bool) bool {
	if locked {
		return false
	}
	var in protocol.ScreenInput
	if json.Unmarshal(data, &in) != nil {
		return false
	}
	in.SessionID = sessionID
	_ = sink.ScreenInput(ctx, in)
	return true
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

const screenModeScreen2 = "screen2"

// screenPanelReadLimit, panelden gelen tek WS mesajinin ust siniri: 64 KiB
// metnin JSON kacislariyla sismesini karsilar, yine de bellek icin sinirli.
const screenPanelReadLimit = 512 << 10

// chooseScreenMode, panelin istedigi modu istemci yetenegiyle birlestirir.
// "screen2" ve "auto" yalniz istemci screen2 pazarladiysa screen2 olur; aksi
// halde eski yol (mjpeg/h264 ya da bos = istemci varsayilani).
func chooseScreenMode(requested string, supportsScreen2 bool) string {
	switch requested {
	case screenModeScreen2, "auto":
		if supportsScreen2 {
			return screenModeScreen2
		}
		return ""
	}
	return requested
}

func screenModeLegacy(requested string) string {
	if requested == "mjpeg" || requested == "h264" {
		return requested
	}
	return ""
}

// screenControl, panelin screen2 icin yolladigi JSON kontrol mesaji.
type screenControl struct {
	kind  string // keyframe | settings | view_only | video (value=supported, layer=mode)
	opts  tunnel.Screen2Options
	value bool   // view_only degeri
	text  string // clipboard metni
	layer string // settings: auto|high|low (bos => kaliteden turetilir)
}

// parseScreenControl, {"type":"keyframe"} ve {"type":"settings",...} mesajlarini
// ayristirir; digerleri (girdi olaylari) icin ok=false doner. Display yoksa -1
// (degistirme); negatif/asiri degerler sifira (degismez) indirilir.
func parseScreenControl(data []byte) (screenControl, bool) {
	var m struct {
		Type     string `json:"type"`
		FPS      int    `json:"fps"`
		Quality  int    `json:"quality"`
		MaxWidth int    `json:"max_width"`
		Display  *int   `json:"display"`
		Value    bool   `json:"value"`
		Text     string `json:"text"`
		Layer    string `json:"layer"`
		Mode     string `json:"mode"`
		Supports bool   `json:"supported"`
	}
	if json.Unmarshal(data, &m) != nil {
		return screenControl{}, false
	}
	switch m.Type {
	case "keyframe":
		return screenControl{kind: "keyframe"}, true
	case "video":
		return screenControl{kind: "video", value: m.Supports, layer: m.Mode}, true
	case "view_only":
		return screenControl{kind: "view_only", value: m.Value}, true
	case "clipboard":
		return screenControl{kind: "clipboard", text: m.Text}, true
	case "settings":
		clamp := func(v, max int) int {
			if v < 0 || v > max {
				return 0
			}
			return v
		}
		c := screenControl{kind: "settings", layer: m.Layer, opts: tunnel.Screen2Options{
			FPS:      clamp(m.FPS, 60),
			Quality:  clamp(m.Quality, 100),
			MaxWidth: clamp(m.MaxWidth, 16384),
			Display:  -1,
		}}
		if m.Display != nil && *m.Display >= 0 && *m.Display < 32 {
			c.opts.Display = *m.Display
		}
		return c, true
	}
	return screenControl{}, false
}

// screen2Flags, ham screen2 mesajinin bayrak baytini okur (offset 13).
func screen2Flags(b []byte) (keyframe, first bool) {
	if len(b) < 14 {
		return false, false
	}
	return b[13]&0x02 != 0, b[13]&0x04 != 0
}

// pumpScreen2, in kanalindaki ham mesajlari out'a tasir. out doluysa (panel
// yavas) mesaj dusurulur ve yeni bir FIRST+KEYFRAME mesaja kadar digerleri
// atilir; her dusmede en fazla saniyede bir askKeyframe cagrilir. ctx bitince
// ya da in kapaninca doner.
func pumpScreen2(ctx context.Context, in <-chan []byte, out chan<- []byte, askKeyframe func()) {
	resync := false
	var lastAsk time.Time
	ask := func() {
		if time.Since(lastAsk) >= time.Second {
			lastAsk = time.Now()
			askKeyframe()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-in:
			if !ok {
				return
			}
			if resync {
				if k, f := screen2Flags(b); !(k && f) {
					ask()
					continue
				}
			}
			select {
			case out <- b:
				resync = false
			default:
				resync = true
				ask()
			}
		}
	}
}

// serveScreen2Shared, panel WS'ini paylasimli screen2 yayinina baglar.
// handled=false: screen2 acilamadi (istemci desteklemiyor) ve cagiran eski yola
// dusmeli. handed: slot yayina devredildi (cagiran birakmamali).
func (s *Server) serveScreen2Shared(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn,
	sess *tunnel.Session, clientID string, o tunnel.Screen2Options, viewOnly, locked bool,
	release func()) (handled, handed bool) {
	v, err := s.screen2Hubs.join(clientID, sess, o, viewOnly, locked, release)
	if errors.Is(err, tunnel.ErrScreen2Unsupported) {
		return false, false
	}
	if err != nil {
		s.logger().Error("paylasimli screen2 acilamadi", "client_id", clientID, "hata", err)
		msg := "ekran acilamadi"
		if errors.Is(err, ErrTooManyViewers) {
			msg = "izleyici siniri dolu"
		}
		conn.Close(websocket.StatusInternalError, msg)
		return true, false
	}
	defer v.leave()
	// Pano mesaji 64 KiB metin + JSON kacisi tasiyabilir (varsayilan okuma siniri 32 KiB).
	conn.SetReadLimit(screenPanelReadLimit)
	s.logger().Info("ekran ws izleyici katildi (screen2 paylasimli)", "client_id", clientID, "session_id", v.hub.sid)

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

	go func() {
		writeText := func(b []byte) bool {
			wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
			defer wcancel()
			return conn.Write(wctx, websocket.MessageText, b) == nil
		}
		first, _ := json.Marshal(map[string]any{"type": "mode", "mode": screenModeScreen2})
		if !writeText(first) || !writeText(v.viewOnlyMsg()) {
			cancel()
			return
		}
		if !v.hub.inpOK {
			msg, _ := json.Marshal(map[string]any{"type": "input_status", "state": protocol.InputStateUnknown})
			if !writeText(msg) {
				cancel()
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-v.hub.done:
				// Yayin kapandi (istemci hatasi/yeniden baglanma): bekleyen
				// mesajlari (hata metni) bosalt, sonra bitir.
				for {
					select {
					case b := <-v.msgs:
						writeText(b)
					default:
						cancel()
						return
					}
				}
			case b := <-v.out:
				wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
				err := conn.Write(wctx, websocket.MessageBinary, b)
				wcancel()
				v.kick() // kuyrukta yer acildi (katmanli gonderici)
				if err != nil {
					cancel()
					return
				}
			case b := <-v.cur:
				// Imlec konumu: birlestirilmis en yeni deger; hizi agent ~30/sn'ye kisar.
				if !writeText(b) {
					cancel()
					return
				}
			case b := <-v.msgs:
				if !writeText(b) {
					cancel()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return true, true
		}
		if c, ok := parseScreenControl(data); ok {
			switch c.kind {
			case "keyframe":
				v.keyframe()
			case "settings":
				v.applySettings(c)
			case "video":
				v.setVideoPrefs(c.value, c.layer)
			case "view_only":
				v.setViewOnly(c.value)
			case "clipboard":
				v.clipboard(ctx, c.text)
			}
			continue
		}
		var in protocol.ScreenInput
		if json.Unmarshal(data, &in) != nil {
			continue
		}
		v.input(ctx, in)
	}
}
