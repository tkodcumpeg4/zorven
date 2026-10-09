<script setup lang="ts">
import type { Tunnel, Client, Hostname, TunnelAccessMode, TunnelAccessInput, IPAllowlistRule, TunnelProto, TunnelExposure, TunnelReplica, TunnelMetricsResp, TunnelLBConfig, LBBackendStatus, LBStrategy, TunnelUDPConfig, UDPLiveStats, UDPStatMinute, GameStatus, TunnelDoor, DoorGrant, AccessEvent, AccessEventSummary } from '~/types/api'
// TrafficPolicy/TrafficConfig useApi üzerinden kullanılıyor; burada form state yerel tiptir.

const api = useApi()
const { t, te } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const { openUpgrade, loadBillingData } = useBilling()
const { githubEnabled, googleEnabled, platformDomain } = useAuth()
const { isPrivileged } = useRole()
const anyOAuthProvider = computed(() => githubEnabled.value || googleEnabled.value)
// Rezerve-port bağlantı adresi için platform apex (ör. app.zorven.app → zorven.app).
// Once sunucunun bildirdigi platform domaini; yoksa gecerli host (IP/localhost
// oldugu gibi), alan adiysa son iki etiket.
const platformHost = computed(() => {
  if (platformDomain.value) return platformDomain.value
  if (typeof window === 'undefined') return 'zorven.app'
  const host = window.location.hostname
  if (/^[\d.]+$/.test(host) || host.includes(':') || !host.includes('.')) return host
  return host.split('.').slice(-2).join('.')
})

const tunnels = ref<Tunnel[]>([])
const clients = ref<Client[]>([])
const hostnames = ref<Hostname[]>([])
const pending = ref(true)
const saving = ref(false)
const formError = ref('')

// Tünel başına erişim koruması durumu (mode!=none && enabled) — satır rozeti için.
const protectedMap = reactive<Record<string, boolean>>({})

// name TAM hostname değil, tek bir DNS etiketi: sunucu bunu platform
// domainiyle birleştirip kiracı kapsamlı adı otomatik verir.
const form = reactive({
  name: '',
  client_id: '',
  target: 'http://localhost:8000',
  hostname_id: '',
})

/** Tünelin gösterilecek adları. */
const namesOf = (tn: Tunnel) => (tn.hostnames ?? []).map(h => h.fqdn)

/** Henüz hiçbir tünele bağlanmamış boşta duran domainler */
const unattachedHostnames = computed(() => hostnames.value.filter(h => !h.tunnel_id))

onMounted(async () => {
  let tns: Tunnel[] = []
  try {
    const [t1, c, h] = await Promise.all([
      api.listTunnels(),
      api.listClients(),
      api.listHostnames(),
    ])
    tns = t1
    tunnels.value = t1
    clients.value = c
    hostnames.value = h
    if (clients.value[0]) form.client_id = clients.value[0].id
  } catch (e) {
    // Başarısız yükleme sonsuz spinner bırakmasın: hata göster, spinner'ı kapat.
    toast.error(hostnameError(e, t('tunnels.loadFailed')))
  } finally {
    pending.value = false
  }
  // Erişim koruması rozetlerini arka planda doldur (hata sessizce yutulur).
  void Promise.all(tns.map(async (tn) => {
    try {
      const a = await api.getTunnelAccess(tn.id)
      protectedMap[tn.id] = a.mode !== 'none' && a.enabled
    } catch { /* yok say */ }
  }))
})

// --- Detaylı ayarlar modalı (Genel + Protokol + Erişim + IP) ---
type SettingsTab = 'general' | 'protocol' | 'access' | 'ip' | 'ha' | 'traffic' | 'metrics' | 'stats' | 'raw'
const accessTunnel = ref<Tunnel | null>(null) // açık modalın tüneli
const settingsTab = ref<SettingsTab>('general')
const accessLoading = ref(false)
const accessSaving = ref(false)
const accessForm = reactive({
  mode: 'none' as TunnelAccessMode,
  enabled: true,
  username: '',
  password: '',
  hasPassword: false,
  providers: [] as string[],
  allowedEmails: '', // satır/virgülle ayrılmış e-posta listesi
})
// Web ile kapı açma (ham TCP/UDP) — Erişim sekmesinde
const isRawTunnel = computed(() => accessTunnel.value?.proto === 'tcp' || accessTunnel.value?.proto === 'udp')
const doorInfo = ref<TunnelDoor | null>(null)
const doorLoading = ref(false)
const doorSaving = ref(false)
const doorGrants = ref<DoorGrant[]>([])
const doorForm = reactive({ enabled: false, duration: 43200 })
const doorDurationKeys: Record<number, string> = { 3600: 'doorDur1h', 43200: 'doorDur12h', 86400: 'doorDur24h', 604800: 'doorDur7d' }
const doorUrlCopied = ref(false)
// mTLS (FAZ 6.6) — Erişim sekmesi altında
const mtlsForm = reactive({ enabled: false, ca_pem: '' })
const mtlsHasCa = ref(false)
const mtlsSaving = ref(false)
// Genel sekme
const generalForm = reactive({ target: '', enabled: true, client_id: '' })
const generalSaving = ref(false)
const attachDomainId = ref('') // eklenecek boşta domain
const domainSaving = ref(false)
// Protokol sekme
const protoForm = reactive({ proto: 'http' as TunnelProto, exposure: 'port' as TunnelExposure })
const protoSaving = ref(false)
// UDP+SNI henüz yok: udp'ye geçince sni seçiliyse port'a düş.
watch(() => protoForm.proto, (p) => {
  if (p === 'udp' && protoForm.exposure === 'sni') protoForm.exposure = 'port'
})
// IP sekme
const ipRules = ref<IPAllowlistRule[]>([])
const ipLoading = ref(false)
const ipNewCidr = ref('')
const ipNewDesc = ref('')
const ipSaving = ref(false)
// HA (replika) sekme
const replicas = ref<TunnelReplica[]>([])
const replicaLoading = ref(false)
const replicaAddId = ref('')
const replicaSaving = ref(false)

// Ham TCP/UDP tünellerinde "UDP ve Oyun" sekmesi de görünür (FAZ 4 / F24).
const settingsTabs = computed<SettingsTab[]>(() => {
  // member: erisim/IP/HA/trafik/UDP ayarlari sunucuda owner/admin'e ozel (403) — sekmeler gizlenir.
  const base: SettingsTab[] = isPrivileged.value
    ? ['general', 'protocol', 'access', 'stats', 'ip', 'ha', 'traffic', 'metrics']
    : ['general', 'protocol', 'stats', 'metrics']
  const p = accessTunnel.value?.proto
  return isPrivileged.value && (p === 'tcp' || p === 'udp') ? [...base, 'raw'] : base
})

// FAZ 4 / F24 — UDP sınırları + istatistik + oyun sunucusu durumu
type UDPField = 'idle_timeout_sec' | 'max_packet_bytes' | 'max_pps' | 'max_flow_pps' | 'max_flows'
const udpFields: UDPField[] = ['idle_timeout_sec', 'max_packet_bytes', 'max_pps', 'max_flow_pps', 'max_flows']
const udpLoading = ref(false)
const udpSaving = ref(false)
const udpLive = ref<UDPLiveStats | null>(null)
const udpSeries = ref<UDPStatMinute[]>([])
const udpDefaults = ref<TunnelUDPConfig | null>(null)
const udpForm = reactive<TunnelUDPConfig>({ idle_timeout_sec: 90, max_packet_bytes: 65507, max_pps: 0, max_flow_pps: 0, max_flows: 1024 })
const gameLoading = ref(false)
const gameStatus = ref<GameStatus | null>(null)

function fmtCount(n: number): string {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'G'
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return String(Math.round(n))
}

const udpLiveCards = computed(() => {
  const l = udpLive.value
  const dropped = l ? l.dropped_rate + l.dropped_size + l.dropped_flows : 0
  return [
    { key: 'flows', value: l ? String(l.active_flows) : '-' },
    { key: 'ppsIn', value: l ? fmtCount(l.pps_in) : '-' },
    { key: 'ppsOut', value: l ? fmtCount(l.pps_out) : '-' },
    { key: 'dropped', value: l ? fmtCount(dropped) : '-' },
    { key: 'bytesIn', value: l ? fmtCount(l.bytes_in) + 'B' : '-' },
    { key: 'bytesOut', value: l ? fmtCount(l.bytes_out) + 'B' : '-' },
    { key: 'flowsTotal', value: l ? fmtCount(l.flows_total) : '-' },
    { key: 'packets', value: l ? fmtCount(l.packets_in + l.packets_out) : '-' },
  ]
})

const udpSeriesMax = computed(() => Math.max(1, ...udpSeries.value.map(p => p.packets_in + p.packets_out)))

async function loadUDP(tunnelId: string) {
  udpLoading.value = true
  try {
    const r = await api.getTunnelUDP(tunnelId)
    Object.assign(udpForm, r.config)
    udpDefaults.value = r.defaults
    udpLive.value = r.live
    udpSeries.value = r.series || []
  } catch { /* sekme boş kalır */ } finally {
    udpLoading.value = false
  }
}

function resetUDP() {
  if (udpDefaults.value) Object.assign(udpForm, udpDefaults.value)
}

async function saveUDP() {
  const tn = accessTunnel.value
  if (!tn) return
  udpSaving.value = true
  try {
    await api.setTunnelUDP(tn.id, {
      idle_timeout_sec: Number(udpForm.idle_timeout_sec),
      max_packet_bytes: Number(udpForm.max_packet_bytes),
      max_pps: Number(udpForm.max_pps),
      max_flow_pps: Number(udpForm.max_flow_pps),
      max_flows: Number(udpForm.max_flows),
    })
    toast.success(t('tunnels.rawSaved'))
    await loadUDP(tn.id)
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.data?.error || err?.message || t('tunnels.rawSaveFailed'))
  } finally {
    udpSaving.value = false
  }
}

async function probeGame() {
  const tn = accessTunnel.value
  if (!tn) return
  gameLoading.value = true
  try {
    gameStatus.value = await api.getGameStatus(tn.id)
  } catch (err: any) {
    gameStatus.value = null
    toast.error(err?.data?.error?.message || err?.data?.error || err?.message || t('tunnels.rawGameFailed'))
  } finally {
    gameLoading.value = false
  }
}

// FAZ 4 / F21 — yük dengeleme + sağlık kontrolü
const lbLoading = ref(false)
const lbSaving = ref(false)
const lbCandidates = ref<string[]>([])
const lbHealth = ref<LBBackendStatus[]>([])
const lbStrategies: LBStrategy[] = ['round_robin', 'weighted', 'least_connections', 'latency']
const lbForm = reactive<TunnelLBConfig>({
  strategy: 'round_robin', weights: {}, health_enabled: false, health_path: '/',
  interval_sec: 10, timeout_sec: 3, unhealthy_threshold: 3, healthy_threshold: 2,
})
// Trafik politikası sekme (FAZ 6)
type KV = { name: string, value: string }
const trafficLoading = ref(false)
const trafficSaving = ref(false)
const trafficForm = reactive({
  enabled: false,
  respSet: [] as KV[],
  respRemove: '', // virgülle ayrılmış
  reqSet: [] as KV[],
  reqRemove: '',
  redirects: [] as { match_prefix: string, location: string, status: number }[],
})
// Metrikler sekme (FAZ 6.3)
const metricsLoading = ref(false)
const metricsWindow = ref('1h')
const metricsData = ref<TunnelMetricsResp | null>(null)
// Uyarı (alert) — FAZ 6.4
const alertLoading = ref(false)
const alertSaving = ref(false)
const alertState = ref('ok')
const alertForm = reactive({ enabled: false, error_rate_pct: 10, window_min: 5, min_requests: 20, notify_email: '' })

