package store

import (
	"context"
	"errors"
	"time"
)

// ErrUpdateSlugTaken, ayni slug ile baska bir guncelleme yazisi varken doner.
var ErrUpdateSlugTaken = errors.New("guncelleme slug zaten kullanimda")

// Guncelleme yazisi turleri.
const (
	UpdateKindRelease     = "release"
	UpdateKindUpdate      = "update"
	UpdateKindMaintenance = "maintenance"
)

// UpdateEmailStats, toplu duyuru e-postasinin sayaclari.
type UpdateEmailStats struct {
	Total  int `json:"total"`
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
	// Done, kuyruk bittiginde true olur.
	Done bool `json:"done"`
	// Interrupted, kuyruk bitmeden surec durdugunda (yeniden baslatma) true olur;
	// otomatik devam ETMEZ, yonetici "Devam et" ile surdurur.
	Interrupted bool `json:"interrupted"`
}

// Alici basina gonderim durumlari (updates_email_deliveries.status).
const (
	DeliverySent   = "sent"
	DeliveryFailed = "failed"
)

// Toplu gonderim kipleri (ClaimUpdatePostResume).
const (
	SendModeResume      = "resume"       // yalniz henuz gonderilmemisler
	SendModeRetryFailed = "retry_failed" // yalniz status=failed olanlar
)

// UpdatePost, "Guncellemeler" yazisi (surum notu / guncelleme / planli bakim).
type UpdatePost struct {
	ID               string           `json:"id"`
	Slug             string           `json:"slug"`
	Kind             string           `json:"kind"`
	Version          string           `json:"version"`
	TitleTR          string           `json:"title_tr"`
	BodyTR           string           `json:"body_tr"`
	TitleEN          string           `json:"title_en"`
	BodyEN           string           `json:"body_en"`
	TitleJA          string           `json:"title_ja"`
	BodyJA           string           `json:"body_ja"`
	MaintenanceStart *time.Time       `json:"maintenance_start"`
	MaintenanceEnd   *time.Time       `json:"maintenance_end"`
	PublishedAt      *time.Time       `json:"published_at"` // nil = taslak
	CreatedBy        string           `json:"created_by"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	EmailedAt        *time.Time       `json:"emailed_at"`
	EmailStats       UpdateEmailStats `json:"email_stats"`
}

// AnnouncementRecipient, duyuru e-postasi alacak (dogrulanmis, abone) kullanici.
type AnnouncementRecipient struct {
	UserID string
	Email  string
	Lang   string // "" = bilinmiyor (tr'ye duser)
	// Verified, e-posta dogrulanmis mi. Depo yalniz dogrulanmislari doner; kuyruk
	// yine de ikinci kez kontrol eder (savunma derinligi).
	Verified bool
}

// AnnouncementPref, kullanicinin duyuru tercihi. OPT-IN: satir yoksa
// Subscribed=false ve Asked=false (henuz sorulmadi). Satir varsa Asked=true;
// subscribed=false satiri "reddetti / abonelikten cikti" demektir.
type AnnouncementPref struct {
	Subscribed bool   `json:"subscribed"`
	Asked      bool   `json:"asked"`
	Lang       string `json:"lang"`
	Source     string `json:"source"` // signup | profile | banner | unsub
}

// UpdatesStore, Guncellemeler ozelligi icin depo arayuzu. Store'a degil ayri
// arayuze konur: yalniz Postgres uygular, API katmani tip assertion ile alir.
type UpdatesStore interface {
	// ListUpdatePosts, yazilari yeniden eskiye listeler. onlyPublished=true ise
	// yalniz yayinlanmislar (published_at <= simdi). Toplam sayiyi da doner.
	ListUpdatePosts(ctx context.Context, onlyPublished bool, limit, offset int) ([]UpdatePost, int, error)
	GetUpdatePost(ctx context.Context, id string) (UpdatePost, error)
	GetUpdatePostBySlug(ctx context.Context, slug string, onlyPublished bool) (UpdatePost, error)
	CreateUpdatePost(ctx context.Context, p UpdatePost) (UpdatePost, error)
	UpdateUpdatePost(ctx context.Context, p UpdatePost) (UpdatePost, error)
	DeleteUpdatePost(ctx context.Context, id string) error
	SetUpdatePostPublished(ctx context.Context, id string, publish bool) (UpdatePost, error)

	// ClaimUpdatePostEmail, toplu gonderimi ATOMIK olarak sahiplenir: yazi
	// yayinlanmis ve (force || emailed_at bos) ise emailed_at'i ayarlar ve true
	// doner. Aksi halde false (idempotans).
	ClaimUpdatePostEmail(ctx context.Context, id string, force bool, total int) (bool, error)
	SetUpdatePostEmailStats(ctx context.Context, id string, st UpdateEmailStats) error

	// ClaimUpdatePostResume, yarim kalmis gonderimi (resume: done=false;
	// retry_failed: done=true ve failed>0) atomik sahiplenir; emailed_at degismez.
	ClaimUpdatePostResume(ctx context.Context, id, mode string, total int) (bool, error)
	// MarkInterruptedUpdateSends, acilista bitmemis (done=false) gonderimleri
	// interrupted=true yapar; sayisini doner. Otomatik devam etmez.
	MarkInterruptedUpdateSends(ctx context.Context) (int, error)

	// Alici basina gonderim gunlugu (updates_email_deliveries).
	RecordUpdateDelivery(ctx context.Context, postID, userID, status, errMsg string) error
	// UpdateDeliveryStatuses, verilen kullanicilarin durumunu doner (kaydi olmayan yok).
	UpdateDeliveryStatuses(ctx context.Context, postID string, userIDs []string) (map[string]string, error)
	CountUpdateDeliveries(ctx context.Context, postID string) (sent, failed int, err error)
	// ResetUpdateDeliveries, "herkese yeniden gonder" (force) oncesi gunlugu siler.
	ResetUpdateDeliveries(ctx context.Context, postID string) error

	// ListAnnouncementRecipients, dogrulanmis e-postali ve abone kullanicilari
	// kullanici kimligine gore anahtar-sayfalamayla doner (afterUserID'den sonra).
	ListAnnouncementRecipients(ctx context.Context, afterUserID string, limit int) ([]AnnouncementRecipient, error)
	CountAnnouncementRecipients(ctx context.Context) (int, error)

	GetAnnouncementPref(ctx context.Context, userID string) (AnnouncementPref, error)
	// SetAnnouncementPref tercihi yazar; lang bos ise mevcut dil korunur.
	// source: signup | profile | banner | unsub. subscribed=true ise consented_at yazilir.
	SetAnnouncementPref(ctx context.Context, userID string, subscribed bool, lang, source string) error
	// SetAnnouncementLang yalniz e-posta dilini degistirir (panel dili degisince);
	// abonelik, onay kaynagi ve onay zamani DEGISMEZ. Satir yoksa hicbir sey yapmaz.
	SetAnnouncementLang(ctx context.Context, userID, lang string) error
	// IsAnnouncementSubscribed gonderim aninda son kontrol icindir (satir yoksa false: opt-in).
	IsAnnouncementSubscribed(ctx context.Context, userID string) (bool, error)
}
