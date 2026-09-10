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
//   1) E-postasina gonderilmis bekleyen davetleri (invitation) kabul et → uyelik.
//   2) Hicbir uyeligi yoksa KISISEL bir organizasyon olustur (owner). Boylece
//      kullanici asla "ten_default" kiracisina dusmez (bu, onu yanlislikla
//      Platform Admin yapiyordu — bkz. admin.go isPlat).
async function provisionNewUser(userId: string, email: string, name: string): Promise<void> {
  const client = await hookPool.connect()
  try {
    // 1) Bekleyen davetleri kabul et.
    const invites = await client.query(
      `SELECT "id", "organizationId", "role" FROM "invitation"
       WHERE lower("email") = lower($1) AND "status" = 'pending'`,
      [email],
    )
    for (const inv of invites.rows) {
      await client.query(
        `INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
         SELECT $1, $2, $3, $4, now()
         WHERE NOT EXISTS (
           SELECT 1 FROM "member" WHERE "organizationId" = $2 AND "userId" = $3
         )`,
        [genId("mem"), inv.organizationId, userId, inv.role || "member"],
      )
      await client.query(`DELETE FROM "invitation" WHERE "id" = $1`, [inv.id])
    }

    // 2) Hala uyeligi yoksa kisisel organizasyon olustur.
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

const socialProviders: Record<string, any> = {}

if (process.env.GITHUB_CLIENT_ID && process.env.GITHUB_CLIENT_SECRET) {
  socialProviders.github = {
    clientId: process.env.GITHUB_CLIENT_ID,
    clientSecret: process.env.GITHUB_CLIENT_SECRET,
  }
}

export const auth = betterAuth({
  database: new Pool({
    connectionString,
  }),
  secret,
  baseURL: process.env.BETTER_AUTH_URL || "https://localhost:8443",
  trustedOrigins: async (request) => {
    const origin = request?.headers?.get("origin")
    const list = [
      "https://localhost:8443",
      "https://127.0.0.1:8443",
      "http://localhost:3000",
      "http://localhost:3002",
      "http://127.0.0.1:3000",
      "http://127.0.0.1:3002",
    ]
    // Uretim origin'i: BETTER_AUTH_URL (or. https://zorven.app) ve platform
    // domaini. Tunel sunucusu /api/auth'u buraya proxy'lerken tarayicinin
    // gonderdigi Origin bu domaindir; CSRF kontrolu icin guvenilir sayilmali.
    if (process.env.BETTER_AUTH_URL) list.push(process.env.BETTER_AUTH_URL)
    if (process.env.PLATFORM_DOMAIN) {
      list.push(`https://${process.env.PLATFORM_DOMAIN}`)
      list.push(`https://www.${process.env.PLATFORM_DOMAIN}`)
      list.push(`https://panel.${process.env.PLATFORM_DOMAIN}`)
      list.push(`https://app.${process.env.PLATFORM_DOMAIN}`)
    }
    if (origin && !list.includes(origin)) {
      list.push(origin)
    }
    return list
  },
  emailAndPassword: {
    enabled: true,
    // Kayit olan kullanici e-postasini dogrulamadan giris YAPAMAZ.
    requireEmailVerification: true,
    minPasswordLength: 8,
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
    // oturumla sessiz adres degisimini onler).
    changeEmail: {
      enabled: true,
      sendChangeEmailVerification: async ({ user, newEmail, url }) => {
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
  rateLimit: {
    enabled: true,
    window: 60, // saniye
    max: 60, // uc basina varsayilan
    customRules: {
      "/sign-up/email": { window: 3600, max: 5 },
      "/sign-in/email": { window: 300, max: 10 },
      "/forget-password": { window: 3600, max: 3 },
      "/reset-password": { window: 3600, max: 5 },
      "/change-email": { window: 3600, max: 3 },
      "/change-password": { window: 3600, max: 5 },
      "/update-user": { window: 3600, max: 10 },
      "/two-factor/send-otp": { window: 300, max: 5 },
      "/two-factor/verify-otp": { window: 300, max: 10 },
      "/two-factor/verify-totp": { window: 300, max: 10 },
      "/two-factor/verify-backup-code": { window: 3600, max: 10 },
    },
  },
  socialProviders,
  plugins: [
    organization({
      allowUserToCreateOrganization: true,
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
