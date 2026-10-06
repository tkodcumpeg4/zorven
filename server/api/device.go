package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

// deviceCodeTTL, bir eslestirme talebinin gecerli kalma suresi.
const deviceCodeTTL = 10 * time.Minute

// deviceMaxPending, bellekte tutulan bekleyen talep tavani. Uc PUBLIC oldugu
// icin sinirsiz harita bellek tuketme vektoru olurdu.
const deviceMaxPending = 10000

// deviceNameMax, cihaz adinin rune cinsinden ust siniri.
const deviceNameMax = 64

// deviceReq, bekleyen bir "tarayicidan giris" (device authorization) talebi.
type deviceReq struct {
	DeviceCode string
	UserCode   string
	Status     string // pending | approving | approved | denied
	Token      string // approved oldugunda dolar (tek kullanimlik)
	Name       string
	CreatedAt  time.Time
}

// DeviceAuth, bekleyen device-auth taleplerini bellekte tutar. Kisa omurlu
// olduklari icin kaliciliga gerek yok; sunucu restart olursa istemci yeniden
// eslesir.
type DeviceAuth struct {
	mu       sync.Mutex
	byDevice map[string]*deviceReq
	byUser   map[string]*deviceReq
}

// NewDeviceAuth, bos bir kayit defteri olusturur ve suresi dolanlari temizleyen
// arka plan dongusunu baslatir.
func NewDeviceAuth() *DeviceAuth {
	d := &DeviceAuth{
		byDevice: make(map[string]*deviceReq),
		byUser:   make(map[string]*deviceReq),
	}
	go d.gcLoop()
	return d
}

func (d *DeviceAuth) gcLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		cut := time.Now().Add(-deviceCodeTTL)
		d.mu.Lock()
		for k, r := range d.byDevice {
			if r.CreatedAt.Before(cut) {
				delete(d.byDevice, k)
				delete(d.byUser, r.UserCode)
			}
		}
		d.mu.Unlock()
	}
}

// decodeOptional, govdesi OPSIYONEL uclar icin: bos govde gecerlidir (v
// degismez); bozuk/bilinmeyen alanli govde 400 yazar ve false doner.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		return true
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "istek govdesi cok buyuk")
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// userCode, insanin tarayiciya yazabilecegi kisa kod (or. "K7QM-3XZP").
func userCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // karisan karakterler yok
	var sb strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			sb.WriteByte('-')
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		sb.WriteByte(alphabet[n.Int64()])
	}
	return sb.String()
}

// deviceCode, POST /api/v1/device/code — PUBLIC. Yeni bir eslestirme talebi
// olusturur ve istemciye kodlari doner.
func (s *Server) deviceCode(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.Devices = NewDeviceAuth()
	}
	req := &deviceReq{
		DeviceCode: randHex(24),
		UserCode:   userCode(),
		Status:     "pending",
		CreatedAt:  time.Now(),
	}
	var body struct {
		Name string `json:"name"`
	}
	// Govde OPSIYONEL: bos govde = varsayilan ad. decode bos govdede 400
	// yazdigi icin dogrudan cagrilamaz (cift yanit olurdu). Bozuk govde 400.
	if !decodeOptional(w, r, &body) {
		return
	}
	req.Name = strings.TrimSpace(body.Name)
	if rs := []rune(req.Name); len(rs) > deviceNameMax {
		req.Name = string(rs[:deviceNameMax])
	}
	if req.Name == "" {
		req.Name = "masaustu"
	}

	s.Devices.mu.Lock()
	if len(s.Devices.byDevice) >= deviceMaxPending {
		s.Devices.mu.Unlock()
		writeJSONError(w, http.StatusServiceUnavailable, "too_many_pending",
			"cok fazla bekleyen eslestirme talebi; biraz sonra tekrar deneyin")
		return
	}
	s.Devices.byDevice[req.DeviceCode] = req
	s.Devices.byUser[req.UserCode] = req
	s.Devices.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":      req.DeviceCode,
		"user_code":        req.UserCode,
		"verification_uri": "/device",
		"interval":         3,
		"expires_in":       int(deviceCodeTTL.Seconds()),
	})
}

