/**
 * Ekran performans katmani: ajanin saniyelik olcum ozeti (screen2_stats) ve
 * "darbogaz ipucu" mantigi. Saf fonksiyonlar; DOM/Vue bagimliligi yok.
 */

/** Bir boru hatti asamasinin pencere ozeti (ms). */
export interface AgentStageStat { avg: number, p95: number, n: number }

/** Ajanin katman basina gercek kalite/hedef fps degeri (eski ajanda yok). */
export interface AgentLayerStat {
  id: number
  /** Gercekte kullanilan kalite: katman spec'i ile uyarlama kalitesinin asgarisi (geri bildirim indirimi dusulmus). */
  quality: number
  /** Katmanin kalite tavani (sunucunun istedigi); 0 => tavan yok. */
  spec_quality: number
  target_fps: number
}

/** Sunucunun bu izleyiciye teslim ozeti (stats mesajina sunucu ekler). */
export interface ServerDelivery {
  /** Izleyiciye teslim edilen kare/sn (kisit yoksa ajanin geldigi hiz). */
  fps: number
  /** Pencerede atlanan/birlestirilen kare. */
  skipped: number
  /** Bekleyen verinin yasi (ms). */
  queue_ms: number
  /** Ajan geri bildirimi (screen2_feedback) etkin mi: ajan bu bilgiyle fps/kaliteyi ayarlar. */
  feedback: boolean
}

/** Ajanin ~1 sn'lik penceresi (sunucu "stats" mesaji olarak iletir). */
export interface AgentStats {
  window_ms: number
  /** Gercekten gonderilen kare/sn ve yakalanan tur/sn. */
  fps: number
  capture_fps: number
  target_fps: number
  max_fps: number
  quality: number
  /** Yuvada birlestirilen (atlanan ara) is sayisi; degisim olmadigi icin atlanan tur. */
  merged: number
  idle: number
  kbps: number
  /** dxgi | gdi | kbinani | darwin ... */
  method: string
  src_w: number
  src_h: number
  w: number
  h: number
  capture: AgentStageStat
  scale: AgentStageStat
  diff: AgentStageStat
  encode: AgentStageStat
  pack: AgentStageStat
  write: AgentStageStat
  /** Katmanli yayinda bu izleyicinin katmani; -1 katmansiz. */
  layer: number
  layers: AgentLayerStat[]
  srv: ServerDelivery | null
  /** Anlik akis modu: tiles | video (eski ajanda bos => tiles). */
  mode: string
  /** Video modunda gonderilen kbit/sn, kodlayici hedef bit hizi (bit/sn) ve kodlayici adi. */
  video_kbps: number
  video_bitrate: number
  backend: string
}

/** Tarayici tarafi olcumler (useScreen saniyede bir uretir). */
export interface BrowserPerf {
  recvFps: number
  drawFps: number
  decodeMs: number
  queueDepth: number
  recvKBps: number
}

export const STAGE_KEYS = ['capture', 'scale', 'diff', 'encode', 'pack', 'write'] as const
export type StageKey = typeof STAGE_KEYS[number]

/** none: saglikli; idle: ekran degismiyor; browser/network/<asama>: sinirlayici. */
export type BottleneckKind = 'none' | 'idle' | 'browser' | 'viewer' | 'network' | 'capture' | 'captureGdi' | 'captureSrc' | 'scale' | 'diff' | 'encode' | 'pack'

export interface Bottleneck {
  kind: BottleneckKind
  /** Vurgulanacak asama (her zaman en yavas asama; saglikli olsa da). */
  slowest: StageKey | null
  /** Sinirlayici asama suresi / hedef kare suresi (0..). */
  load: number
  /** viewer ipucu: ajanin gonderdigi / izleyiciye ulasan kare hizi. */
  sent?: number
  got?: number
}

