// Kisisel veri disa aktarma ("Verilerimi indir"): kullanicinin kendisi hakkinda tuttugumuz
// veriyi tek JSON olarak toplar.
//
// Neden Nitro'da (Go'da degil): kaynak tablolar (Better Auth user/account/session/member/
// invitation ve Go'nun audit_log/api_tokens/clients tablolari) AYNI Postgres'te; web servisi
// zaten bu veritabanina baglidir (auth.ts hookPool). Kimlik Better Auth oturumundan gelir
// (account-plugin.ts), boylece Go'ya ic cagri ya da ikinci bir kimlik mekanizmasi gerekmez.
//
// GIZLI DEGERLER ASLA DAHIL EDILMEZ: parola ozeti, oturum/OAuth jetonlari (accessToken,
// refreshToken, idToken), TOTP sirri ve yedek kodlar, API anahtari sirri/ozeti. Sorgular bu
// sutunlari hic SECMEZ (account-export.test.ts SQL metnini bununla tarar).
//
// Bolumler EN IYI CABA (profil haric): ticari/kapali katmanda olmayan tablolar (acik surumde
// audit_log, user_risk, duyuru tercihleri...) ya da gecici bir sorgu hatasi tum disa aktarmayi
// bozmaz; atlanan bolum "unavailable" listesinde acikca soylenir.

/** SQL calistirabilen en kucuk arayuz (pg.Pool bunu saglar; testte sahte). */
export interface Db {
  query: (text: string, values?: unknown[]) => Promise<{ rows: any[] }>
}

export interface ExportOptions {
  /** Saat; testlerde enjekte edilir. */
  now?: Date
  /** Denetim kaydi satir tavani (varsayilan 20000); asilirsa auditLog.truncated=true. */
  auditLimit?: number
  /** Su anki oturumun kimligi (oturum listesinde current=true). */
  currentSessionId?: string
}

export interface UserDataExport {
  format: "zorven-personal-data-export"
  version: 1
  generatedAt: string
  profile: Record<string, unknown>
  signInMethods: unknown[]
  twoFactor: { enabled: boolean; authenticatorConfigured: boolean }
  sessions: unknown[]
  organizations: unknown[]
  apiKeys: unknown[]
  auditLog: { entries: unknown[]; truncated: boolean }
  invitationsSent: unknown[]
  devices: unknown[]
  preferences: unknown
  signup: unknown
  authorizedApps: unknown[]
  excluded: string
  /** Veritabaninda bulunamayan/okunamayan bolumler (bos = hepsi dahil). */
  unavailable: string[]
}

const EXCLUDED_NOTE =
  "Not included for security: password hashes, session tokens, OAuth access/refresh/ID tokens, " +
  "TOTP secret and backup codes, and API key secrets or hashes."

const DEFAULT_AUDIT_LIMIT = 20_000

const iso = (v: unknown): string | null => {
  if (v === null || v === undefined) return null
  const d = v instanceof Date ? v : new Date(String(v))
  return Number.isNaN(d.getTime()) ? String(v) : d.toISOString()
}

