package api

// Guncellemeler (surum / guncelleme / bakim duyurulari) ve duyuru e-postasi.
//
//	GET    /api/v1/public/updates                 — herkese acik, yalniz yayinlanmislar (lang, page, limit)
//	GET    /api/v1/admin/updates                  — platform admin: tum yazilar (taslak dahil)
//	POST   /api/v1/admin/updates                  — olustur (taslak)
//	GET    /api/v1/admin/updates/{id}             — tek yazi
//	PUT    /api/v1/admin/updates/{id}             — guncelle
//	DELETE /api/v1/admin/updates/{id}             — sil
//	POST   /api/v1/admin/updates/{id}/publish     — yayinla
//	POST   /api/v1/admin/updates/{id}/unpublish   — taslaga al
//	POST   /api/v1/admin/updates/preview          — Markdown onizleme (guvenli HTML)
//	POST   /api/v1/admin/updates/import           — JSON ice aktarma (bkz. updates_import.go)
//	GET    /api/v1/admin/updates/export           — JSON disa aktarma
//	GET    /api/v1/admin/updates/subscribers     — duyuru alacak kullanici sayisi
//	POST   /api/v1/admin/updates/{id}/test-email  — yalniz oturumdaki admine test e-postasi
//	POST   /api/v1/admin/updates/{id}/send        — abonelere gonder (idempotent). Govde: action=send|resume|retry_failed;
//	                                               force=true + confirm="RESEND" herkese yeniden gonderir
//	GET    /api/v1/announcements/preference       — oturumdaki kullanicinin tercihi
//	PUT    /api/v1/announcements/preference       — tercihi degistir
//	PUT    /api/v1/announcements/preference/lang  — yalniz e-posta dilini panel diline esitle

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/updates"
)

// PublicUpdatesPath, kimlik istemeyen liste ucu (main.go PublicPaths'e eklenir).
const PublicUpdatesPath = "/api/v1/public/updates"

func (s *Server) registerUpdates(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PublicUpdatesPath, s.publicUpdates)
	mux.HandleFunc("GET /api/v1/admin/updates", s.adminListUpdates)
	mux.HandleFunc("POST /api/v1/admin/updates", s.adminCreateUpdate)
	mux.HandleFunc("POST /api/v1/admin/updates/preview", s.adminPreviewUpdate)
	mux.HandleFunc("POST /api/v1/admin/updates/import", s.adminImportUpdates)
	mux.HandleFunc("GET /api/v1/admin/updates/export", s.adminExportUpdates)
	mux.HandleFunc("GET /api/v1/admin/updates/subscribers", s.adminUpdateSubscribers)
	mux.HandleFunc("GET /api/v1/admin/updates/{id}", s.adminGetUpdate)
	mux.HandleFunc("PUT /api/v1/admin/updates/{id}", s.adminUpdateUpdate)
	mux.HandleFunc("DELETE /api/v1/admin/updates/{id}", s.adminDeleteUpdate)
	mux.HandleFunc("POST /api/v1/admin/updates/{id}/publish", s.adminPublishUpdate)
	mux.HandleFunc("POST /api/v1/admin/updates/{id}/unpublish", s.adminUnpublishUpdate)
	mux.HandleFunc("POST /api/v1/admin/updates/{id}/test-email", s.adminTestUpdateEmail)
	mux.HandleFunc("POST /api/v1/admin/updates/{id}/send", s.adminSendUpdate)
	mux.HandleFunc("GET /api/v1/announcements/preference", s.getAnnouncementPref)
	mux.HandleFunc("PUT /api/v1/announcements/preference", s.putAnnouncementPref)
	mux.HandleFunc("PUT /api/v1/announcements/preference/lang", s.putAnnouncementLang)
}

// updatesStore, depo Guncellemeler'i destekliyorsa doner; yoksa 501 yazar.
func (s *Server) updatesStore(w http.ResponseWriter) (store.UpdatesStore, bool) {
	us, ok := s.Store.(store.UpdatesStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "not_supported", "guncellemeler bu depoda desteklenmiyor")
		return nil, false
	}
	return us, true
}

// requireUpdatesAdmin, yalniz platform admin'e izin verir ve depoyu doner.
func (s *Server) requireUpdatesAdmin(w http.ResponseWriter, r *http.Request) (store.UpdatesStore, bool) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca platform admin yetkisiyle kullanilabilir")
		return nil, false
	}
	return s.updatesStore(w)
}

