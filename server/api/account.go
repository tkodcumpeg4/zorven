package api

// Hesap silme (kisisel veri) uclari.
//
//	GET  /api/v1/account/deletion-preview  oturumlu kullanici: hesap silinirse ne olacagi
//	                                       (silinecek organizasyonlar, ayrilacaklari, engeller)
//	POST /api/v1/internal/account/purge    YALNIZ web servisi (Better Auth beforeDelete):
//	                                       govde {"user_id":"...","dry_run":true|false}
//
// Hesabin kendisi Better Auth'ta silinir (parola + e-posta onay baglantisi:
// web/server/lib/auth.ts). Better Auth kullanici satirini silmeden once ic ucu
// cagirir: tek uyeli organizasyonlar "Organizasyonu sil" ile ayni adimlarla tum
// kaynaklariyla silinir, diger organizasyonlardaki uyelik/istemci/kisisel API
// anahtari kaldirilir, denetim kaydi anonimlestirilir (pgstore/account_delete.go).
// Ardindan bellekteki canli durum (istemci baglantilari, yakalama onbellegi,
// oturum onbellegi, yonlendirme) burada temizlenir.
//
// Ic uc PublicPaths'tedir (oturum cerezi tasimaz) ama kimligi YALNIZCA imzayla
// dogrular: HMAC-SHA256(AccountKey, "zorven-account-v1\n<unix saniye>\n<govde>").
// AccountKey, BETTER_AUTH_SECRET'tan HKDF ile turetilir (main.go keyPurposeAccount;
// web tarafi web/server/lib/account-internal.ts ayni turetimi yapar). Imza zaman
// asimi 2 dakikadir; islem idempotenttir (tekrar oynatma ayni sonucu verir).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	accountSigPrefix  = "zorven-account-v1"
	accountSigMaxSkew = 2 * time.Minute
	// Govde kucuktur ({"user_id","dry_run"}); buyuk govde reddedilir.
	accountBodyMax = 4 << 10
)

// Better Auth kullanici kimlikleri harf/rakam (+ _ -); baska bicim reddedilir.
var accountUserIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// accountSignature, web ile Go arasindaki istek imzasi (onek, zaman ve govde baglanir).
func accountSignature(key []byte, ts string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(accountSigPrefix))
	mac.Write([]byte("\n"))
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyAccountSignature, X-Zorven-Ts / X-Zorven-Sig basliklarini sabit zamanli dogrular.
func verifyAccountSignature(key []byte, tsHeader, sigHeader string, body []byte, now time.Time) bool {
	if len(key) == 0 {
		return false
	}
	sec, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return false
	}
	skew := now.Sub(time.Unix(sec, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > accountSigMaxSkew {
		return false
	}
	return hmac.Equal([]byte(accountSignature(key, tsHeader, body)), []byte(sigHeader))
}

func (s *Server) registerAccount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/account/deletion-preview", s.accountDeletionPreview)
	mux.HandleFunc("POST /api/v1/internal/account/purge", s.accountPurgeInternal)
}

// accountPlanResponse, plan + "silinebilir mi" bayragi.
type accountPlanResponse struct {
	store.AccountDeletionPlan
	Deletable bool `json:"can_delete"`
}

func planResponse(p store.AccountDeletionPlan) accountPlanResponse {
	return accountPlanResponse{AccountDeletionPlan: p, Deletable: p.CanDelete()}
}

// accountDeletionPreview, panelin "Hesabi sil" penceresi icin onizleme. Yalnizca
// oturum acmis kullanici kendi hesabi icin okur; API token'i ve admin anahtari
// kullanici kimligi tasimadigindan reddedilir.
func (s *Server) accountDeletionPreview(w http.ResponseWriter, r *http.Request) {
	if _, isToken := apiScopesFromContext(r.Context()); isToken {
		writeJSONError(w, http.StatusForbidden, "forbidden", "hesap bilgisi API token ile okunamaz; panelden oturum acin")
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusForbidden, "forbidden", "hesap bilgisi icin oturum acmis bir kullanici gerekli")
		return
	}
	plan, err := s.Store.PlanAccountDeletion(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, planResponse(plan))
}

// accountPurgeInternal, Better Auth'un beforeDelete kancasi (ve istek asamasindaki
// dry_run on kontrolu) icin ic uc.
func (s *Server) accountPurgeInternal(w http.ResponseWriter, r *http.Request) {
	if len(s.AccountKey) == 0 {
		writeJSONError(w, http.StatusServiceUnavailable, "not_configured", "hesap silme anahtari yapilandirilmamis")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, accountBodyMax+1))
	if err != nil || len(body) > accountBodyMax {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "govde okunamadi ya da cok buyuk")
		return
	}
	if !verifyAccountSignature(s.AccountKey, r.Header.Get("X-Zorven-Ts"), r.Header.Get("X-Zorven-Sig"), body, time.Now()) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_signature", "gecersiz ya da suresi dolmus imza")
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(body, &req); err != nil || !accountUserIDRe.MatchString(req.UserID) {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "gecersiz kullanici kimligi")
		return
	}

	if req.DryRun {
		plan, err := s.Store.PlanAccountDeletion(r.Context(), req.UserID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, planResponse(plan))
		return
	}

	res, err := s.Store.PurgeAccount(r.Context(), req.UserID)
	var blocked *store.AccountBlockedError
	if errors.As(err, &blocked) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{"code": "account_blocked", "message": blocked.Error()},
			"plan":  planResponse(blocked.Plan),
		})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.afterAccountPurge(req.UserID, res)
	s.logger().Info("hesap verisi silindi", "takma_ad", res.Pseudonym,
		"silinen_org", len(res.DeletedOrgs), "ayrilan_org", len(res.LeftOrgs),
		"iptal_token", res.RevokedTokens, "denetim_satiri", res.AuditRows)
	writeJSON(w, http.StatusOK, res)
}

// afterAccountPurge, silmeden sonra bellekteki canli durumu temizler
// (deleteOrganization ile ayni adimlar).
func (s *Server) afterAccountPurge(userID string, res store.AccountPurgeResult) {
	for _, o := range res.DeletedOrgs {
		// Bellek sicak onbelleginde kalan govde yakalamalari.
		s.Captures.ForgetTenant(o.ID)
	}
	// Silinen istemcilerin canli baglantilarini kopar: token'lari artik yok.
	if s.Hub != nil {
		for _, id := range res.ClientIDs {
			if sess, ok := s.Hub.Get(id); ok {
				sess.Close("account deleted")
			}
		}
	}
	// Oturum onbellegi (~30 sn) silinen organizasyonu/uyeligi tutmasin.
	if s.BetterAuth != nil {
		if len(res.DeletedOrgs) > 0 {
			// Platform yoneticisinin "yonetime gec" ile bu organizasyonu aktif secmis
			// oturumlari da olabilir (bkz. deleteOrganization).
			s.BetterAuth.InvalidateAll()
		} else {
			s.BetterAuth.InvalidateUser(userID)
		}
	}
	// Tuneller gitti; yonlendirme tablosu tazelenmeli.
	s.tunnelsChanged()
}
