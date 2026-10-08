package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/domain"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/rawproxy"
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

	// Captures, FAZ 2 istek inspector deposu (opt-in tam yakalama). nil olabilir.
	Captures *reqlog.CaptureStore

	// DNSVerifier, custom domain dogrulamasi icin DNS istemcisi (nil ise DefaultDNSVerifier).
	DNSVerifier domain.DNSVerifier

	// PlatformDomain, kiraci subdomainlerinin uretilecegi domain
	// (or. "rpshell.app"). BOS ise subdomain tahsisi KAPALIDIR: tek etiket
	// kendi basina bir adres degildir, uydurmaktansa acikca reddediyoruz.
	PlatformDomain string

	// PanelHosts, panelin/kontrol duzleminin sunuldugu hostlar (--control-host).
	// WebSocket Origin kontrolu yalnizca bunlara izin verir; kiraci tunel alt
	// alanlari (*.PlatformDomain) panel sayilmaz. Bos ise apex/www/panel/app.
	PanelHosts []string

	// OnTunnelChange, tunel eklendiginde/degistiginde/silindiginde cagrilir.
	// Ingress router'ini HEMEN tazelemek icin: 10sn yoklamayi beklemeye gerek kalmaz.
	OnTunnelChange func()

	// OnPlanChange, bir kiracinin plani/aboneligi degisince cagrilir (bant
	// genisligi onbellegini atmak icin). nil olabilir.
	OnPlanChange func(tenantID string)

	// ReservedHost (K1), FQDN platformun kendi hostu mu (control/panel, tanitim,
	// analytics, ziyaretci callback'i). true ise kiraci bu adi alamaz. nil ise
	// yalnizca reserved_names tablosu denetlenir.
	ReservedHost func(fqdn string) bool

	// LBHealth (FAZ 4 / F21), tunelin adaylarinin CANLI saglik durumu.
	// Ingress router'dan saglanir; nil ise saglik bilgisi donulmez.
	LBHealth func(tunnelID string, clients []string) []ingress.BackendStatus

	// Door, web ile kapi acma servisi (grant iptali + onbellek gecersiz kilma). nil olabilir.
	Door *door.Service

	// UDPStats (FAZ 4 / F24), UDP tunelinin canli istatistikleri (rawproxy).
	UDPStats func(tunnelID string) (rawproxy.UDPLiveStats, bool)

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
	// MailClient, mail istemcisi erisimi (IMAP/SMTP gonderim) sunucu ayarlari.
	// nil ise "Mail uygulamalarinda kullan" ozelligi kapalidir.
	MailClient *mail.ClientConfig
}

