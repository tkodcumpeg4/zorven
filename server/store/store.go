// Package store, sunucunun kalici verisine erisimi soyutlar.
//
// Tum DB erisimi bu arayuzun arkasindadir. MVP'de tek implementasyon SQLite;
// R4'te (multi-tenant) Postgres eklenecek ve degisiklik tek dosyayla sinirli kalacak.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var (
	ErrNotFound             = errors.New("kayit bulunamadi")
	ErrHostnameTaken        = errors.New("hostname zaten bir tunele bagli")
	ErrTenantNotFound       = errors.New("kiraci bulunamadi")
	ErrUserNotFound         = errors.New("kullanici bulunamadi")
	ErrProjectSlugTaken     = errors.New("proje slug zaten kullanımda")
	ErrProjectDefaultDelete = errors.New("varsayılan proje silinemez")
	// ErrProjectNotEmpty: projede hala secret/policy var; silinirse
	// bu kayitlar hicbir projede listelenmez ve gorunmez olurdu.
	ErrProjectNotEmpty   = errors.New("proje bos degil")
	ErrSecretNameTaken   = errors.New("secret adı zaten kullanımda")
	ErrSecretKeyMissing  = errors.New("secret şifreleme anahtarı yapılandırılmamış")
	ErrUnknownKeyVersion = errors.New("bilinmeyen secret anahtar sürümü")
	ErrPolicyNameTaken   = errors.New("policy adı zaten kullanımda")
	ErrInvitationExpired = errors.New("davetin süresi dolmuş")
	ErrInvitationEmail   = errors.New("bu davet başka bir e-posta adresine gönderilmiş")
	ErrInvitationClosed  = errors.New("bu davet artık geçerli değil")
)

// Project, bir kiraciya ait alt proje. FAZ 0 (F00).
type Project struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// Secret, projeye ait sifreli bir sir. FAZ 1 (F06). Value ASLA listede/GET'te
// donmez; yalnizca CreateSecret sonucu (bir kez) tasir.
type Secret struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	ProjectID  string    `json:"project_id"`
	Name       string    `json:"name"`
	KeyVersion int       `json:"key_version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Value      string    `json:"-"`
}

// Policy, birlesik edge gateway policy'si (match -> action). FAZ 1 (F04).
type Policy struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	Enabled   bool            `json:"enabled"`
	Priority  int             `json:"priority"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Bindings  []PolicyBinding `json:"bindings,omitempty"`
}

// PolicyBinding, bir policy'nin bir tunele veya hostname'e baglanmasi. FAZ 1 (F04).
type PolicyBinding struct {
	PolicyID string `json:"policy_id"`
	TunnelID string `json:"tunnel_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// PolicyRoute, ingress router snapshot'i icin: bir host'a bagli etkin policy.
type PolicyRoute struct {
	Host string
	// TunnelID, bag bir tunele yapildiysa dolu (hostname bagi icin bos).
	TunnelID string
	Policy   Policy
}

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
	ProjectID string    `json:"project_id,omitempty"`
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

	// Cihaz alanlari (FAZ 3 / F14). Bunlar DB'de TUTULUR: baglanti kopunca
	// kaybolmasinlar ki cihaz offline iken de tanimlanabilsin.
	Hostname     string            `json:"hostname,omitempty"`
	OS           string            `json:"os,omitempty"`   // "windows"
	Arch         string            `json:"arch,omitempty"` // "amd64"
	IPs          []string          `json:"ips,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	LastMetrics  *protocol.Metrics `json:"last_metrics,omitempty"` // en son BILINEN metrik
}

// Yuk dengeleme stratejileri (FAZ 4 / F21).
const (
	LBRoundRobin       = "round_robin"
	LBWeighted         = "weighted"
	LBLeastConnections = "least_connections"
	LBLatency          = "latency"
)

