// Hesap silme: Better Auth user.deleteUser ayarlari ve on kontrol kancasi.
//
// Better Auth 1.7.3 /delete-user (node_modules/better-auth/dist/api/routes/update-user.mjs):
//   1. sendDeleteAccountVerification verilmisse e-postayla baglanti gider ve tazelik/parola
//      kontrolu YAPILMAZ (parola yalniz GONDERILIRSE dogrulanir). Bu yuzden asagidaki
//      deleteGuard on kancasi sarttir: taze oturum + parolali hesapta parola + Go on kontrolu.
//   2. Baglantidaki GET /delete-user/callback: oturum + tek kullanimlik jeton dogrulanir,
//      sonra beforeDelete -> kullanici silinir (oturum/hesap/uyelik/2FA CASCADE ile gider).
//
// Kullanicinin verisinin geri kalani (tek uyeli organizasyonlar, istemciler, API anahtarlari,
// denetim anonimlestirme) Go'dadir: beforeDelete ic ucu cagirir (account-internal.ts).
//
// Hata kodlari panelin yerel metinlerine eslenir (profile.accountDelete.*); `message` ise
// API tuketicileri icin istegin diliyle (cerez > Accept-Language > tr) yazilir.

import { APIError, getAuthoritativeSessionFromCtx } from "better-auth/api"
import {
  AccountBlockedError,
  AccountServiceError,
  type AccountClient,
  type AccountPlan,
} from "./account-internal.ts"
import type { EmailLang } from "./email-lang.ts"
import { deleteAccountEmail } from "./email-account.ts"
import type { OutgoingMail } from "./email-callbacks.ts"

/** Onay baglantisinin omru (sn): kisa, geri alinamaz bir islem icin 30 dakika. */
export const DELETE_TOKEN_EXPIRES_SECONDS = 30 * 60

/** Silme istegi icin oturumun azami yasi (ms): bundan eski oturum yeniden giris ister. */
export const DELETE_SESSION_FRESH_MS = 60 * 60 * 1000

export type DeleteErrorCode =
  | "ACCOUNT_DELETE_PASSWORD_REQUIRED"
  | "ACCOUNT_DELETE_SESSION_NOT_FRESH"
  | "ACCOUNT_DELETE_OWNER_BLOCKED"
  | "ACCOUNT_DELETE_PLATFORM_BLOCKED"
  | "ACCOUNT_DELETE_UNAVAILABLE"

const MESSAGES: Record<DeleteErrorCode, Record<EmailLang, string>> = {
  ACCOUNT_DELETE_PASSWORD_REQUIRED: {
    tr: "Hesabı silmek için parolanı girmelisin.",
    en: "Enter your password to delete the account.",
  },
  ACCOUNT_DELETE_SESSION_NOT_FRESH: {
    tr: "Güvenlik için hesabı silmeden önce yeniden giriş yapmalısın.",
    en: "For your security, sign in again before deleting your account.",
  },
  ACCOUNT_DELETE_OWNER_BLOCKED: {
    tr: "Başka üyeleri olan bir organizasyonun sahibisin ({orgs}). Önce sahipliği devret ya da organizasyonu sil; sonra hesabını silebilirsin.",
    en: "You are the owner of an organization that has other members ({orgs}). Transfer ownership or delete the organization first, then delete your account.",
  },
  ACCOUNT_DELETE_PLATFORM_BLOCKED: {
    tr: "Platform yöneticisi hesapları panelden silinemez.",
    en: "Platform administrator accounts cannot be deleted from the panel.",
  },
  ACCOUNT_DELETE_UNAVAILABLE: {
    tr: "Hesap silme şu anda yapılamıyor. Lütfen biraz sonra tekrar dene.",
    en: "Account deletion is temporarily unavailable. Please try again shortly.",
  },
}

export function deleteErrorMessage(code: DeleteErrorCode, lang: EmailLang, orgs = ""): string {
  return MESSAGES[code][lang].replace("{orgs}", () => orgs)
}