func writeEntitlementError(w http.ResponseWriter, err error) {
	var entErr *entitlements.EntitlementError
	if errors.As(err, &entErr) {
		// api_contract §1 bicimi (writeJSONError ile ayni): panel ve istemciler
		// err.data.error.code / .message okur; limit ayrintilari da error icinde.
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]any{
				"code":     entErr.Code,
				"message":  entErr.Message,
				"resource": entErr.Resource,
				"limit":    entErr.Limit,
				"current":  entErr.Current,
			},
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
	mux.HandleFunc("GET /api/v1/status", s.statusPublic)
	mux.HandleFunc("GET /api/v1/me", s.me)
	mux.HandleFunc("GET /api/v1/openapi.yaml", s.openapiSpec)
	mux.HandleFunc("GET /api/v1/openapi.en.yaml", s.openapiSpecEN)

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

	// FAZ 0 (F00) — Projeler
	mux.HandleFunc("GET /api/v1/projects", s.listProjects)
	mux.HandleFunc("POST /api/v1/projects", s.createProject)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.deleteProject)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", s.renameProject)
	// Organizasyon silme: yalnizca oturum acmis owner/admin, API token ile DEGIL.
	mux.HandleFunc("DELETE /api/v1/organization", s.deleteOrganization)

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
	// Yuk dengeleme + saglik kontrolu (FAZ 4 / F21)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/lb", s.getTunnelLB)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/lb", s.setTunnelLB)
	// UDP ileri + oyun sunucusu durumu (FAZ 4 / F24)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/udp", s.getTunnelUDP)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/udp", s.setTunnelUDP)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/game-status", s.getGameStatus)
	// Web ile kapi acma (ham TCP/UDP tunelleri)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/door", s.getTunnelDoor)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/door", s.setTunnelDoor)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/door/grants", s.listDoorGrants)
	mux.HandleFunc("DELETE /api/v1/tunnels/{id}/door/grants/{grantId}", s.revokeDoorGrant)
	mux.HandleFunc("PATCH /api/v1/tunnels/{id}", s.updateTunnel)
	mux.HandleFunc("DELETE /api/v1/tunnels/{id}", s.deleteTunnel)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/access", s.getTunnelAccess)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/access", s.setTunnelAccess)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/access-events", s.listAccessEvents)
	mux.HandleFunc("GET /api/v1/tunnels/{id}/access-events/summary", s.accessEventsSummary)
	// FAZ 5 / HA — tünel replikaları (çoklu agent yük dengeleme).
	mux.HandleFunc("GET /api/v1/tunnels/{id}/replicas", s.getTunnelReplicas)
	mux.HandleFunc("POST /api/v1/tunnels/{id}/replicas", s.addTunnelReplica)
	mux.HandleFunc("DELETE /api/v1/tunnels/{id}/replicas/{clientID}", s.removeTunnelReplica)
	// FAZ 6 — trafik politikası (header/redirect kuralları).
	mux.HandleFunc("GET /api/v1/tunnels/{id}/traffic", s.getTunnelTraffic)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/traffic", s.setTunnelTraffic)
	// FAZ 6.3 — per-tünel metrikler.
	mux.HandleFunc("GET /api/v1/tunnels/{id}/metrics", s.getTunnelMetrics)
	// FAZ 6.4 — metrik uyarıları.
	mux.HandleFunc("GET /api/v1/tunnels/{id}/alert", s.getTunnelAlert)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/alert", s.setTunnelAlert)
	// FAZ 6.6 — mTLS (istemci sertifikası).
	mux.HandleFunc("GET /api/v1/tunnels/{id}/mtls", s.getTunnelMTLS)
	mux.HandleFunc("PUT /api/v1/tunnels/{id}/mtls", s.setTunnelMTLS)

	mux.HandleFunc("GET /api/v1/hostnames", s.listHostnames)
	mux.HandleFunc("POST /api/v1/hostnames", s.createHostname)
	mux.HandleFunc("POST /api/v1/hostnames/custom", s.createCustomHostname)
	mux.HandleFunc("PATCH /api/v1/hostnames/{id}", s.patchHostname)
	mux.HandleFunc("POST /api/v1/hostnames/{id}/verify", s.verifyHostname)
	mux.HandleFunc("POST /api/v1/hostnames/{id}/dns-check", s.dnsCheckHostname)
	mux.HandleFunc("DELETE /api/v1/hostnames/{id}", s.deleteHostname)
	// FAZ 6.5 — yol tabanlı yönlendirme (hostname bazlı).
	mux.HandleFunc("GET /api/v1/hostnames/{id}/paths", s.listPathRoutes)
	mux.HandleFunc("POST /api/v1/hostnames/{id}/paths", s.addPathRoute)
	mux.HandleFunc("DELETE /api/v1/hostnames/{id}/paths/{routeID}", s.deletePathRoute)

	// Platform yonetimi: YALNIZCA admin anahtariyla (kiraci oturumu yetmez).
	mux.HandleFunc("POST /api/v1/admin/clients/notify-update", s.adminNotifyUpdate)
	mux.HandleFunc("GET /api/v1/admin/stats", s.adminGetStats)
	mux.HandleFunc("GET /api/v1/admin/tenants", s.adminListTenants)
	mux.HandleFunc("GET /api/v1/admin/clients", s.adminListClients)
	mux.HandleFunc("GET /api/v1/admin/hostnames", s.adminListHostnames)
	mux.HandleFunc("POST /api/v1/admin/switch-tenant", s.adminSwitchTenant)

	// Kötüye kullanım (FAZ 4). Rapor ucu PUBLIC (main.go public dalinda);
	// dondurma + liste PLATFORM ADMIN.
	mux.HandleFunc("POST /api/v1/abuse", s.reportAbuse)
	mux.HandleFunc("GET /api/v1/admin/abuse-reports", s.adminListAbuseReports)
	mux.HandleFunc("POST /api/v1/admin/abuse/freeze", s.adminFreeze)

	mux.HandleFunc("GET /api/v1/subscription", s.getSubscription)
	mux.HandleFunc("GET /api/v1/plans", s.listPlans)
	mux.HandleFunc("GET /api/v1/requests", s.listRequests)
	// FAZ 2 — İstek inspector: yakalama ayarı + tek istek detayı + replay.
	mux.HandleFunc("GET /api/v1/requests/capture", s.getCaptureSettings)
	mux.HandleFunc("POST /api/v1/requests/capture", s.setCaptureSettings)
	mux.HandleFunc("GET /api/v1/requests/{id}", s.getRequestDetail)
	mux.HandleFunc("POST /api/v1/requests/{id}/replay", s.replayRequest)
	mux.HandleFunc("GET /api/v1/events", s.stream)

	// Ekip ve Uye Yonetimi (Team & Member Tokens)
	mux.HandleFunc("GET /api/v1/team/members", s.listTeamMembers)
	mux.HandleFunc("POST /api/v1/team/members/invite", s.inviteTeamMember)
	mux.HandleFunc("PATCH /api/v1/team/members/{id}/role", s.updateMemberRole)
	mux.HandleFunc("DELETE /api/v1/team/members/{id}", s.removeTeamMember)
	mux.HandleFunc("GET /api/v1/team/members/{id}/tokens", s.listMemberTokens)
	mux.HandleFunc("POST /api/v1/team/members/{id}/tokens", s.createMemberToken)
	mux.HandleFunc("DELETE /api/v1/team/members/{id}/tokens/{client_id}", s.revokeMemberToken)
	mux.HandleFunc("POST /api/v1/team/members/{id}/resend", s.resendInvitation)

	// Ekip davetleri: davetli kendi hesabiyla kabul/ret eder.
	mux.HandleFunc("GET /api/v1/invitations", s.listMyInvitations)
	mux.HandleFunc("GET /api/v1/invitations/{id}", s.getInvitation)
	mux.HandleFunc("POST /api/v1/invitations/{id}/accept", s.acceptInvitation)
	mux.HandleFunc("POST /api/v1/invitations/{id}/decline", s.declineInvitation)

	// Webmail (org-slug@mail.<domain>)
	mux.HandleFunc("GET /api/v1/mail/info", s.mailInfo)
	mux.HandleFunc("GET /api/v1/mail/messages", s.listMail)
	mux.HandleFunc("POST /api/v1/mail/send", s.sendMail)
	mux.HandleFunc("GET /api/v1/mail/messages/{id}", s.getMail)
	mux.HandleFunc("DELETE /api/v1/mail/messages/{id}", s.deleteMail)
	mux.HandleFunc("POST /api/v1/mail/messages/{id}/move", s.moveMail)
	mux.HandleFunc("GET /api/v1/mail/folders", s.listMailFolders)
	mux.HandleFunc("GET /api/v1/mail/attachments/{id}", s.getMailAttachment)
	// Mail istemcileri (Gmail uygulamasi, Outlook, Apple Mail, Thunderbird) icin
	// uygulama parolalari ve otomatik yapilandirma.
	mux.HandleFunc("GET /api/v1/mail/app-passwords", s.listMailAppPasswords)
	mux.HandleFunc("POST /api/v1/mail/app-passwords", s.createMailAppPassword)
	mux.HandleFunc("DELETE /api/v1/mail/app-passwords/{id}", s.revokeMailAppPassword)
	mux.HandleFunc("GET /api/v1/mail/mobileconfig", s.mailMobileConfig)

	// Programatik REST API Token Yonetimi
	mux.HandleFunc("GET /api/v1/api-tokens", s.listAPITokens)
	mux.HandleFunc("POST /api/v1/api-tokens", s.createAPIToken)
	mux.HandleFunc("DELETE /api/v1/api-tokens/{id}", s.revokeAPIToken)
	mux.HandleFunc("POST /api/v1/api-tokens/{id}/rotate", s.rotateAPIToken)
	mux.HandleFunc("GET /api/v1/tokens", s.listAPITokens)
	mux.HandleFunc("POST /api/v1/tokens", s.createAPIToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.revokeAPIToken)
	mux.HandleFunc("POST /api/v1/tokens/{id}/rotate", s.rotateAPIToken)

	// Secret Vault (FAZ 1 / F06)
	mux.HandleFunc("GET /api/v1/secrets", s.listSecrets)
	mux.HandleFunc("POST /api/v1/secrets", s.createSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{id}", s.deleteSecret)

	// Cihazlar (FAZ 3 / F14) — istemci kaydi + kalici cihaz bilgisi + canli durum.
	// /clients uclari KALDIRILMADI: mevcut entegrasyonlar bozulmasin.
	// Zorven Network (FAZ 3 / F17): ozel kaynaklar + SOCKS istemcisinin
	// baglandigi WebSocket ucu (yalnizca API token).
	mux.HandleFunc("GET /api/v1/network/resources", s.listNetworkResources)
	mux.HandleFunc("POST /api/v1/network/resources", s.createNetworkResource)
	mux.HandleFunc("GET /api/v1/network/connect", s.connectNetwork)

	mux.HandleFunc("GET /api/v1/devices", s.listDevices)
	mux.HandleFunc("GET /api/v1/devices/{id}", s.getDevice)

	// Uzaktan ajan yapilandirmasi (FAZ 3 / F15). Burada yazilan izin bir
	// GARANTI degildir: ajan yerel bayraklariyla kesistirir, yerelde kapali
	// olan izin uzaktan ACILAMAZ.
	mux.HandleFunc("GET /api/v1/devices/{id}/config", s.getDeviceConfig)
	mux.HandleFunc("PUT /api/v1/devices/{id}/config", s.setDeviceConfig)

	// Cihaz etiketleri (FAZ 3 / F16). Yazma islemleri yalnizca owner/admin.

	// Birlesik Policy motoru (FAZ 1 / F04)
	mux.HandleFunc("GET /api/v1/policies", s.listPolicies)
	mux.HandleFunc("POST /api/v1/policies", s.createPolicy)
	mux.HandleFunc("GET /api/v1/policies/{id}", s.getPolicy)
	mux.HandleFunc("PUT /api/v1/policies/{id}", s.updatePolicy)
	mux.HandleFunc("DELETE /api/v1/policies/{id}", s.deletePolicy)
	mux.HandleFunc("POST /api/v1/policies/{id}/bind", s.bindPolicy)
	mux.HandleFunc("POST /api/v1/policies/{id}/unbind", s.unbindPolicy)

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

// projectFor, istegin hangi proje adina yapildigini soyler (FAZ 0 / F00).
func (s *Server) projectFor(r *http.Request) (string, bool) {
	return projectFromContext(r.Context())
}

// --- health / me -----------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ok",
		"version":           s.Version,
		"connected_clients": s.Hub.Count(),
	})
}