func (s *Server) siteBaseURL() string {
	return "https://" + s.platformDomainOr("zorven.app")
}

// ---- public ----

type publicUpdate struct {
	Slug             string     `json:"slug"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	Title            string     `json:"title"`
	BodyHTML         string     `json:"body_html"`
	Lang             string     `json:"lang"` // gercekte kullanilan dil
	PublishedAt      *time.Time `json:"published_at"`
	MaintenanceStart *time.Time `json:"maintenance_start"`
	MaintenanceEnd   *time.Time `json:"maintenance_end"`
}

func (s *Server) publicUpdates(w http.ResponseWriter, r *http.Request) {
	us, ok := s.updatesStore(w)
	if !ok {
		return
	}
	lang := updates.NormLang(r.URL.Query().Get("lang"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	posts, total, err := us.ListUpdatePosts(r.Context(), true, limit, (page-1)*limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]publicUpdate, 0, len(posts))
	for _, p := range posts {
		title, body, used := updates.Pick(p, lang)
		out = append(out, publicUpdate{
			Slug: p.Slug, Kind: p.Kind, Version: p.Version, Title: title,
			BodyHTML: updates.RenderMarkdown(body), Lang: used,
			PublishedAt: p.PublishedAt, MaintenanceStart: p.MaintenanceStart, MaintenanceEnd: p.MaintenanceEnd,
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{"updates": out, "total": total, "page": page, "limit": limit})
}

// ---- admin CRUD ----

type updateBody struct {
	Slug             string     `json:"slug"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	TitleTR          string     `json:"title_tr"`
	BodyTR           string     `json:"body_tr"`
	TitleEN          string     `json:"title_en"`
	BodyEN           string     `json:"body_en"`
	TitleJA          string     `json:"title_ja"`
	BodyJA           string     `json:"body_ja"`
	MaintenanceStart *time.Time `json:"maintenance_start"`
	MaintenanceEnd   *time.Time `json:"maintenance_end"`
}

const (
	maxTitleLen = 200
	maxBodyLen  = 60000
)

func oneLine(s string) bool { return !strings.ContainsAny(s, "\r\n\x00") }

// validate, govdeyi dogrular ve depo modeline cevirir; hata mesaji doner.
func (b *updateBody) validate() (store.UpdatePost, string) {
	p := store.UpdatePost{
		Slug: strings.ToLower(strings.TrimSpace(b.Slug)), Kind: strings.TrimSpace(b.Kind), Version: strings.TrimSpace(b.Version),
		TitleTR: strings.TrimSpace(b.TitleTR), BodyTR: b.BodyTR, TitleEN: strings.TrimSpace(b.TitleEN), BodyEN: b.BodyEN,
		TitleJA: strings.TrimSpace(b.TitleJA), BodyJA: b.BodyJA,
		MaintenanceStart: b.MaintenanceStart, MaintenanceEnd: b.MaintenanceEnd,
	}
	if p.Kind == "" {
		p.Kind = store.UpdateKindUpdate
	}
	switch {
	case !updates.ValidSlug(p.Slug):
		return p, "slug 1-80 karakter, kucuk harf/rakam/tire olmali"
	case !updates.ValidKind(p.Kind):
		return p, "tur release, update veya maintenance olmali"
	case p.TitleTR == "":
		return p, "Turkce baslik zorunlu"
	case len(p.Version) > 40 || !oneLine(p.Version):
		return p, "surum gecersiz"
	}
	for _, t := range []string{p.TitleTR, p.TitleEN, p.TitleJA} {
		if len(t) > maxTitleLen || !oneLine(t) {
			return p, "baslik cok uzun veya birden cok satir"
		}
	}
	for _, t := range []string{p.BodyTR, p.BodyEN, p.BodyJA} {
		if len(t) > maxBodyLen {
			return p, "metin cok uzun"
		}
	}
	if p.MaintenanceEnd != nil && p.MaintenanceStart == nil {
		return p, "bakim bitisi icin baslangic gerekli"
	}
	if p.MaintenanceStart != nil && p.MaintenanceEnd != nil && !p.MaintenanceEnd.After(*p.MaintenanceStart) {
		return p, "bakim bitisi baslangictan sonra olmali"
	}
	if p.Kind != store.UpdateKindMaintenance {
		p.MaintenanceStart, p.MaintenanceEnd = nil, nil
	}
	return p, ""
}

func (s *Server) writeUpdateErr(w http.ResponseWriter, err error) {
	if err == store.ErrUpdateSlugTaken {
		writeJSONError(w, http.StatusConflict, "slug_taken", "bu slug baska bir yazida kullaniliyor")
		return
	}
	s.fail(w, err)
}