/** "Ad (slug), Ad2 (slug2)" -- en fazla 5 organizasyon. */
function orgList(orgs: { name: string; slug: string }[]): string {
  const shown = orgs.slice(0, 5).map((o) => (o.name && o.name !== o.slug ? `${o.name} (${o.slug})` : o.slug))
  return shown.join(", ") + (orgs.length > 5 ? ` +${orgs.length - 5}` : "")
}

/** Plan silmeyi engelliyorsa 409 hatasi (platform engeli sahiplik engelinden once soylenir). */
export function blockedApiError(plan: AccountPlan, lang: EmailLang): APIError {
  const platform = plan.blocking_orgs.some((o) => o.reason === "platform")
  const code: DeleteErrorCode = platform ? "ACCOUNT_DELETE_PLATFORM_BLOCKED" : "ACCOUNT_DELETE_OWNER_BLOCKED"
  const orgs = plan.blocking_orgs.filter((o) => o.reason !== "platform")
  return new APIError("CONFLICT", { code, message: deleteErrorMessage(code, lang, orgList(orgs)) })
}

function unavailableApiError(lang: EmailLang): APIError {
  return new APIError("SERVICE_UNAVAILABLE", {
    code: "ACCOUNT_DELETE_UNAVAILABLE",
    message: deleteErrorMessage("ACCOUNT_DELETE_UNAVAILABLE", lang),
  })
}

export interface DeleteDeps {
  /** Go ic cagrisi (on kontrol + silme). */
  account: AccountClient
  /** E-posta/hata dili: kayitli tercih > zorven_lang cerezi > Accept-Language > tr. */
  langFor: (userId: string | undefined, source: unknown) => Promise<EmailLang>
  /** E-posta gonderimi (mailer.ts sendMail). */
  send: (m: OutgoingMail) => Promise<void>
  /** Saat (ms); testlerde enjekte edilir. */
  now?: () => number
}

/**
 * POST /delete-user ON KONTROLU (sendDeleteAccountVerification'dan ONCE calisir):
 *   - oturum taze olmali (yeniden giris gerekebilir),
 *   - parolali hesapta parola gonderilmeli (dogrulugunu Better Auth kontrol eder),
 *   - Go on kontrolu: baska uyeli organizasyonun tek sahibi / platform yoneticisi ise engel.
 * Onay jetonuyla tamamlama (body.token) e-posta sahipligiyle zaten dogrulanmistir; atlanir.
 * Oturum yoksa hicbir sey yapmaz (uc kendisi 401 verir).
 */
export function createDeleteGuard(deps: DeleteDeps) {
  const now = deps.now ?? Date.now
  return async function deleteGuard(ctx: any): Promise<void> {
    const body = (ctx.body ?? {}) as { token?: unknown; password?: unknown }
    if (body.token) return
    const session = await getAuthoritativeSessionFromCtx(ctx)
    if (!session?.session || !session.user) return
    const lang = await deps.langFor(session.user.id, ctx)

    const createdAt = new Date(session.session.createdAt).getTime()
    if (!Number.isFinite(createdAt) || now() - createdAt >= DELETE_SESSION_FRESH_MS) {
      throw new APIError("FORBIDDEN", {
        code: "ACCOUNT_DELETE_SESSION_NOT_FRESH",
        message: deleteErrorMessage("ACCOUNT_DELETE_SESSION_NOT_FRESH", lang),
      })
    }

    const credential = await ctx.context.internalAdapter.findCredentialAccount(session.user.id)
    // Better Auth parolayi yalniz TRUTHY ise dogrular (bos metin dogrulamayi atlatirdi): bos da reddedilir.
    if (credential?.password && (typeof body.password !== "string" || body.password === "")) {
      throw new APIError("BAD_REQUEST", {
        code: "ACCOUNT_DELETE_PASSWORD_REQUIRED",
        message: deleteErrorMessage("ACCOUNT_DELETE_PASSWORD_REQUIRED", lang),
      })
    }

    let plan: AccountPlan
    try {
      plan = await deps.account.plan(session.user.id)
    } catch (e) {
      console.error("[account-delete] on kontrol basarisiz:", (e as Error)?.message ?? e)
      throw unavailableApiError(lang)
    }
    if (!plan.can_delete) throw blockedApiError(plan, lang)
  }
}

