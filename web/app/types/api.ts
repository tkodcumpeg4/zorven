/**
 * api_contract.md §1 "Veri Modeli" ile birebir hizalı.
 * MVP'de elle yazıldı; Phase 5'te `tygo` ile Go struct'larından üretilecek.
 * Alan adları Go tarafındaki json tag'leriyle aynı (snake_case) tutulmalı.
 */

export type ClientStatus = 'online' | 'offline'

export interface Metrics {
  cpu_percent: number
  memory_percent: number
  memory_used_mb?: number
  memory_total_mb?: number
  disk_percent?: number
}

export interface Project {
  id: string
  tenant_id: string
  name: string
  slug: string
  created_at: string
}

// Secret Vault (FAZ 1 / F06). Deger yalnizca olusturma yanitinda doner.
export interface Secret {
  id: string
  tenant_id: string
  project_id: string
  name: string
  key_version: number
  created_at: string
  updated_at: string
}

// Birlesik Policy motoru (FAZ 1 / F04).
export interface PolicyMatch {
  path_prefix?: string
  methods?: string[]
  header?: Record<string, string>
  // Geo (F03a) — boş bırakılanlar eşleşmede dikkate alınmaz.
  country?: string[]      // ISO 3166-1 alpha-2
  continent?: string[]    // kıta kodu
  asn?: number[]
  isp?: string[]          // ASN organizasyon adı
  // Liste tabanlı (F03b-d). undefined = "belirtilmedi", false = "olmayan".
  tor_exit?: boolean
  hosting?: boolean
  reputation?: string[]   // liste adları; herhangi biri tutarsa eşleşir
}
export type PolicyActionType =
  | 'deny' | 'set_header' | 'redirect' | 'require_mtls' | 'rate_limit'
  | 'verify_webhook' | 'waf'

export interface PolicyAction {
  type: PolicyActionType
  status?: number
  message?: string
  location?: string
  request?: { set?: Record<string, string>, remove?: string[] }
  response?: { set?: Record<string, string>, remove?: string[] }
  key?: string
  requests?: number
  window_sec?: number
  burst?: number
  // verify_webhook (F05)
  provider?: 'github' | 'stripe' | 'gitlab' | 'shopify' | 'slack'
  secret_ref?: string     // {{secret:ad}} veya düz değer
  tolerance_sec?: number  // zaman damgalı sağlayıcılarda; 0 => 300
  // waf (F02)
  ruleset?: string        // '' | owasp-lite
  patterns?: string[]     // kuruma özel ek regex'ler
}
export interface PolicyRule {
  match: PolicyMatch
  action: PolicyAction
}
export interface PolicyConfig {
  rules: PolicyRule[]
}
export interface PolicyBinding {
  policy_id: string
  tunnel_id?: string
  hostname?: string
}
export interface Policy {
  id: string
  tenant_id: string
  project_id: string
  name: string
  config: PolicyConfig
  enabled: boolean
  priority: number
  created_at: string
  updated_at: string
  bindings?: PolicyBinding[]
}

export interface Client {
  id: string                 // "cli_a1b2c3"
  project_id?: string
  name: string               // "ev-pc"
  status: ClientStatus
  version?: string           // yalnızca bağlıyken
  remote_addr?: string
  created_at: string         // RFC 3339 UTC
  last_seen_at?: string
  is_service?: boolean
  app_kind?: string          // "desktop" | boş (CLI/servis)
  update_available?: boolean // bağlı istemcinin sürümü yayındakinden eski
  latest_version?: string
  metrics?: Metrics
  // Cihaz alanları (F14) — bağlantı kopsa da kalıcıdır.
  hostname?: string
  os?: string                // "windows"
  arch?: string              // "amd64"
  ips?: string[]
  agent_version?: string
  last_metrics?: Metrics     // en son BİLİNEN metrikler (offline iken de dolu)
}

// Device, istemci kaydı + kalıcı cihaz bilgisi + canlı durum (F14).
export interface Device extends Client {
  tunnels?: Tunnel[]         // yalnızca tek cihaz detayında dolu
}

export type TunnelProto = 'http' | 'tcp' | 'udp'
// 'private' (F17): internete hiç açılmaz; yalnızca `zorven connect` ile.
export type TunnelExposure = 'auto' | 'port' | 'sni' | 'private'

