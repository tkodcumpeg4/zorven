package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// mailClientEnabled, IMAP/SMTP gonderim erisiminin acik olup olmadigini soyler.
func (s *Server) mailClientEnabled() bool {
	return s.mailEnabled() && s.MailClient != nil
}

// mailClientInfo, panelin "Mail uygulamalarinda kullan" bolumu icin sunucu ayarlari.
func (s *Server) mailClientInfo() map[string]any {
	if !s.mailClientEnabled() {
		return map[string]any{"enabled": false}
	}
	c := s.MailClient
	return map[string]any{
		"enabled":          true,
		"host":             c.Host,
		"imap_port":        c.IMAPPort,
		"submission_port":  c.SubmissionPort, // 587 STARTTLS (0 = kapali)
		"submissions_port": c.SubmissionsTLS, // 465 SSL/TLS (0 = kapali)
	}
}

// resolveAppPasswordMailbox, istekte istenen kutuyu ve parolanin ait olacagi
// kiraciyi cozer. Bos / kiracinin kendi adresi: kiraci owner/admin'i. Sistem
// kutulari (info@ vb.): yalnizca platform admin.
func (s *Server) resolveAppPasswordMailbox(w http.ResponseWriter, r *http.Request, requested string) (tenantID, mailbox string, ok bool) {
	tid, okT := s.tenantFor(r)
	if !okT {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return "", "", false
	}
	if !s.mailClientEnabled() {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_client_disabled", "mail istemcisi erisimi bu sunucuda yapilandirilmadi")
		return "", "", false
	}
	own, _ := s.mailAddressFor(r, tid)
	req := strings.ToLower(strings.TrimSpace(requested))
	if req == "" || req == strings.ToLower(own) {
		if !s.requirePrivileged(w, r, tid) {
			return "", "", false
		}
		if own == "" {
			writeJSONError(w, http.StatusServiceUnavailable, "no_address", "bu kiraci icin mail adresi uretilemedi")
			return "", "", false
		}
		return tid, strings.ToLower(own), true
	}
	for _, sm := range s.SystemMailboxes {
		if strings.ToLower(sm) == req {
			if !isPlatformAdmin(r.Context()) {
				writeJSONError(w, http.StatusForbidden, "forbidden", "sistem posta kutulari icin uygulama parolasini yalnizca platform yoneticisi yonetebilir")
				return "", "", false
			}
			return store.DefaultTenantID, req, true
		}
	}
	writeJSONError(w, http.StatusForbidden, "forbidden", "bu posta kutusu icin yetkiniz yok")
	return "", "", false
}

// listMailAppPasswords (GET /api/v1/mail/app-passwords?mailbox=).
func (s *Server) listMailAppPasswords(w http.ResponseWriter, r *http.Request) {
	tenantID, mailbox, ok := s.resolveAppPasswordMailbox(w, r, r.URL.Query().Get("mailbox"))
	if !ok {
		return
	}
	list, err := s.Store.ListMailAppPasswords(r.Context(), tenantID, mailbox)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.MailAppPassword{}
	}
	writeJSON(w, http.StatusOK, list)
}

// createMailAppPassword (POST /api/v1/mail/app-passwords): {label, mailbox?}.
// Uretilen parola YALNIZCA bu yanitta doner.
func (s *Server) createMailAppPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label   string `json:"label"`
		Mailbox string `json:"mailbox"`
	}
	if !decode(w, r, &body) {
		return
	}
	tenantID, mailbox, ok := s.resolveAppPasswordMailbox(w, r, body.Mailbox)
	if !ok {
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "Mail uygulamasi"
	}
	if utf8.RuneCountInString(label) > 60 || strings.ContainsAny(label, "\r\n\x00") {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_label", "etiket en fazla 60 karakter olabilir")
		return
	}
	password := mail.GenerateAppPassword()
	hash, err := mail.HashAppPassword(password)
	if err != nil {
		s.fail(w, err)
		return
	}
	saved, err := s.Store.CreateMailAppPassword(r.Context(), store.MailAppPassword{
		TenantID: tenantID, Mailbox: mailbox, Label: label, PasswordHash: hash,
	})
	if err != nil {
		if errors.Is(err, store.ErrMailAppPasswordLimit) {
			writeJSONError(w, http.StatusConflict, "app_password_limit", "bu posta kutusu icin en fazla 20 aktif uygulama parolasi olabilir; once birini iptal edin")
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         saved.ID,
		"mailbox":    saved.Mailbox,
		"label":      saved.Label,
		"password":   password,
		"created_at": saved.CreatedAt,
	})
}

// revokeMailAppPassword (DELETE /api/v1/mail/app-passwords/{id}).
func (s *Server) revokeMailAppPassword(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.mailClientEnabled() {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_client_disabled", "mail istemcisi erisimi bu sunucuda yapilandirilmadi")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "parola id belirtilmedi")
		return
	}
	// Sistem kutularinin parolalari platform kiracisindadir: yalnizca platform admin.
	if tid == store.DefaultTenantID && !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "platform kiracisinin parolalarini yalnizca platform yoneticisi iptal edebilir")
		return
	}
	if tid != store.DefaultTenantID && !s.requirePrivileged(w, r, tid) {
		return
	}
	err := s.Store.RevokeMailAppPassword(r.Context(), tid, id)
	if errors.Is(err, store.ErrNotFound) && isPlatformAdmin(r.Context()) && tid != store.DefaultTenantID {
		err = s.Store.RevokeMailAppPassword(r.Context(), store.DefaultTenantID, id)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// mailMobileConfig (GET /api/v1/mail/mobileconfig?mailbox=): Apple Mail profili.
// Parola icermez; kullanici kurulumda uygulama parolasini girer.
func (s *Server) mailMobileConfig(w http.ResponseWriter, r *http.Request) {
	_, mailbox, ok := s.resolveAppPasswordMailbox(w, r, r.URL.Query().Get("mailbox"))
	if !ok {
		return
	}
	data := s.MailClient.MobileConfig(mailbox, "")
	w.Header().Set("Content-Type", "application/x-apple-aseprofile")
	w.Header().Set("Content-Disposition", `attachment; filename="zorven-mail.mobileconfig"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