// deviceToken, POST /api/v1/device/token — PUBLIC. Istemci bunu dongude
// yoklar; onaylaninca token'i TEK SEFER doner.
func (s *Server) deviceToken(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.Devices = NewDeviceAuth()
	}
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if !decode(w, r, &body) {
		return
	}
	// Durum ve token TEK kilit altinda okunur/temizlenir: onay ile yoklama es
	// zamanli calisir (veri yarisi) ve token yalnizca bir kez teslim edilmeli.
	s.Devices.mu.Lock()
	req, ok := s.Devices.byDevice[body.DeviceCode]
	var status, tok string
	var created time.Time
	if ok {
		status, created = req.Status, req.CreatedAt
		if status == "approved" {
			tok = req.Token
			delete(s.Devices.byDevice, req.DeviceCode)
			delete(s.Devices.byUser, req.UserCode)
		}
	}
	s.Devices.mu.Unlock()
	if !ok {
		writeJSONError(w, http.StatusNotFound, "invalid_device_code", "device_code bulunamadi veya suresi doldu")
		return
	}
	if status != "approved" && time.Since(created) > deviceCodeTTL {
		writeJSONError(w, http.StatusGone, "expired", "eslestirme talebinin suresi doldu")
		return
	}
	switch status {
	case "approved":
		writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "token": tok})
	case "denied":
		writeJSON(w, http.StatusOK, map[string]any{"status": "denied"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
	}
}

// deviceApprove, POST /api/v1/device/approve — GUARDED (dashboard'da giris
// yapmis kullanici). user_code'a karsilik gelen cihaza, kullanicinin kiracisi
// adina yeni bir istemci token'i baglar.
func (s *Server) deviceApprove(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.Devices = NewDeviceAuth()
	}
	var body struct {
		UserCode string `json:"user_code"`
	}
	if !decode(w, r, &body) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.UserCode))
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	// Talebi kilit altinda "approving" olarak SAHIPLEN: ayni kodu es zamanli
	// iki onay iki ayri istemci (ve iki token) uretmesin.
	s.Devices.mu.Lock()
	req, ok := s.Devices.byUser[code]
	if !ok || time.Since(req.CreatedAt) > deviceCodeTTL {
		s.Devices.mu.Unlock()
		writeJSONError(w, http.StatusNotFound, "invalid_user_code", "Kod bulunamadi veya suresi doldu.")
		return
	}
	if req.Status != "pending" {
		s.Devices.mu.Unlock()
		writeJSONError(w, http.StatusConflict, "already_approved", "Bu kod zaten onaylandi.")
		return
	}
	req.Status = "approving"
	name := req.Name
	s.Devices.mu.Unlock()

	// Hata yolunda talep tekrar denenebilir kalsin.
	release := func() {
		s.Devices.mu.Lock()
		if req.Status == "approving" {
			req.Status = "pending"
		}
		s.Devices.mu.Unlock()
	}

	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateClient(r.Context(), tenantID); err != nil {
			release()
			writeEntitlementError(w, err)
			return
		}
	}
	full, tokenID, hash, err := auth.GenerateClient()
	if err != nil {
		release()
		s.fail(w, err)
		return
	}
	// Panelde secili proje varsa istemci o projeye baglanir (createClient ile ayni).
	projID, _ := s.projectFor(r)
	if _, err := s.Store.CreateClientWithProject(r.Context(), tenantID, name, tokenID, hash, projID); err != nil {
		release()
		s.fail(w, err)
		return
	}

	s.Devices.mu.Lock()
	req.Status = "approved"
	req.Token = full
	s.Devices.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}
