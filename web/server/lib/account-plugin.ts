// Zorven hesap eklentisi (Better Auth): etkin oturumlar, kisisel veri disa aktarma ve
// hesap silme kancalari.
//
// Neden Better Auth eklentisi: Go yalnizca /api/auth/* yolunu web konteynerine aktarir
// (server/authproxy.go). Eklenti ucu /api/auth/zorven/... altinda yasar, Better Auth'un
// oturum, Origin/CSRF ve hiz siniri katmanlarini devralir ve Go oturum onbellegi proxy'de
// (POST + oturum cerezi) zaten temizlenir.
//
// Uclar (tumu oturum ister):
//   GET  /zorven/sessions          etkin oturumlar (cihaz/UA, IP, olusturma, son etkinlik, "bu oturum")
//   POST /zorven/sessions/revoke   {id} -> tek oturumu sonlandir (kendi oturumun haric: cikis yap)
//   POST /zorven/account/export    kisisel veri JSON'u (kullanici basina saatte 3)
// Diger tum oturumlari sonlandirmak icin Better Auth'un POST /revoke-other-sessions ucu kullanilir.
//
// OTURUM JETONU ASLA DONMEZ (F-49, auth-hooks.ts): bearer() eklentisi HAM jetonu da kabul eder;
// jeton sayfa JS'ine sizsa oturum calinirdi. Bu yuzden listede jeton yerine oturum `id`si vardir
// ve kutuphanenin GET /list-sessions yaniti da (jeton + tazelik zorunlulugu) burada jetonsuz
// hale getirilir.
//
// Hesap silme kancalari (account-delete.ts): POST /delete-user ON KONTROLU ve
// GET /delete-user/callback on dogrulamasi (dostca hata yonlendirmesi) + hiz sinirlari.

import { createAuthEndpoint, createAuthMiddleware, sensitiveSessionMiddleware, APIError } from "better-auth/api"
import type { BetterAuthPlugin } from "better-auth"
import { buildUserExport, exportFileName, type Db } from "./account-export.ts"
import { createCallbackPrecheck, createDeleteGuard, type DeleteDeps } from "./account-delete.ts"
import { createWindowLimiter, type WindowLimiter } from "./window-limiter.ts"

export interface AccountPluginDeps extends DeleteDeps {
  /** Disa aktarma icin SQL arayuzu (pg.Pool). */
  db: Db
  /** Kullanici basina disa aktarma siniri (varsayilan saatte 3); testlerde enjekte edilir. */
  exportLimiter?: WindowLimiter
}

/** Kullanici basina disa aktarma: saatte 3. */
export const EXPORT_MAX_PER_HOUR = 3

