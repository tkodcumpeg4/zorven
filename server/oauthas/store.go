package oauthas

import (
	"context"
	"errors"
	"time"
)

// Depo hatalari.
var (
	// ErrNotFound: kayit yok (ya da suresi dolmus).
	ErrNotFound = errors.New("oauthas: kayit bulunamadi")
	// ErrUsed: tek kullanimlik kayit (yetkilendirme kodu / refresh token) daha once kullanilmis.
	ErrUsed = errors.New("oauthas: kayit zaten kullanilmis")
)

// Client, kayitli bir OAuth istemcisi (RFC 7591; yalniz kamusal istemciler).
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	GrantTypes   []string
	CreatedIP    string
	CreatedAt    time.Time
}

// Code, kisa omurlu yetkilendirme kodu kaydi (yalniz ozeti saklanir).
type Code struct {
	ClientID      string
	UserID        string
	TenantID      string
	RedirectURI   string
	Scope         string
	Resource      string
	CodeChallenge string
	ExpiresAt     time.Time
	// StepUpAt, "full" onayinin dayandigi taze girisin (oturum acilis) zamani;
	// full verilmediyse nil.
	StepUpAt *time.Time
}

// Grant, bir kullanicinin bir istemciye bir kiraci icin verdigi yetki.
// Access token'lar ve refresh token ailesi bu kayda baglidir.
type Grant struct {
	ID         string
	UserID     string
	TenantID   string
	ClientID   string
	ClientName string // okumada doldurulur
	Resource   string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	// StepUpAt, "full" kapsami verilirken dogrulanan taze girisin zamani.
	StepUpAt *time.Time
}

// IsFull, grant tam erisim kapsamini tasiyor mu.
func (g Grant) IsFull() bool {
	sc, _ := ParseScopes(g.Scope)
	return HasFull(sc)
}

// Active, grant iptal edilmemis mi.
func (g Grant) Active() bool { return g.RevokedAt == nil }

// Store, AS'in kalici depo arayuzu. pgstore ve bellek (test) uygular.
type Store interface {
	CreateClient(ctx context.Context, c Client) error
	GetClient(ctx context.Context, id string) (Client, error)
	CountClients(ctx context.Context) (int, error)

	// CreateCode kodu (ozet olarak) saklar.
	CreateCode(ctx context.Context, hash string, c Code) error
	// ConsumeCode kodu ATOMIK olarak kullanilmis isaretler. Yok/suresi dolmus
	// ErrNotFound, daha once kullanilmis ErrUsed doner; ErrUsed'da Code'un
	// ClientID/UserID/TenantID alanlari doludur (tekrar kullanimda iptal icin).
	ConsumeCode(ctx context.Context, hash string, now time.Time) (Code, error)

	CreateGrant(ctx context.Context, g Grant) error
	GetGrant(ctx context.Context, id string) (Grant, error)
	TouchGrant(ctx context.Context, id string, now time.Time) error
	// RevokeGrant grant'i ve refresh token ailesini iptal eder (idempotent).
	RevokeGrant(ctx context.Context, id string, now time.Time) error
	// ListGrants aktif grant'lari doner. userID bos degilse yalniz o kullanicinin,
	// tenantID bos degilse yalniz o kiracinin.
	ListGrants(ctx context.Context, userID, tenantID string) ([]Grant, error)

	SaveRefresh(ctx context.Context, hash, grantID string, expires time.Time) error
	// RotateRefresh refresh token'i ATOMIK olarak kullanilmis isaretler ve sahibi
	// grant'in kimligini doner. Yok/suresi dolmus ErrNotFound; zaten kullanilmis
	// (yeniden kullanim) ErrUsed + grantID doner (cagiran ailenin tamamini iptal eder).
	RotateRefresh(ctx context.Context, hash string, now time.Time) (grantID string, err error)

	// Prune suresi dolmus kodlari, refresh token'lari, eski iptalleri ve hic
	// kullanilmamis eski istemci kayitlarini siler.
	Prune(ctx context.Context, now time.Time) error
}
