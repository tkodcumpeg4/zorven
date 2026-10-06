package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	list, err := s.Store.ListProjects(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Project{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decode(w, r, &body) {
		return
	}

	body.Name = strings.TrimSpace(body.Name)
	body.Slug = strings.ToLower(strings.TrimSpace(body.Slug))

	if body.Name == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name", "proje adı boş olamaz")
		return
	}
	if body.Slug == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_slug", "proje slug boş olamaz")
		return
	}

	// Plan limiti (FAZ 3.5): yalnizca YENI proje engellenir; siniri asan
	// mevcut projeler silinmez.
	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateProject(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	p, err := s.Store.CreateProject(r.Context(), tenantID, body.Name, body.Slug)
	if errors.Is(err, store.ErrProjectSlugTaken) {
		writeJSONError(w, http.StatusConflict, "slug_taken", "bu proje slug'ı zaten kullanımda")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_project", err.Error())
		return
	}

	s.audit(r, "project.create", p.ID, p.Name)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Proje silmek tum ekibi etkiler: yalnizca owner/admin (kullanici karari).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_id", "proje id zorunlu")
		return
	}

	err := s.Store.DeleteProject(r.Context(), tenantID, id)
	if errors.Is(err, store.ErrProjectDefaultDelete) {
		writeJSONError(w, http.StatusBadRequest, "default_project_cannot_be_deleted", "varsayılan proje silinemez")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "proje bulunamadı")
		return
	}
	if errors.Is(err, store.ErrProjectNotEmpty) {
		// Hata metni sayilari tasir ("... 2 secret, 1 policy").
		writeJSONError(w, http.StatusConflict, "project_not_empty",
			"proje silinemedi: önce içindeki secret, policy ve log hedeflerini silin ("+
				strings.TrimPrefix(err.Error(), store.ErrProjectNotEmpty.Error()+": ")+")")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	s.audit(r, "project.delete", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// renameProject, projenin gorunen adini degistirir. Slug degismez.
func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Yeniden adlandirma da tum ekibin gordugu bir degisiklik: owner/admin.
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 80 {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name", "proje adı 1-80 karakter olmalı")
		return
	}
	p, err := s.Store.RenameProject(r.Context(), tenantID, r.PathValue("id"), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "proje bulunamadı")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "project.rename", p.ID, name)
	writeJSON(w, http.StatusOK, p)
}
