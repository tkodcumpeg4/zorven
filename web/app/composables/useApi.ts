import type {
  AccessEvent, AccessEventSummary, AccessEventsPage,
  Client, Tunnel, Hostname, RequestLog, LogFilter, CreateClientResponse,
  CreateCustomHostnameResponse, VerifyHostnameResponse, DNSCheckResult,
  Subscription, Plan, SubscriptionResponse, PlansResponse,
  AdminGlobalStats, TenantWithCounts, ClientWithTenant, HostnameWithTenant,
  TeamMember, TeamOverview, CreateMemberTokenResponse,
  APIToken, CreateAPITokenResponse, RotateAPITokenResponse, IPAllowlistRule,
  MailInfo, MailMessage, MailAppPassword, MailAppPasswordCreated,
  TunnelAccess, TunnelAccessInput, TunnelDoor, DoorGrant,
  RequestDetail, ReplayResult, ReplayOverrides, Device,
  AbuseReport,
  TunnelReplica, TunnelLBConfig, TunnelLBResp, TunnelUDPConfig, TunnelUDPResp, GameStatus,
  TrafficPolicy, TrafficConfig,
  TunnelMetricsResp,
  TunnelAlert,
  PathRoute,
  TunnelMTLS,
  Project,
  Secret, Policy, PolicyConfig,
  TeamInvitation,
} from '~/types/api'

/**
 * API katmani.
 *
 * USE_MOCK=false: gercek sunucuya baglanir. Endpoint yollari api_contract.md §1
 * ile birebir ayni oldugu icin mock'tan gecis baska degisiklik gerektirmedi.
 *
 * Mock kod bilerek DURUYOR: sunucu ayakta olmadan arayuz uzerinde calisabilmek
 * icin `?mock=1` sorgusuyla veya asagidaki sabiti degistirerek geri acilabilir.
 */
const USE_MOCK = false
const BASE = '/api/v1'

// --- Mock veri seti -------------------------------------------------------

const now = Date.now()
const iso = (msAgo: number) => new Date(now - msAgo).toISOString()

const mockClients: Client[] = [
  { id: 'cli_a1b2c3', name: 'ev-pc',    status: 'online',  version: '0.1.0',
    remote_addr: '203.0.113.45', created_at: iso(86_400_000 * 12), last_seen_at: iso(2_000) },
  { id: 'cli_d4e5f6', name: 'ofis-mac', status: 'online',  version: '0.1.0',
    remote_addr: '198.51.100.22', created_at: iso(86_400_000 * 5), last_seen_at: iso(9_000) },
  { id: 'cli_g7h8i9', name: 'pi-1',     status: 'offline',
    remote_addr: '203.0.113.91', created_at: iso(86_400_000 * 30), last_seen_at: iso(3_600_000 * 6) },
]

const mockHost = (id: string, tunnelID: string, fqdn: string,
  type: Hostname['type'] = 'global'): Hostname =>
  ({ id, tunnel_id: tunnelID, fqdn, type, created_at: iso(86_400_000) })

const mockTunnels: Tunnel[] = [
  { id: 'tun_x9y8z7', client_id: 'cli_a1b2c3',
    target: 'http://localhost:8000', enabled: true,  created_at: iso(86_400_000 * 12),
    hostnames: [mockHost('hst_1', 'tun_x9y8z7', 'api.example.com')] },
  { id: 'tun_p1q2r3', client_id: 'cli_d4e5f6',
    target: 'http://localhost:3000', enabled: true,  created_at: iso(86_400_000 * 5),
    hostnames: [mockHost('hst_2', 'tun_p1q2r3', 'app.example.com')] },
  { id: 'tun_s4t5u6', client_id: 'cli_g7h8i9',
    target: 'http://localhost:8080', enabled: true,  created_at: iso(86_400_000 * 30),
    hostnames: [mockHost('hst_3', 'tun_s4t5u6', 'pi.example.com')] },
  { id: 'tun_v7w8x9', client_id: 'cli_a1b2c3',
    target: 'http://localhost:5173', enabled: false, created_at: iso(86_400_000 * 2),
    // Bir tunelin BIRDEN COK adi olabilir: kisa global ad + kapsamli ad.
    hostnames: [
      mockHost('hst_4', 'tun_v7w8x9', 'staging.example.com'),
      mockHost('hst_5', 'tun_v7w8x9', 'staging--demo.example.com', 'scoped'),
    ] },
]

const PATHS = ['/users/42', '/health', '/api/v1/orders', '/assets/app.js', '/webhooks/stripe', '/login']
const METHODS = ['GET', 'GET', 'GET', 'POST', 'PUT', 'DELETE']
const STATUSES = [200, 200, 200, 200, 201, 304, 404, 500]

let reqSeq = 0
function makeRequest(msAgo: number): RequestLog {
  const tunnel = mockTunnels[Math.floor(Math.random() * mockTunnels.length)]!
  const status = STATUSES[Math.floor(Math.random() * STATUSES.length)]!
  return {
    id: `req_${(++reqSeq).toString(16).padStart(4, '0')}`,
    tunnel_id: tunnel.id,
    ts: iso(msAgo),
    method: METHODS[Math.floor(Math.random() * METHODS.length)]!,
    path: PATHS[Math.floor(Math.random() * PATHS.length)]!,
    status,
    duration_ms: status >= 500 ? 1200 + Math.floor(Math.random() * 800)
                               : 8 + Math.floor(Math.random() * 180),
    bytes_in: Math.floor(Math.random() * 2048),
    bytes_out: 200 + Math.floor(Math.random() * 40_000),
  }
}

const mockRequests: RequestLog[] = Array.from({ length: 60 }, (_, i) => makeRequest(i * 4_000))

// Ağ gecikmesi taklidi — loading state'lerinin gerçekten test edilmesi için
const delay = (ms = 220) => new Promise(r => setTimeout(r, ms))

// --- Public API -----------------------------------------------------------

