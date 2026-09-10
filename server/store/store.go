// Package store, sunucunun kalici verisine erisimi soyutlar.
//
// Tum DB erisimi bu arayuzun arkasindadir. MVP'de tek implementasyon SQLite;
// R4'te (multi-tenant) Postgres eklenecek ve degisiklik tek dosyayla sinirli kalacak.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var (
	ErrNotFound       = errors.New("kayit bulunamadi")
	ErrHostnameTaken  = errors.New("hostname zaten bir tunele bagli")
	ErrTenantNotFound = errors.New("kiraci bulunamadi")
	ErrUserNotFound   = errors.New("kullanici bulunamadi")
)

// Tenant, sistemi kullanan bagimsiz bir musteri/hesap.
type Tenant struct {
	ID        string    `json:"id"`   // ten_xxxxxxxx
	Slug      string    `json:"slug"` // URL/subdomain'de kullanilacak kisa ad
	CreatedAt time.Time `json:"created_at"`
}

// Varsayilan kiraci: tek-sahipli kullanimda ve SQLite gocunde kullanilir.
//
// Kimlik dogrulama henuz kiraci SECMIYOR; o gelene kadar tum yonetim islemleri
// bu kiraci uzerinden yapilir. Sabit bir id kullaniyoruz ki goc ve testler ayni
// satiri beklesin.
const (
	DefaultTenantID   = "ten_default"
	DefaultTenantSlug = "default"
)

// User, GitHub ile giren bir kisi.
type User struct {
	ID string `json:"id"` // usr_xxxxxxxx
	// GitHubID, GitHub'in sayisal id'si. SABITTIR: kisi kullanici adini
	// degistirse bile hesap ayni kalir. Kimligi login ile takip etseydik ad
	// degisiminde YENI bir kiraci acilir ve kisi verisini kaybederdi.
	GitHubID    int64     `json:"-"`
	GitHubLogin string    `json:"github_login"`
	CreatedAt   time.Time `json:"created_at"`
}

// Kiraci uyelik rolleri.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Plan adlari.
const (
	PlanFree       = "free"
	PlanHobby      = "hobby"
	PlanPro        = "pro"
	PlanTeam       = "team"
	PlanEnterprise = "enterprise"
)

// Abonelik durumlari.
const (
	SubStatusActive   = "active"
	SubStatusPastDue  = "past_due"
	SubStatusCanceled = "canceled"
	SubStatusTrialing = "trialing"
)

// Plan, veritabaninda tanimli dinamik plan yapisi.
type Plan struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	PriceMonthly           int    `json:"price_monthly"` // USD cent (0, 500, 1200, 2900)
	MaxClients             *int   `json:"max_clients"`   // nil = sinirsiz
	MaxTunnels             *int   `json:"max_tunnels"`   // nil = sinirsiz
	MaxCustomDomains       *int   `json:"max_custom_domains"`
	BandwidthLimitBytes    *int64 `json:"bandwidth_limit_bytes"`
	BandwidthNormalMbps    int    `json:"bandwidth_normal_mbps"`
	BandwidthThrottledMbps int    `json:"bandwidth_throttled_mbps"`
	MaxScreenStreams       *int   `json:"max_screen_streams"`
	ScreenMaxFPS           int    `json:"screen_max_fps"`
	LogRetentionDays       int    `json:"log_retention_days"`
	MaxMembers             *int   `json:"max_members"`
	HasAPIAccess           bool   `json:"has_api_access"`
	HasIPAllowlist         bool   `json:"has_ip_allowlist"`
	ExtraDevicePrice       int    `json:"extra_device_price"`
	ExtraGBPrice           int    `json:"extra_gb_price"`
}

// Subscription, bir kiracinin abonelik ve kaynak limiti detaylari.
type Subscription struct {
	TenantID             string     `json:"tenant_id"`
	Plan                 string     `json:"plan"`
	Status               string     `json:"status"`
	StripeCustomerID     string     `json:"stripe_customer_id,omitempty"`
	StripeSubscriptionID string     `json:"stripe_subscription_id,omitempty"`
	CurrentPeriodEnd     *time.Time `json:"current_period_end,omitempty"`
	MaxClients           int        `json:"max_clients"`
	MaxCustomDomains     int        `json:"max_custom_domains"`
	MaxTunnels           int        `json:"max_tunnels"`
	BandwidthLimitBytes  int64      `json:"bandwidth_limit_bytes"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`

	// Dinamik plan detaylari (plans tablosundan okunur)
	PlanDetails *Plan `json:"plan_details,omitempty"`
}

