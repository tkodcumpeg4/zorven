package oauthas

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// session, panel oturumunu doner; yoksa 401 yazar.
func (s *Server) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	if s.cfg.Session != nil {
		if ss, ok := s.cfg.Session(r); ok && ss.UserID != "" {
			return ss, true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	oauthErr(w, http.StatusUnauthorized, "login_required", "oturum acmaniz gerekiyor")
	return Session{}, false
}

// originOK, durum degistiren panel uclarinda CSRF denetimi: Origin ZORUNLU ve guvenilir.
func (s *Server) originOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") == "" || s.cfg.OriginTrusted == nil || !s.cfg.OriginTrusted(r) {
		oauthErr(w, http.StatusForbidden, "forbidden_origin", "bu Origin'den istek kabul edilmez")
		return false
	}
	return true
}

func (s *Server) orgs(w http.ResponseWriter, r *http.Request, userID string) ([]Org, bool) {
	if s.cfg.Orgs == nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "organizasyonlar okunamadi")
		return nil, false
	}
	orgs, err := s.cfg.Orgs(r.Context(), userID)
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "organizasyonlar okunamadi")
		return nil, false
	}
	sort.SliceStable(orgs, func(i, j int) bool { return orgs[i].Name < orgs[j].Name })
	return orgs, true
}

// GET /oauth/consent/info?<yetkilendirme parametreleri> -> onay sayfasi verisi.
func (s *Server) consentInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "GET gerekli")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	req, aerr := s.parseAuthz(r, r.URL.Query())
	if aerr != nil {
		oauthErr(w, http.StatusBadRequest, aerr.code, aerr.desc)
		return
	}
	// "full" ayri bir bolumde sunulur (varsayilan KAPALI); normal kapsam listesinde yer almaz.
	scopes := make([]ScopeInfo, 0, len(req.Scopes))
	for _, id := range req.Scopes {
		if id == ScopeFull {
			continue
		}
		si, _ := scopeInfo(id)
		scopes = append(scopes, si)
	}
	host := ""
	if u, err := url.Parse(req.RedirectURI); err == nil {
		host = u.Host
	}
	out := map[string]any{
		"client_id":     req.Client.ID,
		"client_name":   req.Client.Name,
		"redirect_host": host,
		"verified":      false, // DCR ile kayitli istemciler dogrulanmis sayilmaz
		"scopes":        scopes,
		"resource":      req.Resource,
		"user_email":    sess.Email,
	}
	if req.Tunnel != nil {
		// MCP tunel kaynagi: kiraci sabit (tunel sahibi); kullanici secim yapmaz.
		allowed, _, err := s.userAllowed(r.Context(), *req.Tunnel, sess.UserID)
		out["resource_kind"] = "tunnel"
		out["resource_host"] = req.Tunnel.Host
		out["allowed"] = allowed && err == nil
		out["orgs"] = []Org{}
		writeJSON(w, http.StatusOK, out)
		return
	}
	orgs, ok := s.orgs(w, r, sess.UserID)
	if !ok {
		return
	}
	out["resource_kind"] = "management"
	out["orgs"] = orgs
	// Tam erisim: istemci istemese de kullanici onay ekraninda acikca isaretleyerek
	// verebilir; istediyse yalnizca "onerilir". Taze giris durumu sunucuca bildirilir.
	out["full_available"] = true
	out["full_requested"] = HasFull(req.Scopes)
	out["fresh_auth"] = sess.fresh(s.cfg.Now())
	out["step_up_seconds"] = int(StepUpMaxAge.Seconds())
	writeJSON(w, http.StatusOK, out)
}