func (s *Server) adminListUpdates(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	posts, total, err := us.ListUpdatePosts(r.Context(), false, limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": posts, "total": total})
}

func (s *Server) adminGetUpdate(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	p, err := us.GetUpdatePost(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) adminCreateUpdate(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	var b updateBody
	if !decode(w, r, &b) {
		return
	}
	p, msg := b.validate()
	if msg != "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		p.CreatedBy = u.ID
	}
	out, err := us.CreateUpdatePost(r.Context(), p)
	if err != nil {
		s.writeUpdateErr(w, err)
		return
	}
	s.audit(r, "updates.create", out.ID, out.Slug)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminUpdateUpdate(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	var b updateBody
	if !decode(w, r, &b) {
		return
	}
	p, msg := b.validate()
	if msg != "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	p.ID = r.PathValue("id")
	out, err := us.UpdateUpdatePost(r.Context(), p)
	if err != nil {
		s.writeUpdateErr(w, err)
		return
	}
	s.audit(r, "updates.update", out.ID, out.Slug)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminDeleteUpdate(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	if err := us.DeleteUpdatePost(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "updates.delete", r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) setPublished(w http.ResponseWriter, r *http.Request, publish bool) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	out, err := us.SetUpdatePostPublished(r.Context(), r.PathValue("id"), publish)
	if err != nil {
		s.fail(w, err)
		return
	}
	act := "updates.unpublish"
	if publish {
		act = "updates.publish"
	}
	s.audit(r, act, out.ID, out.Slug)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminPublishUpdate(w http.ResponseWriter, r *http.Request) {
	s.setPublished(w, r, true)
}
func (s *Server) adminUnpublishUpdate(w http.ResponseWriter, r *http.Request) {
	s.setPublished(w, r, false)
}

func (s *Server) adminPreviewUpdate(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "bu uc yalnizca platform admin yetkisiyle kullanilabilir")
		return
	}
	var b struct {
		Markdown string `json:"markdown"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Markdown) > maxBodyLen {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "metin cok uzun")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"html": updates.RenderMarkdown(b.Markdown)})
}

func (s *Server) adminUpdateSubscribers(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	n, err := us.CountAnnouncementRecipients(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// ---- e-posta ----

func (s *Server) announceFrom() string {
	return "Zorven <no-reply@" + s.platformDomainOr("zorven.app") + ">"
}

func (s *Server) adminTestUpdateEmail(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok || u == nil || u.Email == "" || u.ID == "" {
		writeJSONError(w, http.StatusBadRequest, "user_session_required",
			"test e-postasi icin kendi hesabinizla giris yapin (e-posta adresiniz gerekli)")
		return
	}
	if s.MailSender == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_unavailable", "mail relay yapilandirilmamis")
		return
	}
	var b struct {
		Lang string `json:"lang"`
	}
	_ = decodeBodyOpt(r, &b)
	p, err := us.GetUpdatePost(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	lang := b.Lang
	if lang == "" {
		if pref, perr := us.GetAnnouncementPref(r.Context(), u.ID); perr == nil {
			lang = pref.Lang
		}
	}
	em := updates.BuildEmail(p, lang, s.siteBaseURL(), s.AnnounceKey, u.ID)
	if _, err := s.MailSender.SendWithHeaders(s.announceFrom(), u.Email, "[TEST] "+em.Subject, em.Text, em.HTML, "", em.Headers, nil); err != nil {
		s.logger().Warn("test duyuru e-postasi gonderilemedi", "hata", err)
		writeJSONError(w, http.StatusBadGateway, "send_failed", "e-posta gonderilemedi")
		return
	}
	s.audit(r, "updates.test_email", p.ID, p.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"sent_to": u.Email})
}

// decodeBodyOpt, bos olabilen JSON govdeyi cozer (sinirli okuma).
func decodeBodyOpt(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
}

func (s *Server) adminSendUpdate(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	if s.MailSender == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_unavailable", "mail relay yapilandirilmamis")
		return
	}
	if len(s.AnnounceKey) == 0 {
		writeJSONError(w, http.StatusServiceUnavailable, "mail_unavailable", "abonelik anahtari yapilandirilmamis")
		return
	}
	var b struct {
		Action  string `json:"action"` // "" | send | resume | retry_failed
		Force   bool   `json:"force"`
		Confirm string `json:"confirm"`
	}
	_ = decodeBodyOpt(r, &b)
	p, err := us.GetUpdatePost(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if p.PublishedAt == nil || p.PublishedAt.After(time.Now()) {
		writeJSONError(w, http.StatusConflict, "not_published", "yalniz yayinlanmis yazi e-postalanabilir")
		return
	}
	if updates.IsRunning(p.ID) {
		writeJSONError(w, http.StatusConflict, "already_sending", "bu yazinin gonderimi suruyor")
		return
	}
	total, err := us.CountAnnouncementRecipients(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	mode := ""
	switch b.Action {
	case "", "send":
		if b.Force && p.EmailedAt != nil && b.Confirm != ForceResendConfirm {
			writeJSONError(w, http.StatusBadRequest, "confirm_required",
				"herkese yeniden gondermek icin confirm=\""+ForceResendConfirm+"\" gerekir")
			return
		}
		// Atomik sahiplenme: ikinci istek (cift tik, yeniden deneme) gonderimi kopyalayamaz.
		claimed, err := us.ClaimUpdatePostEmail(r.Context(), p.ID, b.Force, total)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !claimed {
			writeJSONError(w, http.StatusConflict, "already_emailed",
				"bu yazi zaten e-postalandi; eksikleri gondermek icin action=resume, hepsini yeniden gondermek icin force=true gerekir")
			return
		}
		if b.Force {
			if err := us.ResetUpdateDeliveries(r.Context(), p.ID); err != nil {
				s.fail(w, err)
				return
			}
		}
	case store.SendModeResume, store.SendModeRetryFailed:
		mode = b.Action
		claimed, err := us.ClaimUpdatePostResume(r.Context(), p.ID, mode, total)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !claimed {
			writeJSONError(w, http.StatusConflict, "nothing_to_resume", "devam edilecek veya yeniden denenecek gonderim yok")
			return
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "bad_action", "gecersiz action")
		return
	}
	camp := &updates.Campaign{
		Store: us, Key: s.AnnounceKey, BaseURL: s.siteBaseURL(), From: s.announceFrom(), Rate: 5, Log: s.logger(), Mode: mode,
		Mailer: func(from, to, subject, text, html string, headers []string) error {
			_, err := s.MailSender.SendWithHeaders(from, to, subject, text, html, "", headers, nil)
			return err
		},
	}
	go camp.Run(context.Background(), p, total)
	s.audit(r, "updates.send."+firstNonEmptyStr(b.Action, "send"), p.ID, p.Slug)
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": total})
}

// ---- kullanici tercihi ----

func (s *Server) getAnnouncementPref(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	us, ok := s.updatesStore(w)
	if !ok {
		return
	}
	pref, err := us.GetAnnouncementPref(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pref)
}

func (s *Server) putAnnouncementPref(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	us, ok := s.updatesStore(w)
	if !ok {
		return
	}
	var b struct {
		Subscribed bool   `json:"subscribed"`
		Lang       string `json:"lang"`
		Source     string `json:"source"` // signup | profile | banner
	}
	if !decode(w, r, &b) {
		return
	}
	lang := ""
	if strings.TrimSpace(b.Lang) != "" {
		lang = updates.NormLang(b.Lang)
	}
	if err := us.SetAnnouncementPref(r.Context(), u.ID, b.Subscribed, lang, normPrefSource(b.Source)); err != nil {
		s.fail(w, err)
		return
	}
	pref, err := us.GetAnnouncementPref(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pref)
}

// putAnnouncementLang, panel dili degisince duyuru e-postalarinin dilini esitler.
// Abonelik/onay bilgisine dokunmaz; tercih satiri yoksa (hic sorulmamis) etkisizdir.
func (s *Server) putAnnouncementLang(w http.ResponseWriter, r *http.Request) {
	u, ok := invitationUser(w, r)
	if !ok {
		return
	}
	us, ok := s.updatesStore(w)
	if !ok {
		return
	}
	var b struct {
		Lang string `json:"lang"`
	}
	if !decode(w, r, &b) {
		return
	}
	if strings.TrimSpace(b.Lang) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "lang gerekli")
		return
	}
	if err := us.SetAnnouncementLang(r.Context(), u.ID, updates.NormLang(b.Lang)); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ForceResendConfirm, "herkese yeniden gonder" (force) icin panelde yazilan onay metni.
const ForceResendConfirm = "RESEND"

func normPrefSource(src string) string {
	switch src {
	case "signup", "banner", "profile":
		return src
	}
	return "profile"
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
