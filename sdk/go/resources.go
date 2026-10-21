package zorven

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ---- Tipler ----

// ClientInfo tunel acan ajan makinedir.
type ClientInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"` // online | offline
	Version    string     `json:"version,omitempty"`
	RemoteAddr string     `json:"remote_addr,omitempty"`
	IsService  bool       `json:"is_service,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// CreatedClient istemci olusturma yanitidir. Token yalnizca burada gorunur.
type CreatedClient struct {
	Client ClientInfo `json:"client"`
	Token  string     `json:"token"`
}

// Tunnel hostname -> istemci -> yerel hedef eslestirmesidir.
type Tunnel struct {
	ID        string     `json:"id"`
	ClientID  string     `json:"client_id"`
	Target    string     `json:"target"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
	Hostnames []Hostname `json:"hostnames,omitempty"`
}

// Hostname bir tunelin yayinlandigi addir.
type Hostname struct {
	ID              string     `json:"id"`
	TunnelID        string     `json:"tunnel_id,omitempty"`
	FQDN            string     `json:"fqdn"`
	Type            string     `json:"type"` // global | scoped | legacy | custom
	Verified        bool       `json:"verified"`
	SuspendedReason string     `json:"suspended_reason,omitempty"`
	SuspendedAt     *time.Time `json:"suspended_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// CustomHostname ozel domain baglama yanitidir (DNS dogrulama talimatlariyla).
type CustomHostname struct {
	Hostname
	Instructions map[string]any `json:"instructions,omitempty"`
}

// IPRule CIDR bazli erisim kuralidir.
type IPRule struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id,omitempty"`
	TunnelID    *string   `json:"tunnel_id,omitempty"`
	CIDR        string    `json:"cidr"`
	Description string    `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RequestLog istek logu satiridir.
type RequestLog struct {
	ID         string    `json:"id"`
	TunnelID   string    `json:"tunnel_id"`
	TS         time.Time `json:"ts"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int       `json:"duration_ms"`
}

// AccessEvent ziyaretci giris olayidir.
type AccessEvent struct {
	ID        string    `json:"id"`
	TunnelID  string    `json:"tunnel_id"`
	Hostname  string    `json:"hostname"`
	Method    string    `json:"method"` // basic | oauth
	Provider  string    `json:"provider,omitempty"`
	Identity  string    `json:"identity,omitempty"`
	Success   bool      `json:"success"`
	Reason    string    `json:"reason,omitempty"`
	ClientIP  string    `json:"client_ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AccessEventSummary giris olaylari ozetidir.
type AccessEventSummary struct {
	Window           string         `json:"window"`
	Since            time.Time      `json:"since"`
	Total            int            `json:"total"`
	Success          int            `json:"success"`
	Failure          int            `json:"failure"`
	ByMethod         map[string]int `json:"by_method,omitempty"`
	ByProvider       map[string]int `json:"by_provider,omitempty"`
	ByReason         map[string]int `json:"by_reason,omitempty"`
	UniqueIdentities int            `json:"unique_identities"`
	UniqueIPs        int            `json:"unique_ips"`
	DoorGrants       int            `json:"door_grants"`
	DoorBlocked      int            `json:"door_blocked"`
	DoorBlockedIPs   int            `json:"door_blocked_ips"`
}

// TunnelDoor TCP/UDP tuneli icin "web ile kapi acma" ayaridir.
type TunnelDoor struct {
	TunnelID       string `json:"tunnel_id"`
	Enabled        bool   `json:"enabled"`
	DurationSec    int    `json:"duration_sec"`
	Durations      []int  `json:"durations,omitempty"`
	Available      bool   `json:"available"`
	Host           string `json:"host,omitempty"`
	URL            string `json:"url,omitempty"`
	ConnectAddress string `json:"connect_address,omitempty"`
	Proto          string `json:"proto,omitempty"`
	Exposure       string `json:"exposure,omitempty"`
	AccessMode     string `json:"access_mode,omitempty"`
	AccessReady    bool   `json:"access_ready"`
}

// DoorGrant kapi izni (IP bazli).
type DoorGrant struct {
	ID        string    `json:"id"`
	TunnelID  string    `json:"tunnel_id"`
	IP        string    `json:"ip"`
	Identity  string    `json:"identity,omitempty"`
	Method    string    `json:"method,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Plan kullanilabilir plan.
type Plan struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	PriceMonthly           int    `json:"price_monthly"` // USD cent
	MaxClients             *int   `json:"max_clients"`   // nil = sinirsiz
	MaxTunnels             *int   `json:"max_tunnels"`
	MaxCustomDomains       *int   `json:"max_custom_domains"`
	BandwidthLimitBytes    *int64 `json:"bandwidth_limit_bytes"`
	BandwidthNormalMbps    int    `json:"bandwidth_normal_mbps"`
	BandwidthThrottledMbps int    `json:"bandwidth_throttled_mbps"`
	MaxMembers             *int   `json:"max_members"`
	HasAPIAccess           bool   `json:"has_api_access"`
	HasIPAllowlist         bool   `json:"has_ip_allowlist"`
}

