// Package session, tarayici oturumlarini DURUMSUZ (stateless) imzali cerezle
// yonetir. Bir oturum tablosu tutmak yerine cerezin icine {kim, ne zamana kadar}
// yazar ve HMAC-SHA256 ile imzalar; her istekte yalnizca imza dogrulanir.
//
// Neden DB tablosu degil: MVP tek sahipli ve az sayida oturum var; imzali cerez
// hem cgo gerektirmez. Cerez yalniz kimlik/kapsam + iptal sayaci (Epoch) tasir;
// rol ve uyelik her istekte middleware'de DB'den cozulur, logout ise
// session_epochs sayacini artirarak eski cerezleri iptal eder. Gizli anahtari
// degistirmek de tum oturumlari aninda gecersiz kilar.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// CookieName, oturum cerezinin adi.
const CookieName = "zorven_session"

// Manager, oturum cerezlerini uretir ve dogrular.
type Manager struct {
	secret []byte
}

// NewManager, verilen gizli anahtarla bir yonetici olusturur.
func NewManager(secret []byte) *Manager { return &Manager{secret: secret} }

// GenerateSecret, 32 baytlik rastgele bir gizli anahtar uretip base64 doner.
// Ilk calistirmada uretilip settings'e kaydedilmesi icindir.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

type payload struct {
	Sub    string `json:"sub"`    // GitHub kullanici adi (gosterim icin)
	UID    string `json:"uid"`    // users.id
	TID    string `json:"tid"`    // tenants.id — YETKI KAPSAMI BUDUR
	Method string `json:"method"` // "github"
	Exp    int64  `json:"exp"`    // unix saniye
	// Ver, cerez bicim surumu. 2'den kucuk (eski, epoch'suz) cerezler gecersiz:
	// iptal edilemezlerdi ve github cerezleri kullanici baglami tasimiyordu.
	Ver int `json:"v"`
	// Ep, sunucu tarafi iptal sayaci (session_epochs). Sunucudaki deger cerezdekinden
	// buyukse (logout vb.) cerez gecersizdir; karsilastirma middleware'dedir.
	Ep int64 `json:"ep"`
}

// cookieVersion, gecerli cerez bicim surumu.
const cookieVersion = 2

// Issue, verilen oturum icin ttl sureli imzali bir cerez degeri uretir.
//
// TenantID ZORUNLUDUR: cerez yetki kapsamini tasir, kiracisiz bir oturum
// anlamsizdir ve Verify tarafindan da reddedilir.
func (m *Manager) Issue(s Session, ttl time.Duration) (string, error) {
	if s.TenantID == "" {
		return "", errors.New("oturum kiracisiz uretilemez")
	}
	p := payload{
		Sub: s.Login, UID: s.UserID, TID: s.TenantID, Method: s.Method,
		Exp: time.Now().Add(ttl).Unix(), Ver: cookieVersion, Ep: s.Epoch,
	}
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString(body)
	sig := m.sign(b64)
	return b64 + "." + sig, nil
}

// Session, bir oturumun tasidigi bilgi.
//
// TenantID YETKI KAPSAMIDIR: tum yonetim istekleri bu kiraciya kilitlenir.
type Session struct {
	UserID   string
	TenantID string
	Login    string
	Method   string
	// Epoch, cerez uretildigindeki iptal sayaci; sunucudaki guncel deger ile
	// karsilastirilir (bkz. store.GetSessionEpoch).
	Epoch int64
}

var errBadSession = errors.New("gecersiz oturum")

// Verify, cerez degerini dogrular. Imza tutmuyorsa veya suresi dolduysa hata.
func (m *Manager) Verify(value string) (Session, error) {
	b64, sig, ok := strings.Cut(value, ".")
	if !ok {
		return Session{}, errBadSession
	}
	want := m.sign(b64)
	// Sabit zamanli karsilastirma: imza dogrulamasi zamanlama sizintisi vermesin.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return Session{}, errBadSession
	}
	body, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return Session{}, errBadSession
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return Session{}, errBadSession
	}
	if time.Now().Unix() > p.Exp {
		return Session{}, errBadSession
	}
	// Kiracisiz cerez (Plan 3 oncesinden kalmis olabilir) GECERSIZ: kullanici
	// yeniden giris yapsin. Kabul edilseydi yetki kapsami bos kalir ve kiraci
	// kontrolu sessizce cokerdi.
	if p.TID == "" {
		return Session{}, errBadSession
	}
	if p.Ver < cookieVersion {
		return Session{}, errBadSession
	}
	return Session{UserID: p.UID, TenantID: p.TID, Login: p.Sub, Method: p.Method, Epoch: p.Ep}, nil
}

func (m *Manager) sign(b64 string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(b64))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
