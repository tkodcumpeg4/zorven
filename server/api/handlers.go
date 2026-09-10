package api

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/domain"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Server, yonetim REST API'sini karsilar.
type Server struct {
	Store   store.Store
	Hub     *tunnel.Hub
	Log     *reqlog.Ring
	Events  *events.Broker
	Logger  *slog.Logger
	Version string

	// DNSVerifier, custom domain dogrulamasi icin DNS istemcisi (nil ise DefaultDNSVerifier).
	DNSVerifier domain.DNSVerifier

	// PlatformDomain, kiraci subdomainlerinin uretilecegi domain
	// (or. "rpshell.app"). BOS ise subdomain tahsisi KAPALIDIR: tek etiket
	// kendi basina bir adres degildir, uydurmaktansa acikca reddediyoruz.
	PlatformDomain string

	// OnTunnelChange, tunel eklendiginde/degistiginde/silindiginde cagrilir.
	// Ingress router'ini HEMEN tazelemek icin: 10sn yoklamayi beklemeye gerek kalmaz.
	OnTunnelChange func()

	// GitHub, GitHub OAuth ile giris (opsiyonel; yalnizca yapilandirilinca acilir).
	GitHub GitHubAuth
	// Sessions, GitHub ile giren tarayicilarin imzali oturum cerezini yonetir.
	Sessions *session.Manager

	// BetterAuth, Better Auth oturum dogrulayici ve onbellegi.
	BetterAuth *auth.BetterAuthVerifier

	// Tickets, terminal ve ekran WS'leri icin tek kullanimlik biletler.
	Tickets *TicketStore

	// Entitlements, plan limitlerini ve yetkilerini denetleyen servis.
	Entitlements entitlements.EntitlementService

	// IPFilter, ingress seviyesinde CIDR bazli IP izin listesi motoru.
	IPFilter *ipfilter.Engine

	// Devices, masaustu "tarayicidan giris" (device authorization) akisi icin
	// bekleyen eslesmeleri tutar. Routes() ilk cagrildiginda kurulur.
	Devices *DeviceAuth

	// MailDomain, webmail adreslerinin ureticisi (or. "mail.zorven.app").
	// BOS ise webmail KAPALIDIR (adres uretmez, gonderim reddedilir).
	MailDomain string
	// MailSender, giden e-postalari relay'e teslim eder (nil ise gonderim kapali).
	MailSender *mail.Sender
	// SystemMailboxes, platform (site) sistem adresleridir (info@zorven.app,
	// sales@zorven.app ...). YALNIZCA platform admin (owner) bu adreslerden
	// gonderebilir ve bunlara gelenler ten_default'a dusup owner panelinde gorunur.
	SystemMailboxes []string
}

func writeEntitlementError(w http.ResponseWriter, err error) {
	var entErr *entitlements.EntitlementError
	if errors.As(err, &entErr) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":     entErr.Code,
			"error":    entErr.Message,
			"resource": entErr.Resource,
			"limit":    entErr.Limit,
			"current":  entErr.Current,
		})
		return
	}
	writeJSONError(w, http.StatusForbidden, "entitlement_denied", err.Error())
}

// GitHubAuth, GitHub OAuth yapilandirmasi. Uc alan da doluysa giris ACIK olur.
type GitHubAuth struct {
	ClientID     string
	ClientSecret string
	// Allow, izinli GitHub kullanici adlari (kucuk harfe normalize edilmis).
	// Bos ise HIC KIMSE giremez — kazara herkese acik kalmasin diye bilincli.
	Allow map[string]bool
	// BaseURL, callback URL'sini kurmak icin sabit taban (or. https://sunucu:8443).
	// Bos ise gelen istekten (scheme+host) turetilir.
	BaseURL string

	// OpenSignup true ise GitHub hesabi olan HERKES kaydolabilir ve kendi
	// kiracisini alir. false (VARSAYILAN) ise yalnizca Allow listesindekiler.
	//
	// Varsayilan kapali: bir sunucunun kazara herkese acilmasi, kimsenin
	// girememesinden cok daha kotudur.
	OpenSignup bool
}