/** Better Auth `user.deleteUser` secenegi. */
export function deleteUserOptions(deps: DeleteDeps) {
  return {
    enabled: true,
    deleteTokenExpiresIn: DELETE_TOKEN_EXPIRES_SECONDS,

    // Onay e-postasi: baglanti Better Auth'un /delete-user/callback adresidir.
    sendDeleteAccountVerification: async (
      { user, url }: { user: { id: string; name?: string | null; email: string }; url: string },
      request?: unknown,
    ): Promise<void> => {
      const lang = await deps.langFor(user.id, request)
      const mail = deleteAccountEmail(user.name ?? "", user.email, url, DELETE_TOKEN_EXPIRES_SECONDS / 60, lang)
      await deps.send({ to: user.email, subject: mail.subject, html: mail.html, text: mail.text })
    },

    // Kullanici satiri silinmeden ONCE: Go kisisel/tek uyeli organizasyonlari tum kaynaklariyla
    // siler, diger uyelikleri ve kisisel API anahtarlarini kaldirir. Hata -> silme DURUR
    // (Go idempotenttir; kullanici tekrar deneyebilir). E-posta baglantisindan (tarayici GET)
    // gelen hata ham JSON yerine panele yonlendirilir (callbackBrowserRedirect).
    beforeDelete: async (user: { id: string }, request?: unknown): Promise<void> => {
      const lang = await deps.langFor(user.id, request)
      let err: APIError
      try {
        await deps.account.purge(user.id)
        return
      } catch (e) {
        if (e instanceof AccountBlockedError) err = blockedApiError(e.plan, lang)
        else {
          if (e instanceof AccountServiceError) console.error("[account-delete] silme basarisiz:", e.message)
          else console.error("[account-delete] beklenmeyen hata:", e)
          err = unavailableApiError(lang)
        }
      }
      throw isBrowserGet(request) ? errorRedirect(String((err as any).body?.code ?? "UNKNOWN")) : err
    },
  }
}

/** Silme baglantisi (GET callback) basarisiz olursa panelin bu sayfasina `?accountDeleteError=<KOD>` ile donulur. */
export const DELETE_ERROR_REDIRECT = "/profile"

function errorLocation(code: string): string {
  return `${DELETE_ERROR_REDIRECT}?accountDeleteError=${encodeURIComponent(code)}`
}

function errorRedirect(code: string): APIError {
  return new APIError("FOUND", undefined, { location: errorLocation(code) })
}

// E-posta baglantisini tarayici GET ile acar; POST (JSON API) istemcileri hata govdesi bekler.
function isBrowserGet(request: unknown): boolean {
  return (request as { method?: string } | null | undefined)?.method === "GET"
}

/**
 * GET /delete-user/callback ON KONTROLU (salt okunur; jetonu TUKETMEZ): oturum yok (baglanti baska
 * tarayicida acildi), jeton gecersiz/suresi dolmus ya da baska kullaniciya ait ise Better Auth'un
 * ham JSON 404'u yerine panele `?accountDeleteError=<KOD>` ile yonlendirilir. Kutuphane ayni
 * kontrolleri kendisi de yapar; bu yalnizca dostca hata yuzeyidir. (after kancasi kullanilamaz:
 * Better Auth uc hata verince durum kodunu after kancasindan degistirmeye izin vermiyor.)
 */
export function createCallbackPrecheck(deps: Pick<DeleteDeps, "now">) {
  const now = deps.now ?? Date.now
  return async function callbackPrecheck(ctx: any): Promise<void> {
    const token = String(ctx.query?.token ?? "")
    const session = await getAuthoritativeSessionFromCtx(ctx)
    if (!session?.session || !session.user) throw ctx.redirect(errorLocation("SESSION_REQUIRED"))
    const v = token ? await ctx.context.internalAdapter.findVerificationValue(`delete-account-${token}`) : null
    if (!v || v.value !== session.user.id || new Date(v.expiresAt).getTime() <= now()) {
      throw ctx.redirect(errorLocation("INVALID_TOKEN"))
    }
  }
}