// serverStartedAt, süreç başlangıcı (uptime hesabı için). Paket yüklenince set edilir
// ki ayrı bir kurulum adımı gerekmesin.
var serverStartedAt = time.Now()

// statusPublic, herkese açık durum sayfasının beslediği JSON (FAZ 6).
//
//	GET /api/v1/status — bileşen sağlığı (api, veritabanı, tünel ingress) + uptime.
//
// Kimlik gerektirmez; status.html bunu çeker. DB ping kısa timeout'la yapılır ki
// veritabanı takılıysa sayfa yine de yanıt versin.
func (s *Server) statusPublic(w http.ResponseWriter, r *http.Request) {
	components := []map[string]any{
		{"name": "api", "status": "operational"},
		{"name": "tunnel_ingress", "status": "operational"},
	}
	overall := "operational"

	dbStatus := "operational"
	if s.Store != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Store.Ping(ctx); err != nil {
			dbStatus = "down"
			overall = "degraded"
		}
	}
	components = append(components, map[string]any{"name": "database", "status": dbStatus})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":            overall,
		"version":           s.Version,
		"uptime_seconds":    int(time.Since(serverStartedAt).Seconds()),
		"connected_clients": s.Hub.Count(),
		"components":        components,
		"checked_at":        time.Now().UTC().Format(time.RFC3339),
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