/** Ajan paketini guvenle cozer (eksik/yanlis alanlar 0 olur); gecersizse null. */
export function parseAgentStats(raw: unknown): AgentStats | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Record<string, unknown>
  const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : 0)
  const stage = (v: unknown): AgentStageStat => {
    const o = (v && typeof v === 'object' ? v : {}) as Record<string, unknown>
    return { avg: num(o.avg), p95: num(o.p95), n: num(o.n) }
  }
  return {
    window_ms: num(r.window_ms),
    fps: num(r.fps),
    capture_fps: num(r.capture_fps),
    target_fps: num(r.target_fps),
    max_fps: num(r.max_fps),
    quality: num(r.quality),
    merged: num(r.merged),
    idle: num(r.idle),
    kbps: num(r.kbps),
    method: typeof r.method === 'string' ? r.method.slice(0, 32) : '',
    src_w: num(r.src_w),
    src_h: num(r.src_h),
    w: num(r.w),
    h: num(r.h),
    capture: stage(r.capture),
    scale: stage(r.scale),
    diff: stage(r.diff),
    encode: stage(r.encode),
    pack: stage(r.pack),
    write: stage(r.write),
    layer: typeof r.layer === 'number' ? r.layer : -1,
    layers: parseLayers(r.layers),
    srv: parseDelivery(r.srv),
    mode: r.mode === 'video' ? 'video' : 'tiles',
    video_kbps: num(r.video_kbps),
    video_bitrate: num(r.video_bitrate),
    backend: typeof r.backend === 'string' ? r.backend.slice(0, 32) : '',
  }
}

function parseLayers(v: unknown): AgentLayerStat[] {
  if (!Array.isArray(v)) return []
  const num = (x: unknown) => (typeof x === 'number' && Number.isFinite(x) && x >= 0 ? x : 0)
  const out: AgentLayerStat[] = []
  for (const e of v.slice(0, 4)) {
    if (!e || typeof e !== 'object') continue
    const o = e as Record<string, unknown>
    out.push({
      id: typeof o.id === 'number' ? o.id : -1,
      quality: num(o.quality),
      spec_quality: num(o.spec_quality),
      target_fps: num(o.target_fps),
    })
  }
  return out
}

function parseDelivery(v: unknown): ServerDelivery | null {
  if (!v || typeof v !== 'object') return null
  const o = v as Record<string, unknown>
  const num = (x: unknown) => (typeof x === 'number' && Number.isFinite(x) && x >= 0 ? x : 0)
  return { fps: num(o.fps), skipped: num(o.skipped), queue_ms: num(o.queue_ms), feedback: o.feedback === true }
}

function ownLayer(a: AgentStats): AgentLayerStat | undefined {
  return a.layers.find(x => x.id === a.layer) ?? (a.layer < 0 ? a.layers[0] : undefined)
}

/**
 * "Kalite" satiri icin gercek kullanilan kalite ve (varsa) tavani. Katmanli
 * yayinda izleyicinin katmaninin girdisi; ajan katman bilgisi gondermiyorsa
 * (eski ajan) genel uyarlama kalitesi, tavan bilinmez (null).
 */
export function effectiveQuality(a: AgentStats): { value: number, ceiling: number | null } {
  const l = ownLayer(a)
  if (!l || !l.quality) return { value: a.quality, ceiling: null }
  const ceiling = l.spec_quality > 0 ? l.spec_quality : null
  return { value: l.quality, ceiling }
}

/** Bu izleyicinin katmaninin ajan tarafindaki hedef fps'i (yoksa genel hedef). */
export function layerTargetFps(a: AgentStats): number {
  const l = ownLayer(a)
  return l && l.target_fps > 0 ? l.target_fps : a.target_fps
}

/**
 * Yakalama asamasi icin "normal" sure butcesi (ms): kaynak cozunurlugune
 * orantili (~3 ms/Mpiksel, en az 8 ms). 4K'da (8.3 Mpx) ~25 ms'ye kadar normal.
 */
export function captureBudgetMs(srcW: number, srcH: number): number {
  const mpx = (srcW * srcH) / 1e6
  return Math.max(8, 3 * mpx)
}