export function accountPlugin(deps: AccountPluginDeps) {
  const exportLimiter = deps.exportLimiter ?? createWindowLimiter({ max: EXPORT_MAX_PER_HOUR, windowMs: 3_600_000 })
  const deleteGuard = createDeleteGuard(deps)
  const callbackPrecheck = createCallbackPrecheck(deps)
  const now = deps.now ?? Date.now

  const plugin = {
    id: "zorven-account",

    endpoints: {
      // Etkin oturumlar. Jeton yok; `current` istegi yapan oturumdur.
      zorvenSessions: createAuthEndpoint(
        "/zorven/sessions",
        { method: "GET", use: [sensitiveSessionMiddleware] },
        async (ctx: any) => {
          const { session, user } = ctx.context.session
          const all = await ctx.context.internalAdapter.listSessions(user.id)
          const active = (all as any[]).filter((s) => new Date(s.expiresAt).getTime() > now())
          active.sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
          ctx.setHeader("cache-control", "no-store")
          return ctx.json(
            active.map((s) => ({
              id: s.id,
              current: s.token === session.token,
              createdAt: s.createdAt,
              updatedAt: s.updatedAt,
              expiresAt: s.expiresAt,
              ipAddress: s.ipAddress ?? null,
              userAgent: s.userAgent ?? null,
            })),
          )
        },
      ),

      // Tek oturumu sonlandir (yalniz kullanicinin kendi oturumlari; baskasininki "bulunamadi" sayilir).
      zorvenRevokeSession: createAuthEndpoint(
        "/zorven/sessions/revoke",
        { method: "POST", use: [sensitiveSessionMiddleware] },
        async (ctx: any) => {
          const id = (ctx.body as { id?: unknown } | undefined)?.id
          if (typeof id !== "string" || id === "" || id.length > 200) {
            throw new APIError("BAD_REQUEST", { code: "INVALID_SESSION_ID", message: "gecersiz oturum kimligi" })
          }
          const { session, user } = ctx.context.session
          const mine = (await ctx.context.internalAdapter.listSessions(user.id)) as any[]
          const target = mine.find((s) => s.id === id)
          if (!target) {
            // Baskasinin ya da zaten bitmis oturum: bilgi sizdirmadan idempotent basari.
            return ctx.json({ status: true })
          }
          if (target.token === session.token) {
            throw new APIError("BAD_REQUEST", {
              code: "CANNOT_REVOKE_CURRENT_SESSION",
              message: "bu oturumu sonlandirmak icin cikis yapin",
            })
          }
          await ctx.context.internalAdapter.deleteSession(target.token)
          return ctx.json({ status: true })
        },
      ),

      // Kisisel veri disa aktarma. POST: Origin/CSRF denetimi (Better Auth) calissin, baska
      // bir sitenin baglantisi hakkimizi tuketemesin.
      zorvenExportData: createAuthEndpoint(
        "/zorven/account/export",
        { method: "POST", use: [sensitiveSessionMiddleware] },
        async (ctx: any) => {
          const { session, user } = ctx.context.session
          const lim = exportLimiter.take(user.id, now())
          if (!lim.ok) {
            throw new APIError(
              "TOO_MANY_REQUESTS",
              { code: "EXPORT_RATE_LIMITED", message: `en fazla saatte ${EXPORT_MAX_PER_HOUR} kez indirilebilir` },
              { "Retry-After": String(lim.retryAfterSec) },
            )
          }
          const data = await buildUserExport(deps.db, user.id, {
            now: new Date(now()),
            currentSessionId: session.id,
          })
          ctx.setHeader("content-disposition", `attachment; filename="${exportFileName(new Date(now()))}"`)
          ctx.setHeader("cache-control", "no-store")
          return ctx.json(data)
        },
      ),
    },

    hooks: {
      before: [
        {
          // Hesap silme istegi: taze oturum + parola + Go on kontrolu (e-posta gonderilmeden once).
          matcher: (c: any) => c.path === "/delete-user",
          handler: createAuthMiddleware(async (ctx: any) => {
            await deleteGuard(ctx)
          }),
        },
        {
          // E-posta baglantisi: oturum/jeton gecersizse ham JSON yerine panele dostca yonlendir.
          matcher: (c: any) => c.path === "/delete-user/callback",
          handler: createAuthMiddleware(async (ctx: any) => {
            await callbackPrecheck(ctx)
          }),
        },
      ],
      after: [
        {
          // Kutuphanenin oturum listesi ham jeton doner: jetonsuz hale getir (F-49).
          matcher: (c: any) => c.path === "/list-sessions",
          handler: createAuthMiddleware(async (ctx: any) => {
            const r = ctx.context.returned
            if (Array.isArray(r)) {
              return ctx.json(r.map(({ token: _t, ...rest }: any) => rest))
            }
          }),
        },
      ],
    },

    // IP basina (kullanici basina sinir ayrica endpoint icinde). Yol adlari eklentiye gore
    // /api/auth tabanindan sonraki kisimdir.
    rateLimit: [
      { window: 3600, max: 10, pathMatcher: (p: string) => p === "/zorven/account/export" },
      { window: 3600, max: 5, pathMatcher: (p: string) => p === "/delete-user" },
      { window: 3600, max: 10, pathMatcher: (p: string) => p === "/delete-user/callback" },
    ],
  } satisfies BetterAuthPlugin

  return plugin
}