// adminNotifyUpdate, POST /api/v1/admin/clients/notify-update — tum bagli
// istemcilere "yeni surum var, guncelle" sinyali yayinlar (yeni ikili deploy
// edildikten sonra elle veya betikle tetiklenir). Yalnizca platform admin.
func (s *Server) adminNotifyUpdate(w http.ResponseWriter, r *http.Request) {
	if !isPlatformAdmin(r.Context()) {
		writeJSONError(w, http.StatusForbidden, "forbidden",
			"bu uc yalnizca admin anahtariyla kullanilabilir")
		return
	}
	if s.Hub == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "no_hub", "hub mevcut degil")
		return
	}
	n := s.Hub.BroadcastUpdateAvailable(r.Context(), "")
	writeJSON(w, http.StatusOK, map[string]any{"notified": n})
}

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

			// 2. UYELIK EKLENMEZ: platform admin hedef kiracida "hayalet"tir.
			// Onceden buraya 'owner' rolunde gercek bir member satiri yaziliyordu;
			// bu satir kalici oldugundan admin o kiracinin ekibinde sahip/admin
			// olarak gorunuyor ve uye kotasini tuketiyordu. Yetki zaten
			// middleware'de isDefaultTenantOwner ile verilir; uyelik gerekmez.

			// 3. Kullanicinin aktif oturumlarinda activeOrganizationId guncelle
			_, _ = pgSt.Pool().Exec(r.Context(), `
				UPDATE session SET "activeOrganizationId" = $1 WHERE "userId" = $2
			`, t.ID, u.ID)
		}

		// Onbellegi temizle: yalnizca bu kullanicinin oturumlari degisti;
		// tum kiracilarin onbellegini bosaltmaya gerek yok.
		if s.BetterAuth != nil {
			s.BetterAuth.InvalidateUser(u.ID)
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
		c.AppKind = sess.AppKind
		c.Metrics = sess.Metrics()
	} else if st, ok := s.Hub.Statuses()[c.ID]; ok {
		c.Status, c.Version, c.RemoteAddr = "online", st.Version, st.RemoteAddr
		c.AppKind = st.AppKind
		ls := st.LastSeen
		c.LastSeenAt = &ls
	} else {
		c.Status = "offline"
	}
	// Surum bayragi yalnizca CANLI istemci icin: tur (masaustu/CLI) ancak
	// bagliyken bilinir, offline iken yanlis manifestle karsilastirilmaz.
	if c.Status == "online" {
		if latest := latestClientVersion(manifestFor(c.AppKind)); isOutdated(c.Version, latest) {
			c.UpdateAvailable, c.LatestVersion = true, latest
		}
	}
	return c
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	projID, _ := s.projectFor(r)
	var list []store.Client
	var err error
	if projID != "" {
		list, err = s.Store.ListClientsByProject(r.Context(), tenantID, projID)
	} else {
		list, err = s.Store.ListClients(r.Context(), tenantID)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Client{}
	}
	// F16: cagiranin erisemedigi cihazlar listede HIC gorunmez.
	if list, err = s.filterClientsForCaller(r, tenantID, list); err != nil {
		s.fail(w, err)
		return
	}
	for i := range list {
		list[i] = s.enrich(list[i])
	}
	if limit, cursor, ok := parsePage(r); ok {
		page, next, more := paginate(list, func(c store.Client) string { return c.ID }, limit, cursor)
		setPageHeaders(w, r, next, more)
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsWrite) {
		return
	}
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
	projID, _ := s.projectFor(r)
	c, err := s.Store.CreateClientWithProject(r.Context(), tenantID, strings.TrimSpace(body.Name), tokenID, hash, projID)
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
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
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
	if !s.requireDeviceAccess(w, r, tenantID, c.ID) {
		return
	}
	writeJSON(w, http.StatusOK, s.enrich(c))
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsWrite) {
		return
	}
	id := r.PathValue("id")
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.requireDeviceAccess(w, r, tenantID, id) {
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
	s.Events.PublishTenant(tenantID, events.TypeClientDisconnected, map[string]string{"client_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsWrite) {
		return
	}
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
	if !s.requireDeviceAccess(w, r, tenantID, id) {
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
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}
	projID, _ := s.projectFor(r)
	var list []store.Tunnel
	var err error
	if projID != "" {
		list, err = s.Store.ListTunnelsByProject(r.Context(), tenantID, projID)
	} else {
		list, err = s.Store.ListTunnels(r.Context(), tenantID)
	}
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
	if limit, cursor, ok := parsePage(r); ok {
		page, next, more := paginate(list, func(t store.Tunnel) string { return t.ID }, limit, cursor)
		setPageHeaders(w, r, next, more)
		writeJSON(w, http.StatusOK, page)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Gecici tunel TTL sinirlari (FAZ 2 / F07). Alt sinir, saniyelik omurlu
// kayitlarla supurucuyu mesgul etmemek icin; ust sinir, "gecici" olanin
// kaliciya donusmemesi icin.
const (
	MinTunnelTTLSec = 60
	MaxTunnelTTLSec = 24 * 60 * 60
)

func (s *Server) createTunnel(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	var body struct {
		// Name, tunelin kisa adi (tek DNS etiketi). Tam hostname DEGIL:
		// sunucu bunu platform domainiyle birlestirir.
		Name       string `json:"name"`
		ClientID   string `json:"client_id"`
		Target     string `json:"target"`
		HostnameID string `json:"hostname_id,omitempty"`
		// NoDomain (FAZ 6.5): true ise otomatik domain ATANMAZ. Tünel yalnızca yol
		// tabanlı yönlendirmede hedef olarak kullanılacaksa (kendi adresi olmadan).
		NoDomain bool `json:"no_domain,omitempty"`
		// TTLSec (FAZ 2 / F07): >0 ise tünel GECICIDIR ve süre dolunca arka plan
		// süpürücüsü tarafından kaldırılır. 0 / yok = kalıcı tünel.
		TTLSec int `json:"ttl_sec,omitempty"`
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
	// Otomatik kapsamli ad (ad--kiraci) verilecekse marka deseni reddedilir.
	if !body.NoDomain && body.HostnameID == "" && store.ContainsBrandKeyword(name) {
		writeJSONError(w, http.StatusConflict, "name_reserved", brandReservedMsg)
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
	// O6 (F16): erisimi kisitli bir cihaza tunel acilamaz.
	if !s.requireDeviceAccess(w, r, tenantID, body.ClientID) {
		return
	}

	projID, _ := s.projectFor(r)
	var t store.Tunnel
	var err error
	if body.TTLSec > 0 {
		// Gecici tunel. Ust sinir var cunku "gecici" olan sey uzatilabilir
		// olmamali; daha uzun omur isteyen kalici tunel acar.
		if body.TTLSec < MinTunnelTTLSec || body.TTLSec > MaxTunnelTTLSec {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_ttl",
				fmt.Sprintf("ttl_sec %d ile %d saniye arasinda olmali", MinTunnelTTLSec, MaxTunnelTTLSec))
			return
		}
		expires := time.Now().UTC().Add(time.Duration(body.TTLSec) * time.Second)
		t, err = s.Store.CreateEphemeralTunnel(r.Context(), tenantID, body.ClientID, body.Target, projID, expires)
	} else {
		t, err = s.Store.CreateTunnelWithProject(r.Context(), tenantID, body.ClientID, body.Target, projID)
	}
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
	} else if !body.NoDomain && s.PlatformDomain != "" {
		// Secilen veya onceden olusturulan domain yoksa girilen ada gore otomatik domain ata.
		// NoDomain=true ise atlanir (yol-yonlendirme hedefi olarak adressiz tunel).
		if tenant, err := s.Store.GetTenant(r.Context(), tenantID); err != nil {
			s.Logger.Warn("kapsamli ad icin kiraci okunamadi", "kiraci", tenantID, "hata", err)
		} else {
			fqdn := scopedFQDN(name, tenant.Slug, s.PlatformDomain)
			h, err := s.Store.AddHostnameWithProject(r.Context(), tenantID, t.ID, fqdn, store.HostTypeScoped, projID)
			if err != nil {
				s.Logger.Warn("kapsamli ad verilemedi", "fqdn", fqdn, "hata", err)
			} else {
				t.Hostnames = []store.Hostname{h}
				s.autoScanHostname(r.Context(), fqdn)
			}
		}
	}

	s.tunnelsChanged()
	s.pushClientConfig(body.ClientID)
	s.Events.PublishTenant(tenantID, events.TypeTunnelCreated, t)
	s.audit(r, "tunnel.create", t.ID, body.Target)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTunnel(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
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
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	var body struct {
		Target   *string `json:"target"`
		Enabled  *bool   `json:"enabled"`
		ClientID *string `json:"client_id"`
		Proto    *string `json:"proto"`
		Exposure *string `json:"exposure"`
	}
	if !decode(w, r, &body) {
		return
	}
	// Hedef, HTTP tünelinde http(s):// olmalı; ham TCP/UDP'de host:port
	// (opsiyonel tcp://) kabul edilir. Kesin proto burada bilinmeyebilir, bu
	// yüzden iki biçimden birine uyması yeter.
	if body.Target != nil && !validTarget(*body.Target) && !validRawTarget(*body.Target) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_target",
			"target http(s):// ile başlamalı veya host:port biçiminde olmalı")
		return
	}
	// Protokol / maruziyet doğrulaması + normalizasyon.
	if body.Proto != nil || body.Exposure != nil {
		proto, exposure, err := normalizeProtoExposure(body.Proto, body.Exposure)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_proto", err.Error())
			return
		}
		body.Proto = &proto
		body.Exposure = &exposure
	}

	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant",
			"istek kiraci kapsami olmadan ulasti")
		return
	}

	// Ozel kaynak (F17) PATCH ile public yapilamaz ve protokolu degistirilemez:
	// aksi halde exposure=port ile kurum ici bir kaynak internete acilirdi.
	// Hedef ve etkinlik degistirilebilir; public yapmak icin silip yeniden olusturun.
	if body.Proto != nil || body.Exposure != nil {
		if cur, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id")); err == nil &&
			cur.Exposure == store.ExposurePrivate {
			writeJSONError(w, http.StatusUnprocessableEntity, "private_resource",
				"özel kaynağın protokolü veya maruziyeti değiştirilemez; public yapmak için silip yeniden oluşturun")
			return
		}
	}

	// İstemci değişikliği: yeni istemci bu kiracıya ait mi? Eski istemciyi de
	// öğren ki tüneli ondan düşürecek config_update itebilelim.
	oldClientID := ""
	// O6 (F16): mevcut tunelin bagli oldugu cihaza erisim yoksa guncelleme de yok.
	if cur, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id")); err == nil {
		if !s.requireDeviceAccess(w, r, tenantID, cur.ClientID) {
			return
		}
	}
	if body.ClientID != nil {
		if strings.TrimSpace(*body.ClientID) == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_client", "client_id boş olamaz")
			return
		}
		if _, err := s.Store.GetClient(r.Context(), tenantID, *body.ClientID); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "client_not_found", "belirtilen client_id bulunamadi")
			return
		}
		if !s.requireDeviceAccess(w, r, tenantID, *body.ClientID) {
			return
		}
		if cur, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id")); err == nil {
			oldClientID = cur.ClientID
		}
	}

	t, err := s.Store.UpdateTunnel(r.Context(), tenantID, r.PathValue("id"),
		store.TunnelPatch{Target: body.Target, Enabled: body.Enabled, ClientID: body.ClientID, Proto: body.Proto, Exposure: body.Exposure})
	if err != nil {
		s.fail(w, err)
		return
	}

	// Rezerve port yaşam döngüsü: port modundaysa ve portu yoksa tahsis et;
	// port modundan çıktıysa portu serbest bırak.
	if t.Exposure == store.ExposurePort && (t.Proto == store.ProtoTCP || t.Proto == store.ProtoUDP) {
		if t.PublicPort == 0 {
			if p, aerr := s.allocateTunnelPort(r.Context(), tenantID, t.ID); aerr == nil {
				t.PublicPort = p
			} else {
				s.Logger.Warn("rezerve port tahsis edilemedi", "tunnel", t.ID, "hata", aerr)
			}
		}
	} else if t.PublicPort != 0 {
		if err := s.Store.SetTunnelPort(r.Context(), tenantID, t.ID, 0); err == nil {
			t.PublicPort = 0
		}
	}

	s.tunnelsChanged()
	s.pushClientConfig(t.ClientID)
	if oldClientID != "" && oldClientID != t.ClientID {
		s.pushClientConfig(oldClientID) // eski istemciden tüneli düşür
	}
	s.Events.PublishTenant(tenantID, events.TypeTunnelUpdated, t)
	writeJSON(w, http.StatusOK, t)
}

