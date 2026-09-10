// Kendi sunucumuzda (VDS) calisan Postfix uzerinden e-posta gonderimi.
//
// Harici saglayici (Resend/SendGrid) YOK: nodemailer dogrudan VDS'teki Postfix'e
// (varsayilan localhost:587) baglanir, Postfix de alicinin sunucusuna teslim eder.
// Teslim edilebilirlik icin SPF/DKIM/DMARC/PTR DNS kayitlari sarttir (bkz.
// docs/auth-plan.md Karar 2).
//
// SMTP yapilandirilmamissa (dev ortami): mail GONDERILMEZ, baglanti/onay linki
// konsola yazilir. Boylece Postfix kurmadan da kayit akisini test edebiliriz.

import nodemailer from "nodemailer"
import type { Transporter } from "nodemailer"

const FROM = process.env.SMTP_FROM || "Zorven <no-reply@zorven.app>"

// SMTP_HOST verilmediyse dev modundayiz kabul edilir: transport kurulmaz.
const SMTP_HOST = process.env.SMTP_HOST || ""
const SMTP_PORT = Number(process.env.SMTP_PORT || 587)
const SMTP_SECURE = process.env.SMTP_SECURE === "true" // 465 icin true; 587 STARTTLS icin false
const SMTP_USER = process.env.SMTP_USER || ""
const SMTP_PASS = process.env.SMTP_PASS || ""
// Lokal Postfix self-signed sertifika kullanabilir; dogrulamayi gevsetmek icin.
const SMTP_ALLOW_SELF_SIGNED = process.env.SMTP_ALLOW_SELF_SIGNED === "true"

let transporter: Transporter | null = null

function getTransporter(): Transporter | null {
  if (!SMTP_HOST) return null // dev fallback (konsola yaz)
  if (transporter) return transporter

  transporter = nodemailer.createTransport({
    host: SMTP_HOST,
    port: SMTP_PORT,
    secure: SMTP_SECURE,
    // Lokal Postfix genelde auth istemez; USER/PASS bos ise auth gonderme.
    auth: SMTP_USER ? { user: SMTP_USER, pass: SMTP_PASS } : undefined,
    tls: SMTP_ALLOW_SELF_SIGNED ? { rejectUnauthorized: false } : undefined,
  })
  return transporter
}

export interface MailInput {
  to: string
  subject: string
  html: string
  text?: string
}

// sendMail, bir e-postayi gonderir. SMTP yoksa (dev) konsola dusurur ve sessizce
// basarili sayar ki kayit akisi kesilmeSIN.
export async function sendMail({ to, subject, html, text }: MailInput): Promise<void> {
  const tx = getTransporter()
  if (!tx) {
    // Dev fallback: gercek gonderim yok, linki gorebilmek icin logla.
    console.warn(
      `[mailer] SMTP yapilandirilmadi (SMTP_HOST bos). Mail GONDERILMEDI.\n` +
        `  -> Alici: ${to}\n  -> Konu: ${subject}\n` +
        `  -> Icerik (metin): ${text || "(yalniz html)"}`,
    )
    return
  }
  await tx.sendMail({
    from: FROM,
    to,
    subject,
    html,
    text: text || html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim(),
  })
}
