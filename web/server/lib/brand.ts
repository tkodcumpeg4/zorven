// Marka koruma kurallari (kimlik avi / taklit onleme).
//
// Go tarafindaki esi: server/store/brandnames.go (BrandKeywords). IKI liste de
// ayni tutulmalidir. Yeni bir marka sozcugu eklemek icin ikisine de ekleyin;
// tam eslesme ("support", "admin"...) icin ise yeni bir migration ile
// reserved_names tablosuna satir ekleyin.
//
// Kural: bu sozcuklerden HERHANGI BIRINI iceren organizasyon/kiraci slug'i
// ("zorven-login", "myzorven"...) kullanilamaz; tireler yok sayilir ("zor-ven").
export const BRAND_KEYWORDS = ["zorven", "rpshell"]

export function containsBrandKeyword(label: string): boolean {
  const l = label.toLowerCase().replace(/-/g, "")
  return BRAND_KEYWORDS.some((k) => l.includes(k))
}

// Otomatik uretilen slug'tan marka sozcuklerini cikarir (kayit akisi hata vermesin).
// Bos kalirsa "user" doner.
export function stripBrandKeywords(slug: string): string {
  let s = slug.toLowerCase()
  while (containsBrandKeyword(s)) {
    const before = s
    for (const k of BRAND_KEYWORDS) s = s.split(k).join("")
    if (s === before) s = s.replace(/-/g, "") // "zor-ven"
  }
  s = s.replace(/^-+|-+$/g, "")
  return s || "user"
}

// Kullanicinin SECTIGI slug icin ret nedeni: "brand" | "reserved" | null.
// isReserved: reserved_names tam eslesme denetimi.
export async function slugRejectReason(
  slug: string,
  isReserved: (name: string) => Promise<boolean>,
): Promise<"brand" | "reserved" | null> {
  const s = slug.toLowerCase().trim()
  if (s === "default") return null // platform kiracisi
  if (containsBrandKeyword(s)) return "brand"
  if (await isReserved(s)) return "reserved"
  return null
}