// Rezerve port aralığı (Mod A). VDS'te docker-compose + firewall bu aralığı açar.
// Docker port-başına-proxy açtığından aralık makul tutulur (100 port).
const (
	reservedPortMin = 10000
	reservedPortMax = 10099
)

// allocateTunnelPort, aralikta bos bir portu tunele atar ve doner.
func (s *Server) allocateTunnelPort(ctx context.Context, tenantID, id string) (int, error) {
	used := map[int]bool{}
	if list, err := s.Store.ListReservedPortTunnels(ctx); err == nil {
		for _, tn := range list {
			if tn.PublicPort > 0 {
				used[tn.PublicPort] = true
			}
		}
	}
	for p := reservedPortMin; p <= reservedPortMax; p++ {
		if used[p] {
			continue
		}
		if err := s.Store.SetTunnelPort(ctx, tenantID, id, p); err != nil {
			// Yarış (unique ihlali) olabilir; sonraki portu dene.
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("bos rezerve port kalmadi (%d-%d)", reservedPortMin, reservedPortMax)
}

func (s *Server) deleteTunnel(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
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
	s.Events.PublishTenant(tenantID, events.TypeTunnelDeleted, map[string]string{"id": id})
	s.audit(r, "tunnel.delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// --- tunnel access control (FAZ 1a) ----------------------------------------

// accessConfigOut, politika config'ini panele/istemciye GÜVENLİ döndürür:
// password_hash ASLA dışa verilmez, yerine has_password bilgisi konur.
func accessConfigOut(p store.TunnelAccessPolicy) map[string]any {
	var raw map[string]any
	_ = json.Unmarshal(p.Config, &raw)
	if raw == nil {
		raw = map[string]any{}
	}
	safe := map[string]any{}
	if u, ok := raw["username"]; ok {
		safe["username"] = u
	}
	if _, ok := raw["password_hash"]; ok {
		safe["has_password"] = true
	}
	if v, ok := raw["providers"]; ok {
		safe["providers"] = v
	}
	if v, ok := raw["allowed_emails"]; ok {
		safe["allowed_emails"] = v
	}
	return map[string]any{
		"tunnel_id": p.TunnelID, "mode": p.Mode, "enabled": p.Enabled, "config": safe,
	}
}

func (s *Server) getTunnelAccess(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	p, err := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accessConfigOut(p))
}

func (s *Server) setTunnelAccess(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Mode    string `json:"mode"`
		Enabled bool   `json:"enabled"`
		Config  struct {
			Username      string   `json:"username"`
			Password      string   `json:"password"` // düz metin — burada hash'lenir
			Providers     []string `json:"providers"`
			AllowedEmails []string `json:"allowed_emails"`
		} `json:"config"`
	}
	if !decode(w, r, &body) {
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "none"
	}
	if mode != "none" && mode != "basic" && mode != "oauth" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_mode", "mode none|basic|oauth olmalidir")
		return
	}

	var cfg []byte
	switch mode {
	case "basic":
		if strings.TrimSpace(body.Config.Username) == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_config", "basic mod icin username zorunlu")
			return
		}
		hash := ""
		if body.Config.Password != "" {
			h, err := auth.HashSecret(body.Config.Password)
			if err != nil {
				s.fail(w, err)
				return
			}
			hash = h
		} else {
			// Parola verilmediyse mevcut hash'i koru (username/enabled güncellemesi).
			if cur, err := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, id); err == nil {
				var c struct {
					PasswordHash string `json:"password_hash"`
				}
				_ = json.Unmarshal(cur.Config, &c)
				hash = c.PasswordHash
			}
			if hash == "" {
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid_config", "basic mod icin password zorunlu")
				return
			}
		}
		cfg, _ = json.Marshal(map[string]string{"username": strings.TrimSpace(body.Config.Username), "password_hash": hash})
	case "oauth":
		cfg, _ = json.Marshal(map[string]any{
			"providers": body.Config.Providers, "allowed_emails": body.Config.AllowedEmails,
		})
	default:
		cfg = []byte("{}")
	}

	if err := s.Store.SetTunnelAccessPolicy(r.Context(), tenantID, store.TunnelAccessPolicy{
		TunnelID: id, Mode: mode, Config: cfg, Enabled: body.Enabled,
	}); err != nil {
		s.fail(w, err)
		return
	}
	// Router snapshot'ını hemen tazele ki politika beklemeden etkin olsun.
	s.tunnelsChanged()
	s.audit(r, "tunnel.access", id, "mode="+mode)

	p, err := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accessConfigOut(p))
}

