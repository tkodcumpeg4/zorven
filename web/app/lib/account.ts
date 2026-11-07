/**
 * Hesap silme / kisisel veri / etkin oturumlar icin panel yardimcilari. Saf fonksiyonlar:
 * DOM/Vue bagimliligi yok (node --test ile sinanir). Sunucu karsiliklari:
 * web/server/lib/account-delete.ts (hata kodlari, tazelik suresi),
 * web/server/lib/account-export.ts (dosya adi).
 */

/** Sunucunun bildirdigi hata kodlari; her biri `account.errors.<KOD>` metnine eslenir. */
export const ACCOUNT_ERROR_CODES = [
  'ACCOUNT_DELETE_PASSWORD_REQUIRED',
  'ACCOUNT_DELETE_SESSION_NOT_FRESH',
  'ACCOUNT_DELETE_OWNER_BLOCKED',
  'ACCOUNT_DELETE_PLATFORM_BLOCKED',
  'ACCOUNT_DELETE_UNAVAILABLE',
  'INVALID_PASSWORD',
  'INVALID_TOKEN',
  'SESSION_REQUIRED',
  'EXPORT_RATE_LIMITED',
  'CANNOT_REVOKE_CURRENT_SESSION',
] as const

/** i18n anahtari: bilinen kod -> ozel metin, aksi halde genel metin. */
export function accountErrorKey(code: unknown): string {
  const c = typeof code === 'string' ? code : ''
  return (ACCOUNT_ERROR_CODES as readonly string[]).includes(c) ? `account.errors.${c}` : 'account.errors.generic'
}

/**
 * Silme istegi icin oturumun azami yasi (ms). Sunucu (account-delete.ts
 * DELETE_SESSION_FRESH_MS) ile AYNI olmali; panel yalniz erken uyarir, karar sunucudadir.
 */
export const DELETE_SESSION_FRESH_MS = 60 * 60 * 1000

/**
 * E-posta onay baglantisinin omru (dk). Sunucu (account-delete.ts DELETE_TOKEN_EXPIRES_SECONDS / 60)
 * ile AYNI olmali; e-postadaki ve penceredeki sure metni buradan gelir.
 */
export const DELETE_LINK_MINUTES = 30

/** Oturum, silme istegi icin yeterince taze mi? Bilinmeyen/gecersiz tarih "taze degil" sayilir. */
export function sessionIsFresh(createdAt: string | Date | null | undefined, nowMs: number = Date.now()): boolean {
  if (!createdAt) return false
  const t = (createdAt instanceof Date ? createdAt : new Date(createdAt)).getTime()
  return Number.isFinite(t) && nowMs - t < DELETE_SESSION_FRESH_MS
}

/** Indirme dosya adi: zorven-data-YYYYMMDD.json (UTC; sunucudaki Content-Disposition ile ayni). */
export function exportFileName(now: Date = new Date()): string {
  return `zorven-data-${now.toISOString().slice(0, 10).replaceAll('-', '')}.json`
}

/** Silinecek organizasyonun icerik ozeti icin yer tutucular; hepsi 0 ise null (satir gosterilmez). */
export function contentCounts(o: {
  tunnels?: number
  clients?: number
  domains?: number
  apps?: number
}): { tunnels: number, clients: number, domains: number, apps: number } | null {
  const c = { tunnels: o.tunnels ?? 0, clients: o.clients ?? 0, domains: o.domains ?? 0, apps: o.apps ?? 0 }
  return c.tunnels + c.clients + c.domains + c.apps > 0 ? c : null
}

/** Better Auth istemci hatasindan ({ status, code, message }) kod cikarir. */
export function errorCode(e: unknown): string {
  const x = e as { code?: unknown, error?: { code?: unknown }, data?: { code?: unknown } } | null
  const c = x?.code ?? x?.error?.code ?? x?.data?.code
  return typeof c === 'string' ? c : ''
}