async function openSettings(tn: Tunnel, tab: SettingsTab = 'general') {
  accessTunnel.value = tn
  settingsTab.value = !isPrivileged.value && ['access', 'ip', 'ha', 'traffic', 'raw'].includes(tab) ? 'general' : tab
  accessLoading.value = true
  accessForm.mode = 'none'
  accessForm.enabled = true
  accessForm.username = ''
  accessForm.password = ''
  accessForm.hasPassword = false
  accessForm.providers = []
  accessForm.allowedEmails = ''
  generalForm.target = tn.target
  generalForm.enabled = tn.enabled
  generalForm.client_id = tn.client_id
  attachDomainId.value = ''
  protoForm.proto = tn.proto || 'http'
  protoForm.exposure = (tn.exposure && tn.exposure !== 'auto') ? tn.exposure : 'port'
  ipRules.value = []
  ipNewCidr.value = ''
  ipNewDesc.value = ''
  replicas.value = []
  replicaAddId.value = ''
  metricsData.value = null
  resetStats()
  void loadReplicas(tn.id)
  void loadLB(tn.id)
  udpLive.value = null
  udpSeries.value = []
  gameStatus.value = null
  if (tn.proto === 'udp') void loadUDP(tn.id)
  void loadTraffic(tn.id)
  void loadMetrics(tn.id)
  void loadAlert(tn.id)
  void loadMTLS(tn.id)
  doorInfo.value = null
  doorGrants.value = []
  doorForm.enabled = false
  doorForm.duration = 43200
  if (tn.proto === 'tcp' || tn.proto === 'udp') void loadDoor(tn.id)
  try {
    const a = await api.getTunnelAccess(tn.id)
    accessForm.mode = a.mode
    accessForm.enabled = a.enabled
    accessForm.username = a.config.username || ''
    accessForm.hasPassword = !!a.config.has_password
    accessForm.providers = a.config.providers || []
    accessForm.allowedEmails = (a.config.allowed_emails || []).join('\n')
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.accessLoadFailed'))
    accessTunnel.value = null
  } finally {
    accessLoading.value = false
  }
  void loadIPRules(tn.id)
}

async function loadReplicas(tunnelId: string) {
  replicaLoading.value = true
  try {
    replicas.value = await api.getTunnelReplicas(tunnelId)
  } catch { /* sessizce */ } finally {
    replicaLoading.value = false
  }
}

async function loadLB(tunnelId: string) {
  lbLoading.value = true
  try {
    const r = await api.getTunnelLB(tunnelId)
    Object.assign(lbForm, r.config, { weights: { ...(r.config.weights || {}) } })
    lbCandidates.value = r.candidates || []
    lbHealth.value = r.health || []
  } catch { /* sekme boş kalır; kaydetme hatası ayrıca gösterilir */ } finally {
    lbLoading.value = false
  }
}

function lbStatusOf(clientID: string): LBBackendStatus | undefined {
  return lbHealth.value.find(h => h.client_id === clientID)
}

function lbWeight(clientID: string): number {
  const w = lbForm.weights[clientID]
  return w === undefined ? 1 : w
}

function setLbWeight(clientID: string, v: string) {
  const n = Math.max(0, Math.min(1000, Math.floor(Number(v) || 0)))
  lbForm.weights = { ...lbForm.weights, [clientID]: n }
}

async function saveLB() {
  const tn = accessTunnel.value
  if (!tn) return
  lbSaving.value = true
  try {
    const weights: Record<string, number> = {}
    if (lbForm.strategy === 'weighted') {
      for (const c of lbCandidates.value) weights[c] = lbWeight(c)
    }
    await api.setTunnelLB(tn.id, {
      strategy: lbForm.strategy,
      weights,
      health_enabled: lbForm.health_enabled,
      health_path: lbForm.health_path,
      interval_sec: Number(lbForm.interval_sec),
      timeout_sec: Number(lbForm.timeout_sec),
      unhealthy_threshold: Number(lbForm.unhealthy_threshold),
      healthy_threshold: Number(lbForm.healthy_threshold),
    })
    toast.success(t('tunnels.lbSaved'))
    await loadLB(tn.id)
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.data?.error || err?.message || t('tunnels.lbSaveFailed'))
  } finally {
    lbSaving.value = false
  }
}

