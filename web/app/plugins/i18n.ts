// ACIK SURUM override (scripts/oss/overrides): panel yalniz TR + EN.
import { createI18n } from 'vue-i18n'
import tr from '~/i18n/locales/tr.json'
import en from '~/i18n/locales/en.json'

// Global çeviri erişimi: composable ve saf yardımcı fonksiyonlar (useAuth,
// hostnameError, useBilling) setup bağlamı dışında da çalışabildiği için
// useI18n() yerine bu referansı kullanır. Plugin kurulumunda atanır.
let _global: { t: (key: string, ...args: any[]) => string } | null = null

/** Aktif dil kodu (tr|en|ja); i18n hazır değilse tr. */
export function currentLang(): 'tr' | 'en' | 'ja' {
  const l = (_global as any)?.locale
  const v = l && typeof l === 'object' && 'value' in l ? l.value : l
  return v === 'en' ? v : 'tr'
}

/** Tarih/saat bicimlendirme icin BCP47 etiketi (tr-TR, en-US, ja-JP). */
export function dateLocale(): string {
  const l = currentLang()
  return l === 'ja' ? 'ja-JP' : l === 'en' ? 'en-US' : 'tr-TR'
}

/** Setup dışından güvenli çeviri; i18n hazır değilse anahtarı döndürür. */
export function gt(key: string, named?: Record<string, unknown>): string {
  if (!_global) return key
  return named ? _global.t(key, named) : _global.t(key)
}

// Panel çok dilli (TR varsayılan + EN). @nuxtjs/i18n modülü Rolldown/Vite 8 ile
// çakıştığı için vue-i18n doğrudan kuruluyor. Dil tercihi localStorage'da.
// Kayıtlı tercih yoksa tarayıcı dili kullanılır: Türkçe -> tr, diğer her dil -> en.
export default defineNuxtPlugin((nuxtApp) => {
  let initial = 'tr'
  if (import.meta.client) {
    let saved: string | null = null
    try {
      saved = localStorage.getItem('zorven_lang')
    } catch {
      /* gizli sekme/erişim yok */
    }
    if (saved === 'en' || saved === 'tr') initial = saved
    else initial = browserLang()
  }
  const i18n = createI18n({
    legacy: false,
    globalInjection: true,
    locale: initial,
    fallbackLocale: 'tr',
    messages: { tr, en },
  })
  nuxtApp.vueApp.use(i18n)
  _global = i18n.global as any
})

/** Tarayıcının tercih ettiği dillerden ilk tanınanı seçer; Türkçe -> tr, aksi halde İngilizce. */
export function browserLang(langs: readonly string[] = navigator.languages?.length ? navigator.languages : [navigator.language]): 'tr' | 'en' | 'ja' {
  for (const l of langs) {
    const p = (l || '').toLowerCase().split('-')[0]
    if (p === 'tr') return 'tr'
    if (p) return 'en'
  }
  return 'tr'
}
