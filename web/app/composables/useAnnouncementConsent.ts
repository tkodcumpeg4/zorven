// Urun duyurusu e-postasi OPT-IN akisi.
//
// Kayit formundaki (isaretsiz) onay kutusu secimi, e-posta dogrulamasi veya
// OAuth yonlendirmesi yuzunden oturum henuz yokken verilebilir. Bu yuzden secim
// localStorage'a "bekleyen" olarak yazilir ve ilk kimlikli panel yuklemesinde
// (layout) sunucuya uygulanir. Boylece e-posta dogrulamasi/OAuth turu sonrasinda
// da kaybolmaz. Bekleyen kayit e-postaya baglidir: baska bir kullanici girerse
// uygulanmaz (OAuth'ta e-posta onceden bilinmez, bos = ilk giris yapan).
const KEY = 'zorven_announce_pending'

type Pending = { yes: boolean, email: string, at: number }

function read(): Pending | null {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const p = JSON.parse(raw) as Pending
    // 14 gunden eski bekleyen secim gecersiz
    if (!p || typeof p.yes !== 'boolean' || Date.now() - (p.at || 0) > 14 * 86400000) return null
    return p
  } catch {
    return null
  }
}

export function useAnnouncementConsent() {
  const api = useApi()
  const { locale } = useI18n()
  const showBanner = ref(false)
  const busy = ref(false)

  /** Kayit/OAuth oncesi secimi sakla. yes=false yalniz e-posta kaydinda (acik "hayir") yazilir. */
  function setPending(yes: boolean, email = '') {
    try { localStorage.setItem(KEY, JSON.stringify({ yes, email: email.trim().toLowerCase(), at: Date.now() })) } catch { /* erisim yok */ }
  }

  /**
   * Oturum acildiktan sonra cagrilir: bekleyen secimi uygular, yoksa ve kullaniciya
   * hic sorulmadiysa bir kez bilgilendirme bandini gosterir.
   */
  async function init(userEmail: string) {
    try {
      const p = read()
      if (p && (!p.email || p.email === (userEmail || '').trim().toLowerCase())) {
        await api.setAnnouncementPref(p.yes, String(locale.value), 'signup')
        try { localStorage.removeItem(KEY) } catch { /* erisim yok */ }
        return
      }
      const pref = await api.getAnnouncementPref()
      showBanner.value = !pref.asked
    } catch {
      showBanner.value = false
    }
  }

  /** Bant yaniti: Evet -> abone (banner); Hayir/kapat -> reddetti olarak kaydedilir, tekrar sorulmaz. */
  async function answer(yes: boolean): Promise<boolean> {
    busy.value = true
    try {
      await api.setAnnouncementPref(yes, String(locale.value), 'banner')
      showBanner.value = false
      return true
    } catch {
      return false
    } finally {
      busy.value = false
    }
  }

  return { showBanner, busy, setPending, init, answer }
}
