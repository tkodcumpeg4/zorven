import { betterAuth } from "better-auth"
import { organization, bearer, twoFactor } from "better-auth/plugins"
import pg from "pg"
import { randomBytes } from "crypto"
import { sendMail } from "./mailer"
import {
  verificationEmail,
  resetPasswordEmail,
  otpEmail,
  changeEmailApprovalEmail,
} from "./email-templates"

const { Pool } = pg

const connectionString =
  process.env.DATABASE_URL ||
  process.env.ZORVEN_DB_DSN ||
  "postgres://rpshell:rpshell@localhost:5432/rpshell"

const secret =
  process.env.BETTER_AUTH_SECRET ||
  process.env.ZORVEN_SESSION_SECRET ||
  "rpshell-dev-session-secret-key-change-in-production-min-32-chars"

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

function genId(prefix: string): string {
  return `${prefix}_${randomBytes(8).toString("hex")}`
}

// Kullaniciya benzersiz, okunabilir bir organizasyon slug'i uret.
async function uniqueOrgSlug(email: string): Promise<string> {
  const base =
    (email.split("@")[0] || "team")
      .toLowerCase()
      .replace(/[^a-z0-9-]+/g, "-")
      .replace(/^-+|-+$/g, "") || "team"
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

export const auth = betterAuth({
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
