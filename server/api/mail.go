package api

import (
	"encoding/base64"
	"errors"
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
		"can_send_external": true,
		"system_addresses":  systemAddrs,
		"client_access":     s.mailClientInfo(),
	})
}

// listMail (GET /api/v1/mail/messages?box=inbox|sent|trash|drafts|<klasor adi>): mesaj listesi.
// Liste KLASOR esas alinarak yapilir (IMAP ile tasinan mesajlar dogru listede cikar).
func (s *Server) listMail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	folder, okBox := mailBoxFolder(r.URL.Query().Get("box"))
	if !okBox {
		writeJSONError(w, http.StatusBadRequest, "invalid_box", "gecersiz klasor adi")
		return
	}
	msgs, err := s.Store.ListMailMessages(r.Context(), tenantID, folder, 200)
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
	if !m.Seen {
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
	// Alici TEK ayristiricidan gecer (mail.ParseAddress); plan kisiti ve SMTP
	// zarfi bu yalin adresten turetilir. Liste, tirnakli yerel kisim, CR/LF reddedilir.
	to, perr := mail.ParseAddress(body.To)
	if perr != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_recipient", "gecerli tek bir alici adresi girin")
		return
	}
	inReplyTo := strings.TrimSpace(body.InReplyTo)
	if !mail.ValidHeaderValue(inReplyTo) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_in_reply_to", "in_reply_to gecersiz karakter iceriyor")
		return
	}
	if !mail.ValidHeaderValue(body.Subject) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_subject", "konu satir sonu iceremez")
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "empty_body", "mesaj govdesi bos olamaz")
		return
	}

	// Acik surumde harici alicilara gonderim serbesttir (plan siniri yok).
	// Zarf alicisi ayrisan/dogrulanan adrestir (to, yukarida dogrulandi).

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

	messageID, err := s.MailSender.Send(from, to, subject, body.Body, "", inReplyTo, atts)
	if errors.Is(err, mail.ErrNoSuchMailbox) {
		writeJSONError(w, http.StatusUnprocessableEntity, "unknown_recipient", "bu adreste bir posta kutusu yok: "+to)
		return
	}
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
		InReplyTo: inReplyTo,
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

// mailBoxFolder, panelin box parametresini mail_messages.folder degerine cevirir.
// "" ve inbox gelen kutusudur; sistem adlari disindakiler kullanici klasoru adidir.
func mailBoxFolder(box string) (string, bool) {
	switch strings.ToLower(box) {
	case "", "inbox":
		return store.MailFolderInbox, true
	case "sent":
		return store.MailFolderSent, true
	case "trash":
		return store.MailFolderTrash, true
	case "drafts":
		return store.MailFolderDrafts, true
	}
	if store.ValidateMailFolderName(box) != nil {
		return "", false
	}
	return store.MailUserFolderKey(box), true
}

// listMailFolders (GET /api/v1/mail/folders): kiracinin kullanici klasorleri
// (IMAP ile olusturulanlar), ada gore sirali. Sistem klasorleri dahil degildir.
func (s *Server) listMailFolders(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	names, err := s.Store.ListTenantMailFolderNames(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": names})
}

// moveMail (POST /api/v1/mail/messages/{id}/move): {folder}. Hedef bir sistem
// klasoru (inbox|trash|drafts|sent) veya kiracida var olan bir kullanici klasorudur.
func (s *Server) moveMail(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Folder string `json:"folder"`
	}
	if !decode(w, r, &body) {
		return
	}
	target, okBox := mailBoxFolder(body.Folder)
	if !okBox || strings.TrimSpace(body.Folder) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_folder", "gecersiz hedef klasor")
		return
	}
	if name, isUser := store.MailUserFolderName(target); isUser {
		names, err := s.Store.ListTenantMailFolderNames(r.Context(), tenantID)
		if err != nil {
			s.fail(w, err)
			return
		}
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "folder_not_found", "klasor bulunamadi")
			return
		}
	}
	if _, err := s.Store.MoveMailMessage(r.Context(), tenantID, id, target); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "folder": body.Folder})
}
