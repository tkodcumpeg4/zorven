package api

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// mailEnabled, webmail'in yapilandirilip yapilandirilmadigini soyler.
func (s *Server) mailEnabled() bool {
	return strings.TrimSpace(s.MailDomain) != ""
}

// mailAddressFor, kiracinin webmail adresini (slug@domain) uretir.
func (s *Server) mailAddressFor(r *http.Request, tenantID string) (string, bool) {
	if !s.mailEnabled() {
		return "", false
	}
	t, err := s.Store.GetTenant(r.Context(), tenantID)
	if err != nil || strings.TrimSpace(t.Slug) == "" {
		return "", false
	}
	return t.Slug + "@" + s.MailDomain, true
}

// mailInfo (GET /api/v1/mail/info): kiracinin mail adresi, aktiflik ve
// okunmamis sayisi.
func (s *Server) mailInfo(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.mailEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "address": "", "unseen": 0})
		return
	}
	addr, _ := s.mailAddressFor(r, tenantID)
	unseen, err := s.Store.CountUnseenMail(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Harici gonderim yalnizca Enterprise (veya platform admin). Digerleri ic
	// (@mail.<domain>) adreslere gonderebilir.
	canExternal := isPlatformAdmin(r.Context())
	if !canExternal {
		if sub, e := s.Store.GetSubscription(r.Context(), tenantID); e == nil {
			canExternal = strings.ToLower(strings.TrimSpace(sub.Plan)) == "enterprise"
		}
	}
	// Sistem posta kutulari YALNIZCA platform admin'e (owner) gosterilir.
	var systemAddrs []string
	if isPlatformAdmin(r.Context()) {
		systemAddrs = s.SystemMailboxes
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":           true,
		"address":           addr,
		"domain":            s.MailDomain,
		"unseen":            unseen,
		"can_send_external": canExternal,
		"system_addresses":  systemAddrs,
	})
}

// listMail (GET /api/v1/mail/messages?box=inbox|sent): mesaj listesi.
func (s *Server) listMail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	direction := "inbound"
	if r.URL.Query().Get("box") == "sent" {
		direction = "outbound"
	}
	msgs, err := s.Store.ListMailMessages(r.Context(), tenantID, direction, 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	if msgs == nil {
		msgs = []store.MailMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// getMail (GET /api/v1/mail/messages/{id}): tam icerik; gelen mesaji okundu isaretler.
func (s *Server) getMail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "mesaj id belirtilmedi")
		return
	}
	m, err := s.Store.GetMailMessage(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if m.Direction == "inbound" && !m.Seen {
		_ = s.Store.MarkMailSeen(r.Context(), tenantID, id)
		m.Seen = true
	}
	atts, _ := s.Store.ListMailAttachments(r.Context(), tenantID, id)
	if atts == nil {
		atts = []store.MailAttachment{}
	}
	writeJSON(w, http.StatusOK, struct {
		store.MailMessage
		Attachments []store.MailAttachment `json:"attachments"`
	}{m, atts})
}

// getMailAttachment (GET /api/v1/mail/attachments/{id}): eki indirir.
func (s *Server) getMailAttachment(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "ek id belirtilmedi")
		return
	}
	a, err := s.Store.GetMailAttachment(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(a.Filename)+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Content)
}

// sanitizeFilename, Content-Disposition basligina gomulecek dosya adindan
// tehlikeli karakterleri (CR/LF/quote) temizler.
func sanitizeFilename(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "", "\"", "'").Replace(strings.TrimSpace(name))
	if name == "" {
		return "dosya"
	}
	return name
}

