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
	"fmt"
	"html"
	"net/http"
	"strings"

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
	writeJSON(w, http.StatusOK, map[string]any{"invitations": list})
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
	writeJSON(w, http.StatusOK, inv)
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
	sent := s.sendInvitationEmail(inv)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email_sent": sent})
}

// invitationURL, davet kabul sayfasinin adresi.
func (s *Server) invitationURL(id string) string {
	return "https://panel." + s.platformDomainOr("zorven.app") + "/invite?id=" + id
}

// sendInvitationEmail, davet e-postasini gonderir; teslim edildiyse true.
// Alici kisisel bir adres de (gmail vb.) Zorven posta kutusu da
// (ad@mail.zorven.app) olabilir — ikisi de ayni relay'den gider.
func (s *Server) sendInvitationEmail(inv store.TeamInvitation) bool {
	if s.MailSender == nil {
		s.logger().Warn("davet e-postasi gonderilemedi: mail relay yapilandirilmamis", "invitation", inv.ID)
		return false
	}
	url := s.invitationURL(inv.ID)
	org := inv.OrganizationName
	if org == "" {
		org = inv.OrganizationID
	}
	roleTR := "üye"
	if inv.Role == "admin" {
		roleTR = "yönetici"
	}
	who := "Bir ekip yöneticisi"
	if strings.TrimSpace(inv.InviterName) != "" {
		who = inv.InviterName
	}
	subject := fmt.Sprintf("Zorven — %s ekibine davet edildin", org)
	text := fmt.Sprintf("Merhaba,\r\n\r\n%s seni Zorven'deki %q organizasyonuna %s olarak davet etti.\r\n\r\n"+
		"Daveti kabul etmek için bu adrese gidip %s adresine ait hesabınla giriş yap (hesabın yoksa ücretsiz oluştur):\r\n%s\r\n\r\n"+
		"Davet 7 gün geçerlidir. Bu daveti beklemiyorsan e-postayı yok sayabilirsin.\r\n",
		who, org, roleTR, inv.Email, url)
	from := "Zorven <no-reply@" + s.platformDomainOr("zorven.app") + ">"
	if _, err := s.MailSender.Send(from, inv.Email, subject, text, invitationHTML(who, org, roleTR, inv.Email, url), "", nil); err != nil {
		s.logger().Warn("davet e-postasi gonderilemedi", "invitation", inv.ID, "to", inv.Email, "hata", err)
		return false
	}
	return true
}

// invitationHTML, web/server/lib/email-templates.ts ile ayni marka/duzende davet maili.
func invitationHTML(who, org, role, email, url string) string {
	e := html.EscapeString
	return `<!doctype html><html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#0b0e12;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0b0e12;padding:32px 0;"><tr><td align="center">
<table role="presentation" width="480" cellpadding="0" cellspacing="0" style="width:480px;max-width:92%;background:#12161c;border:1px solid #232a33;border-radius:16px;overflow:hidden;">
<tr><td style="padding:28px 32px 8px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td style="vertical-align:middle;"><img src="https://zorven.app/logo-email.png" width="34" height="34" alt="Zorven" style="display:block;width:34px;height:34px;border-radius:9px;border:0;"></td>
<td style="vertical-align:middle;padding-left:12px;font-family:Arial,Helvetica,sans-serif;font-size:19px;font-weight:700;color:#e6edf3;letter-spacing:-0.3px;">Zorven</td>
</tr></table></td></tr>
<tr><td style="padding:12px 32px 32px 32px;font-family:Arial,Helvetica,sans-serif;color:#e6edf3;">
<h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:#e6edf3;">Ekibe davet edildin</h1>
<p style="margin:0;font-size:14px;line-height:1.6;color:#8b98a5;"><b style="color:#e6edf3;">` + e(who) + `</b> seni <b style="color:#e6edf3;">` + e(org) + `</b> organizasyonuna <b style="color:#e6edf3;">` + e(role) + `</b> olarak davet etti.</p>
<p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:#8b98a5;">Kabul etmek için <b style="color:#e6edf3;">` + e(email) + `</b> adresine ait hesabınla giriş yap. Hesabın yoksa bu adresle ücretsiz oluşturabilirsin.</p>
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0;"><tr><td style="border-radius:10px;background:#3ddc84;"><a href="` + e(url) + `" style="display:inline-block;padding:12px 26px;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:700;color:#0b0e12;text-decoration:none;border-radius:10px;">Daveti Görüntüle</a></td></tr></table>
<p style="margin:0;font-size:12px;line-height:1.6;color:#8b98a5;">Davet 7 gün geçerlidir. Bu daveti beklemiyorsan e-postayı yok sayabilirsin.</p>
<p style="font-size:12px;color:#8b98a5;line-height:1.5;margin:6px 0 0 0;">Buton çalışmazsa bu bağlantıyı tarayıcına yapıştır:<br><a href="` + e(url) + `" style="color:#3ddc84;word-break:break-all;">` + e(url) + `</a></p>
</td></tr></table>
<div style="font-family:Arial,Helvetica,sans-serif;font-size:11px;color:#8b98a5;padding:18px 0;">Zorven · Kendi sunucunda barındırılan tünel platformu</div>
</td></tr></table></body></html>`
}