// --- requests / events -----------------------------------------------------

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
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
		// Ingress'in proxy'lemeden reddettigi istekler: ?rejected=true veya ?reason=ip_forbidden.
		f.Reason = q.Get("reason")
		if v := q.Get("rejected"); v == "true" || v == "1" {
			f.Rejected = true
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
		// Kiraci kapsami: Genel Bakis HER ZAMAN ETKIN kiracinin loglarini gosterir
		// (platform admin baska org'a "Yonetime Gec" yaptiginda o org'un loglarini
		// gorur; aksi halde kendi org'unun trafigini gorurdu — capraz-kiraci
		// sizinti). Platform geneli izleme /platform sayfasindadir.
		if tenantID, ok := s.tenantFor(r); ok {
			f.TenantID = tenantID
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

	// Bellek ring fallback (Postgres sorgusu basarisizsa): tenant'a gore suz.
	entries := s.Log.List(limit, q.Get("tunnel_id"))
	if tenantID, ok := s.tenantFor(r); ok && tenantID != "" {
		scoped := make([]reqlog.Entry, 0, len(entries))
		for _, e := range entries {
			if e.TenantID == tenantID {
				scoped = append(scoped, e)
			}
		}
		entries = scoped
	}
	if reason, rej := q.Get("reason"), q.Get("rejected"); reason != "" || rej == "true" || rej == "1" {
		kept := make([]reqlog.Entry, 0, len(entries))
		for _, e := range entries {
			if (reason != "" && e.RejectReason == reason) || (reason == "" && e.RejectReason != "") {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	writeJSON(w, http.StatusOK, entries)
}

// stream, GET /api/v1/events — SSE canli olay akisi.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
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

	// Kiraci kapsami: bu akis yalnizca ETKIN kiracinin olaylarini gonderir
	// (request.completed, tunnel.*, client.*). Aksi halde baska kiracinin tunel
	// hedefleri/istemci kimlikleri ve canli trafigi sizardi.
	viewerTenant, _ := s.tenantFor(r)

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
			if !streamEventVisible(e, viewerTenant) {
				continue
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

// streamEventVisible, SSE olayinin izleyen kiraciya gonderilip
// gonderilemeyecegini soyler. Olay etiketi yoksa istek kaydinin kiracisina
// bakilir; kiraci hic belirlenemiyorsa olay gizlenir (fail-closed).
func streamEventVisible(e events.Event, viewerTenant string) bool {
	if e.Tenant == "" {
		if entry, ok := e.Data.(reqlog.Entry); ok {
			e.Tenant = entry.TenantID
		}
	}
	if !e.VisibleTo(viewerTenant) {
		return false
	}
	// Istek kaydi etiketle celisirse (programlama hatasi) gonderme.
	if entry, ok := e.Data.(reqlog.Entry); ok && entry.TenantID != "" && entry.TenantID != viewerTenant {
		return false
	}
	return true
}

// --- yardimcilar -----------------------------------------------------------

func validTarget(t string) bool {
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")
}

// validRawTarget, ham TCP/UDP tünel hedefini doğrular: host:port veya
// tcp://host:port / udp://host:port. Port 1-65535 aralığında olmalı.
func validRawTarget(t string) bool {
	t = strings.TrimSpace(t)
	for _, p := range []string{"tcp://", "udp://"} {
		t = strings.TrimPrefix(t, p)
	}
	t = strings.TrimSuffix(t, "/")
	host, port, err := net.SplitHostPort(t)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// normalizeProtoExposure, proto/exposure ikilisini doğrular ve normalize eder.
// http her zaman auto'ya düşer; tcp/udp için varsayılan maruziyet 'port'tur.
// Not: panel her zaman ikisini birlikte gönderir; proto nil ise http varsayılır.
func normalizeProtoExposure(protoP, exposureP *string) (proto, exposure string, err error) {
	proto = store.ProtoHTTP
	if protoP != nil {
		proto = strings.ToLower(strings.TrimSpace(*protoP))
	}
	switch proto {
	case store.ProtoHTTP:
		return store.ProtoHTTP, store.ExposureAuto, nil
	case store.ProtoTCP, store.ProtoUDP:
		// devam
	default:
		return "", "", fmt.Errorf("proto http|tcp|udp olmalidir")
	}
	if exposureP != nil {
		exposure = strings.ToLower(strings.TrimSpace(*exposureP))
	}
	switch exposure {
	case "", store.ExposureAuto:
		exposure = store.ExposurePort // tcp/udp varsayılanı: rezerve-port
	case store.ExposurePort, store.ExposureSNI:
		// geçerli
	default:
		return "", "", fmt.Errorf("exposure port|sni olmalidir")
	}
	return proto, exposure, nil
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
		// Uzak ayarlar da ayni mesajla gider (FAZ 3 / F15). Okunamazsa
		// tunel listesi yine de itilir: ayar hatasi tunel guncellemesini
		// bloklamamali.
		settings, err := s.Store.GetDeviceConfigByClient(ctx, clientID)
		if err != nil {
			s.logger().Warn("config_update: cihaz ayarlari alinamadi", "client", clientID, "hata", err)
			settings = nil
		}
		if err := sess.Send(ctx, protocol.ConfigUpdate{
			Type:     protocol.TypeConfigUpdate,
			Tunnels:  specs,
			Settings: settings,
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
