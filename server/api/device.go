package api

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

// deviceCodeTTL, bir eslestirme talebinin gecerli kalma suresi.
const deviceCodeTTL = 10 * time.Minute

// deviceReq, bekleyen bir "tarayicidan giris" (device authorization) talebi.
type deviceReq struct {
	DeviceCode string
	UserCode   string
	Status     string // pending | approved | denied
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
	_ = decode(w, r, &body) // ad opsiyonel; hata onemsiz
	req.Name = strings.TrimSpace(body.Name)
	if req.Name == "" {
		req.Name = "masaustu"
	}

	s.Devices.mu.Lock()
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
	s.Devices.mu.Lock()
	req, ok := s.Devices.byDevice[body.DeviceCode]
	s.Devices.mu.Unlock()
	if !ok {
		writeJSONError(w, http.StatusNotFound, "invalid_device_code", "device_code bulunamadi veya suresi doldu")
		return
	}
	if time.Since(req.CreatedAt) > deviceCodeTTL {
		writeJSONError(w, http.StatusGone, "expired", "eslestirme talebinin suresi doldu")
		return
	}
	switch req.Status {
	case "approved":
		s.Devices.mu.Lock()
		tok := req.Token
		// Tek kullanimlik: token'i teslim ettik, kaydi temizle.
		delete(s.Devices.byDevice, req.DeviceCode)
		delete(s.Devices.byUser, req.UserCode)
		s.Devices.mu.Unlock()
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
	s.Devices.mu.Lock()
	req, ok := s.Devices.byUser[code]
	s.Devices.mu.Unlock()
	if !ok || time.Since(req.CreatedAt) > deviceCodeTTL {
		writeJSONError(w, http.StatusNotFound, "invalid_user_code", "Kod bulunamadi veya suresi doldu.")
		return
	}
	if req.Status == "approved" {
		writeJSONError(w, http.StatusConflict, "already_approved", "Bu kod zaten onaylandi.")
		return
	}

	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateClient(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	full, tokenID, hash, err := auth.GenerateClient()
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Store.CreateClient(r.Context(), tenantID, req.Name, tokenID, hash); err != nil {
		s.fail(w, err)
		return
	}

	s.Devices.mu.Lock()
	req.Status = "approved"
	req.Token = full
	s.Devices.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": req.Name})
}
