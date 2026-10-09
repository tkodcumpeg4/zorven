package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// FAZ 2 — İstek inspector + replay.
//
//	GET  /api/v1/requests/capture       — yakalama açık mı
//	POST /api/v1/requests/capture       — yakalamayı aç/kapat {enabled}
//	GET  /api/v1/requests/{id}          — tek isteğin tam detayı (header+gövde)
//	POST /api/v1/requests/{id}/replay   — isteği tünele yeniden gönder

func (s *Server) getCaptureSettings(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Yakalama deposu yoksa (or. bazi testler / yapilandirma) ozellik kapali.
	enabled := s.Captures != nil && s.Captures.Enabled(tenantID)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

func (s *Server) setCaptureSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	// YAZMA islemi: tam istek/yanit govdesi yakalamayi acar. Salt-okur
	// (analytics:read) token bunu yapamamali.
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.Captures == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "yakalama deposu etkin degil")
		return
	}
	s.Captures.SetEnabled(tenantID, body.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled})
}

// captureOut, bir yakalamayi panele GÜVENLİ döndürür (gövdeler metin olarak).
func captureOut(c *reqlog.Capture, privileged bool) map[string]any {
	reqH, respH := c.ReqHeaders, c.RespHeaders
	if !privileged {
		// F-28: hassas basliklar saklanir ama yalniz owner/admin gorur.
		reqH, respH = maskSensitiveHeaders(reqH), maskSensitiveHeaders(respH)
	}
	return map[string]any{
		"id": c.ID, "tunnel_id": c.TunnelID, "hostname": c.Hostname, "client_ip": c.ClientIP,
		"ts": c.TS, "method": c.Method, "path": c.Path, "query": c.Query,
		"status": c.Status, "duration_ms": c.DurationMS,
		"req_headers": reqH, "req_body": string(c.ReqBody), "req_body_truncated": c.ReqBodyTruncated,
		"resp_headers": respH, "resp_body": string(c.RespBody), "resp_body_truncated": c.RespBodyTruncated,
	}
}

func (s *Server) getRequestDetail(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	c, ok := s.captureFor(tenantID, r.PathValue("id"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found",
			"istek detayi bulunamadi (yakalama kapali olabilir veya kayit dusmus olabilir)")
		return
	}
	// F16: yakalama basliklari/govdeleri (cerez, Authorization) icerir; cagiranin
	// erisemedigi cihazin trafigi gosterilmez.
	if !s.requireCaptureDeviceAccess(w, r, tenantID, c.TunnelID) {
		return
	}
	priv, err := s.callerPrivileged(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, captureOut(c, priv))
}

// maskedHeaderValue, hassas baslik degerlerinin yerine konan maske.
const maskedHeaderValue = "••••••"

// sensitiveHeaderNames, degerleri owner/admin disina gosterilmeyen basliklar.
var sensitiveHeaderNames = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"set-cookie": true, "x-api-key": true, "x-auth-token": true,
	"x-csrf-token": true, "x-xsrf-token": true,
}

func isSensitiveHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if sensitiveHeaderNames[n] {
		return true
	}
	return strings.Contains(n, "api-key") || strings.Contains(n, "apikey") ||
		strings.Contains(n, "secret") || strings.HasSuffix(n, "-token")
}

// maskSensitiveHeaders, hassas basliklarin degerlerini maskeler (baslik adi
// kalir). Girdiyi DEGISTIRMEZ; yeni bir harita doner.
func maskSensitiveHeaders(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		if !isSensitiveHeader(k) {
			out[k] = v
			continue
		}
		m := make([]string, len(v))
		for i := range m {
			m[i] = maskedHeaderValue
		}
		out[k] = m
	}
	return out
}

// captureFor, yakalama deposu yoksa (nil) bulunamadi doner; panik yerine 404.
func (s *Server) captureFor(tenantID, id string) (*reqlog.Capture, bool) {
	if s.Captures == nil {
		return nil, false
	}
	return s.Captures.Get(tenantID, id)
}

// requireCaptureDeviceAccess, yakalamanin tunelini yayinlayan cihaza erisimi
// dogrular. Tunel silinmisse yalnizca cihaz politikasi olmayan (ayricalikli
// veya politikasiz kiraci) cagiran icin detay gosterilir.
func (s *Server) requireCaptureDeviceAccess(w http.ResponseWriter, r *http.Request, tenantID, tunnelID string) bool {
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, tunnelID)
	if err != nil {
		// Tunel yok: hangi cihaza ait oldugu bilinemez. Bos istemci kimligiyle
		// karar ver; politika varsa ve uye ayricaliksizsa reddedilir.
		return s.requireDeviceAccess(w, r, tenantID, "")
	}
	return s.requireDeviceAccess(w, r, tenantID, tun.ClientID)
}

