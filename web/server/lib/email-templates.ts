// Zorven temali (koyu + yesil marka) islem e-postasi sablonlari: tr / en / ja / zh.
//
// E-posta istemcileri harici CSS ve <style> bloklarini cogu zaman yok sayar; bu
// yuzden tum stiller INLINE yazilir ve duzen tablo tabanlidir (Outlook uyumu).
//
// Dil: her sablon son parametre olarak dil alir (varsayilan tr). Dili secmek icin
// email-lang.ts pickEmailLang / pickEmailLangFrom kullanilir. Konu satiri, govde ve
// duz metin surumu ayni dilde uretilir; gorsel tasarim dilden bagimsizdir.
//
// Kural: sozluklerdeki metinler DUZ METINDIR (HTML ya da kacis yok); HTML'e her zaman
// esc() ile girer. Duz metin surumunde baglanti kendi satirindadir (e2e kosumlari,
// SMTP kapaliyken gunlukten baglantiyi bu satirdan alir).

import { type EmailLang, htmlLangOf } from "./email-lang.ts"

export type Mail = { subject: string; html: string; text: string }

const BRAND = "#3ddc84"
const BG = "#0b0e12"
const CARD = "#12161c"
const FG = "#e6edf3"
const MUTED = "#8b98a5"
const LINE = "#232a33"

type L<T> = Record<EmailLang, T>

// ---- ortak parcalar ---------------------------------------------------------

// Basit HTML kacisi (ad, e-posta, baglanti ve sozluk metinleri HTML'e bununla girer).
export function esc(s: string): string {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c] as string))
}

const FOOTER: L<string> = {
  tr: "Zorven · Kendi sunucunda barındırılan tünel platformu",
  en: "Zorven · Self-hosted tunnel platform",
}

const FALLBACK: L<string> = {
  tr: "Buton çalışmazsa bu bağlantıyı tarayıcına yapıştır:",
  en: "If the button doesn't work, paste this link into your browser:",
}

const HELLO: L<(name: string) => string> = {
  tr: (n) => (n ? `Merhaba ${n},` : "Merhaba,"),
  en: (n) => (n ? `Hi ${n},` : "Hi,"),
}

/** Selamlama satiri (duz metin). Ad satir sonu/sekme icermez; bossa addan yoksun selam. */
export function helloLine(name: string | null | undefined, lang: EmailLang = "tr"): string {
  const n = String(name ?? "").replace(/[\r\n\t]+/g, " ").trim()
  return HELLO[lang](n)
}

/** Dis cerceve (logo + kart + alt bilgi). footerExtra: kartin altina ek (HTML) blok. */
export function shell(inner: string, lang: EmailLang = "tr", footerExtra = ""): string {
  return `<!doctype html>
<html lang="${htmlLangOf(lang)}">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:${BG};">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:${BG};padding:32px 0;">
    <tr><td align="center">
      <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="width:480px;max-width:92%;background:${CARD};border:1px solid ${LINE};border-radius:16px;overflow:hidden;">
        <tr><td style="padding:28px 32px 8px 32px;">
          <table role="presentation" cellpadding="0" cellspacing="0"><tr>
            <td style="vertical-align:middle;">
              <img src="https://zorven.app/logo-email.png" width="34" height="34" alt="Zorven" style="display:block;width:34px;height:34px;border-radius:9px;border:0;outline:none;text-decoration:none;">

            </td>
            <td style="vertical-align:middle;padding-left:12px;font-family:Arial,Helvetica,sans-serif;font-size:19px;font-weight:700;color:${FG};letter-spacing:-0.3px;">Zorven</td>
          </tr></table>
        </td></tr>
        <tr><td style="padding:12px 32px 32px 32px;font-family:Arial,Helvetica,sans-serif;color:${FG};">
          ${inner}
        </td></tr>
      </table>${footerExtra}
      <div style="font-family:Arial,Helvetica,sans-serif;font-size:11px;color:${MUTED};padding:18px 0;">
        ${esc(FOOTER[lang])}
      </div>
    </td></tr>
  </table>
</body></html>`
}