// deleteMail (DELETE /api/v1/mail/messages/{id}).
func (s *Server) deleteMail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "mesaj id belirtilmedi")
		return
	}
	if err := s.Store.DeleteMailMessage(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// sendMail (POST /api/v1/mail/send): {to, subject, body, in_reply_to?}.
// Gonderen adresi kiracinin kendi adresidir (slug@domain). Giden kopya kaydedilir.
func (s *Server) sendMail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.mailEnabled() || s.MailSender == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_disabled", "webmail bu sunucuda yapilandirilmadi")
		return
	}

	var body struct {
		To        string `json:"to"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
		InReplyTo string `json:"in_reply_to"`
		// From (opsiyonel): YALNIZCA platform admin bir site sistem adresinden
		// (info@zorven.app ...) gonderebilir. Bos ise kiracinin kendi adresi.
		From string `json:"from"`
		// Attachments (opsiyonel): kullanicinin yukledigi ekler (base64 icerik).
		Attachments []struct {
			Filename      string `json:"filename"`
			ContentType   string `json:"content_type"`
			ContentBase64 string `json:"content_base64"`
		} `json:"attachments"`
	}
	if !decode(w, r, &body) {
		return
	}
	to := strings.TrimSpace(body.To)
	if to == "" || !strings.Contains(to, "@") {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_recipient", "gecerli bir alici adresi girin")
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "empty_body", "mesaj govdesi bos olamaz")
		return
	}

	// Plan siniri: Enterprise OLMAYAN kiracilar YALNIZCA ic adreslere
	// (@mail.<domain>) gonderebilir. Harici (or. gmail.com) gonderim yalnizca
	// Enterprise planinda; platform admin (super-admin) her zaman gonderebilir.
	recipientDomain := ""
	if at := strings.LastIndex(to, "@"); at >= 0 {
		recipientDomain = strings.ToLower(to[at+1:])
	}
	if recipientDomain != s.MailDomain && !isPlatformAdmin(r.Context()) {
		plan := ""
		if sub, err := s.Store.GetSubscription(r.Context(), tenantID); err == nil {
			plan = strings.ToLower(strings.TrimSpace(sub.Plan))
		}
		if plan != "enterprise" {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"code":  "external_mail_not_allowed",
				"error": "Harici e-posta gonderimi yalnizca Enterprise planinda kullanilabilir. Mevcut planinizda yalnizca @" + s.MailDomain + " adreslerine gonderebilirsiniz.",
			})
			return
		}
	}

	from, okAddr := s.mailAddressFor(r, tenantID)
	if !okAddr {
		writeJSONError(w, http.StatusServiceUnavailable, "no_address", "bu kiraci icin mail adresi uretilemedi")
		return
	}

	// Site sistem adresinden gonderim: yalnizca platform admin + whitelist.
	if reqFrom := strings.TrimSpace(strings.ToLower(body.From)); reqFrom != "" && reqFrom != strings.ToLower(from) {
		allowed := false
		if isPlatformAdmin(r.Context()) {
			for _, sm := range s.SystemMailboxes {
				if strings.ToLower(sm) == reqFrom {
					allowed = true
					from = sm
					break
				}
			}
		}
		if !allowed {
			writeJSONError(w, http.StatusForbidden, "from_not_allowed", "bu gonderen adresinden gonderme yetkiniz yok")
			return
		}
	}

	// Ekleri base64'ten coz.
	var atts []mail.Attachment
	for _, a := range body.Attachments {
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(a.ContentBase64))
		if derr != nil || len(raw) == 0 {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_attachment", "ek dosya cozulemedi (base64)")
			return
		}
		fn := strings.TrimSpace(a.Filename)
		if fn == "" {
			fn = "dosya"
		}
		ct := strings.TrimSpace(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		atts = append(atts, mail.Attachment{Filename: fn, ContentType: ct, Content: raw})
	}

	subject := strings.TrimSpace(body.Subject)
	messageID, err := s.MailSender.Send(from, to, subject, body.Body, strings.TrimSpace(body.InReplyTo), atts)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "send_failed", "mail gonderilemedi: "+err.Error())
		return
	}

	saved, err := s.Store.InsertMailMessage(r.Context(), store.MailMessage{
		TenantID:  tenantID,
		Direction: "outbound",
		From:      from,
		To:        to,
		Subject:   subject,
		TextBody:  body.Body,
		MessageID: messageID,
		InReplyTo: strings.TrimSpace(body.InReplyTo),
		Seen:      true,
	})
	if err != nil {
		// Mail gitti ama kayit tutulamadi — kullaniciya basari don, sadece logla.
		s.logMailWarn("giden mail kaydedilemedi", err)
		writeJSON(w, http.StatusCreated, map[string]any{"success": true, "message_id": messageID})
		return
	}
	// Giden ekleri de sakla (kayitli mesaja bagli).
	for _, a := range atts {
		_ = s.Store.InsertMailAttachment(r.Context(), store.MailAttachment{
			MessageID: saved.ID, TenantID: tenantID,
			Filename: a.Filename, ContentType: a.ContentType,
			SizeBytes: int64(len(a.Content)), Content: a.Content,
		})
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) logMailWarn(msg string, err error) {
	if s.Logger != nil {
		s.Logger.Warn(msg, "error", err)
	}
}