// replayResponseMax, replay yanit govdesinin panele donen tavan boyutu.
const replayResponseMax = 256 << 10 // 256 KB

func (s *Server) replayRequest(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	// F-28: replay yakalanan (maskesiz) hassas basliklarla gonderilir; bu
	// yuzden yalniz owner/admin. Uye maskeli degeri gerceginin yerine koyamaz.
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	c, ok := s.captureFor(tenantID, r.PathValue("id"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not_found", "yeniden gonderilecek istek bulunamadi")
		return
	}
	// Orijinal istegin cihazina erisim (detayi gormek) sart.
	if !s.requireCaptureDeviceAccess(w, r, tenantID, c.TunnelID) {
		return
	}

	// Govde OPSIYONEL: govdesiz cagri eski davranistir (orijinali aynen gonder).
	ov, ok := decodeReplayOverrides(w, r)
	if !ok {
		return
	}
	method, path, query, headers, reqBody, targetTunnel, verr := applyReplayOverrides(c, ov)
	if verr != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_overrides", verr)
		return
	}

	// Tünel -> istemci -> oturum. Hedef tünel değiştirilebilir ama YALNIZCA
	// aynı kiracının tünelleri arasından: GetTunnel kiracı kapsamlıdır, bu da
	// başka kiracının tüneline istek attırmayı engeller.
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, targetTunnel)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "tunnel_not_found", "istegin tuneli artik yok")
		return
	}
	// F16: replay hedef cihaza istek attirir; politika ile kisitli bir uye
	// erisemedigi cihaza replay yapamamali (varlik sizmasin diye 404).
	if !s.requireDeviceAccess(w, r, tenantID, tun.ClientID) {
		return
	}
	sess, online := s.Hub.Get(tun.ClientID)
	if !online {
		writeJSONError(w, http.StatusBadGateway, "client_offline", "tunelin istemcisi bagli degil")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	hasBody := len(reqBody) > 0
	ex, err := sess.SendRequest(ctx, protocol.HTTPRequest{
		TunnelID: tun.ID, Method: method, Path: path, Query: query,
		Headers: headers, HasBody: hasBody, ContentLength: int64(len(reqBody)),
		RemoteAddr: clientIP(r),
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "replay_failed", "istek yeniden gonderilemedi: "+err.Error())
		return
	}
	defer sess.FinishExchange(ex.ReqID())

	if hasBody {
		if err := sess.StreamRequestBody(ctx, ex.ReqID(), io.NopCloser(bytes.NewReader(reqBody))); err != nil {
			writeJSONError(w, http.StatusBadGateway, "replay_failed", "govde iletilemedi: "+err.Error())
			return
		}
	}

	started := time.Now()
	select {
	case <-ctx.Done():
		writeJSONError(w, http.StatusGatewayTimeout, "timeout", "yeniden gonderim zaman asimina ugradi")
		return
	case head := <-ex.Head():
		if head.ErrCode != "" {
			writeJSONError(w, http.StatusBadGateway, head.ErrCode, head.ErrMsg)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(ex.Body(), replayResponseMax+1))
		truncated := len(body) > replayResponseMax
		if truncated {
			body = body[:replayResponseMax]
		}
		elapsed := time.Since(started).Milliseconds()

		// F09: orijinal yakalama ile karsilastirma. Orijinal govde kirpildiysa
		// diff yaniltici olur — bunu gizlemek yerine truncated ile bildiriyoruz.
		bodyKind, bodyDiff, bodyTrunc := diffBodies(c.RespBody, body)
		d := replayDiff{
			StatusChanged: c.Status != head.Status,
			OldStatus:     c.Status,
			NewStatus:     head.Status,
			OldDurationMS: c.DurationMS,
			NewDurationMS: elapsed,
			Headers:       diffHeaders(c.RespHeaders, head.Headers),
			BodyKind:      bodyKind,
			Body:          bodyDiff,
			Truncated:     bodyTrunc || truncated || c.RespBodyTruncated,
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": head.Status, "headers": head.Headers,
			"body": string(body), "body_truncated": truncated,
			"duration_ms": elapsed,
			"diff":        d,
		})
	}
}

