package store

import (
	"context"
	"time"
)

// CaptchaSite, Zorven CAPTCHA sitesi (kiraciya ait widget + dogrulama anahtar cifti).
// SecretHash gizli anahtarin SHA-256 onaltilik ozetidir; API cevabina ASLA konmaz.
type CaptchaSite struct {
	ID         string   `json:"id"`
	TenantID   string   `json:"tenant_id"`
	Name       string   `json:"name"`
	SiteKey    string   `json:"site_key"`
	SecretHash string   `json:"-"`
	Domains    []string `json:"domains"`
	Difficulty int      `json:"difficulty"`
	// PassEnabled/PassTTLMinutes: dogrulama sonrasi birinci taraf cerez (zcap_pass).
	PassEnabled    bool      `json:"-"`
	PassTTLMinutes int       `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CaptchaSitePatch, kismi guncelleme. nil alan = degistirme. Domains yalniz
// SetDomains=true ise uygulanir (bos dilim "her alan adi" demektir).
type CaptchaSitePatch struct {
	Name           *string
	Domains        []string
	SetDomains     bool
	Difficulty     *int
	PassEnabled    *bool
	PassTTLMinutes *int
}

// CaptchaCounterDay, bir sitenin bir gunluk sayaclari.
type CaptchaCounterDay struct {
	SiteID       string    `json:"site_id"`
	Day          time.Time `json:"day"`
	Challenge    int64     `json:"challenge"`
	Solve        int64     `json:"solve"`
	VerifyOK     int64     `json:"verify_ok"`
	VerifyFail   int64     `json:"verify_fail"`
	Renewals     int64     `json:"renewals"`
	UniquePasses int64     `json:"unique_passes"`
}

// CaptchaCounterDelta, bir site icin toplu sayac artisi.
type CaptchaCounterDelta struct {
	SiteID       string
	Challenge    int64
	Solve        int64
	VerifyOK     int64
	VerifyFail   int64
	Renewals     int64
	UniquePasses int64
}

// CaptchaStore, CAPTCHA icin depo arayuzu. Store'a degil ayri arayuze konur:
// yalniz Postgres uygular; API katmani tip assertion ile alir, desteklemeyen
// depoda 501 doner.
type CaptchaStore interface {
	ListCaptchaSites(ctx context.Context, tenantID string) ([]CaptchaSite, error)
	GetCaptchaSite(ctx context.Context, tenantID, id string) (CaptchaSite, error)
	// GetCaptchaSiteByKey/BySecretHash kiraci kapsami olmadan arar (public uclar).
	GetCaptchaSiteByKey(ctx context.Context, siteKey string) (CaptchaSite, error)
	GetCaptchaSiteBySecretHash(ctx context.Context, secretHash string) (CaptchaSite, error)
	CreateCaptchaSite(ctx context.Context, in CaptchaSite) (CaptchaSite, error)
	UpdateCaptchaSite(ctx context.Context, tenantID, id string, p CaptchaSitePatch) (CaptchaSite, error)
	RotateCaptchaSiteSecret(ctx context.Context, tenantID, id, newSecretHash string) (CaptchaSite, error)
	// DeleteCaptchaSite siteyi ve sayaclarini siler.
	DeleteCaptchaSite(ctx context.Context, tenantID, id string) error

	// AddCaptchaCounters gunluk sayaclara delta ekler (upsert; toplu).
	AddCaptchaCounters(ctx context.Context, day time.Time, deltas []CaptchaCounterDelta) error
	// CaptchaCounters, since (dahil) sonrasi gunluk satirlari doner.
	CaptchaCounters(ctx context.Context, siteIDs []string, since time.Time) ([]CaptchaCounterDay, error)
	// PruneCaptchaCounters, before oncesi gunluk satirlari siler.
	PruneCaptchaCounters(ctx context.Context, before time.Time) error
}
