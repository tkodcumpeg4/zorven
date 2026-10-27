/**
 * H.264 video modu (screen2_video) tarayici tarafi: cerceve ayristirma ve
 * WebCodecs VideoDecoder sarmalayicisi. DOM/Vue bagimliligi yok (canvas disaridan verilir).
 *
 * Tel bicimi (shared/protocol/screen2_video.go): 14 bayt BodyFrame basligi
 * (tip 9, streamID, seq, bayrak: bit1 anahtar kare) + tur (1 config | 2 chunk | 3 stop).
 *   config: w u16, h u16, codecLen u8, codec, description (avcC)
 *   chunk : timestamp_us u64, veri (AVCC, uzunluk onekli NAL)
 *   stop  : video durdu, akis karoya doner
 */

export type VideoPref = 'auto' | 'tiles' | 'video'

export const FRAME_VIDEO = 9
export const VIDEO_KIND_CONFIG = 1
export const VIDEO_KIND_CHUNK = 2
export const VIDEO_KIND_STOP = 3

const VIDEO_PREF_KEY = 'zorven.screen.video'

export function loadVideoPref(): VideoPref {
  try {
    const v = localStorage.getItem(VIDEO_PREF_KEY)
    if (v === 'auto' || v === 'tiles' || v === 'video') return v
  } catch { /* yok say */ }
  return 'auto'
}

export function saveVideoPref(p: VideoPref) {
  try { localStorage.setItem(VIDEO_PREF_KEY, p) } catch { /* yok say */ }
}

/** Panelin gosterdigi video durumu (perf katmani + secici). */
export interface VideoInfo {
  /** Ajan video modunu pazarlamis mi (sunucu caps.video). */
  available: boolean
  /** Bu tarayici WebCodecs ile H.264 cozebiliyor mu. */
  supported: boolean
  /** Sunucunun birlestirdigi etkin mod: tiles | auto | video. */
  effective: string
  /** Desteksiz izleyici sayisi (video_policy.unsupported). */
  unsupported: number
  /** Su an ekranda video mu gosteriliyor. */
  active: boolean
  codec: string
  w: number
  h: number
  /** Son penceredeki video kare/sn, kbit/sn ve ortalama cozme suresi (ms). */
  fps: number
  kbps: number
  decodeMs: number
}

export function emptyVideoInfo(): VideoInfo {
  return {
    available: false, supported: false, effective: 'tiles', unsupported: 0, active: false,
    codec: '', w: 0, h: 0, fps: 0, kbps: 0, decodeMs: 0,
  }
}

export interface VideoFrameMsg {
  kind: number
  seq: number
  key: boolean
  w: number
  h: number
  codec: string
  desc: Uint8Array
  /** chunk zaman damgasi (mikrosaniye). */
  ts: number
  data: Uint8Array
}

const HEADER = 14

/** FrameVideo mu (ilk bayt 9). */
export function isVideoFrame(buf: ArrayBuffer): boolean {
  return buf.byteLength > HEADER && new Uint8Array(buf, 0, 1)[0] === FRAME_VIDEO
}

/** Video cercevesini guvenle cozer; bozuksa null. */
export function parseVideoFrame(buf: ArrayBuffer): VideoFrameMsg | null {
  if (buf.byteLength < HEADER + 1) return null
  const dv = new DataView(buf)
  if (dv.getUint8(0) !== FRAME_VIDEO) return null
  const m: VideoFrameMsg = {
    kind: dv.getUint8(HEADER),
    seq: dv.getUint32(9),
    key: (dv.getUint8(13) & 0x02) !== 0,
    w: 0,
    h: 0,
    codec: '',
    desc: new Uint8Array(0),
    ts: 0,
    data: new Uint8Array(0),
  }
  const o = HEADER + 1
  if (m.kind === VIDEO_KIND_CONFIG) {
    if (buf.byteLength < o + 5) return null
    m.w = dv.getUint16(o)
    m.h = dv.getUint16(o + 2)
    const cl = dv.getUint8(o + 4)
    if (!m.w || !m.h || cl === 0 || buf.byteLength < o + 5 + cl) return null
    m.codec = new TextDecoder().decode(new Uint8Array(buf, o + 5, cl))
    m.desc = new Uint8Array(buf, o + 5 + cl)
    return m
  }
  if (m.kind === VIDEO_KIND_CHUNK) {
    if (buf.byteLength < o + 9) return null
    m.ts = dv.getUint32(o) * 4294967296 + dv.getUint32(o + 4)
    m.data = new Uint8Array(buf, o + 8)
    return m
  }
  if (m.kind === VIDEO_KIND_STOP) return m
  return null
}