// POST /oauth/consent/decision
func (s *Server) consentDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "POST gerekli")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !s.originOK(w, r) {
		return
	}
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	var body struct {
		ClientID            string   `json:"client_id"`
		RedirectURI         string   `json:"redirect_uri"`
		ResponseType        string   `json:"response_type"`
		CodeChallenge       string   `json:"code_challenge"`
		CodeChallengeMethod string   `json:"code_challenge_method"`
		State               string   `json:"state"`
		Scope               string   `json:"scope"`
		Resource            string   `json:"resource"`
		Decision            string   `json:"decision"`
		TenantID            string   `json:"tenant_id"`
		Scopes              []string `json:"scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBytes)).Decode(&body); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "gecersiz JSON")
		return
	}
	v := url.Values{}
	v.Set("client_id", body.ClientID)
	v.Set("redirect_uri", body.RedirectURI)
	v.Set("response_type", body.ResponseType)
	v.Set("code_challenge", body.CodeChallenge)
	v.Set("code_challenge_method", body.CodeChallengeMethod)
	v.Set("scope", body.Scope)
	v.Set("resource", body.Resource)
	req, aerr := s.parseAuthz(r, v)
	if aerr != nil {
		oauthErr(w, http.StatusBadRequest, aerr.code, aerr.desc)
		return
	}
	redirect := func(params map[string]string) {
		if body.State != "" {
			params["state"] = body.State
		}
		params["iss"] = s.Issuer(r)
		u, _ := url.Parse(req.RedirectURI)
		q := u.Query()
		for k, val := range params {
			q.Set(k, val)
		}
		u.RawQuery = q.Encode()
		writeJSON(w, http.StatusOK, map[string]string{"redirect": u.String()})
	}
	if body.Decision != "allow" {
		redirect(map[string]string{"error": "access_denied", "error_description": "kullanici reddetti"})
		return
	}
	tenantID := body.TenantID
	if req.Tunnel != nil {
		// MCP tunel kaynagi: kiraci tunel sahibidir; erisim e-posta/uyelik politikasina baglidir.
		allowed, _, err := s.userAllowed(r.Context(), *req.Tunnel, sess.UserID)
		if err != nil || !allowed {
			oauthErr(w, http.StatusForbidden, "access_denied", "bu MCP kaynagina erisim izniniz yok")
			return
		}
		tenantID = req.Tunnel.TenantID
	} else {
		orgs, ok := s.orgs(w, r, sess.UserID)
		if !ok {
			return
		}
		member := false
		for _, o := range orgs {
			if o.ID == body.TenantID {
				member = true
				break
			}
		}
		if !member {
			oauthErr(w, http.StatusForbidden, "access_denied", "bu organizasyonun uyesi degilsiniz")
			return
		}
	}
	// Yikici kapsamlar (silme) acikca secilmedikce VERILMEZ: istemci `scopes` listesi
	// gondermediyse yalnizca yikici olmayanlar onaylanir.
	granted := make([]string, 0, len(req.Scopes))
	for _, sc := range req.Scopes {
		if si, _ := scopeInfo(sc); !si.Destructive {
			granted = append(granted, sc)
		}
	}
	// Istemci "full" istediyse varsayilan olarak YINE verilmez: kullanici acikca isaretlemeli.
	granted = withoutScope(granted, ScopeFull)
	if body.Scopes != nil {
		for _, sc := range body.Scopes {
			if _, ok := scopeInfo(sc); !ok {
				oauthErr(w, http.StatusBadRequest, "invalid_scope", "bilinmeyen kapsam: "+sc)
				return
			}
		}
		// "full" istemci istemese de kullanici onay ekraninda isaretleyerek verebilir
		// (yalniz yonetim kaynaginda); digerleri istenenin alt kumesi olmali.
		if !subsetOf(withoutScope(body.Scopes, ScopeFull), withoutScope(req.Scopes, ScopeFull)) && !HasFull(req.Scopes) {
			oauthErr(w, http.StatusBadRequest, "invalid_scope", "istenenden fazla kapsam verilemez")
			return
		}
		granted = body.Scopes
	}
	var stepUp *time.Time
	if HasFull(granted) {
		if req.Tunnel != nil {
			oauthErr(w, http.StatusBadRequest, "invalid_scope", "'full' kapsami MCP tunel kaynaklarinda verilemez")
			return
		}
		// Step-up: panel oturumu son StepUpMaxAge icinde (parola/2FA ile) acilmis olmali.
		now := s.cfg.Now()
		if !sess.fresh(now) {
			s.audit(tenantID, "user:"+sess.UserID, s.ip(r), "oauth.full_denied", req.Client.ID, "reason=stale_login")
			oauthErr(w, http.StatusForbidden, "reauth_required", "tam erisim icin son 5 dakika icinde yeniden giris yapmalisiniz")
			return
		}
		at := sess.AuthTime.UTC()
		stepUp = &at
		granted = uniqueScopes(granted)
	}
	if len(granted) == 0 {
		oauthErr(w, http.StatusBadRequest, "invalid_scope", "en az bir kapsam secilmeli")
		return
	}
	code := randToken(32)
	err := s.cfg.Store.CreateCode(r.Context(), hashToken(code), Code{
		ClientID: req.Client.ID, UserID: sess.UserID, TenantID: tenantID, RedirectURI: req.RedirectURI,
		Scope: strings.Join(granted, " "), Resource: req.Resource, CodeChallenge: req.Challenge,
		ExpiresAt: s.cfg.Now().Add(codeTTL), StepUpAt: stepUp,
	})
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "kod uretilemedi")
		return
	}
	redirect(map[string]string{"code": code})
}

// grantView, panele donen grant ozeti.
type grantView struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	TenantID   string     `json:"tenant_id"`
	TenantName string     `json:"tenant_name"`
	UserID     string     `json:"user_id"`
	Own        bool       `json:"own"`
	Scopes     []string   `json:"scopes"`
	Resource   string     `json:"resource"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// Full, grant tam erisim (full) kapsamini tasiyor; StepUpAt onayin dayandigi taze giris.
	Full     bool       `json:"full"`
	StepUpAt *time.Time `json:"step_up_at,omitempty"`
}