export interface Tunnel {
  id: string                 // "tun_x9y8z7"
  project_id?: string
  client_id: string
  target: string             // "http://localhost:8000"
  enabled: boolean
  created_at: string
  /** Taşınan protokol. http (varsayılan), tcp veya udp (FAZ 3 / D2). */
  proto?: TunnelProto
  /** Ziyaretçi maruziyeti. http→auto; tcp/udp→port (rezerve) veya sni (forwarder). */
  exposure?: TunnelExposure
  /** Mod A (rezerve-port) atanan TCP/UDP portu; 0/undefined ise atanmamış. */
  public_port?: number
  /** Tünelin yayınlandığı adlar. Bir tünelin BİRDEN ÇOK adı olabilir. */
  hostnames?: Hostname[]
  /** Özel kaynağın adı (F17), ör. "db.internal". Yalnızca exposure === 'private'. */
  private_name?: string
}

/**
 * - `global` : `<ad>.<platform>`              — kullanıcının seçtiği kısa ad
 * - `scoped` : `<tünel>--<kiracı>.<platform>` — her zaman müsait, otomatik verilir
 * - `legacy` : göç öncesi serbest ad (ör. `api.localhost`)
 * - `custom` : kullanıcının kendi domaini (ör. `api.mydomain.com`)
 */
export type HostnameType = 'global' | 'scoped' | 'legacy' | 'custom'

export interface VerificationInstructions {
  cname_record: string
  cname_target: string
  txt_record: string
  txt_value: string
}

export interface Hostname {
  id: string                 // "hst_a1b2c3"
  project_id?: string
  tunnel_id: string | null
  fqdn: string               // "api.rpshell.app"
  type: HostnameType
  verified?: boolean
  verify_token?: string
  instructions?: VerificationInstructions
  created_at: string
}

export interface Tenant {
  id: string
  slug: string
  created_at: string
}

export interface TenantWithCounts extends Tenant {
  clients_count: number
  tunnels_count: number
  hostnames_count: number
  plan?: string
}

export interface Plan {
  id: string
  name: string
  price_monthly: number // Cent cinsinden (örn: 500 = $5.00)
  max_clients: number | null
  max_tunnels: number | null
  max_custom_domains: number | null
  bandwidth_limit_bytes: number | null
  bandwidth_normal_mbps: number
  bandwidth_throttled_mbps: number
  max_screen_streams: number | null
  screen_max_fps: number
  log_retention_days: number
  max_members: number | null
  has_api_access: boolean
  has_ip_allowlist: boolean
  extra_device_price: number
  extra_gb_price: number
  created_at: string
  updated_at: string
}

export interface Subscription {
  tenant_id: string
  plan: 'free' | 'hobby' | 'pro' | 'team' | 'enterprise' | string
  status: 'active' | 'past_due' | 'canceled' | 'trialing' | string
  stripe_customer_id?: string | null
  stripe_subscription_id?: string | null
  current_period_end?: string | null
  max_clients: number
  max_custom_domains: number
  max_tunnels: number
  bandwidth_limit_bytes: number
  plan_details?: Plan | null
  created_at: string
  updated_at: string
}

export interface TenantUsage {
  clients_count: number
  custom_domains_count: number
  tunnels_count: number
  bandwidth_used_bytes: number
  active_screen_streams: number
  is_throttled: boolean
}

export interface SubscriptionResponse {
  subscription: Subscription
  usage: TenantUsage
}

export interface PlansResponse {
  plans: Plan[]
}

export interface ClientWithTenant extends Client {
  tenant_slug: string
}

export interface HostnameWithTenant extends Hostname {
  tenant_slug: string
  target?: string
}

export interface AdminGlobalStats {
  tenants_count: number
  clients_count: number
  online_clients_count: number
  tunnels_count: number
  active_tunnels_count: number
  hostnames_count: number
  custom_domains_count: number
}

export interface CreateTunnelPayload {
  name: string
  client_id: string
  target: string
  hostname_id?: string
}

export interface CreateHostnamePayload {
  name: string
  tunnel_id?: string
}

export interface CreateCustomHostnamePayload {
  fqdn: string
  tunnel_id?: string
}

export interface PatchHostnamePayload {
  tunnel_id: string | null
}

