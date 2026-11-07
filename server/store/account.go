package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Hesap silme (kisisel veri). Plan, kullanicinin organizasyon uyeliklerini
// siniflandirir; PurgeAccount bu plana gore siler. Tipler burada, SQL
// pgstore/account_delete.go'dadir.

// Engel nedenleri (AccountOrg.Reason).
const (
	// AccountBlockSoleOwner: kullanici, BASKA uyeleri olan bir organizasyonun
	// tek sahibi. Once sahipligi devretmeli ya da organizasyonu silmeli.
	AccountBlockSoleOwner = "sole_owner"
	// AccountBlockPlatform: platform organizasyonu (ten_default) yoneticisi.
	// Platform hesabi panelden silinemez.
	AccountBlockPlatform = "platform"
)

// AccountOrg, hesap silme planinda gecen bir organizasyon.
type AccountOrg struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Members int    `json:"members"`
	// Reason yalniz Blockers listesinde dolu (AccountBlock*).
	Reason string `json:"reason,omitempty"`
	// Icerik sayilari yalniz silinecek organizasyonlar icin (onizleme metni).
	Tunnels int `json:"tunnels,omitempty"`
	Clients int `json:"clients,omitempty"`
	Domains int `json:"domains,omitempty"`
	Apps    int `json:"apps,omitempty"`
	Secrets int `json:"secrets,omitempty"`
}

// AccountDeletionPlan, bir kullanici hesabi silinirse ne olacagini anlatir.
type AccountDeletionPlan struct {
	UserID string `json:"user_id"`
	// Delete: kullanicinin TEK uyesi oldugu (kisisel) organizasyonlar; tum
	// kaynaklariyla (tunel, istemci, anahtar, alan adi, posta, uygulama) silinir.
	Delete []AccountOrg `json:"delete_orgs"`
	// Leave: baska uyeleri olan organizasyonlar; yalniz kullanicinin uyeligi,
	// istemcileri ve kisisel API anahtarlari kaldirilir.
	Leave []AccountOrg `json:"leave_orgs"`
	// Blockers: bos degilse hesap SILINEMEZ.
	Blockers []AccountOrg `json:"blocking_orgs"`
}

// CanDelete, silmeyi engelleyen bir durum olmadigini soyler.
func (p AccountDeletionPlan) CanDelete() bool { return len(p.Blockers) == 0 }

// AccountBlockedError, plan engel icerdigi icin silmenin reddedildigini bildirir.
type AccountBlockedError struct{ Plan AccountDeletionPlan }

func (e *AccountBlockedError) Error() string {
	names := make([]string, 0, len(e.Plan.Blockers))
	for _, b := range e.Plan.Blockers {
		names = append(names, b.Slug)
	}
	return fmt.Sprintf("hesap silinemez: engelleyen organizasyonlar: %s", strings.Join(names, ", "))
}

// AccountPurgeResult, PurgeAccount'un sonucu. Cagiran, bellekteki canli
// durumu (istemci baglantilari, yakalama onbellegi, yonlendirme) buna gore temizler.
type AccountPurgeResult struct {
	Pseudonym   string       `json:"pseudonym"`
	DeletedOrgs []AccountOrg `json:"deleted_orgs"`
	LeftOrgs    []AccountOrg `json:"left_orgs"`
	// ClientIDs: silinen istemciler (baglantilari kesilmeli).
	ClientIDs []string `json:"client_ids"`
	// RevokedTokens: iptal edilen kisisel API anahtari sayisi.
	RevokedTokens int `json:"revoked_tokens"`
	// AuditRows: takma ada cevrilen denetim kaydi sayisi.
	AuditRows int `json:"audit_rows"`
}

// AccountPseudonym, silinen kullanicinin denetim kayitlarinda kullanilan KARARLI
// takma adi: ayni kullanicinin tum kayitlari ayni takma adi tasir (iliskilendirilebilir)
// ama e-posta ya da kullanici kimligi geri cikarilamaz (tek yonlu ozet).
func AccountPseudonym(userID string) string {
	sum := sha256.Sum256([]byte("zorven-deleted-user-v1|" + userID))
	return "deleted-user-" + hex.EncodeToString(sum[:])[:10]
}