async function addReplica() {
  const tn = accessTunnel.value
  if (!tn || !replicaAddId.value) return
  replicaSaving.value = true
  try {
    await api.addTunnelReplica(tn.id, replicaAddId.value)
    replicaAddId.value = ''
    await loadReplicas(tn.id)
    void loadLB(tn.id)
    toast.success(t('tunnels.replicaAdded'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.replicaAddFailed'))
  } finally {
    replicaSaving.value = false
  }
}

async function removeReplica(clientID: string) {
  const tn = accessTunnel.value
  if (!tn) return
  replicaSaving.value = true
  try {
    await api.removeTunnelReplica(tn.id, clientID)
    await loadReplicas(tn.id)
    void loadLB(tn.id)
    toast.success(t('tunnels.replicaRemoved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.replicaRemoveFailed'))
  } finally {
    replicaSaving.value = false
  }
}

// --- Trafik politikası (FAZ 6) ---
function kvFromRecord(r?: Record<string, string>): KV[] {
  return Object.entries(r || {}).map(([name, value]) => ({ name, value }))
}
function recordFromKv(list: KV[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const { name, value } of list) {
    if (name.trim()) out[name.trim()] = value
  }
  return out
}
function listFromCsv(s: string): string[] {
  return s.split(',').map(x => x.trim()).filter(Boolean)
}

async function loadTraffic(tunnelId: string) {
  trafficLoading.value = true
  trafficForm.enabled = false
  trafficForm.respSet = []
  trafficForm.respRemove = ''
  trafficForm.reqSet = []
  trafficForm.reqRemove = ''
  trafficForm.redirects = []
  try {
    const p = await api.getTunnelTraffic(tunnelId)
    trafficForm.enabled = p.enabled
    const c = p.config || {}
    trafficForm.respSet = kvFromRecord(c.response_headers?.set)
    trafficForm.respRemove = (c.response_headers?.remove || []).join(', ')
    trafficForm.reqSet = kvFromRecord(c.request_headers?.set)
    trafficForm.reqRemove = (c.request_headers?.remove || []).join(', ')
    trafficForm.redirects = (c.redirects || []).map(r => ({ ...r }))
  } catch { /* sessizce */ } finally {
    trafficLoading.value = false
  }
}

async function saveTraffic() {
  const tn = accessTunnel.value
  if (!tn) return
  trafficSaving.value = true
  try {
    const config = {
      request_headers: { set: recordFromKv(trafficForm.reqSet), remove: listFromCsv(trafficForm.reqRemove) },
      response_headers: { set: recordFromKv(trafficForm.respSet), remove: listFromCsv(trafficForm.respRemove) },
      redirects: trafficForm.redirects
        .filter(r => r.location.trim())
        .map(r => ({ match_prefix: r.match_prefix.trim(), location: r.location.trim(), status: Number(r.status) || 302 })),
    }
    await api.setTunnelTraffic(tn.id, { enabled: trafficForm.enabled, config })
    toast.success(t('tunnels.trafficSaved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.trafficSaveFailed'))
  } finally {
    trafficSaving.value = false
  }
}

// --- mTLS (FAZ 6.6) ---
async function loadMTLS(tunnelId: string) {
  mtlsForm.enabled = false
  mtlsForm.ca_pem = ''
  mtlsHasCa.value = false
  try {
    const m = await api.getTunnelMTLS(tunnelId)
    mtlsForm.enabled = m.enabled
    mtlsForm.ca_pem = m.ca_pem || ''
    mtlsHasCa.value = m.has_ca
  } catch { /* sessizce */ }
}
async function saveMTLS() {
  const tn = accessTunnel.value
  if (!tn) return
  if (mtlsForm.enabled && !mtlsForm.ca_pem.trim()) { toast.error(t('tunnels.mtlsNeedCa')); return }
  mtlsSaving.value = true
  try {
    await api.setTunnelMTLS(tn.id, { enabled: mtlsForm.enabled, ca_pem: mtlsForm.ca_pem.trim() })
    mtlsHasCa.value = mtlsForm.ca_pem.trim() !== ''
    toast.success(t('tunnels.mtlsSaved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.mtlsSaveFailed'))
  } finally {
    mtlsSaving.value = false
  }
}

// --- Erişim istatistikleri (Basic/OAuth giriş olayları) ---
const statsWindow = ref<'24h' | '7d' | '30d'>('24h')
const statsLoading = ref(false)
const statsSummary = ref<AccessEventSummary | null>(null)
const statsEvents = ref<AccessEvent[]>([])
const statsCursor = ref('')
const statsHasMore = ref(false)
const statsMoreLoading = ref(false)
const statsError = ref(false)
const statsLoadedFor = ref('')
function resetStats() {
  statsSummary.value = null
  statsEvents.value = []
  statsCursor.value = ''
  statsHasMore.value = false
  statsError.value = false
  statsLoadedFor.value = ''
}
async function loadStats() {
  const tn = accessTunnel.value
  if (!tn) return
  statsLoading.value = true
  statsError.value = false
  try {
    const [sum, page] = await Promise.all([
      api.getAccessEventsSummary(tn.id, statsWindow.value),
      api.listAccessEvents(tn.id, 25),
    ])
    if (accessTunnel.value?.id !== tn.id) return
    statsSummary.value = sum
    statsEvents.value = page.items
    statsCursor.value = page.nextCursor
    statsHasMore.value = page.hasMore
    statsLoadedFor.value = tn.id
  } catch {
    statsError.value = true
  } finally {
    statsLoading.value = false
  }
}
async function loadMoreStats() {
  const tn = accessTunnel.value
  if (!tn || !statsHasMore.value || statsMoreLoading.value) return
  statsMoreLoading.value = true
  try {
    const page = await api.listAccessEvents(tn.id, 25, statsCursor.value)
    if (accessTunnel.value?.id !== tn.id) return
    statsEvents.value = [...statsEvents.value, ...page.items]
    statsCursor.value = page.nextCursor
    statsHasMore.value = page.hasMore
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.statsLoadFailed'))
  } finally {
    statsMoreLoading.value = false
  }
}
// Sekme ilk açıldığında yükle.
watch(settingsTab, (tab) => {
  if (tab === 'stats' && accessTunnel.value && statsLoadedFor.value !== accessTunnel.value.id && !statsLoading.value) void loadStats()
})
const statsTiles = computed(() => {
  const s = statsSummary.value
  if (!s) return []
  const tiles: { key: string, value: number, cls?: string }[] = [
    { key: 'total', value: s.total },
    { key: 'success', value: s.success, cls: 'text-emerald-500' },
    { key: 'failure', value: s.failure, cls: s.failure > 0 ? 'text-danger' : '' },
    { key: 'identities', value: s.unique_identities },
    { key: 'ips', value: s.unique_ips },
  ]
  if (isRawTunnel.value) {
    tiles.push({ key: 'doorGrants', value: s.door_grants }, { key: 'doorBlocked', value: s.door_blocked })
  }
  return tiles
})
function statsBreakdown(m: Record<string, number> | undefined) {
  const rows = Object.entries(m || {}).filter(([k]) => k !== '').sort((a, b) => b[1] - a[1])
  const max = Math.max(1, ...rows.map(r => r[1]))
  return rows.map(([k, v]) => ({ key: k, value: v, pct: Math.max(3, Math.round(v / max * 100)) }))
}
const statsBreakdowns = computed(() => [
  { id: 'method', rows: statsBreakdown(statsSummary.value?.by_method) },
  { id: 'provider', rows: statsBreakdown(statsSummary.value?.by_provider) },
  { id: 'reason', rows: statsBreakdown(statsSummary.value?.by_reason) },
])
const statsEmpty = computed(() => {
  const s = statsSummary.value
  return !!s && s.total === 0 && s.door_grants === 0 && s.door_blocked === 0 && statsEvents.value.length === 0
})
function statsLabel(group: string, key: string): string {
  const k = `tunnels.statsLbl_${group}_${key}`
  return te(k) ? t(k) : key
}
function statsReason(reason: string): string {
  const k = `tunnels.statsReason_${reason}`
  return te(k) ? t(k) : (reason || '-')
}
function shortUA(ua: string): string {
  if (!ua) return '-'
  const m = ua.match(/(Edg|OPR|Firefox|Chrome|Safari|curl|Postman\w*|python-requests|Go-http-client)\/?([\d.]*)/i)
  const base = m ? `${m[1]}${m[2] ? ' ' + m[2].split('.')[0] : ''}` : ua
  return base.length > 28 ? base.slice(0, 27) + '…' : base
}

// --- Metrikler (FAZ 6.3) ---
async function loadMetrics(tunnelId: string) {
  metricsLoading.value = true
  try {
    metricsData.value = await api.getTunnelMetrics(tunnelId, metricsWindow.value)
  } catch { metricsData.value = null } finally {
    metricsLoading.value = false
  }
}
function onMetricsWindowChange() {
  if (accessTunnel.value) void loadMetrics(accessTunnel.value.id)
}
// Grafik için ölçekleme: en yüksek bucket count'u (min 1).
const metricsMaxCount = computed(() => {
  const bs = metricsData.value?.buckets || []
  return Math.max(1, ...bs.map(b => b.count))
})

async function loadAlert(tunnelId: string) {
  alertLoading.value = true
  try {
    const a = await api.getTunnelAlert(tunnelId)
    alertForm.enabled = a.enabled
    alertForm.error_rate_pct = a.error_rate_pct
    alertForm.window_min = a.window_min
    alertForm.min_requests = a.min_requests
    alertForm.notify_email = a.notify_email
    alertState.value = a.state || 'ok'
  } catch { /* sessizce */ } finally {
    alertLoading.value = false
  }
}
async function saveAlert() {
  const tn = accessTunnel.value
  if (!tn) return
  alertSaving.value = true
  try {
    await api.setTunnelAlert(tn.id, {
      enabled: alertForm.enabled,
      error_rate_pct: Number(alertForm.error_rate_pct) || 10,
      window_min: Number(alertForm.window_min) || 5,
      min_requests: Number(alertForm.min_requests) || 0,
      notify_email: alertForm.notify_email.trim(),
    })
    toast.success(t('tunnels.alertSaved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.alertSaveFailed'))
  } finally {
    alertSaving.value = false
  }
}

// HA sekmesinde eklenebilecek istemciler: birincil ve zaten replika olanlar hariç.
const availableReplicaClients = computed(() => {
  const tn = accessTunnel.value
  if (!tn) return []
  const taken = new Set([tn.client_id, ...replicas.value.map(r => r.client_id)])
  return clients.value.filter(c => !taken.has(c.id))
})

async function loadIPRules(tunnelId: string) {
  ipLoading.value = true
  try {
    const res = await api.listIPRules(tunnelId)
    ipRules.value = res.rules
  } catch { /* sessizce */ } finally {
    ipLoading.value = false
  }
}

async function saveGeneral() {
  const tn = accessTunnel.value
  if (!tn) return
  if (!generalForm.target.trim()) { toast.error(t('tunnels.errTargetRequired')); return }
  if (!generalForm.client_id) { toast.error(t('tunnels.errClientRequired')); return }
  generalSaving.value = true
  try {
    const updated = await api.updateTunnel(tn.id, {
      target: generalForm.target.trim(),
      enabled: generalForm.enabled,
      client_id: generalForm.client_id,
    })
    // hostnames alanını koru (updateTunnel döndürmeyebilir)
    Object.assign(tn, { ...updated, hostnames: updated.hostnames ?? tn.hostnames })
    toast.success(t('tunnels.settingsSaved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.updateFailed'))
  } finally {
    generalSaving.value = false
  }
}

// Boşta bir domaini bu tünele bağla.
async function attachDomain() {
  const tn = accessTunnel.value
  if (!tn || !attachDomainId.value) return
  domainSaving.value = true
  try {
    const updated = await api.updateHostname(attachDomainId.value, tn.id)
    // Yerel durumları tazele
    const h = hostnames.value.find(x => x.id === updated.id)
    if (h) h.tunnel_id = tn.id
    tn.hostnames = [...(tn.hostnames ?? []), ...(h ? [h] : [])]
    attachDomainId.value = ''
    toast.success(t('tunnels.domainAttached'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.domainAttachFailed'))
  } finally {
    domainSaving.value = false
  }
}

// Domaini bu tünelden ayır.
async function detachDomain(h: Hostname) {
  const tn = accessTunnel.value
  if (!tn) return
  try {
    await api.updateHostname(h.id, null)
    const local = hostnames.value.find(x => x.id === h.id)
    if (local) local.tunnel_id = null
    tn.hostnames = (tn.hostnames ?? []).filter(x => x.id !== h.id)
    toast.info(t('tunnels.domainDetached'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.domainDetachFailed'))
  }
}

async function saveProtocol() {
  const tn = accessTunnel.value
  if (!tn) return
  protoSaving.value = true
  try {
    const updated = await api.updateTunnel(tn.id, { proto: protoForm.proto, exposure: protoForm.exposure })
    Object.assign(tn, updated)
    protoForm.proto = updated.proto || 'http'
    protoForm.exposure = (updated.exposure && updated.exposure !== 'auto') ? updated.exposure : 'port'
    toast.success(t('tunnels.settingsSaved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.updateFailed'))
  } finally {
    protoSaving.value = false
  }
}

async function addIPRule() {
  const tn = accessTunnel.value
  if (!tn || !ipNewCidr.value.trim()) return
  ipSaving.value = true
  try {
    const rule = await api.createIPRule(ipNewCidr.value.trim(), ipNewDesc.value.trim(), tn.id)
    ipRules.value.push(rule)
    ipNewCidr.value = ''
    ipNewDesc.value = ''
    toast.success(t('tunnels.ipAdded'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.ipAddFailed'))
  } finally {
    ipSaving.value = false
  }
}

async function removeIPRule(rule: IPAllowlistRule) {
  try {
    await api.deleteIPRule(rule.id)
    ipRules.value = ipRules.value.filter(r => r.id !== rule.id)
    toast.info(t('tunnels.ipRemoved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.ipRemoveFailed'))
  }
}

async function loadDoor(tunnelId: string) {
  doorLoading.value = true
  try {
    const d = await api.getTunnelDoor(tunnelId)
    if (accessTunnel.value?.id !== tunnelId) return
    doorInfo.value = d
    doorForm.enabled = d.enabled
    doorForm.duration = d.duration_sec
    doorGrants.value = d.enabled ? await api.listDoorGrants(tunnelId) : []
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.doorLoadFailed'))
  } finally {
    doorLoading.value = false
  }
}

async function saveDoor() {
  const tn = accessTunnel.value
  if (!tn) return
  doorSaving.value = true
  try {
    doorInfo.value = await api.setTunnelDoor(tn.id, { enabled: doorForm.enabled, duration_sec: doorForm.duration })
    doorGrants.value = doorInfo.value.enabled ? await api.listDoorGrants(tn.id) : []
    toast.success(t('tunnels.doorSaved'))
  } catch (err: any) {
    const msg = err?.data?.error?.message || err?.message || t('tunnels.doorSaveFailed')
    toast.error(msg)
  } finally {
    doorSaving.value = false
  }
}

async function copyDoorUrl() {
  if (!doorInfo.value?.url) return
  try {
    await navigator.clipboard.writeText(doorInfo.value.url)
    doorUrlCopied.value = true
    toast.success(t('tunnels.doorCopied'))
    setTimeout(() => { doorUrlCopied.value = false }, 1500)
  } catch { /* pano erişimi yok */ }
}

async function refreshDoorGrants() {
  const tn = accessTunnel.value
  if (!tn) return
  try {
    doorGrants.value = await api.listDoorGrants(tn.id)
  } catch { /* sessizce */ }
}

async function revokeGrant(g: DoorGrant) {
  const tn = accessTunnel.value
  if (!tn) return
  try {
    await api.revokeDoorGrant(tn.id, g.id)
    doorGrants.value = doorGrants.value.filter(x => x.id !== g.id)
    toast.info(t('tunnels.doorRevoked'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.doorRevokeFailed'))
  }
}

async function saveAccess() {
  const tn = accessTunnel.value
  if (!tn) return
  if (accessForm.mode === 'basic') {
    if (!accessForm.username.trim()) { toast.error(t('tunnels.accessUserRequired')); return }
    if (!accessForm.hasPassword && !accessForm.password) { toast.error(t('tunnels.accessPassRequired')); return }
  }
  const emails = accessForm.allowedEmails
    .split(/[\n,;]+/).map(e => e.trim().toLowerCase()).filter(Boolean)
  if (accessForm.mode === 'oauth') {
    if (!anyOAuthProvider.value) { toast.error(t('tunnels.accessOAuthUnavailable')); return }
    if (!accessForm.providers.length) { toast.error(t('tunnels.accessProviderRequired')); return }
  }
  accessSaving.value = true
  try {
    let config: TunnelAccessInput['config'] = {}
    if (accessForm.mode === 'basic') {
      config = { username: accessForm.username.trim(), password: accessForm.password || undefined }
    } else if (accessForm.mode === 'oauth') {
      config = { providers: accessForm.providers, allowed_emails: emails }
    }
    const payload: TunnelAccessInput = {
      mode: accessForm.mode,
      enabled: accessForm.mode === 'none' ? false : accessForm.enabled,
      config,
    }
    const res = await api.setTunnelAccess(tn.id, payload)
    protectedMap[tn.id] = res.mode !== 'none' && res.enabled
    accessForm.hasPassword = res.config.has_password ?? accessForm.hasPassword
    accessForm.password = ''
    toast.success(t('tunnels.accessSaved'))
    if (isRawTunnel.value) void loadDoor(tn.id)
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.accessSaveFailed'))
  } finally {
    accessSaving.value = false
  }
}

const clientById = (id: string) => clients.value.find(c => c.id === id)
const clientName = (id: string) => clientById(id)?.name ?? id
const clientStatusOf = (id: string) => clientById(id)?.status
/** Seçicide/dropdownda istemciyi durumuyla birlikte etiketle. */
const clientLabel = (c: Client) =>
  `${c.name} — ${c.status === 'online' ? t('tunnels.online') : t('tunnels.offline')}`

async function submit() {
  if (!form.name.trim() || !form.client_id || !form.target.trim()) return
  formError.value = ''
  saving.value = true
  try {
    const created = await api.createTunnel({
      name: form.name.trim(),
      client_id: form.client_id,
      target: form.target.trim(),
      hostname_id: form.hostname_id === '__none__' ? undefined : (form.hostname_id || undefined),
      no_domain: form.hostname_id === '__none__',
    })
    tunnels.value.push(created)
    form.name = ''
    form.hostname_id = ''
    hostnames.value = await api.listHostnames()
    toast.success(t('tunnels.created'))
    loadBillingData()
  } catch (e: any) {
    const code = apiErrorCode(e)
    const msg = hostnameError(e, t('tunnels.createFailed'))
    formError.value = msg
    if (code === 'plan_limit_reached') {
      openUpgrade(msg, 'tunnels')
    } else {
      toast.error(msg)
    }
  } finally {
    saving.value = false
  }
}

async function toggle(tn: Tunnel) {
  try {
    const updated = await api.updateTunnel(tn.id, { enabled: !tn.enabled })
    Object.assign(tn, updated)
    toast.info(tn.enabled ? t('tunnels.activated') : t('tunnels.stopped'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.updateFailed'))
  }
}

async function remove(tn: Tunnel) {
  try {
    await api.deleteTunnel(tn.id)
    tunnels.value = tunnels.value.filter(x => x.id !== tn.id)
    hostnames.value = await api.listHostnames()
    toast.info(t('tunnels.deleted'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.deleteFailed'))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('tunnels.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('tunnels.subtitle') }}</p>
    </header>

    <PanelFrame :label="t('tunnels.newTunnel')">
      <form class="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-[1fr_160px_1fr_180px_auto]" @submit.prevent="submit">
        <div>
          <label for="name" class="label-sys mb-1 block">{{ t('tunnels.name') }}</label>
          <input id="name" v-model="form.name" placeholder="api"
                 class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm
                        placeholder:text-fg-subtle focus:border-accent">
        </div>

        <div>
          <label for="client" class="label-sys mb-1 block">{{ t('tunnels.client') }}</label>
          <select id="client" v-model="form.client_id" :disabled="!clients.length"
                  class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5
                         font-mono text-sm focus:border-accent disabled:cursor-not-allowed disabled:opacity-50">
            <option v-if="!clients.length" value="">—</option>
            <option v-for="c in clients" :key="c.id" :value="c.id">{{ clientLabel(c) }}</option>
          </select>
        </div>

        <div>
          <label for="target" class="label-sys mb-1 block">{{ t('tunnels.target') }}</label>
          <input id="target" v-model="form.target"
                 class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm
                        focus:border-accent">
        </div>

        <div>
          <label for="domainSelect" class="label-sys mb-1 block">{{ t('tunnels.domainOptional') }}</label>
          <select id="domainSelect" v-model="form.hostname_id"
                  class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5
                         font-mono text-sm focus:border-accent">
            <option value="">{{ t('tunnels.autoDomain') }}</option>
            <option value="__none__">{{ t('tunnels.noDomain') }}</option>
            <option v-for="h in unattachedHostnames" :key="h.id" :value="h.id">
              {{ h.fqdn }}
            </option>
          </select>
        </div>

        <div class="flex items-end sm:col-span-2 lg:col-span-1">
          <button type="submit" :disabled="saving"
                  class="w-full cursor-pointer rounded bg-accent px-4 py-1.5 text-sm font-medium
                         text-on-accent transition-opacity duration-150 hover:opacity-90
                         disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto">
            {{ saving ? t('tunnels.adding') : t('common.add') }}
          </button>
        </div>

        <p v-if="!pending && !clients.length" class="text-sm text-fg-muted sm:col-span-2 lg:col-span-5">{{ t('tunnels.noClients') }}</p>
        <p v-if="formError" class="text-sm text-danger sm:col-span-2 lg:col-span-5" role="alert">{{ formError }}</p>
      </form>
    </PanelFrame>

    <PanelFrame :label="t('tunnels.definedTunnels')" :meta="`${tunnels.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <p v-else-if="!tunnels.length" class="px-4 py-8 text-center text-sm text-fg-muted">{{ t('tunnels.noTunnels') }}</p>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colNames') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colClient') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colTarget') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colCreated') }}</th>
              <th class="px-4 py-2"><span class="sr-only">{{ t('tunnels.colActions') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="tn in tunnels" :key="tn.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5 font-mono text-xs">
                <p v-for="fqdn in namesOf(tn)" :key="fqdn" class="truncate">{{ fqdn }}</p>
                <span v-if="!namesOf(tn).length" class="text-fg-subtle">{{ t('tunnels.noName') }}</span>
              </td>
              <td class="px-4 py-2.5">
                <span class="inline-flex items-center gap-1.5">
                  <span
                    class="size-1.5 rounded-full"
                    :class="clientStatusOf(tn.client_id) === 'online' ? 'bg-accent' : clientStatusOf(tn.client_id) === 'offline' ? 'bg-fg-subtle' : 'bg-danger'"
                    :title="clientStatusOf(tn.client_id) === 'online' ? t('tunnels.online') : clientStatusOf(tn.client_id) === 'offline' ? t('tunnels.offline') : t('tunnels.unknownClient')"
                  />
                  <span v-if="clientById(tn.client_id)">{{ clientName(tn.client_id) }}</span>
                  <span v-else class="font-mono text-xs text-fg-subtle">{{ tn.client_id }} <span class="text-danger">({{ t('tunnels.unknownClient') }})</span></span>
                </span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted">{{ tn.target }}</td>
              <td class="px-4 py-2.5"><StatusPill :status="tn.enabled ? 'enabled' : 'disabled'" /></td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(tn.created_at) }}</td>
              <td class="px-4 py-2.5">
                <div class="flex justify-end gap-1">
                  <button
                    class="relative cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent"
                    :title="t('tunnels.settings', { name: namesOf(tn)[0] ?? tn.id })"
                    @click="openSettings(tn)"
                  >
                    <Icon name="lucide:settings" class="size-4" />
                    <span
                      v-if="protectedMap[tn.id]"
                      class="absolute -right-0.5 -top-0.5 grid size-2.5 place-items-center rounded-full bg-emerald-500"
                      :title="t('tunnels.accessProtected')"
                    />
                  </button>
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors
                           duration-150 hover:bg-surface-2 hover:text-fg"
                    :aria-label="tn.enabled ? t('tunnels.stopTunnel', { name: namesOf(tn)[0] ?? tn.id }) : t('tunnels.startTunnel', { name: namesOf(tn)[0] ?? tn.id })"
                    @click="toggle(tn)"
                  >
                    <Icon :name="tn.enabled ? 'lucide:pause' : 'lucide:play'" class="size-4" />
                  </button>
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors
                           duration-150 hover:bg-danger/10 hover:text-danger"
                    :aria-label="t('tunnels.deleteTunnel', { name: namesOf(tn)[0] ?? tn.id })"
                    :disabled="!isPrivileged"
                    :title="isPrivileged ? undefined : t('common.adminOnly')"
                    @click="remove(tn)"
                  >
                    <Icon name="lucide:trash-2" class="size-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>

    <!-- Detaylı Ayarlar Modalı (Genel + Protokol + Erişim + IP) -->
    <div
      v-if="accessTunnel"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      @click.self="accessTunnel = null"
    >
      <div class="modal-pop flex h-[80vh] max-h-[620px] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-line bg-surface shadow-xl">
        <div class="flex items-start justify-between border-b border-line p-5 pb-4">
          <div class="flex items-center gap-2">
            <Icon name="lucide:settings" class="size-5 text-accent" />
            <div>
              <h2 class="text-base font-semibold text-fg">{{ t('tunnels.settingsTitle') }}</h2>
              <p class="font-mono text-xs text-fg-muted">{{ namesOf(accessTunnel)[0] ?? accessTunnel.id }}</p>
            </div>
          </div>
          <button class="rounded p-1 text-fg-muted hover:bg-surface-2 hover:text-fg" @click="accessTunnel = null">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <!-- Gövde: sol sekme çubuğu + içerik -->
        <div class="flex min-h-0 flex-1">
          <!-- Sol dikey sekme çubuğu -->
          <nav class="w-28 shrink-0 space-y-0.5 overflow-y-auto border-r border-line p-2 sm:w-36">
            <button
              v-for="tab in settingsTabs"
              :key="tab"
              type="button"
              class="block w-full rounded-md px-2.5 py-1.5 text-left text-xs font-medium transition-colors duration-150"
              :class="settingsTab === tab ? 'bg-accent/10 text-accent' : 'text-fg-muted hover:bg-surface-2 hover:text-fg'"
              :title="t('tunnels.tabHelp' + tab.charAt(0).toUpperCase() + tab.slice(1))"
              @click="settingsTab = tab"
            >
              {{ t('tunnels.tab' + tab.charAt(0).toUpperCase() + tab.slice(1)) }}
            </button>
          </nav>

          <!-- İçerik (sabit yükseklik + kendi içinde kaydırma; smooth geçiş) -->
          <div class="min-w-0 flex-1 overflow-y-auto p-5">
          <div v-if="accessLoading" class="py-6 text-center text-sm text-fg-muted">{{ t('common.loading') }}</div>

          <div v-else :key="settingsTab" class="tabpane">

        <!-- GENEL -->
        <div v-if="settingsTab === 'general'" class="space-y-4">
          <div>
            <div class="mb-1 flex items-center justify-between gap-2">
              <label for="genTarget" class="label-sys">{{ t('tunnels.target') }}</label>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.targetHint')">?</span>
            </div>
            <input id="genTarget" v-model="generalForm.target"
                   class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm focus:border-accent">
          </div>
          <div>
            <div class="mb-1 flex items-center justify-between gap-2">
              <label for="genClient" class="label-sys">{{ t('tunnels.client') }}</label>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.clientHint')">?</span>
            </div>
            <select id="genClient" v-model="generalForm.client_id"
                    class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm focus:border-accent">
              <option v-for="c in clients" :key="c.id" :value="c.id">{{ clientLabel(c) }}</option>
            </select>
          </div>
          <label class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
            <span class="text-sm text-fg">{{ t('tunnels.enabledLabel') }}</span>
            <input v-model="generalForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
          </label>
          <div class="flex justify-end">
            <button :disabled="generalSaving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveGeneral">
              {{ generalSaving ? t('common.saving') : t('common.save') }}
            </button>
          </div>

          <!-- Domainler -->
          <div class="space-y-2 border-t border-line pt-4">
            <span class="label-sys block">{{ t('tunnels.domainsLabel') }}</span>
            <ul v-if="accessTunnel.hostnames && accessTunnel.hostnames.length" class="divide-y divide-line rounded border border-line">
              <li v-for="h in accessTunnel.hostnames" :key="h.id" class="flex items-center justify-between gap-2 px-3 py-2">
                <span class="truncate font-mono text-xs text-fg">{{ h.fqdn }}</span>
                <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('tunnels.domainDetach')" @click="detachDomain(h)">
                  <Icon name="lucide:unlink" class="size-3.5" />
                </button>
              </li>
            </ul>
            <p v-else class="text-[11px] text-fg-subtle">{{ t('tunnels.noDomainAttached') }}</p>
            <div class="flex gap-2">
              <select v-model="attachDomainId" class="flex-1 cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                <option value="">{{ t('tunnels.attachDomainSelect') }}</option>
                <option v-for="h in unattachedHostnames" :key="h.id" :value="h.id">{{ h.fqdn }}</option>
              </select>
              <button :disabled="domainSaving || !attachDomainId" class="shrink-0 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="attachDomain">
                {{ t('tunnels.domainAttach') }}
              </button>
            </div>
            <p class="text-[11px] text-fg-subtle">{{ t('tunnels.domainManageHint') }}</p>
          </div>
        </div>

        <!-- PROTOKOL -->
        <div v-else-if="settingsTab === 'protocol'" class="space-y-4">
          <p class="text-xs text-fg-muted">{{ t('tunnels.protocolIntro') }}</p>

          <!-- Protokol seçimi -->
          <div class="space-y-2">
            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="protoForm.proto === 'http' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="protoForm.proto" type="radio" value="http" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.protoHttp') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.protoHttpHint')">?</span>
            </label>
            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="protoForm.proto === 'tcp' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="protoForm.proto" type="radio" value="tcp" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.protoTcp') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.protoTcpHint')">?</span>
            </label>
            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="protoForm.proto === 'udp' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="protoForm.proto" type="radio" value="udp" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.protoUdp') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.protoUdpHint')">?</span>
            </label>
          </div>

          <!-- Maruziyet modu (tcp/udp) -->
          <div v-if="protoForm.proto !== 'http'" class="space-y-2 rounded border border-line bg-surface-1 p-3">
            <span class="label-sys block">{{ t('tunnels.exposureLabel') }}</span>
            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="protoForm.exposure === 'port' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="protoForm.exposure" type="radio" value="port" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.exposurePort') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.exposurePortHint', { domain: platformHost })">?</span>
            </label>
            <label class="flex items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="[protoForm.exposure === 'sni' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle', protoForm.proto === 'udp' ? 'cursor-not-allowed opacity-60' : 'cursor-pointer']">
              <span class="flex items-center gap-2.5">
                <input v-model="protoForm.exposure" type="radio" value="sni" :disabled="protoForm.proto === 'udp'" class="accent-[var(--color-accent)]">
                <span>
                  <span class="block text-sm font-medium text-fg">{{ t('tunnels.exposureSni') }}</span>
                  <span v-if="protoForm.proto === 'udp'" class="block text-xs text-amber-700 dark:text-amber-400">{{ t('tunnels.sniUdpUnavailable') }}</span>
                </span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.exposureSniHint')">?</span>
            </label>
          </div>

          <!-- Bağlantı bilgisi (kayıtlı proto tcp/udp ise) -->
          <div v-if="accessTunnel.proto && accessTunnel.proto !== 'http'" class="rounded border border-dashed border-line p-3 text-xs">
            <span class="mb-1 block font-medium text-fg">{{ t('tunnels.connectInfo') }}</span>
            <template v-if="accessTunnel.exposure === 'port'">
              <p v-if="accessTunnel.public_port" class="font-mono text-fg">{{ platformHost }}:{{ accessTunnel.public_port }}</p>
              <p v-else class="text-fg-muted">{{ t('tunnels.connectPortPending') }}</p>
            </template>
            <template v-else>
              <code class="block rounded bg-bg px-2 py-1 font-mono text-fg">zorven forward {{ namesOf(accessTunnel)[0] ?? accessTunnel.id }}{{ accessTunnel.proto === 'udp' ? ' --udp' : '' }}</code>
              <p class="mt-1 text-fg-muted">{{ t('tunnels.connectSniHint') }}</p>
            </template>
          </div>

          <div class="flex justify-end">
            <button :disabled="protoSaving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveProtocol">
              {{ protoSaving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </div>

        <!-- IP İZİN LİSTESİ -->
        <div v-else-if="settingsTab === 'ip'" class="space-y-3">
          <p class="text-xs text-fg-muted">{{ t('tunnels.ipIntro') }}</p>
          <div class="flex gap-2">
            <input v-model="ipNewCidr" :placeholder="t('tunnels.ipCidrPlaceholder')"
                   class="w-40 rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
            <input v-model="ipNewDesc" :placeholder="t('tunnels.ipDescPlaceholder')"
                   class="flex-1 rounded border border-line bg-bg px-2.5 py-1.5 text-xs focus:border-accent">
            <button :disabled="ipSaving || !ipNewCidr.trim()" class="shrink-0 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="addIPRule">
              {{ t('common.add') }}
            </button>
          </div>
          <p v-if="ipLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
          <p v-else-if="!ipRules.length" class="rounded border border-dashed border-line py-4 text-center text-xs text-fg-muted">{{ t('tunnels.ipEmpty') }}</p>
          <ul v-else class="divide-y divide-line rounded border border-line">
            <li v-for="rule in ipRules" :key="rule.id" class="flex items-center justify-between gap-2 px-3 py-2">
              <div class="min-w-0">
                <span class="font-mono text-xs text-fg">{{ rule.cidr }}</span>
                <span v-if="rule.description" class="ml-2 truncate text-[11px] text-fg-muted">{{ rule.description }}</span>
              </div>
              <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="removeIPRule(rule)">
                <Icon name="lucide:trash-2" class="size-3.5" />
              </button>
            </li>
          </ul>
        </div>

        <!-- HA (REPLİKA) -->
        <div v-else-if="settingsTab === 'ha'" class="space-y-3">
          <p class="text-xs text-fg-muted">{{ t('tunnels.haIntro') }}</p>

          <div class="rounded border border-line px-3 py-2">
            <span class="label-sys block">{{ t('tunnels.haPrimary') }}</span>
            <span class="mt-0.5 block font-mono text-xs text-fg">{{ clientById(accessTunnel?.client_id || '')?.name || accessTunnel?.client_id }}</span>
          </div>

          <div class="flex gap-2">
            <select v-model="replicaAddId" :disabled="!availableReplicaClients.length"
                    class="flex-1 cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent disabled:opacity-50">
              <option value="">{{ availableReplicaClients.length ? t('tunnels.haSelectClient') : t('tunnels.haNoClient') }}</option>
              <option v-for="c in availableReplicaClients" :key="c.id" :value="c.id">{{ clientLabel(c) }}</option>
            </select>
            <button :disabled="replicaSaving || !replicaAddId" class="shrink-0 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="addReplica">
              {{ t('common.add') }}
            </button>
          </div>

          <p v-if="replicaLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
          <p v-else-if="!replicas.length" class="rounded border border-dashed border-line py-4 text-center text-xs text-fg-muted">{{ t('tunnels.haEmpty') }}</p>
          <ul v-else class="divide-y divide-line rounded border border-line">
            <li v-for="rep in replicas" :key="rep.client_id" class="flex items-center justify-between gap-2 px-3 py-2">
              <div class="flex min-w-0 items-center gap-2">
                <span class="size-1.5 shrink-0 rounded-full" :class="rep.online ? 'bg-emerald-400' : 'bg-fg-subtle'" :title="rep.online ? t('platform.online') : t('platform.offline')" />
                <span class="truncate font-mono text-xs text-fg">{{ rep.name || rep.client_id }}</span>
              </div>
              <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="removeReplica(rep.client_id)">
                <Icon name="lucide:trash-2" class="size-3.5" />
              </button>
            </li>
          </ul>

          <!-- YÜK DENGELEME + SAĞLIK KONTROLÜ (FAZ 4 / F21) -->
          <div class="space-y-3 border-t border-line pt-3">
            <div class="flex items-center gap-2">
              <Icon name="lucide:scale" class="size-3.5 text-fg-muted" />
              <span class="label-sys">{{ t('tunnels.lbTitle') }}</span>
            </div>
            <p class="text-xs text-fg-muted">{{ t('tunnels.lbIntro') }}</p>

            <p v-if="lbLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
            <template v-else>
              <label class="block">
                <span class="label-sys mb-1 block">{{ t('tunnels.lbStrategy') }}</span>
                <select v-model="lbForm.strategy" class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5 text-xs focus:border-accent">
                  <option v-for="s in lbStrategies" :key="s" :value="s">{{ t('tunnels.lbStrategy_' + s) }}</option>
                </select>
                <span class="mt-1 block text-[11px] text-fg-subtle">{{ t('tunnels.lbStrategyHint_' + lbForm.strategy) }}</span>
              </label>

              <ul class="divide-y divide-line rounded border border-line">
                <li v-for="cid in lbCandidates" :key="cid" class="flex items-center justify-between gap-2 px-3 py-2">
                  <div class="flex min-w-0 items-center gap-2">
                    <span class="size-1.5 shrink-0 rounded-full"
                          :class="!lbForm.health_enabled || !lbStatusOf(cid)?.checked ? 'bg-fg-subtle' : (lbStatusOf(cid)?.healthy ? 'bg-emerald-400' : 'bg-danger')"
                          :title="lbStatusOf(cid)?.last_error || ''" />
                    <span class="truncate font-mono text-xs text-fg">{{ clientById(cid)?.name || cid }}</span>
                    <span v-if="lbForm.health_enabled && lbStatusOf(cid)?.checked" class="shrink-0 text-[11px]" :class="lbStatusOf(cid)?.healthy ? 'text-fg-muted' : 'text-danger'">
                      {{ lbStatusOf(cid)?.healthy ? t('tunnels.lbHealthy') : t('tunnels.lbUnhealthy') }}<template v-if="lbStatusOf(cid)?.latency_ms"> · {{ Math.round(lbStatusOf(cid)?.latency_ms || 0) }} ms</template>
                    </span>
                    <span v-else-if="lbForm.health_enabled" class="shrink-0 text-[11px] text-fg-subtle">{{ t('tunnels.lbNotChecked') }}</span>
                  </div>
                  <div class="flex shrink-0 items-center gap-2">
                    <span class="text-[11px] text-fg-subtle" :title="t('tunnels.lbInFlight')">{{ lbStatusOf(cid)?.in_flight ?? 0 }}</span>
                    <input v-if="lbForm.strategy === 'weighted'" type="number" min="0" max="1000" :value="lbWeight(cid)"
                           :aria-label="t('tunnels.lbWeight')"
                           class="w-16 rounded border border-line bg-bg px-2 py-1 text-right font-mono text-xs focus:border-accent"
                           @change="setLbWeight(cid, ($event.target as HTMLInputElement).value)">
                  </div>
                </li>
              </ul>

              <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
                <input v-model="lbForm.health_enabled" type="checkbox" class="accent-accent">
                {{ t('tunnels.lbHealthEnable') }}
              </label>

              <div v-if="lbForm.health_enabled" class="grid grid-cols-2 gap-2">
                <label v-if="accessTunnel?.proto !== 'tcp' && accessTunnel?.proto !== 'udp'" class="col-span-2 block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.lbHealthPath') }}</span>
                  <input v-model="lbForm.health_path" type="text" placeholder="/healthz" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.lbInterval') }}</span>
                  <input v-model.number="lbForm.interval_sec" type="number" min="2" max="3600" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.lbTimeout') }}</span>
                  <input v-model.number="lbForm.timeout_sec" type="number" min="1" max="30" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.lbUnhealthyThreshold') }}</span>
                  <input v-model.number="lbForm.unhealthy_threshold" type="number" min="1" max="10" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.lbHealthyThreshold') }}</span>
                  <input v-model.number="lbForm.healthy_threshold" type="number" min="1" max="10" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                </label>
                <p class="col-span-2 text-[11px] text-fg-subtle">{{ t('tunnels.lbPanicHint') }}</p>
              </div>

              <div class="flex justify-end gap-2">
                <button class="rounded border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2" :disabled="lbLoading" @click="accessTunnel && loadLB(accessTunnel.id)">
                  {{ t('tunnels.lbRefresh') }}
                </button>
                <button :disabled="lbSaving" class="rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveLB">
                  {{ lbSaving ? t('common.saving') : t('common.save') }}
                </button>
              </div>
            </template>
          </div>
        </div>

        <!-- TRAFİK POLİTİKASI -->
        <div v-else-if="settingsTab === 'traffic'" class="space-y-4">
          <p class="text-xs text-fg-muted">{{ t('tunnels.trafficIntro') }}</p>
          <p v-if="trafficLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
          <template v-else>
            <label class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
              <span class="text-sm text-fg">{{ t('tunnels.trafficEnabled') }}</span>
              <input v-model="trafficForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
            </label>

            <!-- Yanıt başlıkları -->
            <div class="space-y-2">
              <span class="label-sys block">{{ t('tunnels.trafficRespSet') }}</span>
              <div v-for="(row, i) in trafficForm.respSet" :key="'rs'+i" class="flex gap-2">
                <input v-model="row.name" placeholder="X-Frame-Options" class="w-1/3 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <input v-model="row.value" placeholder="DENY" class="flex-1 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="trafficForm.respSet.splice(i, 1)">
                  <Icon name="lucide:trash-2" class="size-3.5" />
                </button>
              </div>
              <button type="button" class="text-xs text-accent hover:underline" @click="trafficForm.respSet.push({ name: '', value: '' })">+ {{ t('common.add') }}</button>
              <div>
                <span class="label-sys mb-1 block">{{ t('tunnels.trafficRespRemove') }}</span>
                <input v-model="trafficForm.respRemove" placeholder="Server, X-Powered-By" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
              </div>
            </div>

            <!-- İstek başlıkları -->
            <div class="space-y-2">
              <span class="label-sys block">{{ t('tunnels.trafficReqSet') }}</span>
              <div v-for="(row, i) in trafficForm.reqSet" :key="'qs'+i" class="flex gap-2">
                <input v-model="row.name" placeholder="X-Forwarded-User" class="w-1/3 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <input v-model="row.value" placeholder="value" class="flex-1 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="trafficForm.reqSet.splice(i, 1)">
                  <Icon name="lucide:trash-2" class="size-3.5" />
                </button>
              </div>
              <button type="button" class="text-xs text-accent hover:underline" @click="trafficForm.reqSet.push({ name: '', value: '' })">+ {{ t('common.add') }}</button>
              <div>
                <span class="label-sys mb-1 block">{{ t('tunnels.trafficReqRemove') }}</span>
                <input v-model="trafficForm.reqRemove" placeholder="Cookie" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
              </div>
            </div>

            <!-- Yönlendirmeler -->
            <div class="space-y-2">
              <span class="label-sys block">{{ t('tunnels.trafficRedirects') }}</span>
              <div v-for="(row, i) in trafficForm.redirects" :key="'rd'+i" class="flex gap-2">
                <input v-model="row.match_prefix" placeholder="/eski" class="w-1/4 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <input v-model="row.location" placeholder="/yeni veya https://…" class="flex-1 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                <select v-model.number="row.status" class="w-20 rounded border border-line bg-bg px-1 py-1.5 font-mono text-xs focus:border-accent">
                  <option :value="301">301</option>
                  <option :value="302">302</option>
                  <option :value="307">307</option>
                  <option :value="308">308</option>
                </select>
                <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="trafficForm.redirects.splice(i, 1)">
                  <Icon name="lucide:trash-2" class="size-3.5" />
                </button>
              </div>
              <button type="button" class="text-xs text-accent hover:underline" @click="trafficForm.redirects.push({ match_prefix: '', location: '', status: 302 })">+ {{ t('common.add') }}</button>
            </div>

            <div class="flex justify-end">
              <button :disabled="trafficSaving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveTraffic">
                {{ trafficSaving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </template>
        </div>

        <!-- METRİKLER -->
        <!-- UDP + OYUN SUNUCUSU (FAZ 4 / F24) -->
        <div v-else-if="settingsTab === 'raw'" class="space-y-5">
          <template v-if="accessTunnel?.proto === 'udp'">
            <p class="text-xs text-fg-muted">{{ t('tunnels.rawIntro') }}</p>
            <p v-if="udpLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
            <template v-else>
              <!-- Canlı durum -->
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <div v-for="m in udpLiveCards" :key="m.key" class="rounded border border-line px-3 py-2">
                  <span class="label-sys block">{{ t('tunnels.rawStat_' + m.key) }}</span>
                  <span class="mt-0.5 block font-mono text-sm text-fg">{{ m.value }}</span>
                </div>
              </div>
              <p v-if="!udpLive" class="text-[11px] text-fg-subtle">{{ t('tunnels.rawNoListener') }}</p>

              <!-- Son 60 dakika -->
              <div v-if="udpSeries.length" class="space-y-1">
                <span class="label-sys block">{{ t('tunnels.rawSeriesTitle') }}</span>
                <div class="flex h-16 items-end gap-px rounded border border-line p-1">
                  <div v-for="p in udpSeries" :key="p.minute" class="flex-1 rounded-sm bg-accent/60"
                       :style="{ height: Math.max(4, Math.round((p.packets_in + p.packets_out) / udpSeriesMax * 100)) + '%' }"
                       :title="new Date(p.minute).toLocaleTimeString() + ' · ' + (p.packets_in + p.packets_out) + ' ' + t('tunnels.rawPackets') + (p.dropped_rate + p.dropped_size + p.dropped_flows ? ' · ' + (p.dropped_rate + p.dropped_size + p.dropped_flows) + ' ' + t('tunnels.rawDropped') : '')" />
                </div>
              </div>

              <!-- Sınırlar -->
              <div class="space-y-2 border-t border-line pt-3">
                <span class="label-sys block">{{ t('tunnels.rawLimitsTitle') }}</span>
                <div class="grid grid-cols-2 gap-2">
                  <label v-for="f in udpFields" :key="f" class="block">
                    <span class="label-sys mb-1 block">{{ t('tunnels.rawField_' + f) }}</span>
                    <input v-model.number="udpForm[f]" type="number" min="0" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
                  </label>
                </div>
                <p class="text-[11px] text-fg-subtle">{{ t('tunnels.rawLimitsHint') }}</p>
                <div class="flex justify-end gap-2">
                  <button class="rounded border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2" @click="resetUDP">{{ t('tunnels.rawReset') }}</button>
                  <button class="rounded border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2" @click="accessTunnel && loadUDP(accessTunnel.id)">{{ t('tunnels.lbRefresh') }}</button>
                  <button :disabled="udpSaving" class="rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveUDP">
                    {{ udpSaving ? t('common.saving') : t('common.save') }}
                  </button>
                </div>
              </div>
            </template>
          </template>

          <!-- Minecraft durum kartı -->
          <div class="space-y-2" :class="accessTunnel?.proto === 'udp' ? 'border-t border-line pt-3' : ''">
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2">
                <Icon name="lucide:gamepad-2" class="size-3.5 text-fg-muted" />
                <span class="label-sys">{{ accessTunnel?.proto === 'udp' ? t('tunnels.rawGameBedrock') : t('tunnels.rawGameJava') }}</span>
              </div>
              <button :disabled="gameLoading" class="rounded border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2 disabled:opacity-50" @click="probeGame">
                {{ gameLoading ? t('common.loading') : t('tunnels.rawGameCheck') }}
              </button>
            </div>
            <p class="text-[11px] text-fg-subtle">{{ t('tunnels.rawGameHint') }}</p>
            <div v-if="gameStatus" class="rounded border border-line p-3">
              <div class="flex items-center gap-2">
                <span class="size-2 shrink-0 rounded-full" :class="gameStatus.online ? 'bg-emerald-400' : 'bg-danger'" />
                <span class="text-sm font-medium text-fg">{{ gameStatus.online ? t('tunnels.rawGameOnline') : t('tunnels.rawGameOffline') }}</span>
                <span v-if="gameStatus.online && gameStatus.latency_ms" class="ml-auto font-mono text-xs text-fg-muted">{{ Math.round(gameStatus.latency_ms) }} ms</span>
              </div>
              <p v-if="gameStatus.error" class="mt-1 text-xs text-danger">{{ gameStatus.error }}</p>
              <template v-if="gameStatus.online">
                <p v-if="gameStatus.motd" class="mt-2 truncate text-xs text-fg">{{ gameStatus.motd }}</p>
                <dl class="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                  <dt class="text-fg-muted">{{ t('tunnels.rawGamePlayers') }}</dt>
                  <dd class="font-mono text-fg">{{ gameStatus.players_online }} / {{ gameStatus.players_max }}</dd>
                  <dt class="text-fg-muted">{{ t('tunnels.rawGameVersion') }}</dt>
                  <dd class="truncate font-mono text-fg">{{ gameStatus.version || '-' }}</dd>
                  <template v-if="gameStatus.game_mode">
                    <dt class="text-fg-muted">{{ t('tunnels.rawGameMode') }}</dt>
                    <dd class="font-mono text-fg">{{ gameStatus.game_mode }}</dd>
                  </template>
                </dl>
                <p v-if="gameStatus.players?.length" class="mt-2 text-[11px] text-fg-muted">{{ gameStatus.players.join(', ') }}</p>
              </template>
            </div>
          </div>
        </div>

        <div v-else-if="settingsTab === 'metrics'" class="space-y-4">
          <div class="flex items-center justify-between gap-2">
            <p class="text-xs text-fg-muted">{{ t('tunnels.metricsIntro') }}</p>
            <select v-model="metricsWindow" class="cursor-pointer rounded border border-line bg-bg px-2 py-1 text-xs focus:border-accent" @change="onMetricsWindowChange">
              <option value="1h">1s</option>
              <option value="6h">6s</option>
              <option value="24h">24s</option>
              <option value="7d">7g</option>
            </select>
          </div>

          <p v-if="metricsLoading" class="py-6 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
          <template v-else-if="metricsData">
            <!-- Özet -->
            <div class="grid grid-cols-3 gap-2">
              <div class="rounded border border-line p-3">
                <div class="text-lg font-bold text-fg">{{ metricsData.summary.total_requests }}</div>
                <div class="text-[11px] text-fg-muted">{{ t('tunnels.metricsTotal') }}</div>
              </div>
              <div class="rounded border border-line p-3">
                <div class="text-lg font-bold" :class="metricsData.summary.error_rate_pct > 5 ? 'text-danger' : 'text-fg'">{{ metricsData.summary.error_rate_pct.toFixed(1) }}%</div>
                <div class="text-[11px] text-fg-muted">{{ t('tunnels.metricsErrorRate') }}</div>
              </div>
              <div class="rounded border border-line p-3">
                <div class="text-lg font-bold text-fg">{{ Math.round(metricsData.summary.avg_ms) }}ms</div>
                <div class="text-[11px] text-fg-muted">{{ t('tunnels.metricsAvgLatency') }}</div>
              </div>
            </div>

            <!-- İstek grafiği (bucket başına; kırmızı = hata içeren dilim) -->
            <div v-if="metricsData.buckets && metricsData.buckets.length" class="rounded border border-line p-3">
              <div class="mb-2 text-[11px] text-fg-muted">{{ t('tunnels.metricsRequestsChart') }}</div>
              <div class="flex h-28 items-end gap-px">
                <div
                  v-for="(b, i) in metricsData.buckets"
                  :key="i"
                  class="flex-1 rounded-t"
                  :class="b.error_count > 0 ? 'bg-danger/70' : 'bg-accent/60'"
                  :style="{ height: Math.max(2, Math.round(b.count / metricsMaxCount * 100)) + '%' }"
                  :title="`${new Date(b.bucket).toLocaleString('tr-TR')} · ${b.count} istek · ${b.error_count} hata · ~${Math.round(b.avg_ms)}ms`"
                />
              </div>
              <div class="mt-2 flex items-center gap-3 text-[10px] text-fg-subtle">
                <span class="flex items-center gap-1"><span class="inline-block size-2 rounded-sm bg-accent/60" />{{ t('tunnels.metricsLegendOk') }}</span>
                <span class="flex items-center gap-1"><span class="inline-block size-2 rounded-sm bg-danger/70" />{{ t('tunnels.metricsLegendErr') }}</span>
                <span class="ml-auto">{{ t('tunnels.metricsMaxLatency', { ms: metricsData.summary.max_ms }) }}</span>
              </div>
            </div>
            <p v-else class="rounded border border-dashed border-line py-8 text-center text-xs text-fg-muted">{{ t('tunnels.metricsEmpty') }}</p>
          </template>
          <p v-else class="rounded border border-dashed border-line py-8 text-center text-xs text-fg-muted">{{ t('tunnels.metricsEmpty') }}</p>

          <!-- Uyarı (alert) — FAZ 6.4 -->
          <div class="space-y-3 border-t border-line pt-4">
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2">
                <span class="label-sys">{{ t('tunnels.alertTitle') }}</span>
                <span v-if="alertForm.enabled" class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium"
                      :class="alertState === 'firing' ? 'bg-danger/15 text-danger' : 'bg-emerald-500/15 text-emerald-500'">
                  <span class="size-1.5 rounded-full" :class="alertState === 'firing' ? 'bg-danger' : 'bg-emerald-500'" />
                  {{ alertState === 'firing' ? t('tunnels.alertFiring') : t('tunnels.alertOk') }}
                </span>
              </div>
              <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
                <span>{{ t('tunnels.alertEnable') }}</span>
                <input v-model="alertForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
              </label>
            </div>

            <template v-if="alertForm.enabled">
              <div class="grid grid-cols-3 gap-2">
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.alertThreshold') }}</span>
                  <input v-model.number="alertForm.error_rate_pct" type="number" min="1" max="100" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.alertWindow') }}</span>
                  <input v-model.number="alertForm.window_min" type="number" min="1" max="1440" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
                </label>
                <label class="block">
                  <span class="label-sys mb-1 block">{{ t('tunnels.alertMinReq') }}</span>
                  <input v-model.number="alertForm.min_requests" type="number" min="0" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
                </label>
              </div>
              <label class="block">
                <span class="label-sys mb-1 block">{{ t('tunnels.alertEmail') }}</span>
                <input v-model="alertForm.notify_email" type="email" placeholder="uyari@ornek.com" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
              </label>
              <p class="text-[11px] text-fg-subtle">{{ t('tunnels.alertHint') }}</p>
            </template>

            <div class="flex justify-end">
              <button :disabled="alertSaving || alertLoading || !isPrivileged" :title="isPrivileged ? undefined : t('common.adminOnly')" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveAlert">
                {{ alertSaving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </div>
        </div>

        <!-- ERİŞİM İSTATİSTİKLERİ -->
        <div v-else-if="settingsTab === 'stats'" class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="min-w-0 flex-1 text-xs text-fg-muted">{{ t('tunnels.statsIntro') }}</p>
            <select v-model="statsWindow" class="cursor-pointer rounded border border-line bg-bg px-2 py-1 text-xs focus:border-accent" :aria-label="t('tunnels.statsWindow')" @change="loadStats">
              <option value="24h">{{ t('tunnels.statsWin24h') }}</option>
              <option value="7d">{{ t('tunnels.statsWin7d') }}</option>
              <option value="30d">{{ t('tunnels.statsWin30d') }}</option>
            </select>
          </div>

          <p v-if="statsLoading && !statsSummary" class="py-6 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
          <p v-else-if="statsError" class="rounded border border-dashed border-line py-8 text-center text-xs text-danger">{{ t('tunnels.statsLoadFailed') }}</p>
          <template v-else-if="statsSummary">
            <div v-if="statsEmpty" class="flex flex-col items-center gap-2 rounded border border-dashed border-line px-4 py-8 text-center">
              <svg class="size-7 text-fg-subtle" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6l7-3z" /><path d="M9.5 12l1.8 1.8L15 10" /></svg>
              <p class="text-xs text-fg-muted">{{ accessForm.mode === 'none' ? t('tunnels.statsEmptyOff') : t('tunnels.statsEmptyNoData') }}</p>
              <button v-if="accessForm.mode === 'none' && isPrivileged" class="rounded border border-line px-3 py-1 text-xs text-fg transition-colors hover:border-accent hover:text-accent" @click="settingsTab = 'access'">{{ t('tunnels.statsGoAccess') }}</button>
            </div>
            <template v-else>
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
                <div v-for="tile in statsTiles" :key="tile.key" class="rounded border border-line p-3">
                  <div class="text-lg font-bold text-fg" :class="tile.cls">{{ tile.value }}</div>
                  <div class="text-[11px] text-fg-muted">{{ t('tunnels.statsTile_' + tile.key) }}</div>
                </div>
              </div>

              <div class="grid gap-3 sm:grid-cols-3">
                <div v-for="g in statsBreakdowns" :key="g.id" class="rounded border border-line p-3">
                  <div class="label-sys mb-2">{{ t('tunnels.statsBy_' + g.id) }}</div>
                  <p v-if="!g.rows.length" class="text-[11px] text-fg-subtle">-</p>
                  <ul v-else class="space-y-1.5">
                    <li v-for="r in g.rows" :key="r.key">
                      <div class="flex items-center justify-between gap-2 text-[11px]">
                        <span class="min-w-0 truncate text-fg">{{ g.id === 'reason' ? statsReason(r.key) : statsLabel(g.id, r.key) }}</span>
                        <span class="shrink-0 font-mono text-fg-muted">{{ r.value }}</span>
                      </div>
                      <div class="mt-0.5 h-1.5 overflow-hidden rounded-full bg-surface-2">
                        <div class="h-full rounded-full" :class="g.id === 'reason' && r.key !== 'ok' ? 'bg-danger/70' : 'bg-accent/70'" :style="{ width: r.pct + '%' }" />
                      </div>
                    </li>
                  </ul>
                </div>
              </div>

              <div class="rounded border border-line">
                <div class="label-sys border-b border-line px-3 py-2">{{ t('tunnels.statsRecent') }}</div>
                <p v-if="!statsEvents.length" class="px-3 py-6 text-center text-xs text-fg-muted">{{ t('tunnels.statsNoEvents') }}</p>
                <div v-else class="overflow-x-auto">
                  <table class="w-full min-w-[560px] text-left text-xs">
                    <thead class="text-[10px] uppercase tracking-wide text-fg-subtle">
                      <tr>
                        <th class="px-3 py-1.5 font-medium">{{ t('tunnels.statsColTime') }}</th>
                        <th class="px-2 py-1.5 font-medium">{{ t('tunnels.statsColMethod') }}</th>
                        <th class="px-2 py-1.5 font-medium">{{ t('tunnels.statsColIdentity') }}</th>
                        <th class="px-2 py-1.5 font-medium">{{ t('tunnels.statsColResult') }}</th>
                        <th class="px-2 py-1.5 font-medium">{{ t('tunnels.statsColIp') }}</th>
                        <th class="px-3 py-1.5 font-medium">{{ t('tunnels.statsColAgent') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="ev in statsEvents" :key="ev.id" class="border-t border-line">
                        <td class="whitespace-nowrap px-3 py-1.5 text-fg-muted" :title="new Date(ev.created_at).toLocaleString()">{{ relativeTime(ev.created_at) }}</td>
                        <td class="whitespace-nowrap px-2 py-1.5 text-fg">{{ statsLabel('method', ev.method) }}<span v-if="ev.provider" class="text-fg-muted"> / {{ statsLabel('provider', ev.provider) }}</span></td>
                        <td class="max-w-[160px] truncate px-2 py-1.5 font-mono text-fg" :title="ev.identity">{{ ev.identity || '-' }}</td>
                        <td class="px-2 py-1.5">
                          <span class="inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 text-[10px] font-medium" :class="ev.success ? 'bg-emerald-500/15 text-emerald-500' : 'bg-danger/15 text-danger'">
                            <span class="size-1.5 rounded-full" :class="ev.success ? 'bg-emerald-500' : 'bg-danger'" />
                            {{ ev.success ? t('tunnels.statsOk') : t('tunnels.statsFail') }}
                          </span>
                          <div v-if="!ev.success" class="mt-0.5 text-[10px] text-fg-muted">{{ statsReason(ev.reason) }}</div>
                        </td>
                        <td class="whitespace-nowrap px-2 py-1.5 font-mono text-fg-muted">{{ ev.client_ip || '-' }}</td>
                        <td class="whitespace-nowrap px-3 py-1.5 text-fg-muted" :title="ev.user_agent">{{ shortUA(ev.user_agent) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <div v-if="statsHasMore" class="border-t border-line p-2 text-center">
                  <button :disabled="statsMoreLoading" class="rounded border border-line px-3 py-1 text-xs text-fg transition-colors hover:border-accent hover:text-accent disabled:opacity-50" @click="loadMoreStats">
                    {{ statsMoreLoading ? t('common.loading') : t('tunnels.statsLoadMore') }}
                  </button>
                </div>
              </div>
            </template>
          </template>
        </div>

        <!-- ERİŞİM -->
        <div v-else-if="settingsTab === 'access'" class="space-y-4">
          <p v-if="isRawTunnel" class="rounded border border-line bg-surface-1 p-2.5 text-xs text-fg-muted">{{ t('tunnels.accessIntroRaw') }}</p>
          <p v-else class="text-xs text-fg-muted">{{ t('tunnels.accessIntro') }}</p>
          <!-- Mod seçimi -->
          <div class="space-y-2">
            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="accessForm.mode === 'none' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="accessForm.mode" type="radio" value="none" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.accessModeNone') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.accessModeNoneHint')">?</span>
            </label>

            <label class="flex cursor-pointer items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="accessForm.mode === 'basic' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'">
              <span class="flex items-center gap-2.5">
                <input v-model="accessForm.mode" type="radio" value="basic" class="accent-[var(--color-accent)]">
                <span class="text-sm font-medium text-fg">{{ t('tunnels.accessModeBasic') }}</span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.accessModeBasicHint')">?</span>
            </label>

            <label class="flex items-center justify-between gap-2.5 rounded border p-2.5 transition-colors"
                   :class="[accessForm.mode === 'oauth' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle', anyOAuthProvider ? 'cursor-pointer' : 'cursor-not-allowed opacity-60']">
              <span class="flex items-center gap-2.5">
                <input v-model="accessForm.mode" type="radio" value="oauth" :disabled="!anyOAuthProvider" class="accent-[var(--color-accent)]">
                <span>
                  <span class="block text-sm font-medium text-fg">{{ t('tunnels.accessModeOauth') }}</span>
                  <span v-if="!anyOAuthProvider" class="block text-xs text-amber-700 dark:text-amber-400">{{ t('tunnels.accessOAuthUnavailable') }}</span>
                </span>
              </span>
              <span class="help-tip" tabindex="0" :data-tip="t('tunnels.accessModeOauthHint')">?</span>
            </label>
          </div>

          <!-- OAuth alanları -->
          <div v-if="accessForm.mode === 'oauth'" class="space-y-3 rounded border border-line bg-surface-1 p-3">
            <div>
              <span class="label-sys mb-1.5 block">{{ t('tunnels.accessProviders') }}</span>
              <div class="flex flex-wrap gap-3">
                <label v-if="googleEnabled" class="flex cursor-pointer items-center gap-1.5 text-sm text-fg">
                  <input v-model="accessForm.providers" type="checkbox" value="google" class="accent-[var(--color-accent)]"> Google
                </label>
                <label v-if="githubEnabled" class="flex cursor-pointer items-center gap-1.5 text-sm text-fg">
                  <input v-model="accessForm.providers" type="checkbox" value="github" class="accent-[var(--color-accent)]"> GitHub
                </label>
              </div>
            </div>
            <div>
              <label for="accessEmails" class="label-sys mb-1 block">{{ t('tunnels.accessAllowedEmails') }}</label>
              <textarea id="accessEmails" v-model="accessForm.allowedEmails" rows="3"
                        placeholder="ali@ornek.com&#10;veli@ornek.com"
                        class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs placeholder:text-fg-subtle focus:border-accent"></textarea>
              <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunnels.accessAllowedEmailsHint') }}</p>
            </div>
          </div>

          <!-- Basic Auth alanları -->
          <div v-if="accessForm.mode === 'basic'" class="space-y-3 rounded border border-line bg-surface-1 p-3">
            <div>
              <label for="accessUser" class="label-sys mb-1 block">{{ t('tunnels.accessUsername') }}</label>
              <input id="accessUser" v-model="accessForm.username" autocomplete="off"
                     class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm focus:border-accent">
            </div>
            <div>
              <label for="accessPass" class="label-sys mb-1 block">{{ t('tunnels.accessPassword') }}</label>
              <input id="accessPass" v-model="accessForm.password" type="password" autocomplete="new-password"
                     :placeholder="accessForm.hasPassword ? t('tunnels.accessPassKeep') : ''"
                     class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm placeholder:text-fg-subtle focus:border-accent">
              <p v-if="accessForm.hasPassword" class="mt-1 text-[11px] text-fg-subtle">{{ t('tunnels.accessPassKeepHint') }}</p>
            </div>
          </div>

          <!-- Etkin toggle (none dışında) -->
          <label v-if="accessForm.mode !== 'none'" class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
            <span class="text-sm text-fg">{{ t('tunnels.accessEnabled') }}</span>
            <input v-model="accessForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
          </label>

          <div class="flex justify-end">
            <button
              :disabled="accessSaving || accessLoading"
              class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50"
              @click="saveAccess"
            >
              {{ accessSaving ? t('common.saving') : t('common.save') }}
            </button>
          </div>

          <!-- Web ile kapı açma (ham TCP/UDP) -->
          <div v-if="isRawTunnel" class="space-y-3 border-t border-line pt-4">
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2">
                <Icon name="lucide:door-open" class="size-4 text-fg-muted" />
                <span class="label-sys">{{ t('tunnels.doorTitle') }}</span>
                <span class="help-tip" tabindex="0" :data-tip="t('tunnels.doorHint')">?</span>
              </div>
            </div>

            <p v-if="doorLoading && !doorInfo" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>

            <template v-else-if="doorInfo">
              <label class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
                <span class="text-sm text-fg">{{ t('tunnels.doorEnable') }}</span>
                <input v-model="doorForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
              </label>
              <p v-if="doorForm.enabled && !doorInfo.access_ready" class="text-xs text-amber-700 dark:text-amber-400">{{ t('tunnels.doorNeedAccess') }}</p>

              <div>
                <span class="label-sys mb-1.5 block">{{ t('tunnels.doorDuration') }}</span>
                <div class="grid grid-cols-4 gap-2">
                  <label v-for="sec in doorInfo.durations" :key="sec"
                         class="flex cursor-pointer items-center justify-center rounded border px-2 py-1.5 text-xs transition-colors"
                         :class="doorForm.duration === sec ? 'border-accent bg-accent/5 text-fg' : 'border-line text-fg-muted hover:border-fg-subtle'">
                    <input v-model="doorForm.duration" type="radio" :value="sec" class="sr-only">
                    {{ t('tunnels.' + (doorDurationKeys[sec] || 'doorDur12h')) }}
                  </label>
                </div>
              </div>

              <div v-if="doorInfo.enabled && doorInfo.url" class="space-y-2 rounded border border-line bg-surface-1 p-3">
                <span class="label-sys block">{{ t('tunnels.doorUrl') }}</span>
                <div class="flex items-center gap-2">
                  <code class="min-w-0 flex-1 truncate rounded bg-bg px-2 py-1 font-mono text-xs text-fg">{{ doorInfo.url }}</code>
                  <button class="inline-flex shrink-0 items-center gap-1 rounded border border-line px-2 py-1 text-xs text-fg-muted hover:bg-surface-2 hover:text-fg" @click="copyDoorUrl">
                    <Icon :name="doorUrlCopied ? 'lucide:check' : 'lucide:copy'" class="size-3.5" /> {{ t('tunnels.doorCopy') }}
                  </button>
                </div>
                <p class="text-[11px] text-fg-subtle">{{ t('tunnels.doorUrlHint') }}</p>
                <p v-if="doorInfo.connect_address" class="text-xs text-fg-muted">
                  {{ t('tunnels.doorConnect') }}: <span class="font-mono text-fg">{{ doorInfo.connect_address }}</span>
                </p>
              </div>

              <div class="flex justify-end">
                <button :disabled="doorSaving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveDoor">
                  {{ doorSaving ? t('common.saving') : t('tunnels.doorSave') }}
                </button>
              </div>

              <!-- Aktif izinler -->
              <div v-if="doorInfo.enabled" class="space-y-2">
                <div class="flex items-center justify-between">
                  <span class="label-sys">{{ t('tunnels.doorGrants') }}</span>
                  <button class="inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-[11px] text-fg-muted hover:bg-surface-2 hover:text-fg" @click="refreshDoorGrants">
                    <Icon name="lucide:refresh-cw" class="size-3" /> {{ t('tunnels.doorRefresh') }}
                  </button>
                </div>
                <p v-if="!doorGrants.length" class="rounded border border-dashed border-line py-3 text-center text-xs text-fg-muted">{{ t('tunnels.doorGrantsEmpty') }}</p>
                <ul v-else class="divide-y divide-line rounded border border-line">
                  <li v-for="g in doorGrants" :key="g.id" class="flex items-center justify-between gap-2 px-3 py-2">
                    <div class="min-w-0">
                      <span class="font-mono text-xs text-fg">{{ g.ip }}</span>
                      <span v-if="g.identity" class="ml-2 truncate text-[11px] text-fg-muted">{{ g.identity }}</span>
                      <p class="text-[11px] text-fg-subtle">{{ t('tunnels.doorGrantUntil') }}: {{ new Date(g.expires_at).toLocaleString() }}</p>
                    </div>
                    <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :title="t('tunnels.doorRevoke')" :aria-label="t('tunnels.doorRevoke')" @click="revokeGrant(g)">
                      <Icon name="lucide:x" class="size-3.5" />
                    </button>
                  </li>
                </ul>
              </div>
            </template>
          </div>

          <!-- mTLS (istemci sertifikası) — FAZ 6.6 (ham TCP/UDP'de yok) -->
          <div v-if="!isRawTunnel" class="space-y-3 border-t border-line pt-4">
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2">
                <span class="label-sys">{{ t('tunnels.mtlsTitle') }}</span>
                <span v-if="mtlsForm.enabled && mtlsHasCa" class="inline-flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-500">
                  <Icon name="lucide:shield-check" class="size-3" /> {{ t('tunnels.mtlsActive') }}
                </span>
                <span class="help-tip" tabindex="0" :data-tip="t('tunnels.mtlsHint')">?</span>
              </div>
              <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
                <span>{{ t('tunnels.mtlsEnable') }}</span>
                <input v-model="mtlsForm.enabled" type="checkbox" class="accent-[var(--color-accent)]">
              </label>
            </div>
            <template v-if="mtlsForm.enabled">
              <div>
                <span class="label-sys mb-1 block">{{ t('tunnels.mtlsCa') }}</span>
                <textarea v-model="mtlsForm.ca_pem" rows="5" placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
                          class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-[11px] placeholder:text-fg-subtle focus:border-accent"></textarea>
                <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunnels.mtlsCaHint') }}</p>
              </div>
            </template>
            <div class="flex justify-end">
              <button :disabled="mtlsSaving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="saveMTLS">
                {{ mtlsSaving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </div>
          </div>
          </div>
          </div>
        </div>

        <div class="flex justify-end border-t border-line p-4">
          <button class="rounded border border-line px-3 py-1.5 text-sm text-fg-muted hover:text-fg" @click="accessTunnel = null">
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Sekme geçişi: yeni sekme (key değişince yeniden mount) yumuşak belirir. */
.tabpane {
  animation: tabIn 0.18s ease;
}
@keyframes tabIn {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

/* Ayar açıklaması: sağda "?" ikonu, üzerine gelince tooltip (FAZ 6 UI isteği). */
.help-tip {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 9999px;
  border: 1px solid var(--line);
  color: var(--fg-subtle);
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  cursor: help;
  flex: 0 0 auto;
  user-select: none;
}
.help-tip:hover,
.help-tip:focus-visible {
  color: var(--accent);
  border-color: var(--accent);
  outline: none;
}
.help-tip::after {
  content: attr(data-tip);
  position: absolute;
  right: calc(100% + 8px);
  top: 50%;
  width: max-content;
  max-width: min(260px, 60vw);
  background: var(--fg);
  color: var(--bg);
  padding: 8px 10px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 400;
  line-height: 1.5;
  text-align: left;
  opacity: 0;
  visibility: hidden;
  transform: translateY(-50%) translateX(4px);
  transition: opacity 0.15s ease, transform 0.15s ease;
  z-index: 30;
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.28);
  white-space: normal;
}
.help-tip:hover::after,
.help-tip:focus-visible::after {
  opacity: 1;
  visibility: visible;
  transform: translateY(-50%) translateX(0);
}

/* Modal açılışı: yumuşak beliriş. */
.modal-pop {
  animation: modalPop 0.18s ease-out;
}
@keyframes modalPop {
  from { opacity: 0; transform: scale(0.98); }
  to { opacity: 1; transform: scale(1); }
}
@media (prefers-reduced-motion: reduce) {
  .tabpane,
  .modal-pop {
    transition: none;
    animation: none;
  }
}
</style>
