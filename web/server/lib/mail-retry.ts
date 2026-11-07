// Giden e-posta icin kucuk yeniden deneme yardimcisi (bagimliliksiz; test edilebilir).
// Gecici hatalar (baglanti/zaman asimi, SMTP 4xx) tam jitterli ustel beklemeyle
// tekrar denenir; kalici hatalar (SMTP 5xx, gecersiz alici) hemen firlatilir.
// Calistirma: cd web && node --test server/lib/mail-retry.test.ts

const TRANSIENT_CODES = new Set([
  "ETIMEDOUT",
  "ESOCKET",
  "ECONNRESET",
  "ECONNREFUSED",
  "ECONNECTION",
  "EAI_AGAIN",
  "EPIPE",
  "EHOSTUNREACH",
  "ENETUNREACH",
])

// nodemailer hatasi gecici mi? SMTP yanit kodu varsa 4xx gecici, 5xx kalici.
export function isTransientMailError(err: unknown): boolean {
  const e = err as { code?: string; responseCode?: number } | null
  if (!e) return false
  if (typeof e.responseCode === "number") return e.responseCode >= 400 && e.responseCode < 500
  return typeof e.code === "string" && TRANSIENT_CODES.has(e.code)
}

export interface RetryOpts {
  attempts?: number // toplam deneme (vars. 3)
  baseMs?: number // vars. 500
  maxMs?: number // vars. 5000
  sleep?: (ms: number) => Promise<void>
  random?: () => number
}

export async function withMailRetry<T>(fn: () => Promise<T>, opts: RetryOpts = {}): Promise<T> {
  const attempts = Math.max(opts.attempts ?? 3, 1)
  const baseMs = opts.baseMs ?? 500
  const maxMs = opts.maxMs ?? 5000
  const sleep = opts.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)))
  const random = opts.random ?? Math.random
  let lastErr: unknown
  for (let i = 1; i <= attempts; i++) {
    try {
      return await fn()
    } catch (err) {
      lastErr = err
      if (i === attempts || !isTransientMailError(err)) throw err
      const ceil = Math.min(maxMs, baseMs * 2 ** (i - 1))
      await sleep(Math.floor(random() * (ceil + 1)))
    }
  }
  throw lastErr
}
