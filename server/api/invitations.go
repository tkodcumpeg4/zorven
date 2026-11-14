package api

// Ekip davetleri: davet HER ZAMAN e-posta ile gonderilir ve davetli kendi
// hesabiyla linke tiklayip KABUL edene kadar uye olmaz.
//
//	GET  /api/v1/invitations                      — oturumdaki e-postaya gelmis bekleyen davetler
//	GET  /api/v1/invitations/{id}                 — davet ayrintisi (yalnizca davetli e-postanin sahibi)
//	POST /api/v1/invitations/{id}/accept          — kabul et → uyelik
//	POST /api/v1/invitations/{id}/decline         — reddet
//	POST /api/v1/team/members/{id}/resend         — bekleyen daveti yeniden e-postala (ekip yonetimi)

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// invitationUser, davet uclari icin oturumdaki Better Auth kullanicisini doner.
// Davet kabulu bir KISININ onayidir; admin anahtari / API token'i kabul edemez.
func invitationUser(w http.ResponseWriter, r *http.Request) (*AuthUser, bool) {
	u, ok := userFromContext(r.Context())
	if !ok || u == nil || u.ID == "" || u.Email == "" || u.Role == "api_token" {
		writeJSONError(w, http.StatusForbidden, "user_session_required",
			"daveti kabul etmek icin kendi hesabinizla giris yapin")
		return nil, false
	}
	return u, true
}

func writeInvitationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "invitation_not_found", "davet bulunamadi")
	case errors.Is(err, store.ErrInvitationEmail):
		writeJSONError(w, http.StatusForbidden, "invitation_email_mismatch",
			"bu davet baska bir e-posta adresine gonderilmis; o adresle giris yapin")
	case errors.Is(err, store.ErrInvitationExpired):
		writeJSONError(w, http.StatusGone, "invitation_expired", "davetin suresi dolmus; yeni davet isteyin")
	case errors.Is(err, store.ErrInvitationClosed):
		writeJSONError(w, http.StatusConflict, "invitation_closed", "bu davet artik gecerli degil")
	default:
		return false
	}
	return true
}

func (s *Server) listMyInvitations(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	list, err := s.Store.ListInvitationsForEmail(r.Context(), u.Email)
	if err != nil {
		s.fail(w, err)
		return
	}
	pg, okPg := pageOf(w, r)
	if !okPg {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": slicePage(w, pg, list)})
}

func (s *Server) getInvitation(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	inv, err := s.Store.GetInvitation(r.Context(), r.PathValue("id"))
	if err != nil {
		if !writeInvitationError(w, err) {
			s.fail(w, err)
		}
		return
	}
	// Davetin ayrintisi yalnizca alicisina: aksi halde id'yi bilen herkes hangi
	// org'un kimi davet ettigini ogrenebilirdi.
	if !strings.EqualFold(inv.Email, u.Email) {
		writeInvitationError(w, store.ErrInvitationEmail)
		return
	}
	// Mevcut alanlar korunur; suresi dolmus bekleyen davet acikca isaretlenir
	// (panel Kabul/Reddet yerine "suresi dolmus" gosterir).
	expired := inv.Status == "pending" && !inv.ExpiresAt.After(time.Now())
	writeJSON(w, http.StatusOK, struct {
		store.TeamInvitation
		Expired bool `json:"expired"`
	}{inv, expired})
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	// Uye limiti kabulde de uygulanir: davetten sonra plan dusmus olabilir ya da
	// limit kontrolu oncesi gonderilmis eski davetler bulunabilir. Zaten uye olan
	// (idempotent kabul) kisi limite takilmaz.
	if s.Entitlements != nil {
		pre, err := s.Store.GetInvitation(r.Context(), r.PathValue("id"))
		if err != nil {
			if !writeInvitationError(w, err) {
				s.fail(w, err)
			}
			return
		}
		if strings.EqualFold(pre.Email, u.Email) && pre.Status == "pending" {
			role, err := s.Store.GetMemberRole(r.Context(), pre.OrganizationID, u.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			if role == "" {
				// Kabul de ayni kota kilidi altinda: es zamanli davet/kabul sayimi tutarli kalsin.
				unlock, ok := s.lockQuota(w, r, pre.OrganizationID, entitlements.QuotaMembers)
				if !ok {
					return
				}
				defer unlock()
				if err := s.Entitlements.CanAcceptMember(r.Context(), pre.OrganizationID); err != nil {
					writeEntitlementError(w, err)
					return
				}
			}
		}
	}
	inv, err := s.Store.AcceptInvitation(r.Context(), r.PathValue("id"), u.ID, u.Email)
	if err != nil {
		if !writeInvitationError(w, err) {
			s.fail(w, err)
		}
		return
	}
	// Uyelik degisti: bu kullanicinin onbellekteki oturumlari eski rol/kiraci
	// bilgisini tasimasin.
	if s.BetterAuth != nil {
		s.BetterAuth.InvalidateUser(u.ID)
	}
	s.audit(r, "team.invitation.accept", inv.ID, inv.OrganizationID)
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) declineInvitation(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeclineInvitation(r.Context(), r.PathValue("id"), u.Email); err != nil {
		if !writeInvitationError(w, err) {
			s.fail(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// resendInvitation, kiracinin bekleyen bir davetini yeniden e-postalar.
func (s *Server) resendInvitation(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	inv, err := s.Store.GetInvitation(r.Context(), r.PathValue("id"))
	if err != nil || inv.OrganizationID != tenantID {
		writeJSONError(w, http.StatusNotFound, "invitation_not_found", "davet bulunamadi")
		return
	}
	if inv.Status != "pending" {
		writeInvitationError(w, store.ErrInvitationClosed)
		return
	}
	// E-postayi bu istegi yapan (yeniden gonderen) yonetici gonderir: dil onunkidir.
	sent := s.sendInvitationEmail(inv, s.mailLangFor(r, actingUserID(r)))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email_sent": sent})
}

// invitationURL, davet kabul sayfasinin adresi.
func (s *Server) invitationURL(id string) string {
	return "https://panel." + s.platformDomainOr("zorven.app") + "/invite?id=" + id
}

// sendInvitationEmail, davet e-postasini gonderir; teslim edildiyse true.
// Alici kisisel bir adres de (gmail vb.) Zorven posta kutusu da
// (ad@mail.zorven.app) olabilir — ikisi de ayni relay'den gider.
// lang: e-postanin dili (tr/en/ja/zh); daveti GONDERENIN dilidir (bkz. invitation_mail.go).
func (s *Server) sendInvitationEmail(inv store.TeamInvitation, lang string) bool {
	if s.MailSender == nil {
		s.logger().Warn("davet e-postasi gonderilemedi: mail relay yapilandirilmamis", "invitation", inv.ID)
		return false
	}
	org := inv.OrganizationName
	if org == "" {
		org = inv.OrganizationID
	}
	subject, text, htmlBody := invitationMail(lang, strings.TrimSpace(inv.InviterName), org, inv.Role, inv.Email, s.invitationURL(inv.ID))
	from := "Zorven <no-reply@" + s.platformDomainOr("zorven.app") + ">"
	if _, err := s.MailSender.Send(from, inv.Email, subject, text, htmlBody, "", nil); err != nil {
		s.logger().Warn("davet e-postasi gonderilemedi", "invitation", inv.ID, "to", inv.Email, "hata", err)
		return false
	}
	return true
}
