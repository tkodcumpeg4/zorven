import { betterAuth } from "better-auth"
import { organization, bearer, twoFactor } from "better-auth/plugins"
import pg from "pg"
import { randomBytes, createHash } from "crypto"
import { APIError, createAuthMiddleware } from "better-auth/api"
import { sendMail } from "./mailer"
import { stripBrandKeywords, slugRejectReason } from "./brand"
import {
  verificationEmail,
  resetPasswordEmail,
  otpEmail,
  changeEmailApprovalEmail,
} from "./email-templates"

const { Pool } = pg

// Uretimde eksik gizli anahtar/DSN sessizce gelistirme varsayilanina dusmez:
// acik hata ile baslatma reddedilir. Gelistirmede uyari loglanir.
const isProd = process.env.NODE_ENV === "production"

function requireEnvOrDevDefault(value: string | undefined, name: string, devDefault: string): string {
  if (value) return value
  if (isProd) {
    throw new Error(`${name} tanimli degil: uretimde zorunlu, baslatma reddedildi`)
  }
  console.warn(`[auth] ${name} tanimli degil; yalniz gelistirme icin varsayilan kullaniliyor`)
  return devDefault
}

const connectionString = requireEnvOrDevDefault(
  process.env.DATABASE_URL || process.env.ZORVEN_DB_DSN,
  "DATABASE_URL (veya ZORVEN_DB_DSN)",
  "postgres://rpshell:rpshell@localhost:5432/rpshell",
)

const secret = requireEnvOrDevDefault(
  process.env.BETTER_AUTH_SECRET || process.env.ZORVEN_SESSION_SECRET,
  "BETTER_AUTH_SECRET (veya ZORVEN_SESSION_SECRET)",
  "rpshell-dev-session-secret-key-change-in-production-min-32-chars",
)

// Signup hook'unun kullandigi ayri bir havuz (Better Auth'un ic havuzuna
// karismadan ham SQL calistirmak icin).
const hookPool = new Pool({ connectionString })

/**
 * FAZ 3.5 — kullanici BASINA sahip olunabilecek organizasyon siniri.
 *
 * Limit, kullanicinin SAHIP oldugu organizasyonlarin EN YUKSEK planindan
 * okunur (plans.max_owned_orgs; NULL = sinirsiz). Hic organizasyonu yoksa
 * (yeni kullanici) veya organizasyonun aboneligi yoksa "free" sayilir.
 * Sinir yalnizca YENI olusturmayi engeller; mevcutlar silinmez.
 *
 * Hata durumunda IZIN VERIR (fail-open) ve loglar: bu bir faturalama siniri,
 * guvenlik kapisi degil; gecici bir DB hatasi kullaniciyi kilitlememeli.
 * Donus: true = limit DOLDU (Better Auth olusturmayi reddeder).
 */
async function organizationLimitReached(userId: string): Promise<boolean> {
  try {
    const { rows } = await hookPool.query(
      `SELECT
         (SELECT count(*)::int FROM "member" WHERE "userId" = $1 AND "role" = 'owner') AS owned,
         (SELECT bool_or(p.max_owned_orgs IS NULL)
            FROM "member" m
            LEFT JOIN subscriptions s ON s.tenant_id = m."organizationId"
            JOIN plans p ON p.id = COALESCE(s.plan, 'free')
           WHERE m."userId" = $1 AND m."role" = 'owner') AS any_unlimited,
         (SELECT max(p.max_owned_orgs)
            FROM "member" m
            LEFT JOIN subscriptions s ON s.tenant_id = m."organizationId"
            JOIN plans p ON p.id = COALESCE(s.plan, 'free')
           WHERE m."userId" = $1 AND m."role" = 'owner') AS best_limit,
         (SELECT max_owned_orgs FROM plans WHERE id = 'free') AS free_limit`,
      [userId],
    )
    const r = rows[0]
    if (!r) return false
    const owned: number = r.owned ?? 0
    let limit: number | null
    if (owned === 0) {
      limit = r.free_limit
    } else if (r.any_unlimited) {
      limit = null
    } else {
      limit = r.best_limit
    }
    if (limit === null || limit === undefined) return false
    return owned >= limit
  } catch (err) {
    console.error("[org-limit] kontrol basarisiz, izin veriliyor:", err)
    return false
  }
}

