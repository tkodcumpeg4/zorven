// Better Auth e-posta geri cagrilari (dogrulama, sifre sifirlama, e-posta degisikligi
// onayi, iki adimli dogrulama kodu): dil secimi + sablon + gonderim TEK yerde.
//
// auth.ts bunlari gercek bagimliliklarla (kayitli dil icin veritabani, nodemailer) kurar;
// email-callbacks.test.ts ayni kodu gercek Better Auth 1.7.3 ile sahte bagimliliklarla
// calistirir (geri cagrilarin istek/baglam parametresini gercekten aldigini dogrular).
//
// Better Auth 1.7.3 imzalari (node_modules/better-auth/dist):
//   sendResetPassword({ user, url, token }, request?)             api/routes/password.mjs
//   sendVerificationEmail({ user, url, token }, request?)         api/routes/email-verification.mjs, sign-up.mjs, sign-in.mjs
//   sendChangeEmailConfirmation({ user, newEmail, url, token }, request?)   api/routes/update-user.mjs
//   twoFactor otpOptions.sendOTP({ user, otp }, ctx?)             plugins/two-factor/otp/index.mjs
// "request" Request (ya da onun klonu), "ctx" uc baglamidir; ikisinden de Cookie ve
// Accept-Language okunur (email-lang.ts readHeader).

import {
  verificationEmail,
  resetPasswordEmail,
  otpEmail,
  changeEmailApprovalEmail,
} from "./email-templates.ts"
import type { EmailLang } from "./email-lang.ts"

export interface MailUser {
  id: string
  name?: string | null
  email: string
}

export interface OutgoingMail {
  to: string
  subject: string
  html: string
  text: string
}

export interface EmailCallbackDeps {
  /** Dil secimi: kullanicinin kayitli tercihi + istek (email-lang.ts oncelik sirasi). */
  langFor: (userId: string | undefined, source: unknown) => Promise<EmailLang>
  /** Gonderim (mailer.ts sendMail). */
  send: (m: OutgoingMail) => Promise<void>
}

export function createEmailCallbacks(deps: EmailCallbackDeps) {
  const { langFor, send } = deps
  return {
    /** "Sifremi unuttum": sifirlama baglantisini e-posta ile yollar. */
    sendResetPassword: async ({ user, url }: { user: MailUser; url: string }, request?: unknown): Promise<void> => {
      const lang = await langFor(user.id, request)
      const { subject, html, text } = resetPasswordEmail(user.name ?? "", url, lang)
      await send({ to: user.email, subject, html, text })
    },

    /** Kayit, girisle tetiklenen yeniden dogrulama ve yeni adres dogrulamasi: ayni sablon ve dil kurali. */
    sendVerificationEmail: async ({ user, url }: { user: MailUser; url: string }, request?: unknown): Promise<void> => {
      const lang = await langFor(user.id, request)
      const { subject, html, text } = verificationEmail(user.name ?? "", url, lang)
      await send({ to: user.email, subject, html, text })
    },

    /** E-posta degisikligi onayi MEVCUT adrese gider (yeni adres newEmail). */
    sendChangeEmailConfirmation: async (
      { user, newEmail, url }: { user: MailUser; newEmail: string; url: string },
      request?: unknown,
    ): Promise<void> => {
      const lang = await langFor(user.id, request)
      const { subject, html, text } = changeEmailApprovalEmail(user.name ?? "", newEmail, url, lang)
      await send({ to: user.email, subject, html, text })
    },

    /** Iki adimli dogrulama e-posta kodu (ikinci parametre uc baglami). */
    sendOTP: async ({ user, otp }: { user: MailUser; otp: string }, ctx?: unknown): Promise<void> => {
      const lang = await langFor(user.id, ctx)
      const { subject, html, text } = otpEmail(user.name ?? "", otp, lang)
      await send({ to: user.email, subject, html, text })
    },
  }
}
