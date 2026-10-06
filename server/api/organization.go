package api

// Organizasyon silme.
//
//	DELETE /api/v1/organization   govde: {"confirm_slug": "<aktif org slug>"}
//
// KURALLAR:
//   - Yalnizca OTURUM ACMIS owner/admin (kullanici karari). API token'lari
//     (kullaniciya bagli olsa bile) ve admin anahtari organizasyon SILEMEZ:
//     geri donussuz bir islem otomasyonla tetiklenmemeli.
//   - Silinecek organizasyonun slug'i govdede birebir yazilmali (yanlis
//     sekmede yanlis organizasyonu silmeyi onler).
//   - Platform organizasyonu (ten_default) silinemez.
//   - Kullanicinin baska bir organizasyonu olmali; yoksa silme sonrasi
//     panelde gidecek yeri kalmazdi.

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Server) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if _, isToken := apiScopesFromContext(r.Context()); isToken {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"organizasyon API token ile silinemez; panelden oturum açarak silin")
		return
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"organizasyon silmek için oturum açmış bir kullanıcı gerekli")
		return
	}
	role, err := s.Store.GetMemberRole(r.Context(), tenantID, u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !isPrivilegedRole(role) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"organizasyonu yalnızca owner veya admin silebilir")
		return
	}
	if tenantID == store.DefaultTenantID {
		writeJSONError(w, http.StatusForbidden, "forbidden", "platform organizasyonu silinemez")
		return
	}

	var body struct {
		ConfirmSlug string `json:"confirm_slug"`
	}
	if !decode(w, r, &body) {
		return
	}
	ten, err := s.Store.GetTenant(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if strings.TrimSpace(body.ConfirmSlug) != ten.Slug {
		writeJSONError(w, http.StatusUnprocessableEntity, "confirm_mismatch",
			"onay için organizasyonun kısa adını ("+ten.Slug+") birebir yazın")
		return
	}

	others, err := s.Store.CountOtherOrganizations(r.Context(), u.ID, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if others == 0 {
		writeJSONError(w, http.StatusConflict, "last_organization",
			"bu, üye olduğunuz tek organizasyon; silmeden önce yeni bir organizasyon oluşturun")
		return
	}

	// Denetim kaydi silmeden ONCE: silme sonrasi kiraci baglami kalmaz.
	s.audit(r, "organization.delete", tenantID, ten.Slug)

	clientIDs, err := s.Store.DeleteOrganization(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Silinen istemcilerin canli baglantilarini kopar: token'lari artik yok.
	for _, id := range clientIDs {
		if sess, ok := s.Hub.Get(id); ok {
			sess.Close("organization deleted")
		}
	}
	// Tuneller gitti; yonlendirme tablosu tazelenmeli.
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "disconnected_clients": len(clientIDs)})
}