// Routes, API mux'unu kurar.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/me", s.me)

	// Tek komutla otomatik kurulum scriptleri ve ikili dagitimi (public)
	mux.HandleFunc("GET /install.sh", s.serveInstallSh)
	mux.HandleFunc("GET /install.ps1", s.serveInstallPs1)
	mux.HandleFunc("GET /bin/{filename}", s.serveBinary)

	// Kimlik dogrulama (public; admin anahtari GEREKTIRMEZ).
	mux.HandleFunc("GET /api/v1/auth/config", s.authConfig)
	mux.HandleFunc("GET /api/v1/auth/github/login", s.githubLogin)
	mux.HandleFunc("GET /api/v1/auth/github/callback", s.githubCallback)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)

	// Masaustu "tarayicidan giris" (device authorization).
	// code + token PUBLIC'tir (auth'suz istemci cagirir); routing main.go'da.
	if s.Devices == nil {
		s.Devices = NewDeviceAuth()
	}
	mux.HandleFunc("POST /api/v1/device/code", s.deviceCode)
	mux.HandleFunc("POST /api/v1/device/token", s.deviceToken)
	mux.HandleFunc("POST /api/v1/device/approve", s.deviceApprove) // GUARDED

	mux.HandleFunc("GET /api/v1/clients", s.listClients)
	mux.HandleFunc("POST /api/v1/clients", s.createClient)
	mux.HandleFunc("GET /api/v1/clients/{id}", s.getClient)
	mux.HandleFunc("DELETE /api/v1/clients/{id}", s.deleteClient)
	mux.HandleFunc("POST /api/v1/clients/{id}/token", s.rotateToken)
	mux.HandleFunc("POST /api/v1/terminal-ticket", s.issueTerminalTicket)
	mux.HandleFunc("GET /api/v1/clients/{id}/terminal", s.terminalHandler)
	mux.HandleFunc("POST /api/v1/screen-ticket", s.issueScreenTicket)
	mux.HandleFunc("GET /api/v1/clients/{id}/screen", s.screenHandler)

	mux.HandleFunc("GET /api/v1/tunnels", s.listTunnels)
	mux.HandleFunc("POST /api/v1/tunnels", s.createTunnel)
	mux.HandleFunc("GET /api/v1/tunnels/{id}", s.getTunnel)
	mux.HandleFunc("PATCH /api/v1/tunnels/{id}", s.updateTunnel)
	mux.HandleFunc("DELETE /api/v1/tunnels/{id}", s.deleteTunnel)

	mux.HandleFunc("GET /api/v1/hostnames", s.listHostnames)
	mux.HandleFunc("POST /api/v1/hostnames", s.createHostname)
	mux.HandleFunc("POST /api/v1/hostnames/custom", s.createCustomHostname)
	mux.HandleFunc("PATCH /api/v1/hostnames/{id}", s.patchHostname)
	mux.HandleFunc("POST /api/v1/hostnames/{id}/verify", s.verifyHostname)
	mux.HandleFunc("DELETE /api/v1/hostnames/{id}", s.deleteHostname)

	// Platform yonetimi: YALNIZCA admin anahtariyla (kiraci oturumu yetmez).
	mux.HandleFunc("GET /api/v1/admin/stats", s.adminGetStats)
	mux.HandleFunc("GET /api/v1/admin/tenants", s.adminListTenants)
	mux.HandleFunc("GET /api/v1/admin/clients", s.adminListClients)
	mux.HandleFunc("GET /api/v1/admin/hostnames", s.adminListHostnames)
	mux.HandleFunc("POST /api/v1/admin/switch-tenant", s.adminSwitchTenant)
	mux.HandleFunc("PUT /api/v1/admin/tenants/{id}/plan", s.adminUpdateTenantPlan)

	mux.HandleFunc("GET /api/v1/subscription", s.getSubscription)
	mux.HandleFunc("GET /api/v1/plans", s.listPlans)
	mux.HandleFunc("GET /api/v1/requests", s.listRequests)
	mux.HandleFunc("GET /api/v1/events", s.stream)

	// Ekip ve Uye Yonetimi (Team & Member Tokens)
	mux.HandleFunc("GET /api/v1/team/members", s.listTeamMembers)
	mux.HandleFunc("POST /api/v1/team/members/invite", s.inviteTeamMember)
	mux.HandleFunc("PATCH /api/v1/team/members/{id}/role", s.updateMemberRole)
	mux.HandleFunc("DELETE /api/v1/team/members/{id}", s.removeTeamMember)
	mux.HandleFunc("GET /api/v1/team/members/{id}/tokens", s.listMemberTokens)
	mux.HandleFunc("POST /api/v1/team/members/{id}/tokens", s.createMemberToken)
	mux.HandleFunc("DELETE /api/v1/team/members/{id}/tokens/{client_id}", s.revokeMemberToken)

	// Webmail (org-slug@mail.<domain>)
	mux.HandleFunc("GET /api/v1/mail/info", s.mailInfo)
	mux.HandleFunc("GET /api/v1/mail/messages", s.listMail)
	mux.HandleFunc("POST /api/v1/mail/send", s.sendMail)
	mux.HandleFunc("GET /api/v1/mail/messages/{id}", s.getMail)
	mux.HandleFunc("DELETE /api/v1/mail/messages/{id}", s.deleteMail)
	mux.HandleFunc("GET /api/v1/mail/attachments/{id}", s.getMailAttachment)

	// Programatik REST API Token Yonetimi
	mux.HandleFunc("GET /api/v1/api-tokens", s.listAPITokens)
	mux.HandleFunc("POST /api/v1/api-tokens", s.createAPIToken)
	mux.HandleFunc("DELETE /api/v1/api-tokens/{id}", s.revokeAPIToken)

	// Ingress IP Izin Listesi (IP Allowlist)
	mux.HandleFunc("GET /api/v1/ip-allowlist", s.listIPRules)
	mux.HandleFunc("POST /api/v1/ip-allowlist", s.createIPRule)
	mux.HandleFunc("PATCH /api/v1/ip-allowlist/{id}", s.updateIPRule)
	mux.HandleFunc("DELETE /api/v1/ip-allowlist/{id}", s.deleteIPRule)

	return mux
}