export type CreateCustomHostnameResponse = Hostname & {
  instructions?: VerificationInstructions
  // Üst zone (ör. example.net) zaten doğrulanmışsa alt alan adı (api.example.net)
  // DNS doğrulaması olmadan otomatik aktif edilir.
  auto_verified?: boolean
  parent_zone?: string
  // Otomatik doğrulanan adın DNS'i zaten platforma yönleniyor mu (CNAME).
  dns_pointed?: boolean
}

export interface DNSCheckResult {
  fqdn: string
  pointed: boolean
  cname_target: string
}

export type VerifyHostnameResponse = Hostname

// --- FAZ 2: İstek inspector ---

export interface RequestDetail {
  id: string
  tunnel_id: string
  hostname?: string
  client_ip?: string
  ts: string
  method: string
  path: string
  query?: string
  status: number
  duration_ms: number
  req_headers?: Record<string, string[]>
  req_body: string
  req_body_truncated: boolean
  resp_headers?: Record<string, string[]>
  resp_body: string
  resp_body_truncated: boolean
}

// F09 — orijinal yakalama ile replay yanıtının karşılaştırması.
export interface FieldDiff {
  path: string
  old?: string
  new?: string
  kind: 'added' | 'removed' | 'changed'
}
export interface ReplayDiff {
  status_changed: boolean
  old_status: number
  new_status: number
  old_duration_ms: number
  new_duration_ms: number
  headers: FieldDiff[]
  body_kind: 'json' | 'text'
  body: FieldDiff[]
  truncated: boolean
}

// F08 — replay öncesi düzenlemeler. Verilmeyen alan orijinalden gelir.
export interface ReplayOverrides {
  method?: string
  path?: string
  query?: string
  headers?: Record<string, string>
  remove_headers?: string[]
  body?: string
  tunnel_id?: string
}

export interface ReplayResult {
  status: number
  headers?: Record<string, string[]>
  body: string
  body_truncated: boolean
  duration_ms?: number
  diff?: ReplayDiff
}

// --- Tünel erişim denetimi ---

export type TunnelAccessMode = 'none' | 'basic' | 'oauth'

export interface TunnelAccess {
  tunnel_id: string
  mode: TunnelAccessMode
  enabled: boolean
  config: {
    username?: string
    has_password?: boolean
    providers?: string[]
    allowed_emails?: string[]
  }
}

// Web ile kapı açma (ham TCP/UDP tünelleri)
export interface TunnelDoor {
  tunnel_id: string
  enabled: boolean
  duration_sec: number
  durations: number[]
  available: boolean
  host: string
  url: string
  connect_address: string
  proto: string
  exposure: string
  access_mode: TunnelAccessMode
  access_ready: boolean
}

export interface DoorGrant {
  id: string
  tunnel_id: string
  ip: string
  identity?: string
  method: 'basic' | 'oauth'
  expires_at: string
  created_at: string
}

export interface TunnelAccessInput {
  mode: TunnelAccessMode
  enabled: boolean
  config: {
    // basic
    username?: string
    password?: string // düz metin; sunucu hash'ler. Boş bırakılırsa mevcut korunur.
    // oauth
    providers?: string[]
    allowed_emails?: string[]
  }
}

// FAZ 4 / F21 — yük dengeleme + sağlık kontrolü.
export type LBStrategy = 'round_robin' | 'weighted' | 'least_connections' | 'latency'
export interface TunnelLBConfig {
  tunnel_id?: string
  strategy: LBStrategy
  weights: Record<string, number>
  health_enabled: boolean
  health_path: string
  interval_sec: number
  timeout_sec: number
  unhealthy_threshold: number
  healthy_threshold: number
}
export interface LBBackendStatus {
  client_id: string
  healthy: boolean
  checked: boolean
  latency_ms?: number
  in_flight: number
  last_check?: string
  last_error?: string
}
// FAZ 4 / F24 — UDP ileri + oyun sunucusu durumu.
export interface TunnelUDPConfig {
  tunnel_id?: string
  idle_timeout_sec: number
  max_packet_bytes: number
  max_pps: number
  max_flow_pps: number
  max_flows: number
}
export interface UDPLiveStats {
  port: number
  since: string
  active_flows: number
  pps_in: number
  pps_out: number
  packets_in: number
  packets_out: number
  bytes_in: number
  bytes_out: number
  flows_total: number
  dropped_rate: number
  dropped_size: number
  dropped_flows: number
}
export interface UDPStatMinute {
  minute: string
  packets_in: number
  packets_out: number
  bytes_in: number
  bytes_out: number
  flows_new: number
  flows_peak: number
  dropped_rate: number
  dropped_size: number
  dropped_flows: number
}
export interface TunnelUDPResp {
  config: TunnelUDPConfig
  defaults: TunnelUDPConfig
  live: UDPLiveStats | null
  series: UDPStatMinute[]
}
export interface GameStatus {
  kind: 'minecraft_java' | 'minecraft_bedrock'
  online: boolean
  version?: string
  protocol?: number
  players_online: number
  players_max: number
  players?: string[]
  motd?: string
  game_mode?: string
  latency_ms: number
  error?: string
}

