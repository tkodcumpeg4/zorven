// Yapistirilan GORSELIN uzak panoya gonderilmesi icin saf mantik (DOM'a bagimli olmayan kisim node --test ile test edilir).
// Tarayiciya bagli parca (createImageBitmap + canvas) `renderPng` olarak disaridan verilir (bkz. renderPngBrowser).

/** Sunucunun pano gorseli siniri (8 MiB; composables/screenWire.ts CLIP_IMAGE_MAX_BYTES ile ayni). */
export const IMAGE_MAX_BYTES = 8 * 1024 * 1024

export interface PasteFileLike { name: string, size: number, type: string }
export interface PasteItemLike<F> { kind: string, type: string, getAsFile: () => F | null }

/**
 * Yapistir olayindan gorsel panoya gidecek TEK gorseli ayiklar; yoksa null.
 * Kosullar: tam bir dosya ve o image/*; ya da dosya listesi bos iken tek bir dosya ogesi ve o image/*
 * (bazi tarayicilar ekran goruntusunu yalniz items'ta verir). Birden cok dosya ya da gorsel
 * olmayan dosya => null (mevcut dosya yukleme akisi).
 */
export function pickPasteImage<F extends PasteFileLike>(files: F[], items: PasteItemLike<F>[]): F | null {
  if (files.length === 1) return /^image\//i.test(files[0]!.type) ? files[0]! : null
  if (files.length > 1) return null
  const fileItems = items.filter(i => i.kind === 'file')
  if (fileItems.length !== 1 || !/^image\//i.test(fileItems[0]!.type)) return null
  return fileItems[0]!.getAsFile()
}

export function isPng(type: string): boolean { return type.toLowerCase() === 'image/png' }

/** Verilen kenar siniri icin yeni boyut (oran korunur, en az 1 px). maxEdge >= buyuk kenar ise ayni boyut. */
export function scaledSize(w: number, h: number, maxEdge: number): { w: number, h: number } {
  const big = Math.max(w, h)
  if (maxEdge <= 0 || big <= maxEdge) return { w, h }
  const r = maxEdge / big
  return { w: Math.max(1, Math.round(w * r)), h: Math.max(1, Math.round(h * r)) }
}

/** Kucultme basamaklari (buyuk kenar, px); 0 = kucultme yok. Her turda PNG tekrar uretilir. */
export const SHRINK_STEPS = [0, 3840, 2560, 1920, 1280]

export type RenderPng = (src: Blob, maxEdge: number) => Promise<Blob>

/**
 * Gorseli sunucunun siniri (8 MiB) icinde bir PNG'ye cevirir.
 * - Zaten PNG ve sinir icindeyse oldugu gibi doner.
 * - Aksi halde renderPng ile (JPEG/WebP/BMP -> PNG, gerekirse kenari kucultup) yeniden dener.
 * - Hicbiri sigmazsa ya da cozme basarisizsa null (cagiran dosya olarak yukler).
 */
export async function pngWithinLimit(src: Blob, renderPng: RenderPng, maxBytes = IMAGE_MAX_BYTES): Promise<Blob | null> {
  if (isPng(src.type) && src.size > 0 && src.size <= maxBytes) return src
  for (let i = 0; i < SHRINK_STEPS.length; i++) {
    // PNG kaynak zaten buyukse kucultmesiz adim ayni boyutu verir: dogrudan kucultmeye gec.
    if (i === 0 && isPng(src.type) && src.size > maxBytes) continue
    let out: Blob
    try { out = await renderPng(src, SHRINK_STEPS[i]!) } catch { return null }
    if (out.size > 0 && out.size <= maxBytes) return out
  }
  return null
}

/** Uzaktan az once gelen gorsel yerel panoya yazilip geri yapistirilirsa (boyut + sure) tekrar gonderilmez. */
export function isEchoImage(last: { size: number, at: number } | null, size: number, now: number, windowMs = 15000): boolean {
  return !!last && last.size === size && now - last.at <= windowMs
}

/** Kaydet dugmesi icin dosya adi: zorven-pano-YYYYMMDD-HHMMSS.png */
export function remoteImageFileName(now: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `zorven-pano-${now.getFullYear()}${p(now.getMonth() + 1)}${p(now.getDate())}-${p(now.getHours())}${p(now.getMinutes())}${p(now.getSeconds())}.png`
}

/** Tarayicida PNG uretir (createImageBitmap + canvas). Yalniz tarayicida calisir; testte sahte renderPng kullanilir. */
export async function renderPngBrowser(src: Blob, maxEdge: number): Promise<Blob> {
  const bmp = await createImageBitmap(src)
  try {
    const { w, h } = scaledSize(bmp.width, bmp.height, maxEdge)
    const cv = document.createElement('canvas')
    cv.width = w
    cv.height = h
    const ctx = cv.getContext('2d')
    if (!ctx) throw new Error('canvas')
    ctx.drawImage(bmp, 0, 0, w, h)
    return await new Promise<Blob>((resolve, reject) => cv.toBlob(b => (b ? resolve(b) : reject(new Error('toBlob'))), 'image/png'))
  } finally {
    bmp.close()
  }
}
