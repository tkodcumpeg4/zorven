// Uzak ekran: yerel panonun (erisen bilgisayar) uzak makineye kendiliginden aktarilmasi icin saf mantik.
// Tarayiciya bagli kisim (navigator.clipboard, tus olaylari) pages/screen/[id].vue'dedir.

/** Sunucunun kabul ettigi pano metni siniri (UTF-8 bayt). */
export const CLIPBOARD_MAX_BYTES = 64 * 1024

export interface KeyLike { code: string, ctrlKey: boolean, metaKey: boolean, altKey: boolean }

/** Yapistir kisayolu mu (Ctrl+V / Cmd+V; Shift ile "duz yapistir" da sayilir)? AltGr+V (Ctrl+Alt) sayilmaz. */
export function isPasteCombo(e: KeyLike): boolean {
  return e.code === 'KeyV' && (e.ctrlKey || e.metaKey) && !e.altKey
}

/** Metin gonderilebilir mi: bos degil ve sinirin altinda. */
export function clipboardFits(text: string): boolean {
  if (!text) return false
  // Hizli yol: her UTF-16 birimi en cok 3 bayt.
  if (text.length * 3 <= CLIPBOARD_MAX_BYTES) return true
  return new TextEncoder().encode(text).length <= CLIPBOARD_MAX_BYTES
}

/**
 * Ertelenmis yapistir: pano okunup uzak makineye gonderilene kadar Ctrl+V tusu bekletilir.
 * Tus bu sirada birakilirsa (keyup) siralama bozulmasin diye keyup da bekletilir ve keydown'dan
 * SONRA gonderilir; aksi halde uzak makinede tus basili kalirdi.
 */
export class DeferredPaste<E> {
  private pending: { down: E, up: E | null } | null = null

  /** Bekleyen bir yapistir var mi? */
  get active(): boolean { return this.pending !== null }

  /** Yapistir tusuna basildi: olayi beklet. Zaten bekleyen varsa (tekrar/oto-tekrar) yok sayilir. */
  begin(down: E): boolean {
    if (this.pending) return false
    this.pending = { down, up: null }
    return true
  }

  /** Bekleme sirasinda ayni tus birakildi: keyup'i sakla. true = olay yutuldu (simdi gonderme). */
  keyup(up: E): boolean {
    if (!this.pending) return false
    this.pending.up = up
    return true
  }

  /** Pano hazir (ya da vazgecildi): sirayla gonderilecek olaylari doner ve durumu sifirlar. */
  finish(): E[] {
    const p = this.pending
    this.pending = null
    if (!p) return []
    return p.up ? [p.down, p.up] : [p.down]
  }
}

// ---- Yapistirilan dosya ve gorseller -----------------------------------------------------------

/** Yapistirarak gonderilebilecek tek dosya siniri ve bir yapistirmadaki dosya sayisi. */
export const PASTE_FILE_MAX_BYTES = 25 * 1024 * 1024
export const PASTE_FILE_MAX_COUNT = 10

export interface FileLike { name: string, size: number, type: string }
export interface PasteData<F extends FileLike> {
  /** Duz metin ("" = yok). Dosya varken metin (dosya adi listesi) yok sayilir. */
  text: string
  /** Sinirlara uyan dosyalar. */
  files: F[]
  /** Siniri asan dosyalarin adlari. */
  tooLarge: string[]
  /** Sayi sinirindan dolayi alinmayan dosya sayisi. */
  dropped: number
}

/**
 * Yapistir olayindaki veriyi ayristirir. Tarayici, isletim sisteminde kopyalanan dosyalari ve
 * ekran goruntulerini `files` olarak verir (gorsel icin ad genelde "image.png").
 */
export function collectPaste<F extends FileLike>(text: string, files: F[], maxBytes = PASTE_FILE_MAX_BYTES, maxCount = PASTE_FILE_MAX_COUNT): PasteData<F> {
  const out: PasteData<F> = { text: '', files: [], tooLarge: [], dropped: 0 }
  if (!files.length) {
    out.text = text
    return out
  }
  for (const f of files) {
    if (f.size > maxBytes) { out.tooLarge.push(f.name); continue }
    if (out.files.length >= maxCount) { out.dropped++; continue }
    out.files.push(f)
  }
  return out
}

/** Panodan gelen gorsele ayirt edici bir ad verir (tarayici hep "image.png" der; ust uste yazilmasin). */
export function pastedFileName(name: string, type: string, now: Date): string {
  if (!/^image\.(png|jpe?g|gif|webp|bmp)$/i.test(name)) return name
  const ext = (name.split('.').pop() || type.split('/').pop() || 'png').toLowerCase()
  const p = (n: number) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${p(now.getMonth() + 1)}${p(now.getDate())}-${p(now.getHours())}${p(now.getMinutes())}${p(now.getSeconds())}`
  return `pano-${stamp}.${ext}`
}

// ---- Otomatik yeniden baglanma -----------------------------------------------------------------

export const RECONNECT_MAX_ATTEMPTS = 8

/** n. denemeden once beklenecek sure (ms): 1, 2, 4, 8, 15, 15... sn. */
export function reconnectDelayMs(attempt: number): number {
  const steps = [1000, 2000, 4000, 8000, 15000]
  return steps[Math.min(Math.max(attempt, 1), steps.length) - 1]!
}

/**
 * Baglanti hatasi yeniden denemeye deger mi? Yetki, plan siniri, bulunamadi gibi kalici hatalarda
 * denemek anlamsizdir; ag hatasi, sunucu hatasi ve cevrimdisi istemci (geri gelebilir) denenir.
 */
export function shouldAutoRetry(httpStatus: number | undefined, errorCode: string | undefined): boolean {
  if (errorCode && /limit_reached|forbidden|unauthorized|not_found|upgrade_required|view_only|disabled/.test(errorCode)) return false
  if (httpStatus === undefined || httpStatus === 0) return true // ag hatasi / WS kapandi
  if (httpStatus === 408 || httpStatus === 425 || httpStatus === 429) return true
  return httpStatus >= 500
}