export interface TunnelLBResp {
  config: TunnelLBConfig
  candidates: string[]
  health: LBBackendStatus[] | null
}

export interface TunnelReplica {
  client_id: string
  name: string
  online: boolean
}

// FAZ 6 — trafik politikası (header/redirect kuralları).
export interface TrafficHeaderRules {
  set?: Record<string, string>
  remove?: string[]
}
export interface TrafficRedirect {
  match_prefix: string
  location: string
  status: number // 301|302|307|308
}
export interface TrafficConfig {
  request_headers?: TrafficHeaderRules
  response_headers?: TrafficHeaderRules
  redirects?: TrafficRedirect[]
}
export interface TrafficPolicy {
  tunnel_id: string
  enabled: boolean
  config: TrafficConfig
}

// FAZ 6.3 — per-tünel metrikler.
export interface MetricBucket {
  bucket: string
  count: number
  error_count: number
  avg_ms: number
  max_ms: number
  bytes_in: number
  bytes_out: number
}
export interface TunnelMetricsSummary {
  total_requests: number
  error_count: number
  error_rate_pct: number
  avg_ms: number
  max_ms: number
}
export interface TunnelMetricsResp {
  window: string
  bucket_sec: number
  buckets: MetricBucket[]
  summary: TunnelMetricsSummary
}

// FAZ 6.6 — mTLS / istemci sertifikası.
export interface TunnelMTLS {
  tunnel_id: string
  enabled: boolean
  ca_pem: string
  has_ca: boolean
}

// FAZ 6.5 — yol tabanlı yönlendirme.
export interface PathRoute {
  id: string
  fqdn: string
  path_prefix: string
  tunnel_id: string
  created_at: string
}

// FAZ 6.4 — metrik uyarısı.
export interface TunnelAlert {
  tunnel_id: string
  enabled: boolean
  error_rate_pct: number
  window_min: number
  min_requests: number
  notify_email: string
  state: string // ok | firing
  last_changed_at?: string | null
}

export interface AbuseReport {
  id: string
  fqdn: string
  reason: string
  reporter_ip?: string
  handled: boolean
  created_at: string
}

export interface RequestLog {
  id: string                 // "req_00f1"
  tunnel_id: string
  tenant_id?: string
  hostname?: string          // isteğin geldiği alan adı
  client_ip?: string         // isteği yapan uzak IP
  ts: string
  method: string
  path: string
  status: number
  duration_ms: number
  bytes_in: number
  bytes_out: number
  /** Ingress istegi proxy'lemeden reddettiyse nedeni (ip_forbidden, policy_deny ...). Bos = proxy'lendi. */
  reject_reason?: string
}

/** Loglar sayfası gelişmiş filtre ölçütleri (sunucuya query param olarak gider). */
export interface LogFilter {
  method?: string
  status?: string            // "2xx" | "4xx" | "404" ...
  hostname?: string
  tunnel_id?: string
  q?: string                 // path içinde arama
  min_dur?: number
  max_dur?: number
  rejected?: string          // "true" => yalnizca ingress'in reddettigi istekler
  reason?: string            // belirli reject_reason
  since?: string             // RFC3339
  until?: string             // RFC3339
  limit?: number
  offset?: number
}

/** api_contract.md §1 "Hata formatı" */
export interface ApiError {
  error: {
    code: string
    message: string
  }
}

