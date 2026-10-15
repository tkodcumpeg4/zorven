package api

// Guncelleme yazilari JSON ice/disa aktarma.
//
//	POST /api/v1/admin/updates/import   — govde: tek yazi | dizi | {"posts":[...]} (sema: server/updates/importexport.go)
//	     sorgu: mode=create_only|upsert (vars. create_only), dry_run=true, publish=true
//	GET  /api/v1/admin/updates/export   — ?ids=a,b (bos = hepsi); ayni semayi doner
//
// Her ogenin yazimi ayri is birimidir; bir ogenin hatasi digerlerini engellemez.
// Yayin yalniz published=true VE publish=true birlikteyken yapilir. E-posta ASLA gonderilmez.

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/updates"
)

const (
	importModeCreateOnly = "create_only"
	importModeUpsert     = "upsert"
)

type importResult struct {
	Index    int      `json:"index"`
	Slug     string   `json:"slug"`
	Action   string   `json:"action"` // create | update | skip | error
	ID       string   `json:"id,omitempty"`
	Publish  bool     `json:"publish"` // yazi bu isle yayinlanir / yayinlandi
	Messages []string `json:"messages,omitempty"`
}

func (s *Server) adminImportUpdates(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	mode := q.Get("mode")
	switch mode {
	case "":
		mode = importModeCreateOnly
	case importModeCreateOnly, importModeUpsert:
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "mode create_only veya upsert olmali")
		return
	}
	dryRun := q.Get("dry_run") == "true"
	allowPublish := q.Get("publish") == "true"

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, updates.MaxImportBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "too_large", "govde cok buyuk")
		return
	}
	items, err := updates.ParseImport(data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_import", err.Error())
		return
	}

	results := make([]importResult, 0, len(items))
	seen := map[string]bool{}
	counts := map[string]int{"create": 0, "update": 0, "skip": 0, "error": 0}
	var createdBy string
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		createdBy = u.ID
	}

	for _, it := range items {
		res := importResult{Index: it.Index, Action: "error"}
		finish := func() {
			counts[res.Action]++
			results = append(results, res)
		}
		if it.Err != "" {
			res.Messages = []string{it.Err}
			finish()
			continue
		}
		p := it.Post
		res.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
		body := updateBody{
			Slug: p.Slug, Kind: p.Kind, Version: p.Version, TitleTR: p.TitleTR, BodyTR: p.BodyTR,
			TitleEN: p.TitleEN, BodyEN: p.BodyEN, TitleJA: p.TitleJA, BodyJA: p.BodyJA,
			MaintenanceStart: p.MaintenanceStart, MaintenanceEnd: p.MaintenanceEnd,
		}
		post, msg := body.validate()
		if msg == "" && strings.TrimSpace(p.BodyTR) == "" {
			msg = "body_tr zorunlu"
		}
		if msg != "" {
			res.Messages = []string{msg}
			finish()
			continue
		}
		if seen[post.Slug] {
			res.Messages = []string{"ayni slug bu dosyada birden cok kez geciyor"}
			finish()
			continue
		}
		seen[post.Slug] = true

		existing, gerr := us.GetUpdatePostBySlug(r.Context(), post.Slug, false)
		exists := gerr == nil
		if gerr != nil && !errors.Is(gerr, store.ErrNotFound) {
			res.Messages = []string{"slug sorgulanamadi"}
			s.logger().Error("ice aktarma slug sorgusu", "hata", gerr)
			finish()
			continue
		}
		wantPublish := p.Published && allowPublish
		if p.Published && !allowPublish {
			res.Messages = append(res.Messages, "published=true yoksayildi: yayin icin publish=true secenegi gerekir (taslak olarak aktarilir)")
		}

		switch {
		case exists && mode == importModeCreateOnly:
			res.Action = "skip"
			res.ID = existing.ID
			res.Messages = []string{"slug zaten var; create_only kipinde atlandi"}
			finish()
			continue
		case exists:
			res.Action = "update"
			res.ID = existing.ID
			res.Publish = wantPublish && existing.PublishedAt == nil
		default:
			res.Action = "create"
			res.Publish = wantPublish
		}
		if dryRun {
			finish()
			continue
		}

		if exists {
			post.ID = existing.ID
			out, uerr := us.UpdateUpdatePost(r.Context(), post)
			if uerr != nil {
				res.Action, res.Publish = "error", false
				res.Messages = append(res.Messages, importWriteMsg(uerr))
				finish()
				continue
			}
			res.ID = out.ID
		} else {
			post.CreatedBy = createdBy
			out, cerr := us.CreateUpdatePost(r.Context(), post)
			if cerr != nil {
				res.Action, res.Publish = "error", false
				res.Messages = append(res.Messages, importWriteMsg(cerr))
				finish()
				continue
			}
			res.ID = out.ID
		}
		s.audit(r, "updates.import."+res.Action, res.ID, post.Slug)
		if res.Publish {
			if _, perr := us.SetUpdatePostPublished(r.Context(), res.ID, true); perr != nil {
				res.Publish = false
				res.Messages = append(res.Messages, "kaydedildi ama yayinlanamadi (taslak kaldi)")
				s.logger().Error("ice aktarma yayin hatasi", "hata", perr)
			} else {
				s.audit(r, "updates.publish", res.ID, post.Slug)
			}
		}
		finish()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run": dryRun, "mode": mode, "publish": allowPublish,
		"counts": counts, "results": results,
	})
}

func importWriteMsg(err error) string {
	if errors.Is(err, store.ErrUpdateSlugTaken) {
		return "bu slug baska bir yazida kullaniliyor"
	}
	return "kaydedilemedi"
}

func (s *Server) adminExportUpdates(w http.ResponseWriter, r *http.Request) {
	us, ok := s.requireUpdatesAdmin(w, r)
	if !ok {
		return
	}
	var want map[string]bool
	if raw := strings.TrimSpace(r.URL.Query().Get("ids")); raw != "" {
		want = map[string]bool{}
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				want[id] = true
			}
		}
	}
	posts, _, err := us.ListUpdatePosts(r.Context(), false, 1000, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := updates.ImportEnvelope{Posts: []updates.ImportPost{}}
	for _, p := range posts {
		if want != nil && !want[p.ID] {
			continue
		}
		out.Posts = append(out.Posts, updates.ExportPost(p))
	}
	w.Header().Set("Content-Disposition", `attachment; filename="zorven-updates-`+time.Now().UTC().Format("20060102")+`.json"`)
	writeJSON(w, http.StatusOK, out)
}