export function button(url: string, label: string): string {
  return `<table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0;"><tr>
    <td style="border-radius:10px;background:${BRAND};">
      <a href="${esc(url)}" style="display:inline-block;padding:12px 26px;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:700;color:${BG};text-decoration:none;border-radius:10px;">${esc(label)}</a>
    </td></tr></table>`
}

export function fallbackLink(url: string, lang: EmailLang = "tr"): string {
  return `<p style="font-size:12px;color:${MUTED};line-height:1.5;margin:6px 0 0 0;">
    ${esc(FALLBACK[lang])}<br>
    <a href="${esc(url)}" style="color:${BRAND};word-break:break-all;">${esc(url)}</a>
  </p>`
}

const h1 = (s: string) =>
  `<h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">${esc(s)}</h1>`
const hello = (s: string) => `<p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${esc(s)}</p>`
const para = (htmlInner: string) =>
  `<p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">${htmlInner}</p>`
const note = (s: string) => `<p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">${esc(s)}</p>`
const bold = (s: string) => `<b style="color:${FG};">${esc(s)}</b>`

// ---- baglantili sablonlar (dogrulama, e-posta degisikligi, sifre sifirlama) ----

interface LinkTexts {
  subject: string
  title: string
  /** {email} yer tutucusu e-posta adresiyle (HTML'de kalin) degisir. */
  intro: string
  button: string
  note: string
  /** Duz metin: baglantidan onceki satir ({email} desteklenir) ve baglantidan sonraki not. */
  textLead: string
  textNote: string
}

function linkMail(lang: EmailLang, name: string, url: string, t: LinkTexts, email = ""): Mail {
  const hi = helloLine(name, lang)
  // Fonksiyonlu degistirme: e-posta adresindeki '$' ozel desen sayilmasin.
  const introHtml = esc(t.intro).replace("{email}", () => bold(email))
  const html = shell(
    `
    ${h1(t.title)}
    ${hello(hi)}
    ${para(introHtml)}
    ${button(url, t.button)}
    ${note(t.note)}
    ${fallbackLink(url, lang)}
  `,
    lang,
  )
  return {
    subject: t.subject,
    html,
    text: `${hi}\n\n${t.textLead.replace("{email}", () => email)}\n${url}\n\n${t.textNote}`,
  }
}

const VERIFY: L<LinkTexts> = {
  tr: {
    subject: "Zorven — E-posta adresini doğrula",
    title: "E-posta adresini doğrula",
    intro: "Zorven hesabını etkinleştirmek için e-posta adresini doğrulaman gerekiyor. Aşağıdaki butona tıklaman yeterli.",
    button: "E-postamı Doğrula",
    note: "Bu isteği sen yapmadıysan bu e-postayı yok sayabilirsin. Bağlantı kısa süre sonra geçersiz olur.",
    textLead: "Zorven hesabını etkinleştirmek için e-postanı doğrula:",
    textNote: "Bu isteği sen yapmadıysan yok say.",
  },
  en: {
    subject: "Zorven — Verify your email address",
    title: "Verify your email address",
    intro: "To activate your Zorven account, you need to verify your email address. Just click the button below.",
    button: "Verify my email",
    note: "If you didn't make this request, you can ignore this email. The link expires shortly.",
    textLead: "Verify your email to activate your Zorven account:",
    textNote: "If you didn't make this request, ignore this email.",
  },
}

const CHANGE: L<LinkTexts> = {
  tr: {
    subject: "Zorven — E-posta değişikliği onayı",
    title: "E-posta değişikliğini onayla",
    intro:
      "Hesabının e-posta adresini {email} olarak değiştirmek için bir istek aldık. Onaylamak için aşağıdaki butona tıkla — onayladıktan sonra giriş için yeni adresini kullanırsın.",
    button: "Değişikliği Onayla",
    note: "Bu isteği sen yapmadıysan bu e-postayı yok say — adresin değişmeden kalır ve hesabın güvende olur.",
    textLead: "Hesabının e-posta adresini {email} olarak değiştirmek için:",
    textNote: "Bu isteği sen yapmadıysan yok say.",
  },
  en: {
    subject: "Zorven — Confirm email change",
    title: "Confirm your email change",
    intro:
      "We received a request to change your account's email address to {email}. Click the button below to confirm — after you confirm, you will use your new address to sign in.",
    button: "Confirm change",
    note: "If you didn't make this request, ignore this email — your address stays the same and your account remains safe.",
    textLead: "To change your account's email address to {email}, confirm here:",
    textNote: "If you didn't make this request, ignore this email.",
  },
}

