// Islem e-postalarinin dili (tr/en/ja/zh): TEK yardimci, tum sablonlar bunu kullanir.
//
// Oncelik (ilk bulunan kazanir):
//   1. kullanicinin kayitli dil tercihi (announcement_prefs.lang, panelde sectigi dil)
//   2. panel dili cerezi (zorven_lang; panel her dil degisiminde ve ilk acilista yazar)
//   3. istegin Accept-Language basligi
//   4. tr
//
// Accept-Language'da tanimadigimiz bir dil (de, fr...) acikca isteniyorsa "en"
// donulur (panel arayuzu de bilinmeyen tarayici dillerinde Ingilizceye duser,
// bkz. app/plugins/i18n.ts browserLang). Baslik hic yoksa ya da yalniz "*" ise tr.
//
// Yan etkisiz saf islevler (veritabani sorgusu savedEmailLang icinde, enjekte edilir)
// -> birim testi email-lang.test.ts.

export type EmailLang = "tr" | "en"

export const EMAIL_LANGS: readonly EmailLang[] = ["tr", "en"]
export const DEFAULT_EMAIL_LANG: EmailLang = "tr"

/** Panelin dil tercihini sunucuya tasiyan cerez (app/plugins/i18n.ts yazar). */
export const PANEL_LANG_COOKIE = "zorven_lang"

/** "tr", "TR", "en-US", "ja_JP", "zh-Hant-TW" -> tr|en|ja|zh; tanimsiz/bos -> null. */
export function normalizeEmailLang(v: unknown): EmailLang | null {
  if (typeof v !== "string") return null
  const primary = v.trim().toLowerCase().split(/[-_]/)[0] ?? ""
  return (EMAIL_LANGS as readonly string[]).includes(primary) ? (primary as EmailLang) : null
}

/** <html lang> degeri (Cince icin zh-CN; duyuru e-postalariyla ayni). */
export function htmlLangOf(lang: EmailLang): string {
  return lang
}

/**
 * Accept-Language'dan dil: q degerine gore (esitte yazim sirasi) ilk desteklenen dil;
 * hicbiri desteklenmiyorsa ama bir dil acikca isteniyorsa "en"; baslik yok/bos/"*" ise null.
 */
export function langFromAcceptLanguage(header: string | null | undefined): EmailLang | null {
  if (!header) return null
  const cands: { tag: string; q: number; i: number }[] = []
  header.split(",").forEach((part, i) => {
    const fields = part.split(";")
    const tag = (fields[0] ?? "").trim()
    if (!tag || tag === "*") return
    let q = 1
    for (const p of fields.slice(1)) {
      const m = /^\s*q\s*=\s*([0-9.]+)\s*$/i.exec(p)
      if (m) {
        const f = Number(m[1])
        if (Number.isFinite(f)) q = f
      }
    }
    if (q > 0) cands.push({ tag, q, i })
  })
  if (cands.length === 0) return null
  cands.sort((a, b) => b.q - a.q || a.i - b.i)
  for (const c of cands) {
    const l = normalizeEmailLang(c.tag)
    if (l) return l
  }
  return "en"
}

/** Ham Cookie basligindan panel dili cerezi (zorven_lang); yok/gecersiz -> null. */
export function langFromCookieHeader(cookie: string | null | undefined): EmailLang | null {
  if (!cookie) return null
  for (const part of cookie.split(";")) {
    const eq = part.indexOf("=")
    if (eq < 0) continue
    if (part.slice(0, eq).trim() !== PANEL_LANG_COOKIE) continue
    const l = normalizeEmailLang(part.slice(eq + 1).trim())
    if (l) return l
  }
  return null
}

export interface EmailLangSources {
  /** Kullanicinin kayitli dil tercihi (announcement_prefs.lang); bos/gecersiz yok sayilir. */
  saved?: string | null
  /** Ham Cookie basligi. */
  cookie?: string | null
  /** Ham Accept-Language basligi. */
  acceptLanguage?: string | null
}

/** Oncelik sirasina gore dil secer (bkz. dosya basi). Her zaman gecerli bir dil doner. */
export function pickEmailLang(src: EmailLangSources): EmailLang {
  return (
    normalizeEmailLang(src.saved) ??
    langFromCookieHeader(src.cookie) ??
    langFromAcceptLanguage(src.acceptLanguage) ??
    DEFAULT_EMAIL_LANG
  )
}

// Better Auth geri cagrilari istegi uc bicimde verir: Request, uc baglami
// ({ headers, request }) ya da hic (null/undefined). Hepsinden baslik okunur.
function readHeader(source: unknown, name: string): string | null {
  if (!source) return null
  if (typeof Headers !== "undefined" && source instanceof Headers) return source.get(name)
  const s = source as {
    headers?: { get?: (n: string) => string | null }
    request?: { headers?: { get?: (n: string) => string | null } }
  }
  return s.headers?.get?.(name) ?? s.request?.headers?.get?.(name) ?? null
}

/**
 * Istek/baglamdan (Request, Better Auth uc baglami ya da Headers) dil secer;
 * saved = kullanicinin kayitli tercihi (varsa).
 */
export function pickEmailLangFrom(source: unknown, saved?: string | null): EmailLang {
  return pickEmailLang({
    saved,
    cookie: readHeader(source, "cookie"),
    acceptLanguage: readHeader(source, "accept-language"),
  })
}

/** SQL calistirabilen en kucuk arayuz (pg.Pool bunu saglar; testte sahte). */
export interface Queryable {
  query: (text: string, values: unknown[]) => Promise<{ rows: Array<Record<string, unknown>> }>
}

/**
 * Kullanicinin kayitli dil tercihi: announcement_prefs.lang (sema 0072; panel dil
 * degistikce SetAnnouncementLang ile esitlenir). Satir/deger yoksa ya da sorgu
 * basarisizsa null (en iyi caba: e-posta gonderimini ASLA bozmaz).
 */
export async function savedEmailLang(db: Queryable, userId: string | null | undefined): Promise<EmailLang | null> {
  if (!userId) return null
  try {
    const { rows } = await db.query(`SELECT lang FROM announcement_prefs WHERE user_id = $1`, [userId])
    return normalizeEmailLang(rows[0]?.lang)
  } catch (e) {
    console.warn("[email-lang] kayitli dil okunamadi, yedek siraya geciliyor:", e)
    return null
  }
}