export async function buildUserExport(db: Db, userId: string, opts: ExportOptions = {}): Promise<UserDataExport> {
  const now = opts.now ?? new Date()
  const auditLimit = opts.auditLimit ?? DEFAULT_AUDIT_LIMIT
  const unavailable: string[] = []
  const rows = async (text: string, values: unknown[]): Promise<any[]> => (await db.query(text, values)).rows

  // Profil zorunlu: yoksa disa aktarilacak bir kullanici yoktur.
  const [u] = await rows(
    `SELECT "id", "name", "email", "emailVerified", "image", "createdAt", "updatedAt", "twoFactorEnabled"
       FROM "user" WHERE "id" = $1`,
    [userId],
  )
  if (!u) throw new Error("kullanici bulunamadi")

  // Bolum calistirici: hata -> varsayilan deger + unavailable'a ad.
  async function section<T>(name: string, fallback: T, fn: () => Promise<T>): Promise<T> {
    try {
      return await fn()
    } catch (e) {
      console.warn(`[account-export] ${name} bolumu okunamadi:`, (e as Error)?.message ?? e)
      unavailable.push(name)
      return fallback
    }
  }

  const signInMethods = await section("signInMethods", [] as unknown[], async () =>
    (
      await rows(
        // "password" yalniz VAR/YOK olarak okunur (ozet disari cikmaz).
        `SELECT "providerId", "accountId", "scope", ("password" IS NOT NULL) AS "hasPassword", "createdAt", "updatedAt"
           FROM "account" WHERE "userId" = $1 ORDER BY "createdAt"`,
        [userId],
      )
    ).map((r) => ({
      provider: r.providerId,
      accountId: r.accountId,
      hasPassword: !!r.hasPassword,
      scope: r.scope ?? null,
      linkedAt: iso(r.createdAt),
      updatedAt: iso(r.updatedAt),
    })),
  )

  const twoFactor = await section("twoFactor", { enabled: !!u.twoFactorEnabled, authenticatorConfigured: false }, async () => {
    const r = await rows(`SELECT "verified" FROM "twoFactor" WHERE "userId" = $1`, [userId])
    return { enabled: !!u.twoFactorEnabled, authenticatorConfigured: r.some((x) => x.verified !== false) }
  })

  const sessions = await section("sessions", [] as unknown[], async () =>
    (
      await rows(
        `SELECT "id", "createdAt", "updatedAt", "expiresAt", "ipAddress", "userAgent", "activeOrganizationId"
           FROM "session" WHERE "userId" = $1 ORDER BY "createdAt" DESC`,
        [userId],
      )
    ).map((r) => ({
      id: r.id,
      current: !!opts.currentSessionId && r.id === opts.currentSessionId,
      active: !!r.expiresAt && new Date(r.expiresAt).getTime() > now.getTime(),
      createdAt: iso(r.createdAt),
      lastActiveAt: iso(r.updatedAt),
      expiresAt: iso(r.expiresAt),
      ipAddress: r.ipAddress ?? null,
      userAgent: r.userAgent ?? null,
      activeOrganizationId: r.activeOrganizationId ?? null,
    })),
  )

  const organizations = await section("organizations", [] as unknown[], async () =>
    (
      await rows(
        `SELECT o."id", o."name", o."slug", m."role", m."createdAt"
           FROM "member" m JOIN "organization" o ON o."id" = m."organizationId"
          WHERE m."userId" = $1 ORDER BY m."createdAt"`,
        [userId],
      )
    ).map((r) => ({ id: r.id, name: r.name, slug: r.slug, role: r.role, joinedAt: iso(r.createdAt) })),
  )

  const apiKeys = await section("apiKeys", [] as unknown[], async () =>
    (
      await rows(
        // Sir/ozet/arama kimligi (token_hash, token_id) SECILMEZ; yalniz on ek ve meta veri.
        `SELECT id, tenant_id, name, token_prefix, scopes, last_used_at, expires_at, revoked_at, created_at
           FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`,
        [userId],
      )
    ).map((r) => ({
      id: r.id,
      organizationId: r.tenant_id,
      name: r.name,
      prefix: r.token_prefix,
      scopes: r.scopes ?? [],
      createdAt: iso(r.created_at),
      lastUsedAt: iso(r.last_used_at),
      expiresAt: iso(r.expires_at),
      revokedAt: iso(r.revoked_at),
    })),
  )

  const auditLog = await section("auditLog", { entries: [] as unknown[], truncated: false }, async () => {
    // Aktor e-posta (ya da e-postasiz oturumda kullanici kimligi) olarak yazilir (api/audit.go).
    const r = await rows(
      `SELECT id, tenant_id, action, target, detail, ip, created_at
         FROM audit_log WHERE actor = ANY($1::text[]) ORDER BY created_at DESC LIMIT $2`,
      [[String(u.email ?? "").toLowerCase(), userId].filter(Boolean), auditLimit + 1],
    )
    const truncated = r.length > auditLimit
    return {
      entries: r.slice(0, auditLimit).map((x) => ({
        id: x.id,
        organizationId: x.tenant_id || null,
        action: x.action,
        target: x.target || null,
        detail: x.detail || null,
        ip: x.ip || null,
        createdAt: iso(x.created_at),
      })),
      truncated,
    }
  })

  const invitationsSent = await section("invitationsSent", [] as unknown[], async () =>
    (
      await rows(
        `SELECT "id", "organizationId", "email", "role", "status", "expiresAt", "createdAt"
           FROM "invitation" WHERE "inviterId" = $1 ORDER BY "createdAt" DESC`,
        [userId],
      )
    ).map((r) => ({
      id: r.id,
      organizationId: r.organizationId,
      email: r.email,
      role: r.role ?? null,
      status: r.status,
      createdAt: iso(r.createdAt),
      expiresAt: iso(r.expiresAt),
    })),
  )

  const devices = await section("devices", [] as unknown[], async () =>
    (
      await rows(
        // Token/ozet sutunlari secilmez.
        `SELECT id, tenant_id, name, hostname, os, arch, agent_version, created_at, last_seen_at
           FROM clients WHERE user_id = $1 ORDER BY created_at DESC`,
        [userId],
      )
    ).map((r) => ({
      id: r.id,
      organizationId: r.tenant_id,
      name: r.name,
      hostname: r.hostname ?? null,
      os: r.os ?? null,
      arch: r.arch ?? null,
      agentVersion: r.agent_version ?? null,
      createdAt: iso(r.created_at),
      lastSeenAt: iso(r.last_seen_at),
    })),
  )

  const preferences = await section("preferences", null as unknown, async () => {
    const [r] = await rows(
      `SELECT subscribed, unsubscribed_at, lang, updated_at FROM announcement_prefs WHERE user_id = $1`,
      [userId],
    )
    return r
      ? {
          productAnnouncements: {
            subscribed: !!r.subscribed,
            language: r.lang || null,
            unsubscribedAt: iso(r.unsubscribed_at),
            updatedAt: iso(r.updated_at),
          },
        }
      : null
  })

  const signup = await section("signup", null as unknown, async () => {
    const [r] = await rows(`SELECT signup_ip, signup_country, created_at FROM user_risk WHERE user_id = $1`, [userId])
    return r ? { ip: r.signup_ip || null, country: r.signup_country || null, at: iso(r.created_at) } : null
  })

  const authorizedApps = await section("authorizedApps", [] as unknown[], async () =>
    (
      await rows(
        `SELECT g.id, g.tenant_id, c.client_name, g.resource, g.scope, g.created_at, g.last_used_at, g.revoked_at
           FROM oauth_grants g LEFT JOIN oauth_clients c ON c.client_id = g.client_id
          WHERE g.user_id = $1 ORDER BY g.created_at DESC`,
        [userId],
      )
    ).map((r) => ({
      id: r.id,
      organizationId: r.tenant_id,
      appName: r.client_name || null,
      resource: r.resource,
      scope: r.scope,
      authorizedAt: iso(r.created_at),
      lastUsedAt: iso(r.last_used_at),
      revokedAt: iso(r.revoked_at),
    })),
  )

  return {
    format: "zorven-personal-data-export",
    version: 1,
    generatedAt: now.toISOString(),
    profile: {
      id: u.id,
      name: u.name,
      email: u.email,
      emailVerified: !!u.emailVerified,
      image: u.image ?? null,
      createdAt: iso(u.createdAt),
      updatedAt: iso(u.updatedAt),
    },
    signInMethods,
    twoFactor,
    sessions,
    organizations,
    apiKeys,
    auditLog,
    invitationsSent,
    devices,
    preferences,
    signup,
    authorizedApps,
    excluded: EXCLUDED_NOTE,
    unavailable,
  }
}

/** Indirme dosya adi: zorven-data-YYYYMMDD.json (UTC). */
export function exportFileName(now: Date = new Date()): string {
  const d = now.toISOString().slice(0, 10).replaceAll("-", "")
  return `zorven-data-${d}.json`
}