// TenantUsage, kiracinin anlik tukettigi kaynak sayilari.
type TenantUsage struct {
	ClientsCount        int   `json:"clients_count"`
	CustomDomainsCount  int   `json:"custom_domains_count"`
	TunnelsCount        int   `json:"tunnels_count"`
	BandwidthUsedBytes  int64 `json:"bandwidth_used_bytes"`
	ActiveScreenStreams int   `json:"active_screen_streams"`
	IsThrottled         bool  `json:"is_throttled"`
}

// Client, tunel acan ajan makine. api_contract.md §1 ile hizali.
type Client struct {
	ID string `json:"id"`
	// TenantID, kaydin sahibi kiraci. Disari VERILMEZ: kiraci zaten istegin
	// baglamindan bellidir, ayrica sizdirmaya gerek yok.
	TenantID  string    `json:"-"`
	UserID    string    `json:"user_id,omitempty"`
	Name      string    `json:"name"`
	TokenID   string    `json:"-"` // public arama anahtari, asla disari verilmez
	TokenHash string    `json:"-"` // argon2id(secret)
	CreatedAt time.Time `json:"created_at"`

	// Asagidakiler DB'de tutulmaz; canli baglanti durumundan doldurulur.
	Status     string            `json:"status"` // online | offline
	Version    string            `json:"version,omitempty"`
	RemoteAddr string            `json:"remote_addr,omitempty"`
	LastSeenAt *time.Time        `json:"last_seen_at,omitempty"`
	IsService  bool              `json:"is_service,omitempty"`
	Metrics    *protocol.Metrics `json:"metrics,omitempty"`
}

// TeamMember, organizasyona/kiraciya ait ekip uyesi.
type TeamMember struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	TokensCount int       `json:"tokens_count"`
	// Status: "active" (kabul edilmis uyelik) veya "pending" (bekleyen davet).
	// Bekleyen davetlerde UserID bostur; kullanici kayit olunca uyelige donusur.
	Status string `json:"status"`
}

// MailMessage, webmail icin bir gelen/giden e-posta kaydi.
type MailMessage struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Direction  string    `json:"direction"` // "inbound" | "outbound"
	From       string    `json:"from"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	TextBody   string    `json:"text_body"`
	HTMLBody   string    `json:"html_body"`
	MessageID  string    `json:"message_id"`
	InReplyTo  string    `json:"in_reply_to"`
	Seen       bool      `json:"seen"`
	ReceivedAt time.Time `json:"received_at"`
	Raw        string    `json:"-"`
}

// MailAttachment, bir webmail mesajina bagli ek dosya. Content yalnizca tekil
// getirmede (indirme) doldurulur; listeleme metadata doner.
type MailAttachment struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	TenantID    string    `json:"-"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Content     []byte    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIToken, programatik REST API erisimi icin uretilmis token kaydi.
type APIToken struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	UserID      *string    `json:"user_id,omitempty"`
	Name        string     `json:"name"`
	TokenID     string     `json:"token_id"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// IPAllowlistRule, ingress trafigi icin tanimlanmis IP / CIDR izin kurali.
type IPAllowlistRule struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	TunnelID    *string   `json:"tunnel_id,omitempty"`
	CIDR        string    `json:"cidr"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Tunnel, client -> yerel hedef eslestirmesi.
//
// Hostname burada TUTULMAZ: bir tunel birden cok adla yayinlanabilir, o yuzden
// adlar ayri bir tabloda (bkz. Hostname). Iki yerde tutmak, birinin sessizce
// eskimesi demek olurdu.
type Tunnel struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"-"` // kaydin sahibi kiraci; disari verilmez
	ClientID  string    `json:"client_id"`
	Target    string    `json:"target"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// Hostnames, tunelin yayinlandigi adlar. DB'de tunel satirinda TUTULMAZ;
	// yonetim uclarinda cevabi zenginlestirmek icin doldurulur.
	Hostnames []Hostname `json:"hostnames,omitempty"`
}

