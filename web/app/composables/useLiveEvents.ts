/**
 * Panel canli olay akisi (SSE, GET /api/v1/events) - sekme basina TEK baglanti.
 *
 *  - useLiveEvents().on(tip|tipler, fn): olaya abone ol; donen fonksiyon aboneligi kapatir.
 *    Tip "app.*" gibi on ek ile de verilebilir. Ilk abone baglantiyi acar, son abone
 *    ayrilinca (5 sn sonra) kapanir.
 *  - Baglanti koparsa ussel geri cekilme + tam jitter (500 ms .. 60 sn) ile yeniden baglanir.
 *  - connected: akis ayakta mi (sayfalar yoklamayi buna gore seyreltir).
 *  - Proje / "Yonetime Gec" kiraci degisince baglanti yeni basliklarla yenilenir.
 *
 * useLiveRefresh(fn, opts): sayfalarin yenileme dongusu. Olay gelince (debounce) yeniler;
 * SSE ayaktayken yalniz yavas guvenlik agi (vars. 60 sn), degilken eski aralik (fallbackMs).
 * Hata halinde ussel bekleme (tavan 60 sn), sekme gizliyken durur, gorunur olunca yeniler.
 */
import type { Ref } from 'vue'

type Handler = (data: any, type: string) => void

const SAFETY_MS = 60_000
const BACKOFF_CAP_MS = 60_000

interface Hub {
  connected: Ref<boolean>
  on: (types: string | string[], fn: Handler) => () => void
}

let hub: Hub | null = null

function createHub(): Hub {
  const connected = ref(false)
  const subs = new Map<number, { types: string[], fn: Handler }>()
  let nextId = 1
  let ctrl: AbortController | null = null
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let idleTimer: ReturnType<typeof setTimeout> | null = null
  let fails = 0
  let running = false
  let watching = false

  function dispatch(type: string, data: unknown) {
    for (const s of subs.values()) {
      for (const t of s.types) {
        if (t === type || (t.endsWith('*') && type.startsWith(t.slice(0, -1)))) {
          try { s.fn(data, type) } catch { /* abone hatasi akisi bozmasin */ }
          break
        }
      }
    }
  }

  async function connectOnce(): Promise<void> {
    const api = useApi()
    ctrl = new AbortController()
    const startedAt = Date.now()
    try {
      await api.openEventStream((type, raw) => {
        let data: unknown = null
        try { data = raw ? JSON.parse(raw) : null } catch { return }
        dispatch(type, data)
      }, ctrl.signal, () => { connected.value = true })
    } finally {
      connected.value = false
      // Uzun omurlu baglanti saglikli sayilir; hemen dusenler geri cekilmeyi artirir.
      if (Date.now() - startedAt > 10_000) fails = 0
    }
  }

  async function loop() {
    if (running) return
    running = true
    while (subs.size > 0) {
      try {
        await connectOnce()
      } catch { /* ag/HTTP hatasi: asagida beklenir */ }
      if (subs.size === 0) break
      fails++
      const cap = Math.min(BACKOFF_CAP_MS, 1000 * 2 ** Math.min(fails, 10))
      const delay = Math.max(500, Math.random() * cap)
      await new Promise<void>((res) => { retryTimer = setTimeout(res, delay) })
      retryTimer = null
    }
    running = false
  }

  function reconnectNow() {
    // Baslik degisti: mevcut baglantiyi kes, dongu hemen yeniden baglanir.
    fails = 0
    ctrl?.abort()
  }

  function ensureWatch() {
    if (watching) return
    watching = true
    const { activeProject } = useActiveProject()
    const { adminTenant } = useAdminTenant()
    const scope = effectScope(true)
    scope.run(() => {
      watch([activeProject, adminTenant], () => { if (subs.size > 0) reconnectNow() })
    })
  }

  function on(types: string | string[], fn: Handler): () => void {
    if (import.meta.server) return () => {}
    const id = nextId++
    subs.set(id, { types: Array.isArray(types) ? types : [types], fn })
    if (idleTimer) { clearTimeout(idleTimer); idleTimer = null }
    ensureWatch()
    void loop()
    return () => {
      subs.delete(id)
      if (subs.size === 0 && !idleTimer) {
        idleTimer = setTimeout(() => {
          idleTimer = null
          if (subs.size === 0) {
            ctrl?.abort()
            connected.value = false
          }
        }, 5000)
      }
    }
  }

  return { connected, on }
}