// --- kiraci cozumu ----------------------------------------------------------

// tenantFor, istegin hangi kiraci adina yapildigini soyler.
//
// Kiraci MIDDLEWARE tarafindan context'e konur: oturum cerezinden (GitHub ile
// giren kullanicinin kiracisi) veya admin anahtarindan (varsayilan kiraci).
//
// Kapsamsiz bir istek buraya ULASMAMALIDIR; gelirse programlama hatasidir ve
// cagiran 500 dondurur. Sessizce varsayilan kiraciya dusmek, bir kapsam
// hatasini VERI SIZINTISINA cevirirdi.
func (s *Server) tenantFor(r *http.Request) (string, bool) {
	return tenantFromContext(r.Context())
}

// --- health / me -----------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ok",
		"version":           s.Version,
		"connected_clients": s.Hub.Count(),
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	// Middleware buraya varmadan once yetkiyi dogruladi. Dashboard'a hem giris
	// yontemini hem de KIRACIYI bildiriyoruz ki kullanici hangi hesapta
	// oldugunu gorebilsin.
	out := map[string]any{"authenticated": true, "method": "key"}

	if u, ok := userFromContext(r.Context()); ok && u != nil {
		out["method"] = "better-auth"
		out["user"] = map[string]any{
			"id":    u.ID,
			"email": u.Email,
			"name":  u.Name,
			"role":  u.Role,
		}
	} else if s.Sessions != nil {
		if c, err := r.Cookie(session.CookieName); err == nil {
			if sess, err := s.Sessions.Verify(c.Value); err == nil {
				out["method"] = "github"
				out["user"] = map[string]any{"github_login": sess.Login}
			}
		}
	}
	if tenantID, ok := s.tenantFor(r); ok {
		if t, err := s.Store.GetTenant(r.Context(), tenantID); err == nil {
			out["tenant"] = map[string]any{"id": t.ID, "slug": t.Slug}
		}
	}
	out["platform_admin"] = isPlatformAdmin(r.Context())
	writeJSON(w, http.StatusOK, out)
}