// Subscription abonelik ve kullanim.
type Subscription struct {
	TenantID                     string     `json:"tenant_id"`
	Plan                         string     `json:"plan"`
	Status                       string     `json:"status"`
	CurrentPeriodEnd             *time.Time `json:"current_period_end"`
	BandwidthLimitBytes          int64      `json:"bandwidth_limit_bytes"`
	TrialUsed                    bool       `json:"trial_used"`
	ExtraDevices                 int        `json:"extra_devices"`
	ExtraGB                      int        `json:"extra_gb"`
	EffectiveMaxClients          *int       `json:"effective_max_clients"`
	EffectiveBandwidthLimitBytes *int64     `json:"effective_bandwidth_limit_bytes"`
	CreatedAt                    time.Time  `json:"created_at"`
	PlanDetails                  *Plan      `json:"plan_details,omitempty"`
}

// Health /health yanitidir.
type Health struct {
	Status           string `json:"status"`
	Version          string `json:"version"`
	ConnectedClients int    `json:"connected_clients"`
}

// ---- Istek tipleri ----

// CreateTunnelRequest POST /tunnels govdesi.
type CreateTunnelRequest struct {
	Name       string `json:"name"`
	ClientID   string `json:"client_id"`
	Target     string `json:"target"`
	HostnameID string `json:"hostname_id,omitempty"`
}