const RESET: L<LinkTexts> = {
  tr: {
    subject: "Zorven — Şifre sıfırlama",
    title: "Şifreni sıfırla",
    intro: "Zorven hesabının şifresini sıfırlamak için bir istek aldık. Yeni şifre belirlemek için aşağıdaki butona tıkla.",
    button: "Şifremi Sıfırla",
    note: "Bu isteği sen yapmadıysan bu e-postayı yok say — şifren değişmeden kalır.",
    textLead: "Şifreni sıfırlamak için:",
    textNote: "Bu isteği sen yapmadıysan yok say.",
  },
  en: {
    subject: "Zorven — Password reset",
    title: "Reset your password",
    intro: "We received a request to reset your Zorven account password. Click the button below to set a new password.",
    button: "Reset my password",
    note: "If you didn't make this request, ignore this email — your password stays the same.",
    textLead: "To reset your password:",
    textNote: "If you didn't make this request, ignore this email.",
  },
}

export function verificationEmail(name: string, url: string, lang: EmailLang = "tr"): Mail {
  return linkMail(lang, name, url, VERIFY[lang])
}

export function changeEmailApprovalEmail(name: string, newEmail: string, url: string, lang: EmailLang = "tr"): Mail {
  return linkMail(lang, name, url, CHANGE[lang], newEmail)
}

export function resetPasswordEmail(name: string, url: string, lang: EmailLang = "tr"): Mail {
  return linkMail(lang, name, url, RESET[lang])
}

// ---- iki adimli dogrulama kodu ---------------------------------------------

interface OtpTexts {
  /** {code} yer tutucusu kodla degisir. */
  subject: string
  title: string
  intro: string
  note: string
  /** Duz metin: {code} desteklenir. */
  textLead: string
  textNote: string
}

const OTP: L<OtpTexts> = {
  tr: {
    subject: "Zorven giriş kodu: {code}",
    title: "Giriş doğrulama kodun",
    intro: "Zorven hesabına giriş yapmak için iki adımlı doğrulama kodun aşağıda. Kod kısa süre içinde geçersiz olur.",
    note: "Bu girişi sen yapmadıysan hemen şifreni değiştir — birisi şifreni biliyor olabilir.",
    textLead: "Zorven giriş doğrulama kodun: {code}",
    textNote: "Bu girişi sen yapmadıysan şifreni değiştir.",
  },
  en: {
    subject: "Zorven sign-in code: {code}",
    title: "Your sign-in verification code",
    intro: "Here is your two-step verification code to sign in to your Zorven account. The code expires shortly.",
    note: "If you didn't try to sign in, change your password right away — someone may know it.",
    textLead: "Your Zorven sign-in verification code: {code}",
    textNote: "If this wasn't you, change your password.",
  },
}

export function otpEmail(name: string, code: string, lang: EmailLang = "tr"): Mail {
  const t = OTP[lang]
  const hi = helloLine(name, lang)
  const codeBox = `<div style="margin:22px 0;padding:16px 0;text-align:center;background:#0b0e12;border:1px solid ${LINE};border-radius:12px;">
    <span style="font-family:ui-monospace,'JetBrains Mono',monospace;font-size:30px;font-weight:800;letter-spacing:10px;color:${BRAND};">${esc(code)}</span>
  </div>`
  const html = shell(
    `
    ${h1(t.title)}
    ${hello(hi)}
    ${para(esc(t.intro))}
    ${codeBox}
    ${note(t.note)}
  `,
    lang,
  )
  return {
    subject: t.subject.replace("{code}", () => code),
    html,
    text: `${hi}\n\n${t.textLead.replace("{code}", () => code)}\n\n${t.textNote}`,
  }
}