// Hostname, bir tunelin yayinlandigi ad.
//
// Isim uzayi GLOBALDIR: ayni fqdn iki kiraciya verilemez, cunku DNS global bir
// isim uzayidir.
type Hostname struct {
	ID string `json:"id"`
	// TenantID, kaydin sahibi kiraci; disari verilmez.
	TenantID    string    `json:"-"`
	TunnelID    string    `json:"tunnel_id"`
	FQDN        string    `json:"fqdn"`
	Type        string    `json:"type"`
	Verified    bool      `json:"verified"`
	VerifyToken string    `json:"verify_token,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Hostname tipleri.
//
// global ve scoped, platform domaini altinda TEK ETIKETTIR. Noktali bir bicim
// (<tunel>.<kiraci>.<platform>) kiraci basina ayri wildcard sertifika
// gerektirir ve Let's Encrypt'in kayitli-domain basina haftalik sinirina
// takilir; "--" ayraci bunu tek bir *.<platform> sertifikasiyla cozer.
const (
	HostTypeGlobal = "global" // <ad>.<platform>              — kullanicinin sectigi kisa ad
	HostTypeScoped = "scoped" // <tunel>--<kiraci>.<platform> — her zaman musait
	HostTypeLegacy = "legacy" // goc oncesi serbest ad (or. api.localhost)
	HostTypeCustom = "custom" // musterinin kendi domaini (or. api.musteri.com)
)

// HostRoute, ingress'in bir istegi yonlendirmek icin ihtiyac duydugu her sey.
//
// hostnames + tunnels birlesimidir ve TEK sorguda gelir; sicak yol ikinci bir
// gidis-donus yapmasin diye.
type HostRoute struct {
	FQDN     string
	TenantID string
	TunnelID string
	ClientID string
	Target   string
	Enabled  bool
}

// TunnelPatch, PATCH /tunnels/{id} icin kismi guncelleme.
// nil alan "degistirme" anlamina gelir.
type TunnelPatch struct {
	Target  *string
	Enabled *bool
}

// TenantWithCounts, kiraci ve ona ait kaynak sayilarini tutar (admin paneli icin).
type TenantWithCounts struct {
	Tenant
	ClientsCount   int    `json:"clients_count"`
	TunnelsCount   int    `json:"tunnels_count"`
	HostnamesCount int    `json:"hostnames_count"`
	Plan           string `json:"plan"`
}

// ClientWithTenant, istemci bilgisi ile birlikte kiraci slug'ini tutar.
type ClientWithTenant struct {
	Client
	TenantSlug string `json:"tenant_slug"`
}

// HostnameWithTenant, domain bilgisi ile birlikte kiraci ve hedef tunel bilgisini tutar.
type HostnameWithTenant struct {
	Hostname
	TenantSlug string `json:"tenant_slug"`
	Target     string `json:"target,omitempty"`
}

// AdminGlobalStats, platform genelindeki kaynak ve saglik istatistikleri.
type AdminGlobalStats struct {
	TenantsCount       int `json:"tenants_count"`
	ClientsCount       int `json:"clients_count"`
	OnlineClientsCount int `json:"online_clients_count"`
	TunnelsCount       int `json:"tunnels_count"`
	ActiveTunnelsCount int `json:"active_tunnels_count"`
	HostnamesCount     int `json:"hostnames_count"`
	CustomDomainsCount int `json:"custom_domains_count"`
}

type Store interface {
	// --- Tenants ---
	CreateTenant(ctx context.Context, slug string) (Tenant, error)
	GetTenant(ctx context.Context, id string) (Tenant, error)
	ListTenants(ctx context.Context) ([]Tenant, error)

	// --- Clients (KIRACI KAPSAMLI) ---
	//
	// Baska kiracinin kaydina erisim ErrNotFound doner — "yetkisiz" degil:
	// kaydin VARLIGINI bile sizdirmamak icin.
	CreateClient(ctx context.Context, tenantID, name, tokenID, tokenHash string) (Client, error)
	GetClient(ctx context.Context, tenantID, id string) (Client, error)
	ListClients(ctx context.Context, tenantID string) ([]Client, error)
	DeleteClient(ctx context.Context, tenantID, id string) error
	RotateClientToken(ctx context.Context, tenantID, id, tokenID, tokenHash string) error

	// GetClientByTokenID, KIRACIDAN BAGIMSIZDIR ve oyle KALMALIDIR: istemci
	// kimligi token'in kendisidir, baglanti kuran taraf hangi kiraciya ait
	// oldugunu bilmez. Donen Client.TenantID kiraciyi belirler.
	//
	// tokenID indeksli oldugu icin O(1). Cagiran gizli kismi TokenHash ile
	// dogrular — boylece argon2 baglanti basina bir kez calisir.
	GetClientByTokenID(ctx context.Context, tokenID string) (Client, error)

	// --- Tunnels (KIRACI KAPSAMLI) ---
	//
	// hostname ALMAZ: adlar ayri tabloda tutulur, cagiran tunel olustuktan
	// sonra AddHostname ile bir veya daha fazla ad baglar.
	CreateTunnel(ctx context.Context, tenantID, clientID, target string) (Tunnel, error)
	GetTunnel(ctx context.Context, tenantID, id string) (Tunnel, error)
	ListTunnels(ctx context.Context, tenantID string) ([]Tunnel, error)
	UpdateTunnel(ctx context.Context, tenantID, id string, patch TunnelPatch) (Tunnel, error)
	DeleteTunnel(ctx context.Context, tenantID, id string) error

	// ListTunnelsByClient, bir istemcinin tunelleri. Istemci zaten dogrulanmis
	// oldugu icin (token -> client -> tenant) ayrica tenantID istemez.
	ListTunnelsByClient(ctx context.Context, clientID string) ([]Tunnel, error)

	// --- Hostnames (KIRACI KAPSAMLI) ---
	//
	// typ, HostType* sabitlerinden biri. Ayni fqdn ikinci kez eklenirse
	// ErrHostnameTaken doner (GLOBAL benzersizlik).
	AddHostname(ctx context.Context, tenantID, tunnelID, fqdn, typ string) (Hostname, error)
	AddCustomHostname(ctx context.Context, tenantID, tunnelID, fqdn, verifyToken string) (Hostname, error)
	AttachHostname(ctx context.Context, tenantID, hostnameID, tunnelID string) error
	DetachHostname(ctx context.Context, tenantID, hostnameID string) error
	VerifyHostname(ctx context.Context, tenantID, id string) (Hostname, error)
	GetHostnameByID(ctx context.Context, tenantID, id string) (Hostname, error)
	GetHostnameByFQDN(ctx context.Context, fqdn string) (Hostname, error)
	ListHostnames(ctx context.Context, tenantID string) ([]Hostname, error)
	ListHostnamesByTunnel(ctx context.Context, tenantID, tunnelID string) ([]Hostname, error)
	DeleteHostname(ctx context.Context, tenantID, id string) error

	// --- Platform Admin (Organizasyondan Bagimsiz) ---
	AdminGetGlobalStats(ctx context.Context) (AdminGlobalStats, error)
	AdminListTenantsWithCounts(ctx context.Context) ([]TenantWithCounts, error)
	AdminListAllClients(ctx context.Context) ([]ClientWithTenant, error)
	AdminListAllHostnames(ctx context.Context) ([]HostnameWithTenant, error)

	// --- Plans, Subscriptions & Usage ---
	GetPlan(ctx context.Context, id string) (Plan, error)
	ListPlans(ctx context.Context) ([]Plan, error)
	GetSubscription(ctx context.Context, tenantID string) (Subscription, error)
	UpsertSubscription(ctx context.Context, sub Subscription) error
	UpdateTenantPlan(ctx context.Context, tenantID, plan, status string, periodEnd *time.Time) error
	GetTenantUsage(ctx context.Context, tenantID string) (TenantUsage, error)
	CountClients(ctx context.Context, tenantID string) (int, error)
	CountCustomDomains(ctx context.Context, tenantID string) (int, error)
	CountTunnels(ctx context.Context, tenantID string) (int, error)
	RecordBandwidth(ctx context.Context, tenantID, period string, bytesIn, bytesOut int64) error
	FlushBandwidthDeltas(ctx context.Context, period string, deltas map[string][2]int64) error
	GetBandwidthUsage(ctx context.Context, tenantID, period string) (bytesIn, bytesOut int64, err error)

	// Kalici istek loglari (Loglar ekrani). InsertRequestLogs toplu yazar;
	// QueryRequestLogs gelismis filtrelerle en yeniden eskiye dogru doner.
	InsertRequestLogs(ctx context.Context, entries []reqlog.Entry) error
	QueryRequestLogs(ctx context.Context, f reqlog.Filter) ([]reqlog.Entry, error)

	// ListHostnamesByClient, bir istemcinin tum tunellerinin adlari. Istemci
	// zaten token'iyla dogrulanmis oldugu icin (ListTunnelsByClient ile ayni
	// gerekce) ayrica tenantID istemez. El sikismada tek sorguda tum adlari
	// vermek icin var; tunel basina sorgu (N+1) yapmamak icin.
	ListHostnamesByClient(ctx context.Context, clientID string) ([]Hostname, error)

	// IsReservedName, adin platform icin ayrilip ayrilmadigini soyler
	// (www, api, admin, ...). Buyuk/kucuk harf duyarsizdir.
	IsReservedName(ctx context.Context, name string) (bool, error)

	// ListHostRoutes, TUM kiracilarin yonlendirmeleri — YALNIZCA ingress
	// yonlendirme tablosu icindir: gelen istek yalnizca hostname tasir,
	// kiraci bilinmez. Yonetim uclarinda ASLA kullanilmaz.
	ListHostRoutes(ctx context.Context) ([]HostRoute, error)

	// --- On-demand ACME Sertifika Onbellegi (autocert.Cache) ---
	GetACMEData(ctx context.Context, key string) ([]byte, error)
	PutACMEData(ctx context.Context, key string, data []byte) error
	DeleteACMEData(ctx context.Context, key string) error

	// --- Ice aktarma (tek seferlik SQLite gocu) ---
	//
	// Satiri OLDUGU GIBI yazar: id, token hash ve created_at KORUNUR, yeni id
	// URETILMEZ. Kurulu istemcilerin yeniden yapilandirma gerektirmemesi buna
	// baglidir. Ayni id yeniden aktarilirsa sessizce atlanir (idempotent).
	ImportClient(ctx context.Context, c Client) error
	ImportTunnel(ctx context.Context, t Tunnel) error

	// --- Users / uyelik ---

	// UpsertUserByGitHubID, github_id'ye gore kullaniciyi olusturur veya
	// login'ini gunceller. github_id SABITTIR (bkz. User.GitHubID).
	UpsertUserByGitHubID(ctx context.Context, githubID int64, login string) (User, error)

	// GetTenantForUser, kullanicinin uye oldugu kiraciyi doner; uyelik yoksa
	// ErrTenantNotFound.
	GetTenantForUser(ctx context.Context, userID string) (Tenant, error)

	AddTenantMember(ctx context.Context, tenantID, userID, role string) error
	GetTenantBySlug(ctx context.Context, slug string) (Tenant, error)

	// --- Ekip ve Uye Yonetimi (Team & Member Tokens) ---
	ListTeamMembers(ctx context.Context, tenantID string) ([]TeamMember, error)
	AddTeamMember(ctx context.Context, tenantID, email, name, role, inviterID string) (TeamMember, error)
	UpdateTeamMemberRole(ctx context.Context, tenantID, memberID, role string) error
	RemoveTeamMember(ctx context.Context, tenantID, memberID string) error
	CountTeamMembers(ctx context.Context, tenantID string) (int, error)
	CreateMemberClient(ctx context.Context, tenantID, userID, name, tokenID, tokenHash string) (Client, error)
	ListMemberClients(ctx context.Context, tenantID, userID string) ([]Client, error)

	// --- Programatik REST API Tokenlari ---
	CreateAPIToken(ctx context.Context, tenantID string, userID *string, name, tokenID, tokenHash, prefix string, scopes []string, expiresAt *time.Time) (APIToken, error)
	ListAPITokens(ctx context.Context, tenantID string) ([]APIToken, error)
	GetAPITokenByTokenID(ctx context.Context, tokenID string) (APIToken, error)
	RevokeAPIToken(ctx context.Context, tenantID, id string) error
	TouchAPITokenLastUsed(ctx context.Context, id string) error

	// --- Ingress IP Izin Listesi (IP Allowlist) ---
	CreateIPRule(ctx context.Context, tenantID string, tunnelID *string, cidr, description string) (IPAllowlistRule, error)
	ListIPRules(ctx context.Context, tenantID string, tunnelID *string) ([]IPAllowlistRule, error)
	ListAllActiveIPRules(ctx context.Context) ([]IPAllowlistRule, error)
	UpdateIPRule(ctx context.Context, tenantID, id string, enabled *bool, description *string) (IPAllowlistRule, error)
	DeleteIPRule(ctx context.Context, tenantID, id string) error

	// --- Settings ---
	// Sunucu omru boyunca kalici olmasi gereken kucuk yapilandirma degerleri
	// (or. admin anahtarinin token_id + hash'i).
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error

	// --- Webmail ---
	InsertMailMessage(ctx context.Context, m MailMessage) (MailMessage, error)
	ListMailMessages(ctx context.Context, tenantID, direction string, limit int) ([]MailMessage, error)
	GetMailMessage(ctx context.Context, tenantID, id string) (MailMessage, error)
	MarkMailSeen(ctx context.Context, tenantID, id string) error
	DeleteMailMessage(ctx context.Context, tenantID, id string) error
	CountUnseenMail(ctx context.Context, tenantID string) (int, error)
	// Ekler (attachments)
	InsertMailAttachment(ctx context.Context, a MailAttachment) error
	ListMailAttachments(ctx context.Context, tenantID, messageID string) ([]MailAttachment, error)
	GetMailAttachment(ctx context.Context, tenantID, id string) (MailAttachment, error)

	Close() error
}

// Settings anahtarlari.
const (
	SettingAdminTokenID   = "admin_token_id"
	SettingAdminTokenHash = "admin_token_hash"
)
