/**
 * Gecici tunel (FAZ 2 / F07) yardimcilari: sure secenekleri, yerel hedef
 * normallestirme, ad dogrulama ve kalan sure hesabi. Saf fonksiyonlar;
 * DOM/Vue bagimliligi yok (node --test ile sinanir).
 */

/** REST sinirlari: server/api/handlers.go MinTunnelTTLSec / MaxTunnelTTLSec. */
export const MIN_TTL_SEC = 60
export const MAX_TTL_SEC = 24 * 60 * 60
/** CLI (`zorven ephemeral`) ile ayni varsayilan: 2 saat. */
export const DEFAULT_TTL_SEC = 2 * 60 * 60
/**
 * Panelde sunulan sureler (sn): 15 dk, 30 dk, 1 sa, 2 sa, 6 sa, 12 sa, 24 sa.
 * Serbest giris YOK: sunucu 0 / negatif degeri sessizce "kalici tunel" sayar,
 * hazir liste kalici tunel olusmasini imkansiz kilar.
 */
export const TTL_PRESETS_SEC: readonly number[] = [900, 1800, 3600, 7200, 21600, 43200, 86400]

/** Sunucunun kabul ettigi aralikta tam sayi mi? */
export function isValidTtl(sec: number): boolean {
  return Number.isInteger(sec) && sec >= MIN_TTL_SEC && sec <= MAX_TTL_SEC
}

/**
 * Yerel hedef girdisini sunucunun bekledigi http(s) adresine cevirir; gecersizse ''.
 * CLI (`zorven ephemeral 3000`) ile ayni kisayollar:
 *   "3000" / ":3000"  -> http://localhost:3000
 *   "127.0.0.1:8080"  -> http://127.0.0.1:8080
 *   "https://x.test/" -> https://x.test   (sondaki / atilir)
 * REST ucu yalnizca http:// ve https:// onekini kabul eder (kucuk harf).
 */
export function normalizeTarget(raw: string): string {
  let s = raw.trim()
  if (!s || /\s/.test(s)) return ''
  s = s.replace(/^:(?=\d)/, '') // ":3000" kisayolu
  if (/^\d+$/.test(s)) {
    const port = Number(s)
    return port >= 1 && port <= 65535 ? `http://localhost:${port}` : ''
  }
  const scheme = /^(https?):\/\//i.exec(s)
  if (scheme) s = scheme[1]!.toLowerCase() + s.slice(scheme[1]!.length)
  else if (s.includes('://')) return '' // ftp://, tcp:// vb.: yalnizca http(s) hedefi
  else s = 'http://' + s
  let u: URL
  try { u = new URL(s) } catch { return '' }
  // Makul bir ana makine adi: alan adi/IPv4 etiketleri ya da koseli parantezli IPv6.
  if (!/^(?:[A-Za-z0-9._-]+|\[[0-9A-Fa-f:.]+\])$/.test(u.hostname)) return ''
  return s.replace(/\/+$/, '')
}

/** Tek DNS etiketi: server/api/hostnames.go labelRe + "--" yasagi. */
const LABEL_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

export type EphemeralNameError = '' | 'required' | 'invalid' | 'taken'

/**
 * Tunel adini dogrular. usedNames: bu kiracida zaten kapsamli adi olan etiketler
 * (ayni etiket tekrar verilirse sunucu tuneli ADRESSIZ olusturur).
 */
export function checkTunnelName(raw: string, usedNames: ReadonlySet<string> = new Set()): EphemeralNameError {
  const name = raw.trim().toLowerCase()
  if (!name) return 'required'
  if (!LABEL_RE.test(name) || name.includes('--')) return 'invalid'
  if (usedNames.has(name)) return 'taken'
  return ''
}

/** Kapsamli adlardan ("api--acme.zorven.app") kullanilan tunel etiketlerini cikarir. */
export function usedTunnelLabels(hostnames: ReadonlyArray<{ fqdn: string, type?: string }>): Set<string> {
  const out = new Set<string>()
  for (const h of hostnames) {
    if (h.type && h.type !== 'scoped') continue
    const i = h.fqdn.indexOf('--')
    if (i > 0) out.add(h.fqdn.slice(0, i).toLowerCase())
  }
  return out
}

const NAME_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'

/** Varsayilan ad onerisi: "tmp-k3x9". rand testlerde sabitlenir. */
export function randomEphemeralName(rand: () => number = Math.random): string {
  let s = 'tmp-'
  for (let i = 0; i < 4; i++) s += NAME_ALPHABET[Math.min(NAME_ALPHABET.length - 1, Math.floor(rand() * NAME_ALPHABET.length))]
  return s
}

/** Kalan sure (ms): bitis yok ya da okunamiyorsa null; gecmisse 0 veya negatif. */
export function remainingMs(expiresAt: string | undefined | null, nowMs: number): number | null {
  if (!expiresAt) return null
  const t = Date.parse(expiresAt)
  return Number.isNaN(t) ? null : t - nowMs
}

export type RemainingKind = 'expired' | 'h' | 'hm' | 'm' | 's'
export interface RemainingLabel { kind: RemainingKind, h: number, m: number, s: number }

/**
 * Kalan sureyi gosterim parcalarina boler. Dakika duzeyi EN YAKIN dakikaya yuvarlanir:
 * 2 sa'lik tunel acildiktan hemen sonra "1 sa 59 dk" degil "2 sa" gorunur ve sayac
 * ile sunucu saati arasindaki kucuk fark (zamanlayici gecikmesi, saat sapmasi)
 * acilista "31 dk" gibi sicramalar uretmez. Bir dakikanin altinda saniye gosterilir.
 */
export function remainingLabel(ms: number): RemainingLabel {
  if (ms <= 0) return { kind: 'expired', h: 0, m: 0, s: 0 }
  const sec = Math.ceil(ms / 1000)
  if (sec < 60) return { kind: 's', h: 0, m: 0, s: sec }
  const totalMin = Math.round(sec / 60)
  if (totalMin < 60) return { kind: 'm', h: 0, m: totalMin, s: 0 }
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  return { kind: m === 0 ? 'h' : 'hm', h, m, s: 0 }
}

/** Son 5 dakika: rozet uyari rengine doner. */
export function isExpiringSoon(ms: number): boolean {
  return ms > 0 && ms <= 5 * 60 * 1000
}

/** Tunel adresi (genel ad): HTTPS. */
export function tunnelUrl(fqdn: string): string {
  return `https://${fqdn}`
}