// TunnelLB, tunel basina yuk dengeleme + saglik kontrolu yapilandirmasi.
// TunnelUDP, UDP tunelinin sinirlari (FAZ 4 / F24). Kayit yoksa
// DefaultTunnelUDP uygulanir: herkeste flow ust siniri ve paket boyu siniri var.
type TunnelUDP struct {
	TunnelID       string `json:"tunnel_id"`
	TenantID       string `json:"-"`
	IdleTimeoutSec int    `json:"idle_timeout_sec"`
	MaxPacketBytes int    `json:"max_packet_bytes"`
	MaxPPS         int    `json:"max_pps"`      // 0 = sinirsiz
	MaxFlowPPS     int    `json:"max_flow_pps"` // 0 = sinirsiz
	MaxFlows       int    `json:"max_flows"`
}

// DefaultTunnelUDP, kayitsiz tunelin UDP sinirlari.
func DefaultTunnelUDP(tunnelID string) TunnelUDP {
	return TunnelUDP{TunnelID: tunnelID, IdleTimeoutSec: 90, MaxPacketBytes: 65507, MaxFlows: 1024}
}

// UDPStatMinute, bir UDP tunelinin bir dakikalik istatistik ozeti.
type UDPStatMinute struct {
	TunnelID     string    `json:"-"`
	TenantID     string    `json:"-"`
	Minute       time.Time `json:"minute"`
	PacketsIn    int64     `json:"packets_in"`
	PacketsOut   int64     `json:"packets_out"`
	BytesIn      int64     `json:"bytes_in"`
	BytesOut     int64     `json:"bytes_out"`
	FlowsNew     int64     `json:"flows_new"`
	FlowsPeak    int       `json:"flows_peak"`
	DroppedRate  int64     `json:"dropped_rate"`
	DroppedSize  int64     `json:"dropped_size"`
	DroppedFlows int64     `json:"dropped_flows"`
}

type TunnelLB struct {
	TunnelID           string         `json:"tunnel_id"`
	TenantID           string         `json:"-"`
	Strategy           string         `json:"strategy"`
	Weights            map[string]int `json:"weights"` // client_id -> agirlik
	HealthEnabled      bool           `json:"health_enabled"`
	HealthPath         string         `json:"health_path"`
	IntervalSec        int            `json:"interval_sec"`
	TimeoutSec         int            `json:"timeout_sec"`
	UnhealthyThreshold int            `json:"unhealthy_threshold"`
	HealthyThreshold   int            `json:"healthy_threshold"`
}

// DefaultTunnelLB, kaydi olmayan tunelin (eski) davranisi.
func DefaultTunnelLB(tunnelID string) TunnelLB {
	return TunnelLB{
		TunnelID: tunnelID, Strategy: LBRoundRobin, Weights: map[string]int{},
		HealthPath: "/", IntervalSec: 10, TimeoutSec: 3, UnhealthyThreshold: 3, HealthyThreshold: 2,
	}
}

// DeviceInfo, el sikismasinda gelen cihaz kimligi (FAZ 3 / F14).
// Bos alanlar mevcut kaydi EZMEZ.
type DeviceInfo struct {
	Hostname     string
	OS           string
	Arch         string
	IPs          []string
	AgentVersion string
	Metrics      *protocol.Metrics
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
	// Bekleyen davetlerde UserID bostur; davetli e-postadaki linkten kabul
	// edince uyelige donusur.
	Status string `json:"status"`
	// EmailSent, yalnizca davet yanitinda doldurulur: davet e-postasi teslim
	// edildi mi (relay yoksa/hata varsa false; davet yine de olusur).
	EmailSent *bool `json:"email_sent,omitempty"`
}