/** Tarayici H.264'u WebCodecs ile cozebiliyor mu (ozellik tespiti). */
export async function detectVideoSupport(): Promise<boolean> {
  try {
    const VD = (globalThis as unknown as { VideoDecoder?: { isConfigSupported?: (c: unknown) => Promise<{ supported?: boolean }> } }).VideoDecoder
    if (!VD || typeof VD.isConfigSupported !== 'function') return false
    const r = await VD.isConfigSupported({ codec: 'avc1.42e01f', optimizeForLatency: true })
    return !!r?.supported
  } catch {
    return false
  }
}

export interface ScreenVideoOpts {
  canvas: () => HTMLCanvasElement | undefined
  /** Cizimi, bekleyen karo cizimleriyle SIRALAMAK icin (useScreen zinciri). */
  enqueue: (task: () => void) => void
  /** Sunucudan anahtar kare (video modunda IDR) iste (disarida hiz sinirli). */
  requestKey: () => void
  /** Karo tuvali zaten kurulu mu (boyutu video belirlemesin). */
  canvasReady: () => boolean
  /** Tuval boyutu video ile kuruldu (karo karesi gelmeden). */
  onSize?: (w: number, h: number) => void
  /** Alinan video bayti (metrik). */
  onRecv?: (bytes: number, isChunk: boolean) => void
  /** Cozme suresi (ms) ve cizilen kare. */
  onDecoded?: (ms: number) => void
  onDrawn?: () => void
  /** Etkin/aktif durum degisti. */
  onState?: () => void
}

/**
 * WebCodecs VideoDecoder sarmalayicisi. Karo ile video arasi gecis:
 *  - video cercevesi gelince "live" olur; ilk cozulen kare cizilene kadar son
 *    karo goruntusu ekranda kalir.
 *  - stop cercevesi ya da karo mesaji gelince (useScreen stop() cagirir) kod
 *    cozucu sifirlanir; gec gelen kareler atilir.
 *  - hata/kayip/asiri kuyruk: sonraki anahtar kareye kadar delta atilir ve
 *    anahtar kare istenir.
 */
export class ScreenVideoDecoder {
  live = false
  codec = ''
  w = 0
  h = 0
  private dec: VideoDecoder | null = null
  private cfgKey = ''
  private needKey = true
  private closed = false
  private submitted = new Map<number, number>()
  // 1 sn'lik pencere
  private winBytes = 0
  private winFrames = 0
  private winDecodeSum = 0
  private winDecodeN = 0
  private lastWin = performance.now()

  private o: ScreenVideoOpts

  constructor(o: ScreenVideoOpts) {
    this.o = o
  }

  /** Pencere ozetini doner ve sifirlar. */
  drainWindow(): { fps: number, kbps: number, decodeMs: number } {
    const now = performance.now()
    const sec = Math.max((now - this.lastWin) / 1000, 0.001)
    const r = {
      fps: Math.round((this.winFrames / sec) * 10) / 10,
      kbps: Math.round((this.winBytes * 8) / 1000 / sec),
      decodeMs: this.winDecodeN ? Math.round((this.winDecodeSum / this.winDecodeN) * 10) / 10 : 0,
    }
    this.winBytes = 0
    this.winFrames = 0
    this.winDecodeSum = 0
    this.winDecodeN = 0
    this.lastWin = now
    return r
  }

  handle(buf: ArrayBuffer) {
    if (this.closed) return
    const m = parseVideoFrame(buf)
    if (!m) { this.o.requestKey(); return }
    if (m.kind === VIDEO_KIND_STOP) { this.stop(); return }
    this.o.onRecv?.(buf.byteLength, m.kind === VIDEO_KIND_CHUNK)
    this.winBytes += buf.byteLength
    if (m.kind === VIDEO_KIND_CONFIG) { this.configure(m); return }
    this.decode(m)
  }