// Kullanicinin sectigi organizasyon slug'i marka deseni veya rezerve ad ise
// olusturma/guncelleme reddedilir (kiraci adresleri ad--slug.<domain> oldugu
// icin "zorven-login" gibi slug'lar kimlik avi icin kullanilabilirdi).
// Gecerli slug bicimi: kucuk harf/rakam/tire, bas-son harf ya da rakam, en fazla
// 42 karakter, ardisik tire ("--") yok ("--" tunel adi ile slug'i ayiran
// ayiracidir: ad--slug.<domain>).
const ORG_SLUG_RE = /^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$/

// Serbest metni slug bicimine indirger: kucuk harf, gecersiz -> tire,
// ardisik tireler tek, bas/son tire kirpilir.
function normalizeSlugPart(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

async function assertSlugAllowed(slug: string | undefined): Promise<void> {
  if (!slug) return
  if (!ORG_SLUG_RE.test(slug) || slug.includes("--")) {
    throw new APIError("BAD_REQUEST", {
      message:
        "Kisa ad yalniz kucuk harf, rakam ve tek tire icerebilir (bas/son karakter harf ya da rakam, en fazla 42 karakter).",
    })
  }
  const reason = await slugRejectReason(slug, async (name) => {
    try {
      const { rows } = await hookPool.query(
        `SELECT 1 FROM reserved_names WHERE name = lower($1) LIMIT 1`,
        [name],
      )
      return rows.length > 0
    } catch (err) {
      console.error("[slug] rezerve ad kontrolu basarisiz, izin veriliyor:", err)
      return false
    }
  })
  if (reason) {
    throw new APIError("BAD_REQUEST", {
      message:
        reason === "brand"
          ? "Bu kisa ad marka korumasi nedeniyle kullanilamaz (zorven / rpshell iceremez)."
          : "Bu kisa ad platform icin ayrilmis; baska bir ad secin.",
    })
  }
}

function genId(prefix: string): string {
  return `${prefix}_${randomBytes(8).toString("hex")}`
}

// Kullaniciya benzersiz, okunabilir bir organizasyon slug'i uret.
async function uniqueOrgSlug(email: string): Promise<string> {
  // Marka sozcugu iceren e-posta onegi ("zorven@...") kayit akisini bozmadan
  // donusturulur: sozcuk cikarilir, rastgele ek zaten eklenecek (bkz. brand.ts).
  // Sonuc her zaman ORG_SLUG_RE'ye uyar (nokta/alt cizgi/buyuk harf/"--" yok);
  // taban 30 karakterle sinirlanir: taban + "-" + 6 hex <= 42.
  const base =
    normalizeSlugPart(
      stripBrandKeywords(normalizeSlugPart(email.split("@")[0] || "team") || "team"),
    ).slice(0, 30).replace(/-+$/g, "") || "team"
  for (let i = 0; i < 6; i++) {
    const suffix = randomBytes(3).toString("hex")
    const slug = `${base}-${suffix}`
    const { rows } = await hookPool.query(
      `SELECT 1 FROM "organization" WHERE lower("slug") = lower($1) LIMIT 1`,
      [slug],
    )
    if (rows.length === 0) return slug
  }
  // Cok nadir: tamamen rastgele
  return `team-${randomBytes(6).toString("hex")}`
}

// Yeni kayit olan kullaniciyi provizyonla:
//   - Hicbir uyeligi yoksa KISISEL bir organizasyon olustur (owner). Boylece
//     kullanici asla "ten_default" kiracisina dusmez (bu, onu yanlislikla
//     Platform Admin yapiyordu — bkz. admin.go isPlat).
//   - Ekip davetleri burada OTOMATIK KABUL EDILMEZ: davetli e-postadaki linkten
//     (/invite?id=...) acikca kabul eder. Kayit akisi o sayfaya geri doner.
async function provisionNewUser(userId: string, email: string, name: string): Promise<void> {
  const client = await hookPool.connect()
  try {
    const pending = await client.query(
      `SELECT 1 FROM "invitation"
       WHERE lower("email") = lower($1) AND "status" = 'pending' AND "expiresAt" > now() LIMIT 1`,
      [email],
    )
    const invitedSomewhere = pending.rows.length > 0

    // Uyeligi yoksa kisisel organizasyon olustur.
    const { rows } = await client.query(
      `SELECT count(*)::int AS n FROM "member" WHERE "userId" = $1`,
      [userId],
    )
    if ((rows[0]?.n ?? 0) === 0) {
      const orgId = genId("org")
      const slug = await uniqueOrgSlug(email)
      const orgName = (name && name.trim()) || email.split("@")[0]
      // organization INSERT'i trigger ile tenants tablosuna da yansir.
      await client.query(
        `INSERT INTO "organization" ("id", "name", "slug", "createdAt") VALUES ($1, $2, $3, now())`,
        [orgId, orgName, slug],
      )
      await client.query(
        `INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
         VALUES ($1, $2, $3, 'owner', now())`,
        [genId("mem"), orgId, userId],
      )
    }
  } finally {
    client.release()
  }
}

/**
 * Guvenilir origin listesi: BETTER_AUTH_URL, platform apex/www/panel/app ve
 * gelistirme localhost'lari. Statik: istekten (Origin basligindan) TURETILMEZ.
 */
export function trustedOriginList(env: Record<string, string | undefined> = process.env): string[] {
  const list = [
    "https://localhost:8443",
    "https://127.0.0.1:8443",
    "http://localhost:3000",
    "http://localhost:3002",
    "http://127.0.0.1:3000",
    "http://127.0.0.1:3002",
  ]
  if (env.BETTER_AUTH_URL) {
    try {
      list.push(new URL(env.BETTER_AUTH_URL).origin)
    } catch {
      // gecersiz URL: yok say (Better Auth baseURL'i zaten kendisi ekler)
    }
  }
  const pd = (env.PLATFORM_DOMAIN || "").trim().toLowerCase().replace(/\.$/, "")
  if (pd) {
    list.push(`https://${pd}`, `https://www.${pd}`, `https://panel.${pd}`, `https://app.${pd}`)
  }
  return [...new Set(list)]
}

const socialProviders: Record<string, any> = {}

if (process.env.GITHUB_CLIENT_ID && process.env.GITHUB_CLIENT_SECRET) {
  socialProviders.github = {
    clientId: process.env.GITHUB_CLIENT_ID,
    clientSecret: process.env.GITHUB_CLIENT_SECRET,
  }
}

if (process.env.GOOGLE_CLIENT_ID && process.env.GOOGLE_CLIENT_SECRET) {
  socialProviders.google = {
    clientId: process.env.GOOGLE_CLIENT_ID,
    clientSecret: process.env.GOOGLE_CLIENT_SECRET,
  }
}

// F-46: kullanilmis TOTP kodunun ayni pencerede tekrar kabulu engellenir.
// Better Auth bunu kendisi yapmaz; basarili dogrulamadan sonra kodun ozetini
// kisa sure (30sn adim x ±1 pencere icin 120sn) bellekte tutar. Tek surec
// calistigi icin bellek yeterli; anahtar kullanici/oturum cerezine ozgudur.
const usedTotp = new Map<string, number>()
const TOTP_REPLAY_TTL_MS = 120_000
// Kullaniciyi tanimlayan deger: giris 2. adiminda Better Auth'un two-factor ara
// cerezi (adi "two_factor" icerir; prefix/__Secure- degisebilir), 2FA
// etkinlestirmede oturum cerezi. Hicbiri yoksa null: engel uygulanmaz.
function totpSubject(headers: Headers | undefined): string | null {
  const raw = headers?.get("cookie")
  if (!raw) return null
  let session: string | null = null
  for (const part of raw.split(";")) {
    const i = part.indexOf("=")
    if (i < 0) continue
    const name = part.slice(0, i).trim()
    const val = part.slice(i + 1).trim()
    if (!val) continue
    if (name.includes("two_factor")) return "tf:" + val
    if (name.includes("session_token")) session = "st:" + val
  }
  return session
}
function totpKey(subject: string, code: unknown): string {
  return createHash("sha256").update(subject + "|" + String(code ?? "").trim()).digest("hex")
}
function pruneUsedTotp(now: number): void {
  for (const [k, exp] of usedTotp) if (exp <= now) usedTotp.delete(k)
}

export const auth = betterAuth({
  hooks: {
    before: createAuthMiddleware(async (ctx) => {
      if (ctx.path !== "/two-factor/verify-totp") return
      const now = Date.now()
      pruneUsedTotp(now)
      const subject = totpSubject(ctx.headers)
      if (!subject) return
      const exp = usedTotp.get(totpKey(subject, (ctx.body as { code?: unknown } | undefined)?.code))
      if (exp && exp > now) {
        throw new APIError("BAD_REQUEST", {
          message: "Bu dogrulama kodu zaten kullanildi; authenticator'daki yeni kodu bekleyin.",
        })
      }
    }),
    after: createAuthMiddleware(async (ctx) => {
      if (ctx.path !== "/two-factor/verify-totp") return
      if (ctx.context.returned instanceof APIError) return
      const subject = totpSubject(ctx.headers)
      if (!subject) return
      usedTotp.set(
        totpKey(subject, (ctx.body as { code?: unknown } | undefined)?.code),
        Date.now() + TOTP_REPLAY_TTL_MS,
      )
    }),
  },
  database: new Pool({
    connectionString,
  }),
  secret,
  baseURL: process.env.BETTER_AUTH_URL || "https://localhost:8443",
  // CSRF / Origin / callbackURL dogrulamasi icin YALNIZCA bilinen origin'ler.
  // Istegin kendi Origin'i EKLENMEZ (eklenseydi her origin guvenilir sayilir,
  // koruma fiilen kapanirdi). Kiraci tunel alt alanlari (ad--slug.<domain>)
  // panel ile ayni site oldugu icin ozellikle GUVENILMEZ.
  // Go tarafindaki esi: server/api/csrf.go TrustedOriginHosts (ayni kural).
  // Ek origin gerekiyorsa: BETTER_AUTH_TRUSTED_ORIGINS (virgulle; Better Auth okur).
  trustedOrigins: trustedOriginList(),
  emailAndPassword: {
    enabled: true,
    // Kayit olan kullanici e-postasini dogrulamadan giris YAPAMAZ.
    requireEmailVerification: true,
    minPasswordLength: 8,
    // Sifre sifirlaninca kullanicinin TUM oturumlari kapanir (calinmis oturum
    // sifre sifirlamadan sonra yasamaya devam etmesin). Go onbellegi auth
    // proxy'sinde temizlenir (server/authproxy.go).
    revokeSessionsOnPasswordReset: true,
    // "Sifremi unuttum" akisi: sifirlama linkini e-posta ile yollar.
    sendResetPassword: async ({ user, url }) => {
      const { subject, html, text } = resetPasswordEmail(user.name ?? "", url)
      await sendMail({ to: user.email, subject, html, text })
    },
  },
  emailVerification: {
    // Kayit aninda otomatik dogrulama maili gonder.
    sendOnSignUp: true,
    // Kullanici linke tiklayinca otomatik oturum acilsin (tekrar giris gerekmesin).
    autoSignInAfterVerification: true,
    sendVerificationEmail: async ({ user, url }) => {
      const { subject, html, text } = verificationEmail(user.name ?? "", url)
      await sendMail({ to: user.email, subject, html, text })
    },
  },
  user: {
    // Profil sayfasindan e-posta degistirme. Guvenlik: onay linki MEVCUT
    // adrese gonderilir; kullanici tiklayana kadar e-posta DEGISMEZ (calinmis
    // oturumla sessiz adres degisimini onler). Onaydan sonra Better Auth YENI
    // adrese dogrulama linki yollar (sendVerificationEmail); e-posta ancak o
    // link tiklaninca degisir. Better Auth 1.7.3'te secenek adi
    // sendChangeEmailConfirmation (eski ad sendChangeEmailVerification tanimsiz
    // oldugu icin onay adimi atlanip link dogrudan yeni adrese gidiyordu).
    changeEmail: {
      enabled: true,
      sendChangeEmailConfirmation: async ({ user, newEmail, url }) => {
        const { subject, html, text } = changeEmailApprovalEmail(user.name ?? "", newEmail, url)
        await sendMail({ to: user.email, subject, html, text })
      },
    },
  },
  // Kullanici olusturuldugunda (kayit): bekleyen davetleri kabul et ve/veya
  // kisisel organizasyon olustur. Hata olursa kaydi BOZMA (yalnizca logla) —
  // kullanici sonradan manuel org olusturabilir.
  databaseHooks: {
    user: {
      create: {
        after: async (user: any) => {
          try {
            await provisionNewUser(user.id, user.email, user.name ?? "")
          } catch (e) {
            console.error("[signup-hook] kullanici provizyonu basarisiz:", e)
          }
        },
      },
    },
  },
  // Hassas hesap islemlerine istek siniri (kotu niyetli tekrar denemelere karsi).
  // Varsayilan pencere/limit tum uclara uygulanir; asagidakiler daha katidir.
  // Depolama: bellek (tek dugum icin yeterli; yeniden baslatinca sifirlanir).
  // Yol adlari Better Auth 1.7.3 uclariyla BIREBIR eslesir (tam esitlik).
  rateLimit: {
    enabled: true,
    window: 60, // saniye
    max: 60, // uc basina varsayilan
    customRules: {
      "/sign-up/email": { window: 3600, max: 5 },
      "/sign-in/email": { window: 300, max: 10 },
      "/request-password-reset": { window: 3600, max: 3 },
      "/reset-password": { window: 3600, max: 5 },
      "/send-verification-email": { window: 3600, max: 5 },
      "/change-email": { window: 3600, max: 3 },
      "/change-password": { window: 3600, max: 5 },
      "/update-user": { window: 3600, max: 10 },
      "/verify-password": { window: 300, max: 10 },
      "/two-factor/enable": { window: 300, max: 10 },
      "/two-factor/disable": { window: 300, max: 10 },
      "/two-factor/generate-backup-codes": { window: 300, max: 5 },
      "/two-factor/send-otp": { window: 300, max: 5 },
      "/two-factor/verify-otp": { window: 300, max: 10 },
      "/two-factor/verify-totp": { window: 300, max: 10 },
      "/two-factor/verify-backup-code": { window: 3600, max: 10 },
    },
  },
  advanced: {
    // Istemci IP'si (hiz siniri + oturum kaydi): Go auth proxy'si gelen
    // X-Forwarded-For'u siler ve yalnizca gercek istemci IP'sini TEK deger
    // olarak yazar (server/authproxy.go). Better Auth tek degerli XFF'ye guvenir;
    // birden cok degerli (uydurulmus) baslikta IP'yi bilinmez sayar.
    // trustedProxies BILEREK verilmez: web servisi disari port acmaz, tek
    // giris noktasi Go proxy'sidir.
    ipAddress: {
      ipAddressHeaders: ["x-forwarded-for"],
    },
  },
  socialProviders,
  plugins: [
    organization({
      allowUserToCreateOrganization: true,
      // FAZ 3.5: plan bazli organizasyon siniri (bkz. organizationLimitReached).
      organizationLimit: (user) => organizationLimitReached(user.id),
      // Silme Better Auth'ta KAPALI: onun silmesi yalnizca organization ve
      // uyelik satirlarini siler; kiraci, istemciler ve tuneller kalir ve
      // tuneller yayinda kalmaya devam ederdi. Silme Go sunucusunda
      // (DELETE /api/v1/organization) tum veriyle tek islemde yapilir.
      disableOrganizationDeletion: true,
      // Slug marka/rezerve korumasi (bkz. assertSlugAllowed). Kayit akisinin
      // otomatik kisisel organizasyonu bu kancadan GECMEZ (dogrudan SQL) ve
      // slug'i uniqueOrgSlug'ta zaten donusturulmustur.
      organizationHooks: {
        beforeCreateOrganization: async ({ organization: org }) => {
          await assertSlugAllowed(org.slug)
        },
        beforeUpdateOrganization: async ({ organization: org }) => {
          await assertSlugAllowed(org.slug)
        },
      },
    }),
    // Iki adimli dogrulama (2FA): kullanici ISTEGE BAGLI olarak ayarlardan acar.
    // Iki yontem: (1) Authenticator app (TOTP), (2) E-posta kodu (OTP) — kendi
    // Postfix'imizden gonderilir. Yedek kodlar da uretilir.
    twoFactor({
      issuer: "Zorven",
      otpOptions: {
        // E-posta OTP suresi (saniye) ve gonderim: kendi mail sunucumuz.
        period: 300,
        async sendOTP({ user, otp }) {
          const { subject, html, text } = otpEmail(user.name ?? "", otp)
          await sendMail({ to: user.email, subject, html, text })
        },
      },
    }),
    bearer(),
  ],
})