// TeamInvitation, bir organizasyona bekleyen/islenmis ekip daveti. Davetli,
// e-postadaki linkle (/invite?id=...) kendi hesabiyla girip kabul eder.
type TeamInvitation struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	Email            string    `json:"email"`
	Role             string    `json:"role"`
	Status           string    `json:"status"` // pending | accepted | rejected | canceled
	InviterName      string    `json:"inviter_name,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
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
	ProjectID string    `json:"project_id,omitempty"`
	ClientID  string    `json:"client_id"`
	Target    string    `json:"target"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// Protokol / maruziyet (FAZ 3 / D2). Proto: http|tcp|udp. Exposure: auto|port|sni.
	// PublicPort: Mod A (rezerve-port) icin atanan port; 0 ise atanmamis.
	Proto      string `json:"proto"`
	Exposure   string `json:"exposure"`
	PublicPort int    `json:"public_port,omitempty"`

	// Frozen (FAZ 4): platform admin tarafindan askiya alindiysa true. Dondurulmus
	// tunel ingress'te "askiya alindi" sayfasi doner.
	Frozen bool `json:"frozen,omitempty"`

	// Gecici tunel (FAZ 2 / F07). ExpiresAt dolunca arka plan supurucusu satiri
	// kaldirir. nil = suresiz (kalici tunel).
	Ephemeral bool       `json:"ephemeral,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// PrivateName (FAZ 3 / F17), ozel kaynagin adi (or. "db.internal").
	// Yalnizca Exposure == ExposurePrivate iken dolu.
	PrivateName string `json:"private_name,omitempty"`

	// Hostnames, tunelin yayinlandigi adlar. DB'de tunel satirinda TUTULMAZ;
	// yonetim uclarinda cevabi zenginlestirmek icin doldurulur.
	Hostnames []Hostname `json:"hostnames,omitempty"`
}

// Tunel protokol / maruziyet sabitleri (FAZ 3 / D2).
const (
	ProtoHTTP = "http"
	ProtoTCP  = "tcp"
	ProtoUDP  = "udp"

	ExposureAuto = "auto" // http: yalnizca bu
	ExposurePort = "port" // Mod A: rezerve TCP/UDP portu
	ExposureSNI  = "sni"  // Mod B: 443 SNI + zorven forward
	// ExposurePrivate (FAZ 3 / F17): internete HIC acilmaz. Hostname ve public
	// port atanmaz; yalnizca "zorven connect" ile, yetkili kullaniciya.
	ExposurePrivate = "private"
)

// Hostname, bir tunelin yayinlandigi ad.
//
// Isim uzayi GLOBALDIR: ayni fqdn iki kiraciya verilemez, cunku DNS global bir
// isim uzayidir.
type Hostname struct {
	ID string `json:"id"`
	// TenantID, kaydin sahibi kiraci; disari verilmez.
	TenantID    string    `json:"-"`
	ProjectID   string    `json:"project_id,omitempty"`
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

	// Proto / Exposure (FAZ 3 / D2). SNI-modu ham tunellerini demux ederken
	// ingress bunlara bakar. http tunellerinde Proto=http, Exposure=auto.
	Proto    string
	Exposure string

	// Plan, kiracinin abonelik plani (free|hobby|pro|team|enterprise). FAZ 4:
	// ucretsiz katman platform-domain tunellerinde uyari ara-sayfasi gosterilir.
	// Bos ise 'free' varsayilir.
	Plan string

	// Frozen (FAZ 4): tunel platform admin tarafindan askiya alindi mi.
	Frozen bool

	// PathPrefix (FAZ 6.5): yol-tabanli yonlendirme kurali ise dolu. Bos ise bu
	// hostname'in VARSAYILAN (birincil) route'udur. Router, ayni fqdn icin en uzun
	// eslesen on-eki secer; hicbiri eslesmezse varsayilana duser.
	PathPrefix string

	// ReplicaClientIDs (FAZ 5 / HA): tunelin BIRINCIL ClientID'sine EK olarak
	// ayni yuku paylasan istemciler. Ingress, {ClientID}+ReplicaClientIDs kumesi
	// icinde cevrimici olanlar arasinda round-robin dagitir ve dusen uyeyi atlar.
	// Bos ise tunel yalnizca birincil ClientID ile calisir (geriye uyumlu).
	ReplicaClientIDs []string

	// Erişim denetimi (tunnel_access_policies) — router snapshot'ına gömülür ki
	// ingress hot-path'i per-istek DB sorgusu yapmasin. AccessEnabled=false ise
	// tunel herkese aciktir (varsayilan). Bkz. FAZ 1a.
	AccessMode    string // none | basic | oauth
	AccessConfig  []byte // moda gore JSONB config
	AccessEnabled bool

	// Trafik politikası (tunnel_traffic_policies) — router snapshot'ına gömülür
	// (FAZ 6). TrafficEnabled=false ise hiçbir kural uygulanmaz (varsayılan).
	// Ingress bu ham JSON'ı Reload'da BİR KEZ parse eder (per-istek parse yok).
	TrafficConfig  []byte // JSONB config (request/response headers, redirects)
	TrafficEnabled bool

	// mTLS (tunnel_mtls, FAZ 6.6): MTLSEnabled ise bu hostname'e TLS el sıkışmasında
	// istemci sertifikası zorunlu tutulur; MTLSCAPem imzalayan CA'dır (PEM).
	MTLSEnabled bool
	MTLSCAPem   string
}

// TunnelAccessPolicy, bir tunelin erişim denetimi politikası (FAZ 1a).
type TunnelAccessPolicy struct {
	TunnelID string          `json:"tunnel_id"`
	Mode     string          `json:"mode"` // none | basic | oauth
	Config   json.RawMessage `json:"config"`
	Enabled  bool            `json:"enabled"`
}

// TunnelTrafficPolicy, bir tunelin trafik politikası (FAZ 6).
type TunnelTrafficPolicy struct {
	TunnelID string          `json:"tunnel_id"`
	Config   json.RawMessage `json:"config"`
	Enabled  bool            `json:"enabled"`
}

// TunnelMTLS, bir tunelin mTLS (istemci sertifikasi) yapilandirmasi (FAZ 6.6).
type TunnelMTLS struct {
	TunnelID string `json:"tunnel_id"`
	Enabled  bool   `json:"enabled"`
	CAPem    string `json:"ca_pem"`
}

// PathRoute, bir hostname'in yol-tabanli yonlendirme kurali (FAZ 6.5).
type PathRoute struct {
	ID         string    `json:"id"`
	FQDN       string    `json:"fqdn"`
	PathPrefix string    `json:"path_prefix"`
	TunnelID   string    `json:"tunnel_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// TunnelAlert, bir tunelin metrik uyarı yapılandırması + durumu (FAZ 6.4).