export function useLiveEvents(): Hub {
  if (!hub) hub = createHub()
  return hub
}

export interface LiveRefreshOptions {
  /** Yenilemeyi tetikleyen SSE olay tipleri ("app.*" on eki desteklenir). Bos: yalniz yoklama. */
  events?: string[]
  /** SSE yokken (veya events bos) yoklama araligi, ms. */
  fallbackMs: number
  /** SSE ayaktayken guvenlik agi araligi, ms (vars. 60 sn). */
  safetyMs?: number
  /** true donerse yenileme o tur atlanir (or. kayit sirasinda). */
  skip?: () => boolean
}

/**
 * fn: yenileme. Hata firlatirsa veya false donerse "basarisiz" sayilir (ussel bekleme).
 * Bilesen setup'inda SENKRON cagrilmali (temizlik onBeforeUnmount'a baglanir).
 * Ilk yuklemeyi cagiran yapar; bu yalniz tekrar eden yenilemeyi yonetir.
 */
export function useLiveRefresh(fn: () => unknown | Promise<unknown>, opts: LiveRefreshOptions) {
  const live = useLiveEvents()
  const safety = opts.safetyMs ?? SAFETY_MS
  const hasEvents = !!opts.events?.length
  let timer: ReturnType<typeof setTimeout> | null = null
  let debounce: ReturnType<typeof setTimeout> | null = null
  let fails = 0
  let busy = false
  let stopped = false
  let offEvents: (() => void) | null = null

  const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden'

  function baseMs(): number {
    return hasEvents && live.connected.value ? Math.max(safety, opts.fallbackMs) : opts.fallbackMs
  }
  function nextDelay(): number {
    const base = baseMs()
    const backed = Math.min(base * 2 ** Math.min(fails, 8), Math.max(BACKOFF_CAP_MS, base))
    return backed * (1 + Math.random() * 0.1)
  }
  function schedule() {
    if (stopped) return
    if (timer) clearTimeout(timer)
    timer = setTimeout(tick, nextDelay())
  }
  async function run() {
    if (busy || stopped || hidden()) return
    if (opts.skip?.()) return
    busy = true
    try {
      const r = await fn()
      fails = r === false ? fails + 1 : 0
    } catch {
      fails++
    } finally {
      busy = false
    }
  }
  async function tick() {
    timer = null
    await run() // gizliyken run() hicbir sey yapmaz; dongu yalniz yeniden kurulur
    schedule()
  }
  function onVisible() {
    if (stopped || hidden()) return
    // Gizliyken kacan yenilemeyi hemen yap, sonra donguyu yeniden kur.
    void run().then(schedule)
  }

  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', onVisible)
    schedule()
    if (hasEvents) {
      offEvents = live.on(opts.events!, () => {
        if (hidden()) return // gorunur olunca onVisible yeniler
        if (debounce) clearTimeout(debounce)
        debounce = setTimeout(() => { debounce = null; void run() }, 400)
      })
    }
  }
  // SSE durumu degisince bekleyen zamanlayiciyi yeni araliga gore yeniden kur.
  const stopWatch = watch(live.connected, () => { if (hasEvents && !busy) schedule() })

  function stop() {
    stopped = true
    if (timer) clearTimeout(timer)
    if (debounce) clearTimeout(debounce)
    offEvents?.()
    stopWatch()
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisible)
  }
  onBeforeUnmount(stop)
  return { stop, trigger: () => run() }
}
