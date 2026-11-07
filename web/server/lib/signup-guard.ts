import { APIError } from "better-auth/api"

// Kayit (e-posta/parola) korumasi: CAPTCHA dogrulamasi. Yan etkisiz (fetch ve env enjekte edilir) ->
// birim testi auth-hooks.test.ts icinde.
//
// Ortam:
//   ZORVEN_CAPTCHA_PLATFORM_SECRET   tanimli degilse CAPTCHA atlanir (gelistirme)
//   ZORVEN_API_INTERNAL_URL          Go API tabani (vars. https://<PLATFORM_DOMAIN>)

export type Env = Record<string, string | undefined>
export type FetchFn = (url: string, init?: any) => Promise<Response>

type Lang = "tr" | "en"

const MESSAGES: Record<string, Record<Lang, string>> = {
  captcha_failed: {
    tr: "İnsan doğrulaması başarısız oldu. Lütfen tekrar deneyin.",
    en: "Human verification failed. Please try again.",
  },
}

export function pickLang(acceptLanguage: string | null | undefined): Lang {
  const first = (acceptLanguage || "").split(",")[0]?.trim().toLowerCase() || ""
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
 * Platform CAPTCHA jetonunu (X-Zorven-Captcha) siteverify ile dogrular. Gizli
 * anahtar tanimli degilse atlanir. Hata -> APIError (400, captcha_failed).
 * Kayit ve sifre sifirlama istegi ortak kullanir.
 */
export async function verifyCaptcha(ctx: any, env: Env, f: FetchFn): Promise<void> {
  const secret = (env.ZORVEN_CAPTCHA_PLATFORM_SECRET || "").trim()
  if (!secret) return
  const lang = pickLang(header(ctx, "accept-language"))
  const token = (header(ctx, "x-zorven-captcha") || "").trim()
  if (!token) fail("captcha_failed", lang)
  let ok = false
  try {
    const r = await postJSON(f, `${apiBase(env)}/api/v1/captcha/siteverify`, {
      secret,
      response: token,
      remoteip: clientIp(ctx) || undefined,
    })
    ok = r?.success === true
  } catch (err) {
    // Dogrulama hizmetine ulasilamadi: kapali tut (bot riski > gecici kesinti).
    console.error("[captcha] siteverify basarisiz:", err)
  }
  if (!ok) fail("captcha_failed", lang)
}

/** Better Auth sifre sifirlama istegi yollari (1.7.x: /request-password-reset; eski ad da kapsanir). */
export const PASSWORD_RESET_REQUEST_PATHS = ["/request-password-reset", "/forget-password"]

/** Sifre sifirlama ISTEGI oncesi kontrol (yalniz CAPTCHA; e-posta gonderimini kotuye kullanimdan korur). */
export async function checkPasswordResetRequest(ctx: any, env: Env, f: FetchFn): Promise<void> {
  await verifyCaptcha(ctx, env, f)
}

/**
 * /sign-up/email oncesi kontrol. Hata -> APIError (400, code captcha_failed).
 * Sosyal giris bu yoldan gecmez (saglayici dogrular).
 */
export async function checkSignup(ctx: any, env: Env, f: FetchFn): Promise<void> {
  await verifyCaptcha(ctx, env, f)
}