// --- platform admin --------------------------------------------------------

func (s *Server) adminGetStats(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	stats, err := s.Store.AdminGetGlobalStats(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	stats.OnlineClientsCount = s.Hub.Count()
	writeJSON(w, http.StatusOK, stats)
}

// adminListTenants, tum kiracilari sayilariyla birlikte listeler.
func (s *Server) adminListTenants(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	list, err := s.Store.AdminListTenantsWithCounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.TenantWithCounts{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminListClients(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	list, err := s.Store.AdminListAllClients(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range list {
		if st, ok := s.Hub.Statuses()[list[i].ID]; ok {
			list[i].Status = "online"
			list[i].Version = st.Version
			list[i].RemoteAddr = st.RemoteAddr
			ls := st.LastSeen
			list[i].LastSeenAt = &ls
		} else {
			list[i].Status = "offline"
		}
	}
	if list == nil {
		list = []store.ClientWithTenant{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) adminListHostnames(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	list, err := s.Store.AdminListAllHostnames(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.HostnameWithTenant{}
	}
	writeJSON(w, http.StatusOK, list)
}

// adminSwitchTenant, platform admin yetkisine sahip bir kullanicinin secilen kiraciya gecmesini saglar.
// Better Auth oturumu varsa session ve member tablolarini da gunceller.
func (s *Server) adminSwitchTenant(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca platform admin yetkisiyle kullanilabilir")
		return
	}

	var req struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.TenantID) == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "tenant_id gereklidir")
		return
	}

	target := strings.TrimSpace(req.TenantID)

	// Kiraciyi dogrula (ID veya slug ile)
	t, err := s.Store.GetTenant(r.Context(), target)
	if err != nil {
		t, err = s.Store.GetTenantBySlug(r.Context(), target)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "not_found", "kiraci bulunamadi")
			return
		}
	}

	// Better Auth kullanicisi varsa Postgres'te member ve session kayitlarini guncelle
	if u, ok := userFromContext(r.Context()); ok && u != nil && u.ID != "" {
		if pgSt, ok := s.Store.(*pgstore.Store); ok && pgSt.Pool() != nil {
			// 1. organization tablosunda var mi? Yoksa ekle
			_, _ = pgSt.Pool().Exec(r.Context(), `
				INSERT INTO organization (id, name, slug, "createdAt")
				VALUES ($1, $2, $2, now())
				ON CONFLICT (id) DO NOTHING
			`, t.ID, t.Slug)

			// 2. member tablosunda uyelik var mi? Yoksa ekle
			var memID string
			err := pgSt.Pool().QueryRow(r.Context(),
				`SELECT id FROM member WHERE "organizationId" = $1 AND "userId" = $2 LIMIT 1`,
				t.ID, u.ID,
			).Scan(&memID)
			if err != nil {
				b := make([]byte, 12)
				_, _ = crand.Read(b)
				newMemID := "mem_" + hex.EncodeToString(b)
				_, _ = pgSt.Pool().Exec(r.Context(), `
					INSERT INTO member (id, "organizationId", "userId", role, "createdAt")
					VALUES ($1, $2, $3, 'owner', now())
				`, newMemID, t.ID, u.ID)
			}

			// 3. Kullanicinin aktif oturumlarinda activeOrganizationId guncelle
			_, _ = pgSt.Pool().Exec(r.Context(), `
				UPDATE session SET "activeOrganizationId" = $1 WHERE "userId" = $2
			`, t.ID, u.ID)
		}

		// Onbellegi temizle
		if s.BetterAuth != nil {
			s.BetterAuth.InvalidateAll()
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tenant": map[string]string{
			"id":   t.ID,
			"slug": t.Slug,
		},
	})
}