/** En yavas asama (ortalama sureye gore); hic olcum yoksa null. */
export function slowestStage(a: AgentStats): StageKey | null {
  let best: StageKey | null = null
  let bv = 0
  for (const k of STAGE_KEYS) {
    if (a[k].avg > bv) {
      bv = a[k].avg
      best = k
    }
  }
  return best
}

/**
 * Darbogaz ipucu.
 *
 * Boru hatti iki asamadir: A = yakalama+olcekleme+fark, B = kodlama+paketleme+
 * yazma; asamalar paralel calisir, kare hizini YAVAS olan sinirlar. Hedef kare
 * suresine (1000/hedef fps) gore:
 *   - Gonderilen fps hedefe yakinsa (>= %90) ya da hicbir asama kare suresinin
 *     %60'ina varmiyorsa: sinirlayici yok. Hedefin altinda ama ajan bossa ve
 *     degisim olmayan tur varsa "idle" (ekran degismiyor, normal).
 *   - Tarayici geride kaliyorsa (cizilen << alinan ya da kuyruk >= 4) "browser".
 *   - Aksi halde en yavas asama; yazma asamasi "network" olarak adlandirilir.
 */
export function findBottleneck(a: AgentStats | null, b: BrowserPerf | null): Bottleneck {
  const none = (slowest: StageKey | null = null): Bottleneck => ({ kind: 'none', slowest, load: 0 })
  const browserSlow = !!b && b.recvFps > 2 && (b.queueDepth >= 4 || b.drawFps < b.recvFps * 0.7)
  if (!a) return browserSlow ? { kind: 'browser', slowest: null, load: 0 } : none()
  const slowest = slowestStage(a)
  const target = layerTargetFps(a)
  const interval = 1000 / Math.max(target, 1)
  const pipeA = a.capture.avg + a.scale.avg + a.diff.avg
  const pipeB = a.encode.avg + a.pack.avg + a.write.avg
  const load = Math.max(pipeA, pipeB) / interval
  if (browserSlow) return { kind: 'browser', slowest, load }
  // Ag/izleyici: ajan gonderiyor ama izleyiciye daha az ulasiyor. Sunucu verisi
  // varsa o (atlanan/kuyruk yasi), yoksa tarayicinin aldigi hiz kullanilir.
  const sent = a.fps
  if (a.srv && a.srv.fps > 0 && sent >= 5 && (a.srv.skipped > 0 || a.srv.queue_ms > 300) && a.srv.fps < sent * 0.8) {
    return { kind: 'viewer', slowest, load, sent, got: a.srv.fps }
  }
  if (!a.srv && b && b.recvFps > 0 && sent >= 5 && b.recvFps < sent * 0.7) {
    return { kind: 'viewer', slowest, load, sent, got: b.recvFps }
  }
  if (a.fps >= target * 0.9) return { kind: 'none', slowest, load }
  if (load < 0.6) {
    // Ajan rahat ama fps dusuk: ekran degismedigi icin gonderilecek kare yok.
    return { kind: a.idle > 0 || a.capture_fps <= a.fps ? 'idle' : 'none', slowest, load }
  }
  if (!slowest) return none()
  // Hangi asama kare suresini asiyorsa: A tarafinin en yavasi ya da B tarafinin en yavasi.
  if (slowest === 'capture') {
    // Kaynak cozunurlugune gore normal sure icindeyse "yavas yakalama" suclamasi yapma;
    // GDI ipucu yalniz yontem GDI/kbinani iken (dxgi iken asla).
    if (a.capture.avg <= captureBudgetMs(a.src_w, a.src_h)) return { kind: 'captureSrc', slowest, load }
    const m = a.method.toLowerCase()
    return { kind: m === 'gdi' || m === 'kbinani' ? 'captureGdi' : 'capture', slowest, load }
  }
  const kind: BottleneckKind = slowest === 'write' ? 'network' : slowest
  return { kind, slowest, load }
}

/** Panel icin 2 ondalik gostermeyen kisa sayi. */
export function fmtMs(v: number): string {
  if (!v) return '—'
  return v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)
}
