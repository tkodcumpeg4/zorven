import { createHash } from "node:crypto"
import { APIError, createAuthMiddleware, getSessionFromCtx } from "better-auth/api"
import { checkSignup, type Env, type FetchFn } from "./signup-guard.ts"

// Bu dosya yalnizca Better Auth hook'larini icerir (DB/ortam yan etkisi yok);
// boylece birim testi memory adapter ile calisabilir (auth-hooks.test.ts).

// F-46: kullanilmis TOTP kodunun ayni pencerede tekrar kabulu engellenir.
// Better Auth bunu kendisi yapmaz. Anahtar KULLANICI kimligine baglidir
// (giris 2. adiminin two-factor ara cerezi her giriste degisir; cereze bagli
// anahtar ayni kodun yeni bir giris denemesinde yeniden kullanilmasini
// engellemezdi). Basarili dogrulamadan sonra sha256(userId|kod) 120sn
// (30sn adim x ±1 pencere) bellekte tutulur. Web servisi tek surectir; coklu
// replikaya gecilirse bu depo DB'ye tasinmalidir.
const usedTotp = new Map<string, number>()
export const TOTP_REPLAY_TTL_MS = 120_000

function totpKey(userId: string, code: unknown): string {
  return createHash("sha256").update(userId + "|" + String(code ?? "").trim()).digest("hex")
}
function pruneUsedTotp(now: number): void {
  for (const [k, exp] of usedTotp) if (exp <= now) usedTotp.delete(k)
}
/** Yalniz testler icin. */
export function _resetTotpReplayStore(): void {
  usedTotp.clear()
}

// Dogrulamayi yapan kullanici: oturum varsa onun kullanicisi (2FA etkinlestirme),
// yoksa giris 2. adiminin imzali two_factor cerezinin isaret ettigi kullanici
// (Better Auth verifyTwoFactor ile ayni sira).
async function totpUserId(ctx: any): Promise<string | null> {
  const session = await getSessionFromCtx(ctx).catch(() => null)
  if (session?.user?.id) return session.user.id
  const cookie = ctx.context.createAuthCookie("two_factor")
  const signed = await ctx.getSignedCookie(cookie.name, ctx.context.secret)
  if (!signed) return null
  const v = await ctx.context.internalAdapter.findVerificationValue(signed).catch(() => null)
  return v?.value ?? null
}

/**
 * Better Auth hook'lari. env/fetch enjekte edilebilir (testler); varsayilan
 * process.env + global fetch, ortam her istekte okunur.
 */
export function createAuthHooks(deps: { env?: () => Env; fetch?: FetchFn } = {}) {
  const getEnv = deps.env ?? (() => process.env as Env)
  const doFetch: FetchFn = deps.fetch ?? ((url, init) => fetch(url, init))
  return makeHooks(getEnv, doFetch)
}

function makeHooks(getEnv: () => Env, doFetch: FetchFn) {
 return {
  before: createAuthMiddleware(async (ctx) => {
    if (ctx.path === "/sign-up/email") {
      await checkSignup(ctx, getEnv(), doFetch)
      return
    }
    if (ctx.path !== "/two-factor/verify-totp") return
    const now = Date.now()
    pruneUsedTotp(now)
    const userId = await totpUserId(ctx)
    if (!userId) return
    const exp = usedTotp.get(totpKey(userId, (ctx.body as { code?: unknown } | undefined)?.code))
    if (exp && exp > now) {
      throw new APIError("BAD_REQUEST", {
        message: "Bu dogrulama kodu zaten kullanildi; authenticator'daki yeni kodu bekleyin.",
      })
    }
  }),
  after: createAuthMiddleware(async (ctx) => {
    if (ctx.path === "/two-factor/verify-totp") {
      if (ctx.context.returned instanceof APIError) return
      // Giriste yeni oturum olusur (newSession); etkinlestirmede mevcut oturum.
      const userId = (ctx.context as any).newSession?.user?.id ?? (await totpUserId(ctx))
      if (!userId) return
      usedTotp.set(
        totpKey(userId, (ctx.body as { code?: unknown } | undefined)?.code),
        Date.now() + TOTP_REPLAY_TTL_MS,
      )
      return
    }

    // F-49: get-session govdesinde ham oturum token'i donmesin. Panel ve Go
    // proxy oturumu cerezle tasir; govdedeki token'a ihtiyac yoktur.
    if (ctx.path === "/get-session") {
      const r = ctx.context.returned as any
      if (r && !(r instanceof APIError) && typeof r === "object" && r.session && "token" in r.session) {
        const { token: _t, ...session } = r.session
        return ctx.json({ ...r, session })
      }
    }
  }),
 }
}

export const authHooks = createAuthHooks()
