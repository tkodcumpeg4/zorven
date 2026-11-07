// Anahtar basina kayar pencere hiz siniri (bellekte). Better Auth'un yerlesik hiz siniri
// IP + yol anahtarlidir; ayni ofis/NAT arkasindaki kullanicilar birbirini kilitlerdi.
// Kisisel veri disa aktarma gibi KULLANICI basina sinirlar icin bu kullanilir.
//
// Web servisi tek surectir (auth-hooks.ts usedTotp ile ayni varsayim); coklu replikaya
// gecilirse depo veritabanina tasinmalidir. Yeniden baslatma sayaclari sifirlar.
// Saat enjekte edilebilir -> window-limiter.test.ts.

export interface LimitResult {
  ok: boolean
  /** ok=false ise en erken yeniden deneme zamanina kalan saniye (>=1); ok=true ise 0. */
  retryAfterSec: number
}

export interface WindowLimiter {
  /** Anahtar icin bir hak tuketir; hak yoksa tuketmez ve ok=false doner. */
  take(key: string, nowMs?: number): LimitResult
}

export function createWindowLimiter(opts: { max: number; windowMs: number; maxKeys?: number }): WindowLimiter {
  const { max, windowMs } = opts
  const maxKeys = opts.maxKeys ?? 10_000
  const hits = new Map<string, number[]>()

  function prune(nowMs: number): void {
    for (const [k, ts] of hits) {
      const fresh = ts.filter((t) => t > nowMs - windowMs)
      if (fresh.length === 0) hits.delete(k)
      else if (fresh.length !== ts.length) hits.set(k, fresh)
    }
  }

  return {
    take(key: string, nowMs: number = Date.now()): LimitResult {
      const cur = (hits.get(key) ?? []).filter((t) => t > nowMs - windowMs)
      if (cur.length >= max) {
        hits.set(key, cur)
        const oldest = cur[0] ?? nowMs
        return { ok: false, retryAfterSec: Math.max(1, Math.ceil((oldest + windowMs - nowMs) / 1000)) }
      }
      cur.push(nowMs)
      hits.set(key, cur)
      // Bellek siniri: anahtar sayisi tavani asarsa suresi dolanlar atilir, yine fazlaysa en eskiler.
      if (hits.size > maxKeys) {
        prune(nowMs)
        while (hits.size > maxKeys) {
          const first = hits.keys().next().value
          if (first === undefined) break
          hits.delete(first)
        }
      }
      return { ok: true, retryAfterSec: 0 }
    },
  }
}
