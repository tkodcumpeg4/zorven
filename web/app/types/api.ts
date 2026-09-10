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

export interface Client {
  id: string                 // "cli_a1b2c3"
  name: string               // "ev-pc"
  status: ClientStatus
  version?: string           // yalnızca bağlıyken
  remote_addr?: string
  created_at: string         // RFC 3339 UTC
  last_seen_at?: string
  is_service?: boolean
  metrics?: Metrics
}

export interface Tunnel {
  id: string                 // "tun_x9y8z7"
  client_id: string
  target: string             // "http://localhost:8000"
  enabled: boolean
  created_at: string
  /** Tünelin yayınlandığı adlar. Bir tünelin BİRDEN ÇOK adı olabilir. */
  hostnames?: Hostname[]
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
  instructions: VerificationInstructions
}

export type VerifyHostnameResponse = Hostname

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
  /** Harici (mail.<domain> dışı) adrese gönderebilir mi — yalnızca Enterprise. */
  can_send_external?: boolean
  /** Site sistem posta kutuları (info@, sales@ …) — yalnızca owner/platform admin. */
  system_addresses?: string[]
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