type TunnelAlert struct {
	TunnelID       string     `json:"tunnel_id"`
	TenantID       string     `json:"tenant_id,omitempty"` // yalnizca degerlendirici listesinde dolu
	Enabled        bool       `json:"enabled"`
	ErrorRatePct   int        `json:"error_rate_pct"`
	WindowMin      int        `json:"window_min"`
	MinRequests    int        `json:"min_requests"`
	NotifyEmail    string     `json:"notify_email"`
	State          string     `json:"state"` // ok | firing
	LastChangedAt  *time.Time `json:"last_changed_at,omitempty"`
	LastNotifiedAt *time.Time `json:"last_notified_at,omitempty"`
}

// TunnelPatch, PATCH /tunnels/{id} icin kismi guncelleme.
// nil alan "degistirme" anlamina gelir.
type TunnelPatch struct {
	Target   *string
	Enabled  *bool
	ClientID *string // tuneli baska bir istemciye tasi
	Proto    *string
	Exposure *string
	// PublicPort: nil = degistirme; *0 = portu birak (NULL yap); *N = ata.
	PublicPort *int
}

// AbuseReport, bir hostname icin ziyaretci kotuye-kullanim bildirimi (FAZ 4).
type AbuseReport struct {
	ID         string    `json:"id"`
	FQDN       string    `json:"fqdn"`
	Reason     string    `json:"reason"`
	ReporterIP string    `json:"reporter_ip,omitempty"`
	Handled    bool      `json:"handled"`
	CreatedAt  time.Time `json:"created_at"`
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

	// --- Projects (FAZ 0 / F00) ---
	ListProjects(ctx context.Context, tenantID string) ([]Project, error)
	CreateProject(ctx context.Context, tenantID, name, slug string) (Project, error)
	GetProjectBySlug(ctx context.Context, tenantID, slug string) (Project, error)
	GetProjectByID(ctx context.Context, tenantID, id string) (Project, error)
	GetDefaultProject(ctx context.Context, tenantID string) (Project, error)
	DeleteProject(ctx context.Context, tenantID, id string) error
	CountProjects(ctx context.Context, tenantID string) (int, error)
	RenameProject(ctx context.Context, tenantID, id, name string) (Project, error)

	// Organizasyon silme. DeleteOrganization kiraciyi ve TUM verisini tek
	// islemde siler; baglantisi kesilecek istemci kimliklerini doner.
	CountOtherOrganizations(ctx context.Context, userID, exceptOrgID string) (int, error)
	DeleteOrganization(ctx context.Context, tenantID string) ([]string, error)

	// --- Secrets (FAZ 1 / F06) ---
	CreateSecret(ctx context.Context, tenantID, projectID, name, plaintext string) (Secret, error)
	ListSecrets(ctx context.Context, tenantID, projectID string) ([]Secret, error)
	DeleteSecret(ctx context.Context, tenantID, id string) error
	ResolveSecret(ctx context.Context, tenantID, projectID, name string) (string, error)

	// --- Policies (FAZ 1 / F04) ---
	ListPolicies(ctx context.Context, tenantID, projectID string) ([]Policy, error)
	GetPolicy(ctx context.Context, tenantID, id string) (Policy, error)
	CreatePolicy(ctx context.Context, tenantID, projectID, name string, config json.RawMessage, priority int) (Policy, error)
	UpdatePolicy(ctx context.Context, tenantID, id, name string, config json.RawMessage, enabled bool, priority int) (Policy, error)
	DeletePolicy(ctx context.Context, tenantID, id string) error
	BindPolicy(ctx context.Context, tenantID, policyID string, b PolicyBinding) error
	UnbindPolicy(ctx context.Context, tenantID, policyID string, b PolicyBinding) error
	// ListPolicyRoutes, TUM kiracilarin etkin policy'lerini host cozumuyle doner
	// (ingress router snapshot'i icin; ListHostRoutes ile ayni desen).
	ListPolicyRoutes(ctx context.Context) ([]PolicyRoute, error)

	// --- Clients (KIRACI KAPSAMLI) ---
	//
	// Baska kiracinin kaydina erisim ErrNotFound doner — "yetkisiz" degil:
	// kaydin VARLIGINI bile sizdirmamak icin.
	CreateClient(ctx context.Context, tenantID, name, tokenID, tokenHash string) (Client, error)
	CreateClientWithProject(ctx context.Context, tenantID, name, tokenID, tokenHash, projectID string) (Client, error)
	GetClient(ctx context.Context, tenantID, id string) (Client, error)
	ListClients(ctx context.Context, tenantID string) ([]Client, error)
	ListClientsByProject(ctx context.Context, tenantID, projectID string) ([]Client, error)
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
	CreateTunnelWithProject(ctx context.Context, tenantID, clientID, target, projectID string) (Tunnel, error)
	GetTunnel(ctx context.Context, tenantID, id string) (Tunnel, error)
	ListTunnels(ctx context.Context, tenantID string) ([]Tunnel, error)
	ListTunnelsByProject(ctx context.Context, tenantID, projectID string) ([]Tunnel, error)
	UpdateTunnel(ctx context.Context, tenantID, id string, patch TunnelPatch) (Tunnel, error)
	DeleteTunnel(ctx context.Context, tenantID, id string) error

	// Gecici tuneller (FAZ 2 / F07). CreateEphemeralTunnel TTL'li tunel acar;
	// DeleteExpiredTunnels suresi dolmuslari temizler ve sayiyi doner.
	CreateEphemeralTunnel(ctx context.Context, tenantID, clientID, target, projectID string, expiresAt time.Time) (Tunnel, error)
	DeleteExpiredTunnels(ctx context.Context) (int, error)

	// Ozel ag (FAZ 3 / F17). MakeTunnelPrivate tuneli TCP + private yapar ve
	// adini atar; GetPrivateTunnel adla (kiraci icinde, buyuk/kucuk duyarsiz) bulur.
	MakeTunnelPrivate(ctx context.Context, tenantID, id, name string) error
	GetPrivateTunnel(ctx context.Context, tenantID, name string) (Tunnel, error)
	// ListPrivateSubnets (F20): kiracinin ETKIN alt ag kaynaklari
	// (hedefi "subnet:<CIDR>" olan ozel tuneller).
	ListPrivateSubnets(ctx context.Context, tenantID string) ([]Tunnel, error)

	// Yuk dengeleme (FAZ 4 / F21). GetTunnelLB kayit yoksa varsayilani doner.
	// ListTunnelLBs TUM kiracilari kapsar; router yenilemesinde bir kez okunur.
	GetTunnelLB(ctx context.Context, tenantID, tunnelID string) (TunnelLB, error)
	SetTunnelLB(ctx context.Context, tenantID string, lb TunnelLB) error
	ListTunnelLBs(ctx context.Context) ([]TunnelLB, error)

	// UDP ileri (FAZ 4 / F24). GetTunnelUDP kayit yoksa varsayilani doner;
	// ListTunnelUDPs TUM kiracilari kapsar (rawproxy yenilemesinde bir kez).
	GetTunnelUDP(ctx context.Context, tenantID, tunnelID string) (TunnelUDP, error)
	SetTunnelUDP(ctx context.Context, tenantID string, u TunnelUDP) error
	ListTunnelUDPs(ctx context.Context) ([]TunnelUDP, error)
	// InsertUDPStats dakikalik ozetleri yazar (ayni dakika gelirse toplanir).
	InsertUDPStats(ctx context.Context, stats []UDPStatMinute) error
	ListUDPStats(ctx context.Context, tenantID, tunnelID string, since time.Time) ([]UDPStatMinute, error)
	PruneUDPStats(ctx context.Context, before time.Time) (int64, error)

	// Cihaz bilgisi (FAZ 3 / F14). UpdateDeviceInfo el sikismasinda,
	// TouchDeviceSeen baglanti kopusunda cagrilir.
	UpdateDeviceInfo(ctx context.Context, clientID string, d DeviceInfo) error
	TouchDeviceSeen(ctx context.Context, clientID string) error

	// Uzaktan ajan yapilandirmasi (FAZ 3 / F15). Kayit yoksa (nil, nil) doner:
	// "ayar yok" bir hata degildir.
	// GetDeviceConfigByClient KIRACIDAN BAGIMSIZDIR — el sikismasinda kullanilir,
	// orada istemcinin kimligi zaten token'la dogrulanmistir.
	GetDeviceConfig(ctx context.Context, tenantID, clientID string) (*protocol.AgentSettings, error)
	GetDeviceConfigByClient(ctx context.Context, clientID string) (*protocol.AgentSettings, error)
	SetDeviceConfig(ctx context.Context, tenantID, clientID string, cfg protocol.AgentSettings) error

	// Cihaz etiketleri (FAZ 3 / F16).
	GetDeviceTags(ctx context.Context, tenantID, clientID string) (map[string]string, error)
	ListDeviceTagsByTenant(ctx context.Context, tenantID string) (map[string]map[string]string, error)
	SetDeviceTags(ctx context.Context, tenantID, clientID string, tags map[string]string) error
	// GetMemberRole, kullanicinin kiracidaki gercek rolu; uyelik yoksa "".
	GetMemberRole(ctx context.Context, tenantID, userID string) (string, error)

	// ListReservedPortTunnels, public_port atanmis TUM tunelleri (kiracidan
	// bagimsiz) doner. Ham TCP/UDP dinleyici yoneticisi ve port tahsisi kullanir.
	ListReservedPortTunnels(ctx context.Context) ([]Tunnel, error)
	// SetTunnelPort, tunelin rezerve portunu ayarlar (port>0) veya birakir (port=0 -> NULL).
	SetTunnelPort(ctx context.Context, tenantID, id string, port int) error

	// --- Kötüye kullanım (FAZ 4, PLATFORM ADMIN) ---
	//
	// AdminSetTunnelFrozen, bir tuneli (kiracidan bagimsiz) dondurur/cozer.
	AdminSetTunnelFrozen(ctx context.Context, tunnelID string, frozen bool) error
	// AdminFreezeByFQDN, bir hostname'e bagli tuneli dondurur (abuse yanitinda).
	AdminFreezeByFQDN(ctx context.Context, fqdn string, frozen bool) error
	// CreateAbuseReport, bir kotuye-kullanim bildirimi kaydeder.
	CreateAbuseReport(ctx context.Context, fqdn, reason, reporterIP string) (AbuseReport, error)
	// ListAbuseReports, en yeni bildirimleri doner (admin paneli).
	ListAbuseReports(ctx context.Context, limit int) ([]AbuseReport, error)

	// ListTunnelsByClient, bir istemcinin tunelleri. Istemci zaten dogrulanmis
	// oldugu icin (token -> client -> tenant) ayrica tenantID istemez.
	ListTunnelsByClient(ctx context.Context, clientID string) ([]Tunnel, error)

	// --- Tünel replikaları (FAZ 5 / HA) ---
	// AddTunnelReplica, bir tunele ek servis-eden istemci ekler (kiraci sahipligi
	// dogrulanir; tunel ve client ayni kiraciya ait olmali). Idempotenttir.
	AddTunnelReplica(ctx context.Context, tenantID, tunnelID, clientID string) error
	// RemoveTunnelReplica, bir replikayi kaldirir.
	RemoveTunnelReplica(ctx context.Context, tenantID, tunnelID, clientID string) error
	// ListTunnelReplicas, bir tunelin replika istemci ID'lerini doner.
	ListTunnelReplicas(ctx context.Context, tenantID, tunnelID string) ([]string, error)

	// --- Tünel erişim denetimi (FAZ 1a) ---
	// GetTunnelAccessPolicy, tunelin politikasini doner (yoksa mode=none/enabled=false).
	GetTunnelAccessPolicy(ctx context.Context, tenantID, tunnelID string) (TunnelAccessPolicy, error)
	// SetTunnelAccessPolicy, politikayi olusturur/gunceller (upsert). Kiraci sahipligini dogrular.
	SetTunnelAccessPolicy(ctx context.Context, tenantID string, p TunnelAccessPolicy) error

	// --- Trafik politikası (FAZ 6) ---
	// Ping, veritabani baglantisinin canli olup olmadigini kontrol eder (FAZ 6 status page).
	Ping(ctx context.Context) error

	// GetTunnelTrafficPolicy, tunelin trafik politikasini doner (yoksa enabled=false).
	GetTunnelTrafficPolicy(ctx context.Context, tenantID, tunnelID string) (TunnelTrafficPolicy, error)
	// SetTunnelTrafficPolicy, politikayi upsert eder. Kiraci sahipligini dogrular.
	SetTunnelTrafficPolicy(ctx context.Context, tenantID string, p TunnelTrafficPolicy) error

	// --- Hostnames (KIRACI KAPSAMLI) ---
	//
	// typ, HostType* sabitlerinden biri. Ayni fqdn ikinci kez eklenirse
	// ErrHostnameTaken doner (GLOBAL benzersizlik).
	AddHostname(ctx context.Context, tenantID, tunnelID, fqdn, typ string) (Hostname, error)
	AddHostnameWithProject(ctx context.Context, tenantID, tunnelID, fqdn, typ, projectID string) (Hostname, error)
	AddCustomHostname(ctx context.Context, tenantID, tunnelID, fqdn, verifyToken string) (Hostname, error)
	AttachHostname(ctx context.Context, tenantID, hostnameID, tunnelID string) error
	DetachHostname(ctx context.Context, tenantID, hostnameID string) error
	VerifyHostname(ctx context.Context, tenantID, id string) (Hostname, error)
	GetHostnameByID(ctx context.Context, tenantID, id string) (Hostname, error)
	GetHostnameByFQDN(ctx context.Context, fqdn string) (Hostname, error)
	ListHostnames(ctx context.Context, tenantID string) ([]Hostname, error)
	ListHostnamesByProject(ctx context.Context, tenantID, projectID string) ([]Hostname, error)
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

	// TunnelMetrics, per-tunel istek metriklerini zaman dilimlerine bolerek doner (FAZ 6.3).
	TunnelMetrics(ctx context.Context, tenantID, tunnelID string, since time.Time, bucketSec int) ([]reqlog.MetricBucket, error)

	// --- Metrik uyarilari (FAZ 6.4) ---
	// GetTunnelAlert, tunelin uyari yapilandirmasini doner (yoksa varsayilanlar, enabled=false).
	GetTunnelAlert(ctx context.Context, tenantID, tunnelID string) (TunnelAlert, error)
	// SetTunnelAlert, uyari yapilandirmasini upsert eder (durum alanlarina dokunmaz).
	SetTunnelAlert(ctx context.Context, tenantID string, a TunnelAlert) error
	// ListEnabledAlerts, degerlendirici icin TUM kiracilardaki etkin uyarilari doner.
	ListEnabledAlerts(ctx context.Context) ([]TunnelAlert, error)
	// UpdateAlertState, degerlendirme sonrasi durum + bildirim zamanlarini gunceller.
	UpdateAlertState(ctx context.Context, tunnelID, state string, notified bool) error
	// TunnelRequestStats, verilen zamandan beri toplam ve 5xx istek sayisini doner (uyari degerlendirme).
	TunnelRequestStats(ctx context.Context, tenantID, tunnelID string, since time.Time) (total, errors int64, err error)

	// --- mTLS / istemci sertifikasi (FAZ 6.6) ---
	// GetTunnelMTLS, tunelin mTLS yapilandirmasini doner (yoksa enabled=false).
	GetTunnelMTLS(ctx context.Context, tenantID, tunnelID string) (TunnelMTLS, error)
	// SetTunnelMTLS, mTLS yapilandirmasini upsert eder. Kiraci sahipligini dogrular.
	SetTunnelMTLS(ctx context.Context, tenantID string, m TunnelMTLS) error

	// --- Yol tabanli yonlendirme (FAZ 6.5) ---
	// ListPathRouteEntries, TUM path kurallarini router snapshot'i icin doner
	// (her biri hedef tunelin tam HostRoute verisi + PathPrefix ile).
	ListPathRouteEntries(ctx context.Context) ([]HostRoute, error)
	// ListPathRoutes, bir hostname'in (fqdn) yol kurallarini doner (panel/REST).
	ListPathRoutes(ctx context.Context, tenantID, fqdn string) ([]PathRoute, error)
	// AddPathRoute, bir yol kurali ekler (kiraci sahipligi + tunel dogrulanir).
	AddPathRoute(ctx context.Context, tenantID, fqdn, pathPrefix, tunnelID string) (PathRoute, error)
	// DeletePathRoute, bir yol kuralini siler.
	DeletePathRoute(ctx context.Context, tenantID, id string) error

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
	// CountPendingInvitations, suresi dolmamis bekleyen davet sayisi (uye
	// limitinde koltuk sayilir).
	CountPendingInvitations(ctx context.Context, tenantID string) (int, error)
	// Davetler: e-postadaki link ile kabul/ret (uyelik ancak kabul ile olusur).
	GetInvitation(ctx context.Context, id string) (TeamInvitation, error)
	AcceptInvitation(ctx context.Context, id, userID, userEmail string) (TeamInvitation, error)
	DeclineInvitation(ctx context.Context, id, userEmail string) error
	ListInvitationsForEmail(ctx context.Context, email string) ([]TeamInvitation, error)
	// FirstMembership, kullanicinin en eski uyeligi (org, rol). Yoksa ErrNotFound.
	FirstMembership(ctx context.Context, userID string) (orgID, role string, err error)
	CreateMemberClient(ctx context.Context, tenantID, userID, name, tokenID, tokenHash string) (Client, error)
	ListMemberClients(ctx context.Context, tenantID, userID string) ([]Client, error)

	// --- Programatik REST API Tokenlari ---
	CreateAPIToken(ctx context.Context, tenantID string, userID *string, name, tokenID, tokenHash, prefix string, scopes []string, expiresAt *time.Time) (APIToken, error)
	ListAPITokens(ctx context.Context, tenantID string) ([]APIToken, error)
	// GetAPIToken, kiraciya ait iptal edilmemis token'i id ile doner (yetki kontrolu icin).
	GetAPIToken(ctx context.Context, tenantID, id string) (APIToken, error)
	GetAPITokenByTokenID(ctx context.Context, tokenID string) (APIToken, error)
	RevokeAPIToken(ctx context.Context, tenantID, id string) error
	RotateAPIToken(ctx context.Context, tenantID, id, newTokenID, newTokenHash string) (APIToken, error)
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