// GET /oauth/grants[?tenant=<id>]  ve  DELETE /oauth/grants/{id}
func (s *Server) grants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	orgs, ok := s.orgs(w, r, sess.UserID)
	if !ok {
		return
	}
	role := map[string]string{}
	name := map[string]string{}
	for _, o := range orgs {
		role[o.ID] = o.Role
		name[o.ID] = o.Name
	}
	privileged := func(tenant string) bool { return role[tenant] == "owner" || role[tenant] == "admin" }

	if r.Method == http.MethodGet && r.URL.Path == "/oauth/grants" {
		tenant := r.URL.Query().Get("tenant")
		var list []Grant
		var err error
		if tenant != "" {
			if !privileged(tenant) {
				oauthErr(w, http.StatusForbidden, "access_denied", "organizasyondaki tum baglantilari yalnizca sahip/yonetici gorebilir")
				return
			}
			list, err = s.cfg.Store.ListGrants(r.Context(), "", tenant)
		} else {
			list, err = s.cfg.Store.ListGrants(r.Context(), sess.UserID, "")
		}
		if err != nil {
			oauthErr(w, http.StatusInternalServerError, "server_error", "liste okunamadi")
			return
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		out := make([]grantView, 0, len(list))
		for _, g := range list {
			sc, _ := ParseScopes(g.Scope)
			out = append(out, grantView{ID: g.ID, ClientID: g.ClientID, ClientName: g.ClientName, TenantID: g.TenantID,
				TenantName: name[g.TenantID], UserID: g.UserID, Own: g.UserID == sess.UserID, Scopes: sc,
				Resource: g.Resource, CreatedAt: g.CreatedAt, LastUsedAt: g.LastUsedAt,
				Full: HasFull(sc), StepUpAt: g.StepUpAt})
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": out})
		return
	}

	id, hasID := strings.CutPrefix(r.URL.Path, "/oauth/grants/")
	if r.Method == http.MethodDelete && hasID && id != "" {
		if !s.originOK(w, r) {
			return
		}
		g, err := s.cfg.Store.GetGrant(r.Context(), id)
		if err != nil || (g.UserID != sess.UserID && !privileged(g.TenantID)) {
			// Baskasinin grant'inin varligini sizdirma.
			oauthErr(w, http.StatusNotFound, "not_found", "baglanti bulunamadi")
			return
		}
		now := s.cfg.Now()
		if err := s.cfg.Store.RevokeGrant(r.Context(), id, now); err != nil {
			oauthErr(w, http.StatusInternalServerError, "server_error", "iptal edilemedi")
			return
		}
		s.forget(id)
		s.audit(g.TenantID, "user:"+sess.UserID, s.ip(r), "oauth.revoke", id, "client="+g.ClientID+" via=panel")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "desteklenmeyen istek")
}

// withoutScope, listeden bir kapsami cikarir (yeni dilim doner).
func withoutScope(list []string, drop string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// uniqueScopes tekrarlari atar (sirayi korur).
func uniqueScopes(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