// UpdateTunnelRequest PATCH /tunnels/{id}; nil alanlar gonderilmez.
type UpdateTunnelRequest struct {
	Target  *string `json:"target,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

// CreateHostnameRequest POST /hostnames govdesi. Kind: "scoped" (varsayilan) | "short".
type CreateHostnameRequest struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	TunnelID string `json:"tunnel_id,omitempty"`
}

// CreateCustomHostnameRequest POST /hostnames/custom govdesi.
type CreateCustomHostnameRequest struct {
	FQDN     string `json:"fqdn"`
	TunnelID string `json:"tunnel_id,omitempty"`
}

// CreateIPRuleRequest POST /ip-allowlist govdesi. Enabled nil ise sunucu varsayilani (true).
type CreateIPRuleRequest struct {
	CIDR        string  `json:"cidr"`
	TunnelID    *string `json:"tunnel_id,omitempty"`
	Description string  `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// UpdateIPRuleRequest PATCH /ip-allowlist/{id}; nil alanlar gonderilmez.
type UpdateIPRuleRequest struct {
	CIDR        *string `json:"cidr,omitempty"`
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// SetDoorRequest PUT /tunnels/{id}/door govdesi. DurationSec: 3600, 43200, 86400 veya 604800.
type SetDoorRequest struct {
	Enabled     bool `json:"enabled"`
	DurationSec int  `json:"duration_sec,omitempty"`
}

// RequestLogQuery GET /requests filtreleri; sifir degerler gonderilmez.
type RequestLogQuery struct {
	Limit    int // varsayilan 200, maks 1000
	Offset   int
	TunnelID string
	Hostname string
	Method   string
	Status   string // "404" veya "2xx"
	Since    time.Time
	Until    time.Time
	Q        string
}

func (r *RequestLogQuery) values() url.Values {
	q := url.Values{}
	if r == nil {
		return q
	}
	if r.Limit > 0 {
		q.Set("limit", strconv.Itoa(r.Limit))
	}
	if r.Offset > 0 {
		q.Set("offset", strconv.Itoa(r.Offset))
	}
	for k, v := range map[string]string{"tunnel_id": r.TunnelID, "hostname": r.Hostname, "method": r.Method, "status": r.Status, "q": r.Q} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if !r.Since.IsZero() {
		q.Set("since", r.Since.UTC().Format(time.RFC3339))
	}
	if !r.Until.IsZero() {
		q.Set("until", r.Until.UTC().Format(time.RFC3339))
	}
	return q
}

func esc(s string) string { return url.PathEscape(s) }

// ---- Meta ----

// MetaService saglik, kimlik ve plan bilgisi.
type MetaService struct{ c *Client }

// Health GET /health.
func (s *MetaService) Health(ctx context.Context) (*Health, error) {
	var out Health
	if err := s.c.call(ctx, http.MethodGet, "/health", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Me GET /me: gecerli kimlik ve kiraci (sunucu yaniti serbest bicimli JSON).
func (s *MetaService) Me(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := s.c.call(ctx, http.MethodGet, "/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Plans GET /plans.
func (s *MetaService) Plans(ctx context.Context) ([]Plan, error) {
	var out []Plan
	if err := s.c.call(ctx, http.MethodGet, "/plans", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- Clients ----

// ClientsService istemci (ajan) islemleri.
type ClientsService struct{ c *Client }

// List GET /clients (tek sayfa).
func (s *ClientsService) List(ctx context.Context, o *ListOptions) (*Page[ClientInfo], error) {
	return listPage[ClientInfo](ctx, s.c, "/clients", o.values())
}

// Iter tum istemcileri sayfa sayfa dolasir.
func (s *ClientsService) Iter(ctx context.Context, o *ListOptions) iter.Seq2[ClientInfo, error] {
	return iterate(ctx, o, func(lo ListOptions) (*Page[ClientInfo], error) { return s.List(ctx, &lo) })
}

// ListAll tum istemcileri toplar.
func (s *ClientsService) ListAll(ctx context.Context, o *ListOptions) ([]ClientInfo, error) {
	return collect(s.Iter(ctx, o))
}

// Get GET /clients/{id}.
func (s *ClientsService) Get(ctx context.Context, id string) (*ClientInfo, error) {
	var out ClientInfo
	if err := s.c.call(ctx, http.MethodGet, "/clients/"+esc(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create POST /clients. Donen token yalnizca bir kez gorunur.
func (s *ClientsService) Create(ctx context.Context, name string) (*CreatedClient, error) {
	var out CreatedClient
	body := map[string]string{"name": name}
	if err := s.c.call(ctx, http.MethodPost, "/clients", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete DELETE /clients/{id}.
func (s *ClientsService) Delete(ctx context.Context, id string) error {
	return s.c.call(ctx, http.MethodDelete, "/clients/"+esc(id), nil, nil, nil)
}

// RotateToken POST /clients/{id}/token; yeni token'i doner, eskisi gecersiz olur.
func (s *ClientsService) RotateToken(ctx context.Context, id string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := s.c.call(ctx, http.MethodPost, "/clients/"+esc(id)+"/token", nil, nil, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// ---- Tunnels ----

// TunnelsService tunel islemleri.
type TunnelsService struct{ c *Client }

// List GET /tunnels (tek sayfa).
func (s *TunnelsService) List(ctx context.Context, o *ListOptions) (*Page[Tunnel], error) {
	return listPage[Tunnel](ctx, s.c, "/tunnels", o.values())
}

// Iter tum tunelleri sayfa sayfa dolasir.
func (s *TunnelsService) Iter(ctx context.Context, o *ListOptions) iter.Seq2[Tunnel, error] {
	return iterate(ctx, o, func(lo ListOptions) (*Page[Tunnel], error) { return s.List(ctx, &lo) })
}

// ListAll tum tunelleri toplar.
func (s *TunnelsService) ListAll(ctx context.Context, o *ListOptions) ([]Tunnel, error) {
	return collect(s.Iter(ctx, o))
}

// Get GET /tunnels/{id}.
func (s *TunnelsService) Get(ctx context.Context, id string) (*Tunnel, error) {
	var out Tunnel
	if err := s.c.call(ctx, http.MethodGet, "/tunnels/"+esc(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create POST /tunnels.
func (s *TunnelsService) Create(ctx context.Context, req CreateTunnelRequest) (*Tunnel, error) {
	var out Tunnel
	if err := s.c.call(ctx, http.MethodPost, "/tunnels", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update PATCH /tunnels/{id}.
func (s *TunnelsService) Update(ctx context.Context, id string, req UpdateTunnelRequest) (*Tunnel, error) {
	var out Tunnel
	if err := s.c.call(ctx, http.MethodPatch, "/tunnels/"+esc(id), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete DELETE /tunnels/{id}.
func (s *TunnelsService) Delete(ctx context.Context, id string) error {
	return s.c.call(ctx, http.MethodDelete, "/tunnels/"+esc(id), nil, nil, nil)
}

// GetDoor GET /tunnels/{id}/door (yalniz TCP/UDP tunelleri).
func (s *TunnelsService) GetDoor(ctx context.Context, id string) (*TunnelDoor, error) {
	var out TunnelDoor
	if err := s.c.call(ctx, http.MethodGet, "/tunnels/"+esc(id)+"/door", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetDoor PUT /tunnels/{id}/door (Pro+).
func (s *TunnelsService) SetDoor(ctx context.Context, id string, req SetDoorRequest) (*TunnelDoor, error) {
	var out TunnelDoor
	if err := s.c.call(ctx, http.MethodPut, "/tunnels/"+esc(id)+"/door", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDoorGrants GET /tunnels/{id}/door/grants.
func (s *TunnelsService) ListDoorGrants(ctx context.Context, id string) ([]DoorGrant, error) {
	var out []DoorGrant
	if err := s.c.call(ctx, http.MethodGet, "/tunnels/"+esc(id)+"/door/grants", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeDoorGrant DELETE /tunnels/{id}/door/grants/{grantId}.
func (s *TunnelsService) RevokeDoorGrant(ctx context.Context, id, grantID string) error {
	return s.c.call(ctx, http.MethodDelete, "/tunnels/"+esc(id)+"/door/grants/"+esc(grantID), nil, nil, nil)
}

// ---- Hostnames ----

// HostnamesService hostname islemleri.
type HostnamesService struct{ c *Client }

// List GET /hostnames (tek sayfa).
func (s *HostnamesService) List(ctx context.Context, o *ListOptions) (*Page[Hostname], error) {
	return listPage[Hostname](ctx, s.c, "/hostnames", o.values())
}

// Iter tum hostname'leri sayfa sayfa dolasir.
func (s *HostnamesService) Iter(ctx context.Context, o *ListOptions) iter.Seq2[Hostname, error] {
	return iterate(ctx, o, func(lo ListOptions) (*Page[Hostname], error) { return s.List(ctx, &lo) })
}

// ListAll tum hostname'leri toplar.
func (s *HostnamesService) ListAll(ctx context.Context, o *ListOptions) ([]Hostname, error) {
	return collect(s.Iter(ctx, o))
}

// Create POST /hostnames (platform hostname'i).
func (s *HostnamesService) Create(ctx context.Context, req CreateHostnameRequest) (*Hostname, error) {
	var out Hostname
	if err := s.c.call(ctx, http.MethodPost, "/hostnames", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateCustom POST /hostnames/custom (ozel domain; DNS talimatlari doner).
func (s *HostnamesService) CreateCustom(ctx context.Context, req CreateCustomHostnameRequest) (*CustomHostname, error) {
	var out CustomHostname
	if err := s.c.call(ctx, http.MethodPost, "/hostnames/custom", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Attach PATCH /hostnames/{id}: hostname'i tunele baglar.
func (s *HostnamesService) Attach(ctx context.Context, id, tunnelID string) (*Hostname, error) {
	return s.patchTunnel(ctx, id, &tunnelID)
}

// Detach PATCH /hostnames/{id}: tunel baglantisini cozer (tunnel_id: null).
func (s *HostnamesService) Detach(ctx context.Context, id string) (*Hostname, error) {
	return s.patchTunnel(ctx, id, nil)
}

func (s *HostnamesService) patchTunnel(ctx context.Context, id string, tunnelID *string) (*Hostname, error) {
	var out Hostname
	body := map[string]*string{"tunnel_id": tunnelID}
	if err := s.c.call(ctx, http.MethodPatch, "/hostnames/"+esc(id), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete DELETE /hostnames/{id}.
func (s *HostnamesService) Delete(ctx context.Context, id string) error {
	return s.c.call(ctx, http.MethodDelete, "/hostnames/"+esc(id), nil, nil, nil)
}

// Verify POST /hostnames/{id}/verify (ozel domain DNS dogrulamasi).
func (s *HostnamesService) Verify(ctx context.Context, id string) (*Hostname, error) {
	var out Hostname
	if err := s.c.call(ctx, http.MethodPost, "/hostnames/"+esc(id)+"/verify", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- IP Allowlist ----

// IPAllowlistService IP kurali islemleri.
type IPAllowlistService struct{ c *Client }

// List GET /ip-allowlist (sayfalama yok, tum kurallar).
func (s *IPAllowlistService) List(ctx context.Context) ([]IPRule, error) {
	var out []IPRule
	if err := s.c.call(ctx, http.MethodGet, "/ip-allowlist", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Create POST /ip-allowlist.
func (s *IPAllowlistService) Create(ctx context.Context, req CreateIPRuleRequest) (*IPRule, error) {
	var out IPRule
	if err := s.c.call(ctx, http.MethodPost, "/ip-allowlist", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update PATCH /ip-allowlist/{id}.
func (s *IPAllowlistService) Update(ctx context.Context, id string, req UpdateIPRuleRequest) (*IPRule, error) {
	var out IPRule
	if err := s.c.call(ctx, http.MethodPatch, "/ip-allowlist/"+esc(id), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete DELETE /ip-allowlist/{id}.
func (s *IPAllowlistService) Delete(ctx context.Context, id string) error {
	return s.c.call(ctx, http.MethodDelete, "/ip-allowlist/"+esc(id), nil, nil, nil)
}

// ---- Analytics ----

// AnalyticsService istek loglari, giris olaylari ve abonelik.
type AnalyticsService struct{ c *Client }

// Requests GET /requests.
func (s *AnalyticsService) Requests(ctx context.Context, q *RequestLogQuery) ([]RequestLog, error) {
	var out []RequestLog
	if err := s.c.call(ctx, http.MethodGet, "/requests", q.values(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AccessEvents GET /tunnels/{id}/access-events (tek sayfa, en yeniden eskiye).
func (s *AnalyticsService) AccessEvents(ctx context.Context, tunnelID string, o *ListOptions) (*Page[AccessEvent], error) {
	return listPage[AccessEvent](ctx, s.c, "/tunnels/"+esc(tunnelID)+"/access-events", o.values())
}

// IterAccessEvents tum giris olaylarini sayfa sayfa dolasir.
func (s *AnalyticsService) IterAccessEvents(ctx context.Context, tunnelID string, o *ListOptions) iter.Seq2[AccessEvent, error] {
	return iterate(ctx, o, func(lo ListOptions) (*Page[AccessEvent], error) { return s.AccessEvents(ctx, tunnelID, &lo) })
}

// AccessEventSummary GET /tunnels/{id}/access-events/summary. window: "24h" (varsayilan), "7d", "30d".
func (s *AnalyticsService) AccessEventSummary(ctx context.Context, tunnelID, window string) (*AccessEventSummary, error) {
	q := url.Values{}
	if window != "" {
		q.Set("window", window)
	}
	var out AccessEventSummary
	if err := s.c.call(ctx, http.MethodGet, "/tunnels/"+esc(tunnelID)+"/access-events/summary", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Subscription GET /subscription.
func (s *AnalyticsService) Subscription(ctx context.Context) (*Subscription, error) {
	var out Subscription
	if err := s.c.call(ctx, http.MethodGet, "/subscription", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
