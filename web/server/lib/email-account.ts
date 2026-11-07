// Hesap silme onay e-postasi (tr / en / ja / zh).
//
// Dis cerceve, dugme, yedek baglanti ve kacis yardimcilari email-templates.ts'ten gelir
// (tek tasarim, tek kaynak); bu dosya yalnizca hesap silmeye ozgu metni tasir.
// Dil secimi email-lang.ts (kayitli tercih > zorven_lang cerezi > Accept-Language > tr).
//
// Kural (email-templates.ts ile ayni): sozlukteki metinler DUZ METINDIR; HTML'e her zaman
// esc() ile girer. Duz metin surumunde baglanti kendi satirindadir (SMTP kapaliyken
// gelistirmede / e2e'de baglanti gunlukten bu satirdan alinir).

import { type EmailLang } from "./email-lang.ts"
import { type Mail, button, esc, fallbackLink, helloLine, shell } from "./email-templates.ts"

const FG = "#e6edf3"
const MUTED = "#8b98a5"

interface DeleteTexts {
  subject: string
  title: string
  /** {email} yer tutucusu hesap e-postasiyla (HTML'de kalin) degisir. */
  intro: string
  /** Silinecekler listesi. */
  items: string[]
  irreversible: string
  /** {minutes} yer tutucusu baglanti omruyle degisir. */
  expiry: string
  button: string
  note: string
  /** Duz metin: baglantidan onceki satir ({email} desteklenir). */
  textLead: string
}

const TEXTS: Record<EmailLang, DeleteTexts> = {
  tr: {
    subject: "Zorven — Hesap silme onayı",
    title: "Hesabını silmeyi onayla",
    intro: "{email} adresli Zorven hesabını kalıcı olarak silmek için bir istek aldık. Onaylarsan şunlar silinir:",
    items: [
      "Hesabın ve giriş bilgilerin",
      "Tek üyesi olduğun organizasyonlar ve içindeki her şey (tüneller, cihazlar, API anahtarları, alan adları, posta kutuları, uygulamalar)",
      "Diğer organizasyonlardaki üyeliklerin, cihazların ve kişisel API anahtarların",
    ],
    irreversible: "Bu işlem geri alınamaz.",
    expiry: "Bağlantı {minutes} dakika geçerlidir; Zorven'e giriş yaptığın tarayıcıda açmalısın.",
    button: "Hesabımı kalıcı olarak sil",
    note: "Bu isteği sen yapmadıysan bu e-postayı yok say — hesabın silinmez. Güvenlik için şifreni değiştirmeni öneririz.",
    textLead: "{email} adresli Zorven hesabını kalıcı olarak silmek için bağlantıyı aç:",
  },
  en: {
    subject: "Zorven — Confirm account deletion",
    title: "Confirm account deletion",
    intro:
      "We received a request to permanently delete the Zorven account for {email}. If you confirm, the following will be deleted:",
    items: [
      "Your account and sign-in details",
      "Organizations where you are the only member, and everything in them (tunnels, devices, API keys, domains, mailboxes, apps)",
      "Your memberships, devices and personal API keys in other organizations",
    ],
    irreversible: "This cannot be undone.",
    expiry: "The link is valid for {minutes} minutes and must be opened in the browser where you are signed in to Zorven.",
    button: "Permanently delete my account",
    note: "If you didn't make this request, ignore this email — your account will not be deleted. We recommend changing your password for safety.",
    textLead: "To permanently delete the Zorven account for {email}, open this link:",
  },
}

/**
 * Hesap silme onay e-postasi. minutes: onay baglantisinin omru (dakika); url: Better Auth'un
 * urettigi /delete-user/callback baglantisi.
 */
export function deleteAccountEmail(
  name: string,
  email: string,
  url: string,
  minutes: number,
  lang: EmailLang = "tr",
): Mail {
  const t = TEXTS[lang]
  const hi = helloLine(name, lang)
  const expiry = t.expiry.replace("{minutes}", () => String(minutes))
  // Fonksiyonlu degistirme: e-posta adresindeki '$' ozel desen sayilmasin.
  const introHtml = esc(t.intro).replace("{email}", () => `<b style="color:${FG};">${esc(email)}</b>`)
  const items = t.items
    .map((s) => `<li>${esc(s)}</li>`)
    .join("")
  const html = shell(
    `
    <h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">${esc(t.title)}</h1>
    <p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${esc(hi)}</p>
    <p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">${introHtml}</p>
    <ul style="margin:12px 0 0 0;padding-left:18px;font-size:14px;line-height:1.7;color:${MUTED};">${items}</ul>
    <p style="margin:12px 0 0 0;font-size:14px;line-height:1.6;font-weight:700;color:${FG};">${esc(t.irreversible)}</p>
    ${button(url, t.button)}
    <p style="margin:0 0 8px 0;font-size:12px;line-height:1.6;color:${MUTED};">${esc(expiry)}</p>
    <p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">${esc(t.note)}</p>
    ${fallbackLink(url, lang)}
  `,
    lang,
  )
  const lead = t.textLead.replace("{email}", () => email)
  return {
    subject: t.subject,
    html,
    text: `${hi}\n\n${lead}\n${url}\n\n${t.items.map((s) => `- ${s}`).join("\n")}\n${t.irreversible}\n\n${expiry}\n${t.note}`,
  }
}