  private ensure(): VideoDecoder | null {
    if (this.dec && this.dec.state !== 'closed') return this.dec
    const VD = (globalThis as unknown as { VideoDecoder?: typeof VideoDecoder }).VideoDecoder
    if (!VD) return null
    this.dec = new VD({
      output: frame => this.onOutput(frame),
      error: () => {
        // Kod cozucu coktu: yeniden kur, sonraki anahtar kareyi bekle.
        this.dec = null
        this.cfgKey = ''
        this.needKey = true
        this.submitted.clear()
        if (this.live) this.o.requestKey()
      },
    })
    this.cfgKey = ''
    return this.dec
  }

  private configure(m: VideoFrameMsg) {
    const key = `${m.codec}|${m.w}x${m.h}|${Array.from(m.desc).join(',')}`
    const dec = this.ensure()
    if (!dec) return
    if (key === this.cfgKey) { this.live = true; return }
    try {
      dec.configure({
        codec: m.codec,
        codedWidth: m.w,
        codedHeight: m.h,
        description: m.desc.slice(),
        optimizeForLatency: true,
      })
    } catch {
      this.dec = null
      this.o.requestKey()
      return
    }
    this.cfgKey = key
    this.codec = m.codec
    this.w = m.w
    this.h = m.h
    this.needKey = true
    this.live = true
    this.o.onState?.()
  }

  private decode(m: VideoFrameMsg) {
    const dec = this.dec
    if (!dec || dec.state !== 'configured' || !this.cfgKey) { this.o.requestKey(); return }
    this.live = true
    if (this.needKey && !m.key) { this.o.requestKey(); return }
    // Kuyruk birikti (CPU yetmiyor): gecikmeyi buyutme, sonraki IDR'ye kadar at.
    if (dec.decodeQueueSize > 6 && !m.key) {
      this.needKey = true
      this.o.requestKey()
      return
    }
    try {
      const ts = m.ts
      this.submitted.set(ts, performance.now())
      if (this.submitted.size > 64) this.submitted.delete(this.submitted.keys().next().value as number)
      dec.decode(new EncodedVideoChunk({ type: m.key ? 'key' : 'delta', timestamp: ts, data: m.data }))
      if (m.key) this.needKey = false
    } catch {
      this.needKey = true
      this.o.requestKey()
    }
  }

  private onOutput(frame: VideoFrame) {
    const t0 = this.submitted.get(frame.timestamp)
    this.submitted.delete(frame.timestamp)
    if (t0 !== undefined) {
      const ms = performance.now() - t0
      this.winDecodeSum += ms
      this.winDecodeN++
      this.o.onDecoded?.(ms)
    }
    if (!this.live || this.closed) { frame.close(); return }
    this.o.enqueue(() => {
      try {
        if (!this.live || this.closed) return
        const cv = this.o.canvas()
        if (!cv) return
        if (!this.o.canvasReady() && (cv.width !== this.w || cv.height !== this.h)) {
          cv.width = this.w
          cv.height = this.h
          this.o.onSize?.(this.w, this.h)
        }
        const ctx = cv.getContext('2d')
        if (!ctx) return
        // Video tuval boyutuna gerilir (cift boyuta kirpilmis olabilir).
        ctx.drawImage(frame, 0, 0, cv.width, cv.height)
        this.winFrames++
        this.o.onDrawn?.()
      } finally {
        frame.close()
      }
    })
    this.o.onState?.()
  }

  /** Video bitti (stop ya da karo mesaji geldi): kod cozucuyu sifirla. */
  stop() {
    if (!this.live && !this.dec) return
    this.live = false
    this.cfgKey = ''
    this.needKey = true
    this.submitted.clear()
    try { if (this.dec && this.dec.state === 'configured') this.dec.reset() } catch { /* yok say */ }
    this.o.onState?.()
  }

  close() {
    this.closed = true
    this.live = false
    try { this.dec?.close() } catch { /* yok say */ }
    this.dec = null
  }
}
