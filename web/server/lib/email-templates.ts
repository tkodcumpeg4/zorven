// Zorven temali (koyu + yesil marka) Turkce e-posta sablonlari.
//
// E-posta istemcileri harici CSS ve <style> bloklarini cogu zaman yok sayar; bu
// yuzden tum stiller INLINE yazilir ve duzen tablo tabanlidir (Outlook uyumu).

const BRAND = "#3ddc84"
const BG = "#0b0e12"
const CARD = "#12161c"
const FG = "#e6edf3"
const MUTED = "#8b98a5"
const LINE = "#232a33"

function shell(inner: string): string {
  return `<!doctype html>
<html lang="tr">
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
      </table>
      <div style="font-family:Arial,Helvetica,sans-serif;font-size:11px;color:${MUTED};padding:18px 0;">
        Zorven &middot; Kendi sunucunuzda barindirilan tunel platformu
      </div>
    </td></tr>
  </table>
</body></html>`
}

function button(url: string, label: string): string {
  return `<table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0;"><tr>
    <td style="border-radius:10px;background:${BRAND};">
      <a href="${url}" style="display:inline-block;padding:12px 26px;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:700;color:${BG};text-decoration:none;border-radius:10px;">${label}</a>
    </td></tr></table>`
}

function fallbackLink(url: string): string {
  return `<p style="font-size:12px;color:${MUTED};line-height:1.5;margin:6px 0 0 0;">
    Buton calismazsa bu baglantiyi tarayiciniza yapistirin:<br>
    <a href="${url}" style="color:${BRAND};word-break:break-all;">${url}</a>
  </p>`
}

export function verificationEmail(name: string, url: string): { subject: string; html: string; text: string } {
  const hi = name ? `Merhaba ${name},` : "Merhaba,"
  const html = shell(`
    <h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">E-posta adresini doğrula</h1>
    <p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${hi}</p>
    <p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">
      Zorven hesabını etkinleştirmek için e-posta adresini doğrulaman gerekiyor. Aşağıdaki butona tıklaman yeterli.
    </p>
    ${button(url, "E-postamı Doğrula")}
    <p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">
      Bu isteği sen yapmadıysan bu e-postayı yok sayabilirsin. Bağlantı kısa süre sonra geçersiz olur.
    </p>
    ${fallbackLink(url)}
  `)
  return {
    subject: "Zorven — E-posta adresini doğrula",
    html,
    text: `${hi}\n\nZorven hesabını etkinleştirmek için e-postanı doğrula:\n${url}\n\nBu isteği sen yapmadıysan yok say.`,
  }
}

export function changeEmailApprovalEmail(
  name: string,
  newEmail: string,
  url: string,
): { subject: string; html: string; text: string } {
  const hi = name ? `Merhaba ${name},` : "Merhaba,"
  const html = shell(`
    <h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">E-posta değişikliğini onayla</h1>
    <p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${hi}</p>
    <p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">
      Hesabının e-posta adresini <b style="color:${FG};">${newEmail}</b> olarak değiştirmek için bir istek aldık.
      Onaylamak için aşağıdaki butona tıkla — onayladıktan sonra giriş için yeni adresini kullanırsın.
    </p>
    ${button(url, "Değişikliği Onayla")}
    <p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">
      Bu isteği sen yapmadıysan bu e-postayı yok say — adresin değişmeden kalır ve hesabın güvende olur.
    </p>
    ${fallbackLink(url)}
  `)
  return {
    subject: "Zorven — E-posta değişikliği onayı",
    html,
    text: `${hi}\n\nHesabının e-posta adresini ${newEmail} olarak değiştirmek için:\n${url}\n\nBu isteği sen yapmadıysan yok say.`,
  }
}

export function otpEmail(name: string, code: string): { subject: string; html: string; text: string } {
  const hi = name ? `Merhaba ${name},` : "Merhaba,"
  const codeBox = `<div style="margin:22px 0;padding:16px 0;text-align:center;background:#0b0e12;border:1px solid ${LINE};border-radius:12px;">
    <span style="font-family:ui-monospace,'JetBrains Mono',monospace;font-size:30px;font-weight:800;letter-spacing:10px;color:${BRAND};">${esc(code)}</span>
  </div>`
  const html = shell(`
    <h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">Giriş doğrulama kodun</h1>
    <p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${hi}</p>
    <p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">
      Zorven hesabına giriş yapmak için iki adımlı doğrulama kodun aşağıda. Kod kısa süre içinde geçersiz olur.
    </p>
    ${codeBox}
    <p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">
      Bu girişi sen yapmadıysan hemen şifreni değiştir — birisi şifreni biliyor olabilir.
    </p>
  `)
  return {
    subject: `Zorven giriş kodu: ${code}`,
    html,
    text: `${hi}\n\nZorven giriş doğrulama kodun: ${code}\n\nBu girişi sen yapmadıysan şifreni değiştir.`,
  }
}

// Basit HTML kacisi (kod her zaman rakam ama yine de guvenli tarafta kalalim).
function esc(s: string): string {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c] as string))
}

export function resetPasswordEmail(name: string, url: string): { subject: string; html: string; text: string } {
  const hi = name ? `Merhaba ${name},` : "Merhaba,"
  const html = shell(`
    <h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:${FG};">Şifreni sıfırla</h1>
    <p style="margin:0;font-size:14px;line-height:1.6;color:${MUTED};">${hi}</p>
    <p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:${MUTED};">
      Zorven hesabının şifresini sıfırlamak için bir istek aldık. Yeni şifre belirlemek için aşağıdaki butona tıkla.
    </p>
    ${button(url, "Şifremi Sıfırla")}
    <p style="margin:0;font-size:12px;line-height:1.6;color:${MUTED};">
      Bu isteği sen yapmadıysan bu e-postayı yok say — şifren değişmeden kalır.
    </p>
    ${fallbackLink(url)}
  `)
  return {
    subject: "Zorven — Şifre sıfırlama",
    html,
    text: `${hi}\n\nŞifreni sıfırlamak için:\n${url}\n\nBu isteği sen yapmadıysan yok say.`,
  }
}