/** POST /clients yanıtı — token yalnızca burada, bir kez döner */
export interface CreateClientResponse {
  client: Client
  token: string
}

/** SSE olay tipleri — api_contract.md §1 */
export type EventType =
  | 'client.connected'
  | 'client.disconnected'
  | 'tunnel.created'
  | 'tunnel.updated'
  | 'tunnel.deleted'
  | 'request.completed'

export interface MailInfo {
  enabled: boolean
  address: string
  domain?: string
  unseen: number
  /** Harici (mail.<domain> dışı) adrese gönderebilir mi — açık sürümde her zaman true. */
  can_send_external?: boolean
  /** Site sistem posta kutuları (info@, sales@ …) — yalnızca owner/platform admin. */
  system_addresses?: string[]
  /** Mail istemcisi (IMAP/SMTP) erişimi: sunucu ayarları; kapalıysa enabled=false. */
  client_access?: MailClientAccess
}

export interface MailClientAccess {
  enabled: boolean
  host?: string
  imap_port?: number
  /** 587 STARTTLS (0 = kapalı). */
  submission_port?: number
  /** 465 SSL/TLS (0 = kapalı). */
  submissions_port?: number
}

export interface MailAppPassword {
  id: string
  mailbox: string
  label: string
  created_at: string
  last_used_at: string | null
  last_used_ip: string
  revoked_at: string | null
}

/** Oluşturma yanıtı: parola YALNIZCA burada bir kez döner. */
export interface MailAppPasswordCreated {
  id: string
  mailbox: string
  label: string
  password: string
  created_at: string
}

export interface MailAttachment {
  id: string
  message_id: string
  filename: string
  content_type: string
  size_bytes: number
  created_at: string
}

export interface MailMessage {
  id: string
  tenant_id: string
  direction: 'inbound' | 'outbound' | string
  from: string
  to: string
  subject: string
  text_body: string
  html_body?: string
  message_id?: string
  in_reply_to?: string
  seen: boolean
  received_at: string
  /** Yalnızca tekil mesaj (getMail) yanıtında dolu. */
  attachments?: MailAttachment[]
}

export interface TeamMember {
  id: string
  user_id: string
  email: string
  name: string
  role: 'owner' | 'admin' | 'member' | string
  created_at: string
  tokens_count: number
  /** "active" (kabul edilmiş üyelik) veya "pending" (bekleyen davet). */
  status?: 'active' | 'pending' | string
  /** Yalnızca davet yanıtında: davet e-postası teslim edildi mi. */
  email_sent?: boolean
}

/** Ekip daveti: davetli e-postadaki linkten kendi hesabıyla kabul eder. */
export interface TeamInvitation {
  id: string
  organization_id: string
  organization_name: string
  email: string
  role: 'admin' | 'member' | string
  status: 'pending' | 'accepted' | 'rejected' | 'canceled' | string
  inviter_name?: string
  expires_at: string
}

export interface TeamOverview {
  members: TeamMember[]
  count: number
  max_members: number | null
  can_add: boolean
  plan: string
}

export interface CreateMemberTokenResponse {
  client: Client
  token: string
}

export interface APIToken {
  id: string
  tenant_id: string
  user_id?: string | null
  name: string
  token_id: string
  token_prefix: string
  scopes: string[]
  last_used_at?: string | null
  expires_at?: string | null
  created_at: string
}

export interface CreateAPITokenResponse {
  token: string
  api_token: APIToken
}

export interface RotateAPITokenResponse {
  token: string
  api_token: APIToken
}

export interface IPAllowlistRule {
  id: string
  tenant_id: string
  tunnel_id?: string | null
  cidr: string
  description: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface AccessEvent {
  id: string
  tunnel_id: string
  hostname: string
  method: 'basic' | 'oauth' | string
  provider: string
  identity: string
  success: boolean
  reason: string
  client_ip: string
  user_agent: string
  created_at: string
}
export interface AccessEventSummary {
  window: string
  since: string
  total: number
  success: number
  failure: number
  by_method: Record<string, number>
  by_provider: Record<string, number>
  by_reason: Record<string, number>
  unique_identities: number
  unique_ips: number
  door_grants: number
  door_blocked: number
  door_blocked_ips: number
}
export interface AccessEventsPage {
  items: AccessEvent[]
  nextCursor: string
  hasMore: boolean
}