// replayOverrides, F08 icin panelden gelen duzenlemeler. Tum alanlar
// opsiyoneldir; verilmeyen alan orijinal yakalamadan gelir.
//
// Isaretci kullanilmasinin nedeni: "" ile "verilmedi" ayrimi. Kullanici
// govdeyi KASITLI olarak bosaltabilmeli, bu orijinali korumakla ayni sey degil.
type replayOverrides struct {
	Method        *string           `json:"method"`
	Path          *string           `json:"path"`
	Query         *string           `json:"query"`
	Headers       map[string]string `json:"headers"`
	RemoveHeaders []string          `json:"remove_headers"`
	Body          *string           `json:"body"`
	TunnelID      *string           `json:"tunnel_id"`
}

// decodeReplayOverrides, opsiyonel istek govdesini cozer. Govde yoksa nil doner.
func decodeReplayOverrides(w http.ResponseWriter, r *http.Request) (*replayOverrides, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, replayRequestMax))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			"replay govdesi cok buyuk")
		return nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, true
	}
	var body struct {
		Overrides *replayOverrides `json:"overrides"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return nil, false
	}
	return body.Overrides, true
}

// replayRequestMax, duzenlenmis replay istek govdesinin ust siniri.
const replayRequestMax = 1 << 20 // 1 MB

// applyReplayOverrides, yakalamayi baz alip duzenlemeleri uygular.
// Dorduncu donus degeri bos degilse istek REDDEDILIR.
func applyReplayOverrides(c *reqlog.Capture, ov *replayOverrides) (
	method, path, query string, headers map[string][]string, body []byte, tunnelID, verr string,
) {
	method, path, query = c.Method, c.Path, c.Query
	tunnelID = c.TunnelID
	body = c.ReqBody
	headers = cloneHeaders(c.ReqHeaders)

	if ov == nil {
		return method, path, query, headers, body, tunnelID, ""
	}

	if ov.Method != nil {
		m := strings.ToUpper(strings.TrimSpace(*ov.Method))
		if !validHTTPMethod(m) {
			return "", "", "", nil, nil, "", "gecersiz method: " + m
		}
		method = m
	}
	if ov.Path != nil {
		p := strings.TrimSpace(*ov.Path)
		// Yol "/" ile baslamali: aksi halde hedefte beklenmeyen bir goreli
		// cozumleme olur ve istek sessizce baska bir yere gider.
		if !strings.HasPrefix(p, "/") {
			return "", "", "", nil, nil, "", "path '/' ile baslamali"
		}
		if strings.ContainsAny(p, " \t\r\n") {
			return "", "", "", nil, nil, "", "path bosluk veya satir sonu iceremez"
		}
		path = p
	}
	if ov.Query != nil {
		q := strings.TrimPrefix(strings.TrimSpace(*ov.Query), "?")
		if strings.ContainsAny(q, " \t\r\n") {
			return "", "", "", nil, nil, "", "query bosluk veya satir sonu iceremez"
		}
		query = q
	}
	if ov.TunnelID != nil {
		if t := strings.TrimSpace(*ov.TunnelID); t != "" {
			tunnelID = t
		}
	}
	if ov.Body != nil {
		body = []byte(*ov.Body)
	}

	for _, name := range ov.RemoveHeaders {
		deleteHeader(headers, name)
	}
	for name, value := range ov.Headers {
		if !validHeaderName(name) || strings.ContainsAny(value, "\r\n") {
			return "", "", "", nil, nil, "", "gecersiz baslik: " + name
		}
		deleteHeader(headers, name)
		headers[name] = []string{value}
	}

	// Content-Length HER ZAMAN govdeden yeniden hesaplanir; panelden gelen
	// eski deger govde ile uyusmazsa hedef sunucu istegi yanlis ayristirir.
	deleteHeader(headers, "Content-Length")

	return method, path, query, headers, body, tunnelID, ""
}

func cloneHeaders(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func deleteHeader(h map[string][]string, name string) {
	lower := strings.ToLower(strings.TrimSpace(name))
	for k := range h {
		if strings.ToLower(k) == lower {
			delete(h, k)
		}
	}
}

func validHTTPMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// validHeaderName, RFC 7230 token kurali. Bosluk/iki nokta/kontrol karakteri
// iceren bir ad, istek satirini bolerek baslik enjeksiyonuna yol acabilirdi.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r >= 0x7f {
			return false
		}
		if strings.ContainsRune(":()<>@,;\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}
