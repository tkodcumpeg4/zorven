import type {
  Client, Tunnel, Hostname, RequestLog, LogFilter, CreateClientResponse,
  CreateCustomHostnameResponse, VerifyHostnameResponse,
  Subscription, Plan, SubscriptionResponse, PlansResponse,
  AdminGlobalStats, TenantWithCounts, ClientWithTenant, HostnameWithTenant,
  TeamMember, TeamOverview, CreateMemberTokenResponse,
  APIToken, CreateAPITokenResponse, IPAllowlistRule,
  MailInfo, MailMessage,
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

  /** Kimlik dogrulamali istek. Tum gercek cagrilar bundan gecer. */
  function req<T>(path: string, opts: Record<string, unknown> = {}): Promise<T> {
    const headers: Record<string, string> = {
      ...(opts.headers as Record<string, string> | undefined),
    }
    if (key.value) {
      headers.Authorization = `Bearer ${key.value}`
    }
    return $fetch(`${BASE}${path}`, {
      ...opts,
      headers,
    }) as Promise<T>
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
  async function createTunnel(input: { name: string, client_id: string, target: string, hostname_id?: string }): Promise<Tunnel> {
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

  async function updateTunnel(id: string, patch: Partial<Pick<Tunnel, 'target' | 'enabled'>>): Promise<Tunnel> {
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

  // --- Hostnames ---

  async function listHostnames(): Promise<Hostname[]> {
    if (USE_MOCK) {
      await delay()
      return structuredClone(mockTunnels.flatMap(t => t.hostnames ?? []))
    }
    const res = await req<Hostname[]>(`/hostnames`)
    return res ?? []
  }

  /** `name` tek bir DNS etiketidir; sunucu platform domainiyle birleştirir. tunnelID opsiyoneldir. */
  async function createHostname(name: string, tunnelID?: string): Promise<Hostname> {
    if (USE_MOCK) {
      await delay(400)
      const id = `hst_${Math.random().toString(36).slice(2, 8)}`
      const h = mockHost(id, tunnelID ?? '', `${name}.example.com`)
      if (tunnelID) {
        const t = mockTunnels.find(x => x.id === tunnelID)
        if (t) t.hostnames = [...(t.hostnames ?? []), h]
      }
      return h
    }
    return req<Hostname>(`/hostnames`, { method: 'POST', body: { name, tunnel_id: tunnelID || undefined } })
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

  async function adminUpdateTenantPlan(tenantId: string, plan: string, status = 'active'): Promise<Subscription> {
    if (USE_MOCK) {
      await delay()
      return {
        tenant_id: tenantId,
        plan,
        status,
        max_clients: 15,
        max_custom_domains: 5,
        max_tunnels: 20,
        bandwidth_limit_bytes: 100 * 1024 * 1024 * 1024,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
    }
    return req<Subscription>(`/admin/tenants/${tenantId}/plan`, {
      method: 'PUT',
      body: { plan, status },
    })
  }

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
        name: name || email.split('@')[0],
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
    return req<CreateAPITokenResponse>('/api-tokens', {
      method: 'POST',
      body: { name, scopes, expires_in_days: expiresInDays },
    })
  }

  async function revokeAPIToken(id: string): Promise<{ success: boolean }> {
    if (USE_MOCK) {
      await delay()
      return { success: true }
    }
    return req<{ success: boolean }>(`/api-tokens/${id}`, {
      method: 'DELETE',
    })
  }

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
  async function listMail(box: 'inbox' | 'sent' = 'inbox'): Promise<MailMessage[]> {
    return req<MailMessage[]>(`/mail/messages?box=${box}`)
  }
  async function getMail(id: string): Promise<MailMessage> {
    return req<MailMessage>(`/mail/messages/${id}`)
  }
  async function sendMail(payload: {
    to: string; subject: string; body: string; in_reply_to?: string; from?: string
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

  return {
    terminalWSURL,
    listClients, createClient, deleteClient,
    listTunnels, createTunnel, updateTunnel, deleteTunnel,
    listHostnames, createHostname, createCustomHostname, updateHostname, verifyHostname, deleteHostname,
    listRequests, streamRequests, deviceApprove,
    adminGetStats, adminListTenants, adminListClients, adminListHostnames, adminSwitchTenant,
    getSubscription, listPlans, adminUpdateTenantPlan,
    listTeamMembers, inviteTeamMember, updateMemberRole, removeTeamMember,
    listMemberTokens, createMemberToken, revokeMemberToken,
    listAPITokens, createAPIToken, revokeAPIToken,
    listIPRules, createIPRule, updateIPRule, deleteIPRule,
    mailInfo, listMail, getMail, sendMail, deleteMail, mailAttachmentUrl,
  }
}