export function useApi() {
  const { key } = useAdminKey()
  const { adminTenant } = useAdminTenant()

  /** Kimlik dogrulamali istek. Tum gercek cagrilar bundan gecer. */
  function buildHeaders(opts: Record<string, unknown>): Record<string, string> {
    const headers: Record<string, string> = {
      ...(opts.headers as Record<string, string> | undefined),
    }
    if (key.value) {
      headers.Authorization = `Bearer ${key.value}`
      // Admin anahtari modunda "Yonetime Gec" ile secilen kiraci.
      if (adminTenant.value) headers['X-Tenant-ID'] = adminTenant.value
    }
    const { activeProject } = useActiveProject()
    if (activeProject.value) {
      headers['X-Zorven-Project'] = activeProject.value
    }
    return headers
  }

  function req<T>(path: string, opts: Record<string, unknown> = {}): Promise<T> {
    return $fetch(`${BASE}${path}`, {
      ...opts,
      headers: buildHeaders(opts),
    }) as Promise<T>
  }

  /** req ile ayni, ama yanit basliklarina (sayfalama) da erisim verir. */
  async function reqWithHeaders<T>(path: string, opts: Record<string, unknown> = {}): Promise<{ data: T, headers: Headers }> {
    const res = await $fetch.raw(`${BASE}${path}`, {
      ...opts,
      headers: buildHeaders(opts),
    })
    return { data: res._data as T, headers: res.headers }
  }

  // --- Projeler (FAZ 0 / F00) ---
  async function listProjects(): Promise<Project[]> {
    if (USE_MOCK) {
      await delay()
      return [{ id: 'prj_default', tenant_id: 'ten_default', name: 'Default', slug: 'default', created_at: new Date().toISOString() }]
    }
    const res = await req<Project[]>('/projects')
    return res ?? []
  }

  async function createProject(payload: { name: string; slug: string }): Promise<Project> {
    if (USE_MOCK) {
      await delay()
      return { id: `prj_${Math.random().toString(36).slice(2, 8)}`, tenant_id: 'ten_default', name: payload.name, slug: payload.slug, created_at: new Date().toISOString() }
    }
    return req<Project>('/projects', { method: 'POST', body: payload })
  }

  async function deleteProject(id: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/projects/${id}`, { method: 'DELETE' })
  }

  // Yalnızca görünen ad değişir; slug sabittir (aktif proje seçimi slug'a bakar).
  async function renameProject(id: string, name: string): Promise<Project> {
    return req<Project>(`/projects/${id}`, { method: 'PATCH', body: { name } })
  }

  // Aktif organizasyonu ve TÜM verisini siler. Yalnızca oturum açmış owner/admin;
  // onay için organizasyonun kısa adı (slug) birebir gönderilmeli.
  async function deleteOrganization(confirmSlug: string): Promise<void> {
    await req('/organization', { method: 'DELETE', body: { confirm_slug: confirmSlug } })
  }

  async function listClients(): Promise<Client[]> {
    if (USE_MOCK) { await delay(); return structuredClone(mockClients) }
    const res = await req<Client[]>(`/clients`)
    return res ?? []
  }

  async function createClient(name: string): Promise<CreateClientResponse> {
    if (USE_MOCK) {
      await delay(400)
      const client: Client = {
        id: `cli_${Math.random().toString(36).slice(2, 8)}`,
        name, status: 'offline', created_at: new Date().toISOString(),
      }
      mockClients.push(client)
      return { client, token: `rpsh_live_${Math.random().toString(36).slice(2).padEnd(24, '0')}` }
    }
    return req<CreateClientResponse>(`/clients`, { method: 'POST', body: { name } })
  }

  async function deleteClient(id: string): Promise<void> {
    if (USE_MOCK) {
      await delay()
      const i = mockClients.findIndex(c => c.id === id)
      if (i >= 0) mockClients.splice(i, 1)
      return
    }
    await req(`/clients/${id}`, { method: 'DELETE' })
  }

  async function listTunnels(): Promise<Tunnel[]> {
    if (USE_MOCK) { await delay(); return structuredClone(mockTunnels) }
    const res = await req<Tunnel[]>(`/tunnels`)
    return res ?? []
  }

  /**
   * Tünel oluşturur. `name` TAM hostname değil, tek bir DNS etiketidir
   * (ör. "api"); sunucu bunu platform domainiyle birleştirip kiracı kapsamlı
   * adı otomatik verir.
   */
  async function createTunnel(input: { name: string, client_id: string, target: string, hostname_id?: string, no_domain?: boolean }): Promise<Tunnel> {
    if (USE_MOCK) {
      await delay(400)
      const id = `tun_${Math.random().toString(36).slice(2, 8)}`
      const tunnel: Tunnel = {
        id, client_id: input.client_id, target: input.target,
        enabled: true, created_at: new Date().toISOString(),
        hostnames: [mockHost(`hst_${id}`, id, `${input.name}--demo.example.com`, 'scoped')],
      }
      mockTunnels.push(tunnel)
      return tunnel
    }
    return req<Tunnel>(`/tunnels`, { method: 'POST', body: input })
  }

  async function updateTunnel(id: string, patch: Partial<Pick<Tunnel, 'target' | 'enabled' | 'proto' | 'exposure' | 'client_id'>>): Promise<Tunnel> {
    if (USE_MOCK) {
      await delay()
      const t = mockTunnels.find(x => x.id === id)
      if (!t) throw new Error('tunnel_not_found')
      Object.assign(t, patch)
      return structuredClone(t)
    }
    return req<Tunnel>(`/tunnels/${id}`, { method: 'PATCH', body: patch })
  }

  async function deleteTunnel(id: string): Promise<void> {
    if (USE_MOCK) {
      await delay()
      const i = mockTunnels.findIndex(t => t.id === id)
      if (i >= 0) mockTunnels.splice(i, 1)
      return
    }
    await req(`/tunnels/${id}`, { method: 'DELETE' })
  }

  // --- Tünel erişim denetimi (Basic Auth / OAuth) ---

  async function getTunnelAccess(id: string): Promise<TunnelAccess> {
    if (USE_MOCK) { await delay(); return { tunnel_id: id, mode: 'none', enabled: false, config: {} } }
    return req<TunnelAccess>(`/tunnels/${id}/access`)
  }

  async function setTunnelAccess(id: string, payload: TunnelAccessInput): Promise<TunnelAccess> {
    if (USE_MOCK) { await delay(); return { tunnel_id: id, mode: payload.mode, enabled: payload.enabled, config: {} } }
    return req<TunnelAccess>(`/tunnels/${id}/access`, { method: 'PUT', body: payload })
  }

  // --- Web ile kapı açma (ham TCP/UDP) ---

  async function getTunnelDoor(id: string): Promise<TunnelDoor> {
    if (USE_MOCK) {
      await delay()
      return { tunnel_id: id, enabled: false, duration_sec: 43200, durations: [3600, 43200, 86400, 604800], available: true, host: '', url: '', connect_address: '', proto: 'tcp', exposure: 'port', access_mode: 'none', access_ready: false }
    }
    return req<TunnelDoor>(`/tunnels/${id}/door`)
  }

  async function setTunnelDoor(id: string, payload: { enabled: boolean, duration_sec: number }): Promise<TunnelDoor> {
    if (USE_MOCK) { await delay(); return { ...(await getTunnelDoor(id)), ...payload } }
    return req<TunnelDoor>(`/tunnels/${id}/door`, { method: 'PUT', body: payload })
  }

  async function listDoorGrants(id: string): Promise<DoorGrant[]> {
    if (USE_MOCK) { await delay(); return [] }
    return (await req<DoorGrant[]>(`/tunnels/${id}/door/grants`)) ?? []
  }

  async function revokeDoorGrant(id: string, grantId: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/door/grants/${grantId}`, { method: 'DELETE' })
  }

  // --- Tünel replikaları (FAZ 5 / HA) ---

  async function getTunnelReplicas(id: string): Promise<TunnelReplica[]> {
    if (USE_MOCK) { await delay(); return [] }
    const r = await req<{ replicas: TunnelReplica[], count: number }>(`/tunnels/${id}/replicas`)
    return r?.replicas ?? []
  }

  async function addTunnelReplica(id: string, clientID: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/replicas`, { method: 'POST', body: { client_id: clientID } })
  }

  async function getTunnelLB(id: string): Promise<TunnelLBResp> {
    if (USE_MOCK) {
      await delay()
      return {
        config: { tunnel_id: id, strategy: 'round_robin', weights: {}, health_enabled: false, health_path: '/', interval_sec: 10, timeout_sec: 3, unhealthy_threshold: 3, healthy_threshold: 2 },
        candidates: [],
        health: [],
      }
    }
    return req<TunnelLBResp>(`/tunnels/${id}/lb`)
  }

  async function setTunnelLB(id: string, payload: TunnelLBConfig): Promise<TunnelLBConfig> {
    if (USE_MOCK) { await delay(); return payload }
    return req<TunnelLBConfig>(`/tunnels/${id}/lb`, { method: 'PUT', body: payload })
  }

  async function getTunnelUDP(id: string): Promise<TunnelUDPResp> {
    if (USE_MOCK) {
      await delay()
      const d = { tunnel_id: id, idle_timeout_sec: 90, max_packet_bytes: 65507, max_pps: 0, max_flow_pps: 0, max_flows: 1024 }
      return { config: d, defaults: d, live: null, series: [] }
    }
    return req<TunnelUDPResp>(`/tunnels/${id}/udp`)
  }

  async function setTunnelUDP(id: string, payload: TunnelUDPConfig): Promise<TunnelUDPConfig> {
    if (USE_MOCK) { await delay(); return payload }
    return req<TunnelUDPConfig>(`/tunnels/${id}/udp`, { method: 'PUT', body: payload })
  }

  async function getGameStatus(id: string): Promise<GameStatus> {
    if (USE_MOCK) { await delay(); return { kind: 'minecraft_java', online: false, players_online: 0, players_max: 0, latency_ms: 0, error: 'mock' } }
    return req<GameStatus>(`/tunnels/${id}/game-status`)
  }

  async function removeTunnelReplica(id: string, clientID: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/replicas/${clientID}`, { method: 'DELETE' })
  }

  // --- Trafik politikası (FAZ 6) ---

  async function getTunnelTraffic(id: string): Promise<TrafficPolicy> {
    if (USE_MOCK) { await delay(); return { tunnel_id: id, enabled: false, config: {} } }
    return req<TrafficPolicy>(`/tunnels/${id}/traffic`)
  }

  async function setTunnelTraffic(id: string, payload: { enabled: boolean, config: TrafficConfig }): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/traffic`, { method: 'PUT', body: payload })
  }

  // --- Per-tünel metrikler (FAZ 6.3) ---

  async function getTunnelMetrics(id: string, window: string): Promise<TunnelMetricsResp> {
    if (USE_MOCK) {
      await delay()
      return { window, bucket_sec: 60, buckets: [], summary: { total_requests: 0, error_count: 0, error_rate_pct: 0, avg_ms: 0, max_ms: 0 } }
    }
    return req<TunnelMetricsResp>(`/tunnels/${id}/metrics?window=${encodeURIComponent(window)}`)
  }

  // --- Erisim istatistikleri (Basic/OAuth giris olaylari) ---

  async function getAccessEventsSummary(id: string, window: string): Promise<AccessEventSummary> {
    if (USE_MOCK) {
      await delay()
      return { window, since: new Date().toISOString(), total: 0, success: 0, failure: 0, by_method: {}, by_provider: {}, by_reason: {}, unique_identities: 0, unique_ips: 0, door_grants: 0, door_blocked: 0, door_blocked_ips: 0 }
    }
    return req<AccessEventSummary>(`/tunnels/${id}/access-events/summary?window=${encodeURIComponent(window)}`)
  }

  async function listAccessEvents(id: string, limit = 25, cursor = ''): Promise<AccessEventsPage> {
    if (USE_MOCK) { await delay(); return { items: [], nextCursor: '', hasMore: false } }
    const q = new URLSearchParams({ limit: String(limit) })
    if (cursor) q.set('cursor', cursor)
    const { data, headers } = await reqWithHeaders<AccessEvent[]>(`/tunnels/${id}/access-events?${q.toString()}`)
    return {
      items: data ?? [],
      nextCursor: headers.get('X-Next-Cursor') ?? '',
      hasMore: headers.get('X-Has-More') === 'true',
    }
  }

  // --- Metrik uyarıları (FAZ 6.4) ---

  async function getTunnelAlert(id: string): Promise<TunnelAlert> {
    if (USE_MOCK) { await delay(); return { tunnel_id: id, enabled: false, error_rate_pct: 10, window_min: 5, min_requests: 20, notify_email: '', state: 'ok' } }
    return req<TunnelAlert>(`/tunnels/${id}/alert`)
  }

  async function setTunnelAlert(id: string, payload: { enabled: boolean, error_rate_pct: number, window_min: number, min_requests: number, notify_email: string }): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/alert`, { method: 'PUT', body: payload })
  }

  // --- Yol tabanlı yönlendirme (FAZ 6.5) ---

  async function listPathRoutes(hostnameID: string): Promise<PathRoute[]> {
    if (USE_MOCK) { await delay(); return [] }
    const r = await req<{ fqdn: string, routes: PathRoute[] }>(`/hostnames/${hostnameID}/paths`)
    return r?.routes ?? []
  }

  async function addPathRoute(hostnameID: string, body: { path_prefix: string, tunnel_id: string }): Promise<PathRoute> {
    return req<PathRoute>(`/hostnames/${hostnameID}/paths`, { method: 'POST', body })
  }

  async function deletePathRoute(hostnameID: string, routeID: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/hostnames/${hostnameID}/paths/${routeID}`, { method: 'DELETE' })
  }

  // --- mTLS / istemci sertifikası (FAZ 6.6) ---

  async function getTunnelMTLS(id: string): Promise<TunnelMTLS> {
    if (USE_MOCK) { await delay(); return { tunnel_id: id, enabled: false, ca_pem: '', has_ca: false } }
    return req<TunnelMTLS>(`/tunnels/${id}/mtls`)
  }

  async function setTunnelMTLS(id: string, payload: { enabled: boolean, ca_pem: string }): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/tunnels/${id}/mtls`, { method: 'PUT', body: payload })
  }


  // --- Denetim günlüğü (FAZ 6.8) ---

  // --- Hostnames ---

  async function listHostnames(): Promise<Hostname[]> {
    if (USE_MOCK) {
      await delay()
      return structuredClone(mockTunnels.flatMap(t => t.hostnames ?? []))
    }
    const res = await req<Hostname[]>(`/hostnames`)
    return res ?? []
  }

  /**
   * `name` tek bir DNS etiketidir; sunucu platform domainiyle birleştirir. tunnelID opsiyoneldir.
   * kind: 'scoped' (varsayılan) -> ad--kiracı.platform; 'short' -> ad.platform.
   */
  async function createHostname(name: string, tunnelID?: string, kind: 'scoped' | 'short' = 'scoped'): Promise<Hostname> {
    if (USE_MOCK) {
      await delay(400)
      const id = `hst_${Math.random().toString(36).slice(2, 8)}`
      const h = mockHost(id, tunnelID ?? '', kind === 'short' ? `${name}.example.com` : `${name}--demo.example.com`)
      if (tunnelID) {
        const t = mockTunnels.find(x => x.id === tunnelID)
        if (t) t.hostnames = [...(t.hostnames ?? []), h]
      }
      return h
    }
    return req<Hostname>(`/hostnames`, { method: 'POST', body: { name, kind, tunnel_id: tunnelID || undefined } })
  }

  async function createCustomHostname(fqdn: string, tunnelID?: string): Promise<CreateCustomHostnameResponse> {
    if (USE_MOCK) {
      await delay(400)
      const h: Hostname = {
        id: `hst_${Math.random().toString(36).slice(2, 8)}`,
        tunnel_id: tunnelID || null,
        fqdn,
        type: 'custom',
        verified: false,
        verify_token: 'mock-verify-token-123',
        instructions: {
          cname_record: fqdn,
          cname_target: 'cname.zorven.app',
          txt_record: `_zorven-challenge.${fqdn}`,
          txt_value: 'mock-verify-token-123',
        },
        created_at: new Date().toISOString(),
      }
      if (tunnelID) {
        const t = mockTunnels.find(x => x.id === tunnelID)
        if (t) t.hostnames = [...(t.hostnames ?? []), h]
      }
      return h as CreateCustomHostnameResponse
    }
    return req<CreateCustomHostnameResponse>(`/hostnames/custom`, {
      method: 'POST',
      body: { fqdn, tunnel_id: tunnelID || undefined },
    })
  }

  async function updateHostname(id: string, tunnelID: string | null): Promise<Hostname> {
    if (USE_MOCK) {
      await delay(300)
      for (const t of mockTunnels) {
        const h = (t.hostnames ?? []).find(x => x.id === id)
        if (h) {
          h.tunnel_id = tunnelID
          return h
        }
      }
      throw new Error('hostname_not_found')
    }
    return req<Hostname>(`/hostnames/${id}`, {
      method: 'PATCH',
      body: { tunnel_id: tunnelID },
    })
  }

  async function dnsCheckHostname(id: string): Promise<DNSCheckResult> {
    if (USE_MOCK) { await delay(300); return { fqdn: '', pointed: false, cname_target: 'cname.zorven.app' } }
    return req<DNSCheckResult>(`/hostnames/${id}/dns-check`, { method: 'POST' })
  }

  async function verifyHostname(id: string): Promise<VerifyHostnameResponse> {
    if (USE_MOCK) {
      await delay(500)
      for (const t of mockTunnels) {
        const h = (t.hostnames ?? []).find(x => x.id === id)
        if (h) {
          h.verified = true
          return h
        }
      }
      throw new Error('hostname_not_found')
    }
    return req<VerifyHostnameResponse>(`/hostnames/${id}/verify`, { method: 'POST' })
  }

  async function deleteHostname(id: string): Promise<void> {
    if (USE_MOCK) {
      await delay()
      for (const t of mockTunnels) {
        t.hostnames = (t.hostnames ?? []).filter(h => h.id !== id)
      }
      return
    }
    await req(`/hostnames/${id}`, { method: 'DELETE' })
  }

  async function listRequests(filter: LogFilter | number = 200): Promise<RequestLog[]> {
    // Geriye dönük uyumluluk: sayı verilirse limit olarak kabul et.
    const f: LogFilter = typeof filter === 'number' ? { limit: filter } : filter
    if (USE_MOCK) {
      await delay()
      let rows = structuredClone(mockRequests)
      if (f.status && /^\dxx$/.test(f.status)) { const d = +f.status[0]!; rows = rows.filter(r => r.status >= d * 100 && r.status < d * 100 + 100) }
      else if (f.status) rows = rows.filter(r => r.status === +f.status!)
      if (f.method) rows = rows.filter(r => r.method === f.method)
      if (f.q) rows = rows.filter(r => r.path.includes(f.q!))
      if (f.tunnel_id) rows = rows.filter(r => r.tunnel_id === f.tunnel_id)
      if (f.min_dur) rows = rows.filter(r => r.duration_ms >= f.min_dur!)
      return rows.slice(0, f.limit ?? 200)
    }
    const query: Record<string, string | number> = {}
    for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== '' && v !== null) query[k] = v as string | number
    const res = await req<RequestLog[]>(`/requests`, { query })
    return res ?? []
  }

  // --- FAZ 2: İstek inspector (opt-in yakalama + detay + replay) ---

  async function getCaptureEnabled(): Promise<boolean> {
    if (USE_MOCK) { await delay(); return false }
    const r = await req<{ enabled: boolean }>(`/requests/capture`)
    return !!r?.enabled
  }
  async function setCaptureEnabled(enabled: boolean): Promise<boolean> {
    if (USE_MOCK) { await delay(); return enabled }
    const r = await req<{ enabled: boolean }>(`/requests/capture`, { method: 'POST', body: { enabled } })
    return !!r?.enabled
  }
  async function getRequestDetail(id: string): Promise<RequestDetail> {
    return req<RequestDetail>(`/requests/${id}`)
  }
  // --- FAZ 3 / F14: Cihazlar ---
  async function listDevices(): Promise<Device[]> {
    return req<Device[]>('/devices')
  }
  async function getDevice(id: string): Promise<Device> {
    return req<Device>(`/devices/${id}`)
  }

  // --- FAZ 3 / F17: Zorven Network (özel kaynaklar) ---
  async function listNetworkResources(): Promise<Tunnel[]> {
    const r = await req<{ resources: Tunnel[] }>('/network/resources')
    return r.resources || []
  }
  async function createNetworkResource(p: { name: string, client_id: string, target?: string, subnet?: string }): Promise<Tunnel> {
    return req<Tunnel>('/network/resources', { method: 'POST', body: p })
  }

  async function replayRequest(id: string, overrides?: ReplayOverrides): Promise<ReplayResult> {
    // Düzenleme yoksa gövde HİÇ gönderilmez: sunucu tarafında gövdesiz çağrı
    // "orijinali aynen gönder" anlamına gelir.
    const body = overrides ? { overrides } : undefined
    return req<ReplayResult>(`/requests/${id}/replay`, { method: 'POST', body })
  }

  /** Masaüstü "tarayıcıdan giriş": kullanıcı kodunu onaylar, cihaza token bağlanır. */
  async function deviceApprove(userCode: string): Promise<{ ok: boolean; name?: string }> {
    if (USE_MOCK) { await delay(300); return { ok: true, name: 'masaustu' } }
    return req<{ ok: boolean; name?: string }>(`/device/approve`, { method: 'POST', body: { user_code: userCode } })
  }

  /**
   * Canlı istek akışı. Gerçekte GET /events (SSE) dinlenecek;
   * mock'ta periyodik olarak yeni kayıt üretiliyor.
   * Döndürülen fonksiyon aboneliği kapatır.
   */
  function streamRequests(onRequest: (r: RequestLog) => void): () => void {
    if (USE_MOCK) {
      const timer = setInterval(() => onRequest(makeRequest(0)), 2_600)
      return () => clearInterval(timer)
    }
    // EventSource OZEL BASLIK GONDEREMEZ; Authorization eklenemez.
    // Bu yuzden fetch + ReadableStream ile SSE'yi elle cozuyoruz.
    const ctrl = new AbortController()
    void (async () => {
      try {
        const res = await fetch(`${BASE}/events`, {
          headers: { Authorization: `Bearer ${key.value}` },
          signal: ctrl.signal,
        })
        if (!res.ok || !res.body) return

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buf = ''

        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })

          // SSE olaylari bos satirla ayrilir.
          let sep: number
          while ((sep = buf.indexOf('\n\n')) !== -1) {
            const block = buf.slice(0, sep)
            buf = buf.slice(sep + 2)

            let type = ''
            let data = ''
            for (const line of block.split('\n')) {
              if (line.startsWith('event: ')) type = line.slice(7).trim()
              else if (line.startsWith('data: ')) data += line.slice(6)
              // ": heartbeat" yorum satiri yok sayilir
            }
            if (type === 'request.completed' && data) {
              try { onRequest(JSON.parse(data) as RequestLog) } catch { /* bozuk kare */ }
            }
          }
        }
      } catch {
        // Iptal veya ag hatasi: sessizce biter, cagiran yeniden abone olabilir.
      }
    })()
    return () => ctrl.abort()
  }

  /**
   * Uzak terminal WSS adresini uretir. Token URL'ye KONMAZ (loglara sizar);
   * WSS el sikismasi ayni origin uzerinden middleware ile korunur — tarayici
   * zaten admin anahtarini Authorization basligiyla gonderemez WS'te, bu yuzden
   * sunucu terminal ucu icin cookie/oturum yerine ayni-origin + dev proxy'ye
   * guvenir. (R4'te WS icin kisa omurlu bir bilet mekanizmasi eklenmeli.)
   */
  function terminalWSURL(clientID: string): string {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${location.host}${BASE}/clients/${clientID}/terminal`
  }

  // --- Platform Admin (Organizasyondan Bagimsiz) ---

  async function adminGetStats(): Promise<AdminGlobalStats> {
    if (USE_MOCK) {
      await delay()
      return {
        tenants_count: 3,
        clients_count: 5,
        online_clients_count: 2,
        tunnels_count: 6,
        active_tunnels_count: 5,
        hostnames_count: 8,
        custom_domains_count: 2,
      }
    }
    return req<AdminGlobalStats>(`/admin/stats`)
  }

  async function adminListTenants(): Promise<TenantWithCounts[]> {
    if (USE_MOCK) {
      await delay()
      return [
        { id: 'ten_demo1', slug: 'demo-corp', created_at: new Date().toISOString(), clients_count: 2, tunnels_count: 3, hostnames_count: 4 },
        { id: 'ten_demo2', slug: 'acme-labs', created_at: new Date().toISOString(), clients_count: 1, tunnels_count: 2, hostnames_count: 2 },
      ]
    }
    return req<TenantWithCounts[]>(`/admin/tenants`)
  }

  async function adminListClients(): Promise<ClientWithTenant[]> {
    if (USE_MOCK) {
      await delay()
      return mockClients.map(c => ({ ...c, tenant_slug: 'demo-corp' }))
    }
    return req<ClientWithTenant[]>(`/admin/clients`)
  }

  async function adminListHostnames(): Promise<HostnameWithTenant[]> {
    if (USE_MOCK) {
      await delay()
      return mockTunnels.flatMap(t => (t.hostnames ?? []).map(h => ({
        ...h,
        tenant_slug: 'demo-corp',
        target: t.target,
      })))
    }
    return req<HostnameWithTenant[]>(`/admin/hostnames`)
  }

  async function adminListAbuseReports(): Promise<AbuseReport[]> {
    if (USE_MOCK) { await delay(); return [] }
    const r = await req<{ reports: AbuseReport[], count: number }>(`/admin/abuse-reports`)
    return r?.reports ?? []
  }

  async function adminFreeze(opts: { fqdn?: string, tunnel_id?: string, frozen: boolean }): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/admin/abuse/freeze`, { method: 'POST', body: opts })
  }

  async function adminSwitchTenant(tenantId: string): Promise<{ success: boolean, tenant: { id: string, slug: string } }> {
    if (USE_MOCK) {
      await delay()
      return { success: true, tenant: { id: tenantId, slug: tenantId } }
    }
    return req<{ success: boolean, tenant: { id: string, slug: string } }>(`/admin/switch-tenant`, {
      method: 'POST',
      body: { tenant_id: tenantId },
    })
  }

  // --- Abonelik ve Planlar ---

  async function getSubscription(): Promise<SubscriptionResponse> {
    if (USE_MOCK) {
      await delay()
      return {
        subscription: {
          tenant_id: 'ten_mock',
          plan: 'free',
          status: 'active',
          max_clients: 2,
          max_custom_domains: 0,
          max_tunnels: 2,
          bandwidth_limit_bytes: 5 * 1024 * 1024 * 1024,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        },
        usage: {
          clients_count: 1,
          custom_domains_count: 0,
          tunnels_count: 1,
          bandwidth_used_bytes: 120 * 1024 * 1024,
          active_screen_streams: 0,
          is_throttled: false,
        },
      }
    }
    return req<SubscriptionResponse>(`/subscription`)
  }

  async function listPlans(): Promise<Plan[]> {
    if (USE_MOCK) {
      await delay()
      return [
        {
          id: 'free',
          name: 'Free',
          price_monthly: 0,
          max_clients: 2,
          max_tunnels: 2,
          max_custom_domains: 0,
          bandwidth_limit_bytes: 5 * 1024 * 1024 * 1024,
          bandwidth_normal_mbps: 10,
          bandwidth_throttled_mbps: 1,
          max_screen_streams: 1,
          screen_max_fps: 30,
          log_retention_days: 1,
          max_members: null,
          has_api_access: false,
          has_ip_allowlist: false,
          extra_device_price: 0,
          extra_gb_price: 0,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        },
      ]
    }
    const res = await req<PlansResponse>(`/plans`)
    return res?.plans ?? []
  }

  // --- Team ek cihaz / ek trafik talepleri ---

  // --- Ekip ve Uye Yonetimi (Team Management) ---

  async function listTeamMembers(): Promise<TeamOverview> {
    if (USE_MOCK) {
      await delay()
      return {
        members: [
          { id: 'mem_1', user_id: 'usr_1', email: 'owner@example.com', name: 'Proje Sahibi', role: 'owner', created_at: iso(86_400_000 * 30), tokens_count: 2 },
          { id: 'mem_2', user_id: 'usr_2', email: 'dev@example.com', name: 'Kıdemli Geliştirici', role: 'member', created_at: iso(86_400_000 * 10), tokens_count: 1 },
        ],
        count: 2,
        max_members: 5,
        can_add: true,
        plan: 'team',
      }
    }
    return req<TeamOverview>('/team/members')
  }

  async function inviteTeamMember(email: string, name?: string, role: string = 'member'): Promise<TeamMember> {
    if (USE_MOCK) {
      await delay()
      return {
        id: 'mem_' + Date.now(),
        user_id: 'usr_' + Date.now(),
        email,
        name: name || email.split('@')[0] || '',
        role,
        created_at: new Date().toISOString(),
        tokens_count: 0,
      }
    }
    return req<TeamMember>('/team/members/invite', {
      method: 'POST',
      body: { email, name, role },
    })
  }

  async function updateMemberRole(memberId: string, role: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/team/members/${memberId}/role`, {
      method: 'PATCH',
      body: { role },
    })
  }

  async function removeTeamMember(memberId: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/team/members/${memberId}`, {
      method: 'DELETE',
    })
  }

  async function listMemberTokens(memberId: string): Promise<Client[]> {
    if (USE_MOCK) {
      await delay()
      return mockClients
    }
    return req<Client[]>(`/team/members/${memberId}/tokens`)
  }

  async function createMemberToken(memberId: string, name: string): Promise<CreateMemberTokenResponse> {
    if (USE_MOCK) {
      await delay()
      return {
        client: {
          id: 'cli_' + Date.now(),
          name,
          status: 'offline',
          created_at: new Date().toISOString(),
        },
        token: 'zrv_live_mock_' + Math.random().toString(36).slice(2),
      }
    }
    return req<CreateMemberTokenResponse>(`/team/members/${memberId}/tokens`, {
      method: 'POST',
      body: { name },
    })
  }

  async function revokeMemberToken(memberId: string, clientId: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/team/members/${memberId}/tokens/${clientId}`, {
      method: 'DELETE',
    })
  }

  // --- API Tokens & IP Allowlist ---

  async function listAPITokens(): Promise<{ tokens: APIToken[], count: number }> {
    if (USE_MOCK) {
      await delay()
      return { tokens: [], count: 0 }
    }
    return req<{ tokens: APIToken[], count: number }>('/api-tokens')
  }

  async function createAPIToken(name: string, scopes: string[] = ['*'], expiresInDays = 0): Promise<CreateAPITokenResponse> {
    if (USE_MOCK) {
      await delay()
      const now = new Date().toISOString()
      return {
        token: 'zrv_api_mock12345678_mocksecretkeyvalue',
        api_token: {
          id: 'tok_' + Date.now(),
          tenant_id: 'ten_mock',
          name,
          token_id: 'mock12345678',
          token_prefix: 'mock1234',
          scopes,
          created_at: now,
          expires_at: expiresInDays > 0 ? new Date(Date.now() + expiresInDays * 86400000).toISOString() : null,
        },
      }
    }
    return req<CreateAPITokenResponse>('/tokens', {
      method: 'POST',
      body: { name, scopes, expires_in_days: expiresInDays },
    })
  }

  async function rotateAPIToken(id: string): Promise<RotateAPITokenResponse> {
    if (USE_MOCK) {
      await delay()
      return {
        token: 'zrv_api_mockrot' + Date.now(),
        api_token: {
          id,
          tenant_id: 'ten_mock',
          name: 'Rotated Token',
          token_id: 'mockrot' + Date.now(),
          token_prefix: 'mockrot',
          scopes: ['*'],
          created_at: new Date().toISOString(),
        },
      }
    }
    return req<RotateAPITokenResponse>(`/tokens/${id}/rotate`, {
      method: 'POST',
    })
  }

  async function revokeAPIToken(id: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/tokens/${id}`, {
      method: 'DELETE',
    })
  }

  // --- Servis Hesapları (FAZ 0 / F0A) ---

  async function listIPRules(tunnelId?: string): Promise<{ rules: IPAllowlistRule[], count: number }> {
    if (USE_MOCK) {
      await delay()
      return { rules: [], count: 0 }
    }
    const q = tunnelId ? `?tunnel_id=${encodeURIComponent(tunnelId)}` : ''
    return req<{ rules: IPAllowlistRule[], count: number }>(`/ip-allowlist${q}`)
  }

  async function createIPRule(cidr: string, description = '', tunnelId?: string): Promise<IPAllowlistRule> {
    if (USE_MOCK) {
      await delay()
      const now = new Date().toISOString()
      return {
        id: 'ipr_' + Date.now(),
        tenant_id: 'ten_mock',
        tunnel_id: tunnelId || null,
        cidr,
        description,
        enabled: true,
        created_at: now,
        updated_at: now,
      }
    }
    return req<IPAllowlistRule>('/ip-allowlist', {
      method: 'POST',
      body: { cidr, description, tunnel_id: tunnelId || '' },
    })
  }

  async function updateIPRule(id: string, patch: { enabled?: boolean, description?: string }): Promise<IPAllowlistRule> {
    if (USE_MOCK) {
      await delay()
      const now = new Date().toISOString()
      return {
        id,
        tenant_id: 'ten_mock',
        cidr: '192.168.1.0/24',
        description: patch.description || '',
        enabled: patch.enabled ?? true,
        created_at: now,
        updated_at: now,
      }
    }
    return req<IPAllowlistRule>(`/ip-allowlist/${id}`, {
      method: 'PATCH',
      body: patch,
    })
  }

  async function deleteIPRule(id: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/ip-allowlist/${id}`, {
      method: 'DELETE',
    })
  }

  // --- Webmail ---
  async function mailInfo(): Promise<MailInfo> {
    return req<MailInfo>('/mail/info')
  }
  // box: inbox | sent | trash | drafts veya kullanıcı klasörü adı (IMAP ile oluşturulan).
  async function listMail(box: string = 'inbox'): Promise<MailMessage[]> {
    return req<MailMessage[]>(`/mail/messages?box=${encodeURIComponent(box)}`)
  }
  async function listMailFolders(): Promise<{ folders: string[] }> {
    return req<{ folders: string[] }>('/mail/folders')
  }
  async function moveMail(id: string, folder: string): Promise<{ success: boolean }> {
    return req<{ success: boolean }>(`/mail/messages/${id}/move`, { method: 'POST', body: { folder } })
  }
  async function getMail(id: string): Promise<MailMessage> {
    return req<MailMessage>(`/mail/messages/${id}`)
  }
  async function sendMail(payload: {
    to: string; subject: string; body: string; in_reply_to?: string; from?: string; template?: string
    attachments?: { filename: string; content_type: string; content_base64: string }[]
  }): Promise<MailMessage> {
    return req<MailMessage>('/mail/send', { method: 'POST', body: payload })
  }
  // Ek indirme URL'si (tarayıcı doğrudan açar; oturum çerezi taşınır).
  function mailAttachmentUrl(id: string): string {
    return `${BASE}/mail/attachments/${id}`
  }
  async function deleteMail(id: string): Promise<{ success: boolean }> {
    return req<{ success: boolean }>(`/mail/messages/${id}`, { method: 'DELETE' })
  }
  // Mail istemcileri (IMAP/SMTP) için uygulama parolaları.
  async function listMailAppPasswords(mailbox?: string): Promise<MailAppPassword[]> {
    const q = mailbox ? `?mailbox=${encodeURIComponent(mailbox)}` : ''
    return req<MailAppPassword[]>(`/mail/app-passwords${q}`)
  }
  async function createMailAppPassword(label: string, mailbox?: string): Promise<MailAppPasswordCreated> {
    return req<MailAppPasswordCreated>('/mail/app-passwords', { method: 'POST', body: { label, mailbox } })
  }
  async function revokeMailAppPassword(id: string): Promise<{ success: boolean }> {
    return req<{ success: boolean }>(`/mail/app-passwords/${id}`, { method: 'DELETE' })
  }
  // Apple Mail .mobileconfig profili (tarayıcı doğrudan indirir; parola içermez).
  function mailMobileConfigUrl(mailbox?: string): string {
    return `${BASE}/mail/mobileconfig${mailbox ? `?mailbox=${encodeURIComponent(mailbox)}` : ''}`
  }

  // --- Ekip davetleri (e-postadaki linkten kabul) ---
  async function resendInvitation(id: string): Promise<{ success: boolean, email_sent: boolean }> {
    return req<{ success: boolean, email_sent: boolean }>(`/team/members/${id}/resend`, { method: 'POST' })
  }
  async function listMyInvitations(): Promise<TeamInvitation[]> {
    const res = await req<{ invitations: TeamInvitation[] }>('/invitations')
    return res?.invitations ?? []
  }
  async function getInvitation(id: string): Promise<TeamInvitation> {
    return req<TeamInvitation>(`/invitations/${encodeURIComponent(id)}`)
  }
  async function acceptInvitation(id: string): Promise<TeamInvitation> {
    return req<TeamInvitation>(`/invitations/${encodeURIComponent(id)}/accept`, { method: 'POST' })
  }
  async function declineInvitation(id: string): Promise<void> {
    await req(`/invitations/${encodeURIComponent(id)}/decline`, { method: 'POST' })
  }

  // --- Secret Vault (FAZ 1 / F06) ---
  async function listSecrets(): Promise<{ secrets: Secret[], count: number }> {
    if (USE_MOCK) { await delay(); return { secrets: [], count: 0 } }
    return req<{ secrets: Secret[], count: number }>('/secrets')
  }
  async function createSecret(name: string, value: string): Promise<{ secret: Secret, value: string }> {
    if (USE_MOCK) {
      await delay()
      return { secret: { id: 'sec_' + Date.now(), tenant_id: 'ten_mock', project_id: 'prj_default', name, key_version: 1, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }, value }
    }
    return req<{ secret: Secret, value: string }>('/secrets', { method: 'POST', body: { name, value } })
  }
  async function deleteSecret(id: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/secrets/${id}`, { method: 'DELETE' })
  }

  // --- Birlesik Policy motoru (FAZ 1 / F04) ---
  async function listPolicies(): Promise<{ policies: Policy[], count: number }> {
    if (USE_MOCK) { await delay(); return { policies: [], count: 0 } }
    return req<{ policies: Policy[], count: number }>('/policies')
  }
  async function getPolicy(id: string): Promise<Policy> {
    return req<Policy>(`/policies/${id}`)
  }
  async function createPolicy(payload: { name: string, config: PolicyConfig, priority?: number }): Promise<Policy> {
    if (USE_MOCK) {
      await delay()
      return { id: 'pol_' + Date.now(), tenant_id: 'ten_mock', project_id: 'prj_default', name: payload.name, config: payload.config, enabled: true, priority: payload.priority ?? 100, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), bindings: [] }
    }
    return req<Policy>('/policies', { method: 'POST', body: payload })
  }
  async function updatePolicy(id: string, payload: { name: string, config: PolicyConfig, enabled: boolean, priority: number }): Promise<Policy> {
    return req<Policy>(`/policies/${id}`, { method: 'PUT', body: payload })
  }
  async function deletePolicy(id: string): Promise<void> {
    if (USE_MOCK) { await delay(); return }
    await req(`/policies/${id}`, { method: 'DELETE' })
  }
  async function bindPolicy(id: string, binding: { tunnel_id?: string, hostname?: string }): Promise<void> {
    await req(`/policies/${id}/bind`, { method: 'POST', body: binding })
  }
  async function unbindPolicy(id: string, binding: { tunnel_id?: string, hostname?: string }): Promise<void> {
    await req(`/policies/${id}/unbind`, { method: 'POST', body: binding })
  }

  return {
    terminalWSURL,
    listProjects, createProject, deleteProject, renameProject, deleteOrganization,
    listClients, createClient, deleteClient,
    listTunnels, createTunnel, updateTunnel, deleteTunnel, getTunnelAccess, setTunnelAccess,
    getTunnelDoor, setTunnelDoor, listDoorGrants, revokeDoorGrant,
    getTunnelReplicas, addTunnelReplica, removeTunnelReplica, getTunnelLB, setTunnelLB,
    getTunnelUDP, setTunnelUDP, getGameStatus,
    getTunnelTraffic, setTunnelTraffic,
    getTunnelMetrics, getAccessEventsSummary, listAccessEvents,
    getTunnelAlert, setTunnelAlert,
    listPathRoutes, addPathRoute, deletePathRoute,
    getTunnelMTLS, setTunnelMTLS,
    listHostnames, createHostname, createCustomHostname, updateHostname, verifyHostname, dnsCheckHostname, deleteHostname,
    listRequests, streamRequests, deviceApprove,
    getCaptureEnabled, setCaptureEnabled, getRequestDetail, replayRequest,
    listDevices, getDevice, listNetworkResources, createNetworkResource,
    adminGetStats, adminListTenants, adminListClients, adminListHostnames, adminSwitchTenant,
    adminListAbuseReports, adminFreeze,
    getSubscription, listPlans,
    listTeamMembers, inviteTeamMember, updateMemberRole, removeTeamMember,
    listMemberTokens, createMemberToken, revokeMemberToken,
    resendInvitation, listMyInvitations, getInvitation, acceptInvitation, declineInvitation,
    listAPITokens, createAPIToken, rotateAPIToken, revokeAPIToken,
    listIPRules, createIPRule, updateIPRule, deleteIPRule,
    mailInfo, listMail, listMailFolders, moveMail, getMail, sendMail, deleteMail, mailAttachmentUrl,
    listMailAppPasswords, createMailAppPassword, revokeMailAppPassword, mailMobileConfigUrl,
    listSecrets, createSecret, deleteSecret,
    listPolicies, getPolicy, createPolicy, updatePolicy, deletePolicy, bindPolicy, unbindPolicy,
  }
}