// --- clients ---------------------------------------------------------------

// enrich, DB kaydini canli baglanti durumuyla zenginlestirir.
// status/version/remote_addr/last_seen DB'de TUTULMAZ — bunlar hub'dan gelir.
func (s *Server) enrich(c store.Client) store.Client {
	if sess, ok := s.Hub.Get(c.ID); ok {
		c.Status, c.Version, c.RemoteAddr = "online", sess.Version, sess.RemoteAddr
		ls := sess.LastSeen()
		c.LastSeenAt = &ls
		c.IsService = sess.IsService
		c.Metrics = sess.Metrics()
	} else if st, ok := s.Hub.Statuses()[c.ID]; ok {
		c.Status, c.Version, c.RemoteAddr = "online", st.Version, st.RemoteAddr
		ls := st.LastSeen
		c.LastSeenAt = &ls
	} else {
		c.Status = "offline"
	}
	return c
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	list, err := s.Store.ListClients(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Client{}
	}
	for i := range list {
		list[i] = s.enrich(list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name", "name bos olamaz")
		return
	}

	full, tokenID, hash, err := auth.GenerateClient()
	if err != nil {
		s.fail(w, err)
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateClient(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	c, err := s.Store.CreateClient(r.Context(), tenantID, strings.TrimSpace(body.Name), tokenID, hash)
	if err != nil {
		s.fail(w, err)
		return
	}

	// Token YALNIZCA burada doner; sunucu bundan sonra sadece hash'ini bilir.
	writeJSON(w, http.StatusCreated, map[string]any{
		"client": s.enrich(c),
		"token":  full,
	})
}

func (s *Server) getClient(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	c, err := s.Store.GetClient(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.enrich(c))
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if err := s.Store.DeleteClient(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	// Canli baglantiyi da kopar: token iptal edildi, oturum surmemeli.
	if sess, ok := s.Hub.Get(id); ok {
		sess.Close("client deleted")
	}
	// Client silinince tunelleri CASCADE ile gitti; yonlendirme tazelenmeli.
	s.tunnelsChanged()
	s.Events.Publish(events.TypeClientDisconnected, map[string]string{"client_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	full, tokenID, hash, err := auth.GenerateClient()
	if err != nil {
		s.fail(w, err)
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if err := s.Store.RotateClientToken(r.Context(), tenantID, id, tokenID, hash); err != nil {
		s.fail(w, err)
		return
	}
	// Eski token artik gecersiz — mevcut oturumu kopar ki yeni tokenla baglansin.
	if sess, ok := s.Hub.Get(id); ok {
		sess.Close("token rotated")
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": full})
}

// --- tunnels ---------------------------------------------------------------

func (s *Server) listTunnels(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	list, err := s.Store.ListTunnels(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	list, err = s.withHostnames(r, tenantID, list)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Tunnel{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createTunnel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// Name, tunelin kisa adi (tek DNS etiketi). Tam hostname DEGIL:
		// sunucu bunu platform domainiyle birlestirir.
		Name       string `json:"name"`
		ClientID   string `json:"client_id"`
		Target     string `json:"target"`
		HostnameID string `json:"hostname_id,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == "" || body.ClientID == "" || body.Target == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields",
			"name, client_id ve target zorunlu")
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	if err := validateLabel(name); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_name", err.Error())
		return
	}
	if !validTarget(body.Target) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_target",
			"target http:// veya https:// ile baslamali")
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateTunnel(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	if _, err := s.Store.GetClient(r.Context(), tenantID, body.ClientID); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "client_not_found",
			"belirtilen client_id bulunamadi")
		return
	}

	t, err := s.Store.CreateTunnel(r.Context(), tenantID, body.ClientID, body.Target)
	if err != nil {
		s.fail(w, err)
		return
	}

	if body.HostnameID != "" {
		// Kullanici var olan bos bir domain sectiyse onu tunelle esle
		if h, err := s.Store.GetHostnameByID(r.Context(), tenantID, body.HostnameID); err == nil {
			if err := s.Store.AttachHostname(r.Context(), tenantID, body.HostnameID, t.ID); err == nil {
				h.TunnelID = t.ID
				t.Hostnames = []store.Hostname{h}
			} else {
				s.Logger.Warn("secilen domain tunelle eslenemedi", "hostname_id", body.HostnameID, "hata", err)
			}
		}
	} else if s.PlatformDomain != "" {
		// Secilen veya onceden olusturulan domain yoksa girilen ada gore otomatik domain ata
		if tenant, err := s.Store.GetTenant(r.Context(), tenantID); err != nil {
			s.Logger.Warn("kapsamli ad icin kiraci okunamadi", "kiraci", tenantID, "hata", err)
		} else {
			fqdn := scopedFQDN(name, tenant.Slug, s.PlatformDomain)
			h, err := s.Store.AddHostname(r.Context(), tenantID, t.ID, fqdn, store.HostTypeScoped)
			if err != nil {
				s.Logger.Warn("kapsamli ad verilemedi", "fqdn", fqdn, "hata", err)
			} else {
				t.Hostnames = []store.Hostname{h}
			}
		}
	}

	s.tunnelsChanged()
	s.pushClientConfig(body.ClientID)
	s.Events.Publish(events.TypeTunnelCreated, t)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTunnel(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	t, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if t.Hostnames, err = s.Store.ListHostnamesByTunnel(r.Context(), tenantID, t.ID); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) updateTunnel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target  *string `json:"target"`
		Enabled *bool   `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Target != nil && !validTarget(*body.Target) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_target",
			"target http:// veya https:// ile baslamali")
		return
	}

	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	t, err := s.Store.UpdateTunnel(r.Context(), tenantID, r.PathValue("id"),
		store.TunnelPatch{Target: body.Target, Enabled: body.Enabled})
	if err != nil {
		s.fail(w, err)
		return
	}

	s.tunnelsChanged()
	s.pushClientConfig(t.ClientID)
	s.Events.Publish(events.TypeTunnelUpdated, t)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	// Silmeden ONCE istemciyi ogren; silindikten sonra o istemciye guncel
	// (artik bu tunel olmayan) listeyi config_update ile it.
	clientID := ""
	if t, err := s.Store.GetTunnel(r.Context(), tenantID, id); err == nil {
		clientID = t.ClientID
	}
	if err := s.Store.DeleteTunnel(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	s.pushClientConfig(clientID)
	s.Events.Publish(events.TypeTunnelDeleted, map[string]string{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

// --- requests / events -----------------------------------------------------

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 1000)
		}
	}

	// Kalici depo (Postgres) varsa gelismis filtreli sorgu; yoksa bellek ring.
	if s.Store != nil {
		f := reqlog.Filter{
			TunnelID: q.Get("tunnel_id"),
			Hostname: q.Get("hostname"),
			Method:   q.Get("method"),
			Query:    q.Get("q"),
			Limit:    limit,
		}
		if v := q.Get("offset"); v != "" {
			f.Offset, _ = strconv.Atoi(v)
		}
		// Status: "2xx"/"4xx" (sinif) veya "404" (kesin).
		if st := q.Get("status"); st != "" {
			if strings.HasSuffix(st, "xx") && len(st) == 3 {
				if d, err := strconv.Atoi(st[:1]); err == nil {
					f.StatusMin, f.StatusMax = d*100, d*100+99
				}
			} else if n, err := strconv.Atoi(st); err == nil {
				f.StatusMin, f.StatusMax = n, n
			}
		}
		if v := q.Get("min_dur"); v != "" {
			f.MinDurMS, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := q.Get("max_dur"); v != "" {
			f.MaxDurMS, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := q.Get("since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				f.Since = t
			}
		}
		if v := q.Get("until"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				f.Until = t
			}
		}
		// Kiraci kapsami: platform admin tum kiracilari gorur; degilse
		// yalnizca kendi (etkin) kiracisinin loglari.
		if !isPlatformAdmin(r.Context()) {
			if tenantID, ok := s.tenantFor(r); ok {
				f.TenantID = tenantID
			}
		}

		if logs, err := s.Store.QueryRequestLogs(r.Context(), f); err == nil {
			if logs == nil {
				logs = []reqlog.Entry{}
			}
			writeJSON(w, http.StatusOK, logs)
			return
		}
		// Sorgu hatasinda bellek ring'e dus (asagida).
	}

	writeJSON(w, http.StatusOK, s.Log.List(limit, q.Get("tunnel_id")))
}

// stream, GET /api/v1/events — SSE canli olay akisi.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming_unsupported",
			"bu baglanti akis desteklemiyor")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Ara proxy'ler SSE'yi tamponlamasin.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, stop := s.Events.Subscribe()
	defer stop()

	// Bosta kalan baglantiyi ara proxy'ler kapatmasin diye periyodik yorum.
	ticker := time.NewTicker(events.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()

		case e, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(e.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			flusher.Flush()
		}
	}
}

// --- yardimcilar -----------------------------------------------------------

func validTarget(t string) bool {
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")
}

func (s *Server) tunnelsChanged() {
	if s.OnTunnelChange != nil {
		s.OnTunnelChange()
	}
}

// pushClientConfig, panelden tunel eklenip/silinip/degistirilince BAGLI istemciye
// guncel tunel listesini config_update ile gonderir. Boylece masaustu uygulamasi
// (ve agent) yeniden baglanmaya gerek kalmadan aninda tazelenir. Istemci bagli
// degilse sessizce gecer (bir sonraki hello_ack'te zaten guncel liste gider).
//
// ASENKRON: WS yazimi (Session.Send) writeMu tutar ve yavas/tikali bir istemcide
// writeTimeout'a kadar bloklanabilir. Bunu HTTP yaniti icinde beklememek icin
// ayri bir goroutine + kopuk (detached) context ile calistiririz; istek context'i
// yanit dondugunde iptal olacagi icin kullanilmaz.
func (s *Server) pushClientConfig(clientID string) {
	if s.Hub == nil || clientID == "" {
		return
	}
	sess, ok := s.Hub.Get(clientID)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tunnels, err := s.Store.ListTunnelsByClient(ctx, clientID)
		if err != nil {
			s.logger().Warn("config_update: tuneller alinamadi", "client", clientID, "hata", err)
			return
		}
		names, err := s.Store.ListHostnamesByClient(ctx, clientID)
		if err != nil {
			s.logger().Warn("config_update: adlar alinamadi", "client", clientID, "hata", err)
			return
		}
		byTunnel := make(map[string][]string, len(tunnels))
		for _, n := range names {
			byTunnel[n.TunnelID] = append(byTunnel[n.TunnelID], n.FQDN)
		}
		specs := make([]protocol.TunnelSpec, 0, len(tunnels))
		for _, t := range tunnels {
			if t.Enabled {
				specs = append(specs, protocol.TunnelSpec{ID: t.ID, Hostnames: byTunnel[t.ID], Target: t.Target})
			}
		}
		if err := sess.Send(ctx, protocol.ConfigUpdate{
			Type:    protocol.TypeConfigUpdate,
			Tunnels: specs,
		}, protocol.TypeConfigUpdate); err != nil {
			s.logger().Warn("config_update gonderilemedi", "client", clientID, "hata", err)
		}
	}()
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "kayit bulunamadi")
		return
	}
	s.logger().Error("API hatasi", "hata", err)
	writeJSONError(w, http.StatusInternalServerError, "internal", "beklenmeyen hata")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decode, JSON govdesini cozer. Basarisizsa yaniti kendisi yazar ve false doner.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	// Govde siniri: yonetim API'sinde buyuk govde beklenmiyor; sinirsiz
	// okumak bellek tuketme vektoru olurdu.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}
