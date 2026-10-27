import { APIError } from "better-auth/api"

// Kayit (e-posta/parola) korumasi: CAPTCHA dogrulamasi. Yan etkisiz (fetch ve env enjekte edilir) ->
// birim testi auth-hooks.test.ts icinde.
//
// Ortam:
//   ZORVEN_CAPTCHA_PLATFORM_SECRET   tanimli degilse CAPTCHA atlanir (gelistirme)
//   ZORVEN_API_INTERNAL_URL          Go API tabani (vars. https://<PLATFORM_DOMAIN>)

export type Env = Record<string, string | undefined>
export type FetchFn = (url: string, init?: any) => Promise<Response>

type Lang = "tr" | "en" | "ja"

const MESSAGES: Record<string, Record<Lang, string>> = {
  captcha_failed: {
    tr: "İnsan doğrulaması başarısız oldu. Lütfen tekrar deneyin.",
    en: "Human verification failed. Please try again.",
    ja: "人間確認に失敗しました。もう一度お試しください。",
  },
}

export function pickLang(acceptLanguage: string | null | undefined): Lang {
  const first = (acceptLanguage || "").split(",")[0]?.trim().toLowerCase() || ""
  if (first.startsWith("ja")) return "ja"
  if (first.startsWith("en")) return "en"
  return "tr"
}

function fail(code: "captcha_failed", lang: Lang): never {
  throw new APIError("BAD_REQUEST", { code, message: MESSAGES[code][lang] })
}

export function apiBase(env: Env): string {
  const explicit = (env.ZORVEN_API_INTERNAL_URL || "").trim().replace(/\/+$/, "")
  if (explicit) return explicit
  const pd = (env.PLATFORM_DOMAIN || "zorven.app").trim().replace(/\.$/, "")
  return `https://${pd}`
}

function header(ctx: any, name: string): string | null {
  return ctx?.headers?.get?.(name) ?? ctx?.request?.headers?.get?.(name) ?? null
}

function clientIp(ctx: any): string {
  return (header(ctx, "x-forwarded-for") || "").split(",")[0]?.trim() || ""
}

async function postJSON(f: FetchFn, url: string, body: unknown): Promise<any> {
  const res = await f(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(5000),
  })
  return await res.json()
}

/**
 * /sign-up/email oncesi kontrol. Hata -> APIError (400, code captcha_failed).
 * Sosyal giris bu yoldan gecmez (saglayici dogrular).
 */
export async function checkSignup(ctx: any, env: Env, f: FetchFn): Promise<void> {
  const lang = pickLang(header(ctx, "accept-language"))
  const base = apiBase(env)

  const secret = (env.ZORVEN_CAPTCHA_PLATFORM_SECRET || "").trim()
  if (secret) {
    const token = (header(ctx, "x-zorven-captcha") || "").trim()
    if (!token) fail("captcha_failed", lang)
    let ok = false
    try {
      const r = await postJSON(f, `${base}/api/v1/captcha/siteverify`, {
        secret,
        response: token,
        remoteip: clientIp(ctx) || undefined,
      })
      ok = r?.success === true
    } catch (err) {
      // Dogrulama hizmetine ulasilamadi: kapali tut (bot kaydi riski > gecici kesinti).
      console.error("[captcha] siteverify basarisiz:", err)
    }
    if (!ok) fail("captcha_failed", lang)
  }
}
