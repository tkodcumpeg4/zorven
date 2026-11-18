/**
 * Uzak ekran panel<->sunucu WS'inin SAF yardimcilari (Nuxt/DOM'a bagimli degil;
 * node --test ile test edilir). useScreen.ts bunlari kullanir.
 */

/** Pano gorseli ikili mesajinin ilk bayti (iki yonde): kalan baytlar PNG. Sunucu: protocol.PanelClipImageMarker. */
export const CLIP_IMAGE_MARKER = 0x20

/** PNG en cok 8 MiB (sunucu da zorlar: protocol.MaxScreenClipboardImage). */
export const CLIP_IMAGE_MAX_BYTES = 8 * 1024 * 1024

/** Gidis-donus olcum araligi (ms). */
export const RTT_INTERVAL_MS = 2000

/** Sunucunun pano gorseli reddetme nedenleri (clipboard_status.reason). */
export type ClipImageReason = 'view_only' | 'unsupported' | 'too_large' | 'invalid' | 'rate_limited'

/** PNG imzasi: 89 50 4E 47 0D 0A 1A 0A. */
export function looksLikePng(b: Uint8Array): boolean {
  const sig = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]
  if (b.length < 33) return false
  for (let i = 0; i < sig.length; i++) if (b[i] !== sig[i]) return false
  return true
}

/**
 * Gonderilecek PNG Blob'unu ikili WS mesajina cevirir (marker + PNG).
 * Bos ya da 8 MiB'i asan gorsel icin null doner (gonderilmez; cagiran
 * 'too_large' gosterir).
 */
export function encodeClipboardImage(png: Blob): Blob | null {
  if (png.size === 0 || png.size > CLIP_IMAGE_MAX_BYTES) return null
  return new Blob([new Uint8Array([CLIP_IMAGE_MARKER]), png])
}

/** Sunucudan gelen ikili mesaj bir pano gorseli mi (ilk bayt marker). */
export function isClipboardImageMessage(buf: ArrayBuffer): boolean {
  return buf.byteLength > 1 && new Uint8Array(buf, 0, 1)[0] === CLIP_IMAGE_MARKER
}

/** Pano gorseli mesajindan PNG Blob'u cikarir; gecersizse null (imza kontrol edilir). */
export function decodeClipboardImage(buf: ArrayBuffer): Blob | null {
  if (!isClipboardImageMessage(buf)) return null
  const png = new Uint8Array(buf, 1)
  if (png.length > CLIP_IMAGE_MAX_BYTES || !looksLikePng(png)) return null
  return new Blob([png], { type: 'image/png' })
}

/** caps mesajinin yeni alanlari; eski sunucu/ajanda alan yoktur. */
export interface WireCapsExtra { clipboardImage: boolean, displays?: number, display?: number }

/**
 * caps mesajindan monitor listesi + gorsel panosu alanlarini ayiklar.
 * displays yoksa (eski ajan/sunucu) undefined birakilir: panel 1 varsayar.
 * displays 1..32 disindaysa yok sayilir; display aralik disindaysa 0'a duser.
 */
export function parseCapsExtra(msg: { clipboard_image?: unknown, displays?: unknown, display?: unknown }): WireCapsExtra {
  const out: WireCapsExtra = { clipboardImage: msg.clipboard_image === true }
  const n = msg.displays
  if (typeof n === 'number' && Number.isInteger(n) && n >= 1 && n <= 32) {
    out.displays = n
    const d = msg.display
    out.display = typeof d === 'number' && Number.isInteger(d) && d >= 0 && d < n ? d : 0
  }
  return out
}

/** ping mesaji: t panelin yuksek cozunurluklu saatidir (performance.now()), sunucu aynen yankilar. */
export function buildPing(now: number): string {
  return JSON.stringify({ type: 'ping', t: now })
}

/**
 * pong mesajindan [panel<->sunucu ms, sunucu<->ajan ms | undefined] hesaplar.
 * Gecersiz (sayi olmayan, gelecekteki ya da 60 sn'den eski t) yanitta null.
 * agent_ms yoksa (eski ajan / olcum henuz yok) agentMs undefined.
 */
export function parsePong(msg: { t?: unknown, agent_ms?: unknown }, now: number): { ms: number, agentMs?: number } | null {
  if (typeof msg.t !== 'number' || !Number.isFinite(msg.t)) return null
  const ms = now - msg.t
  if (ms < 0 || ms > 60_000) return null
  const a = msg.agent_ms
  const agentMs = typeof a === 'number' && Number.isFinite(a) && a >= 0 ? a : undefined
  return { ms, agentMs }
}

/** Olcum zamani geldi mi: yalniz sekme gorunurken ve WS aciksa (gizliyken olculmez). */
export function shouldMeasureRtt(visibility: string, wsOpen: boolean): boolean {
  return wsOpen && visibility === 'visible'
}
