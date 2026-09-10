import { createI18n } from 'vue-i18n'
import tr from '~/i18n/locales/tr.json'
import en from '~/i18n/locales/en.json'

// Global çeviri erişimi: composable ve saf yardımcı fonksiyonlar (useAuth,
// hostnameError, useBilling) setup bağlamı dışında da çalışabildiği için
// useI18n() yerine bu referansı kullanır. Plugin kurulumunda atanır.
let _global: { t: (key: string, ...args: any[]) => string } | null = null

/** Setup dışından güvenli çeviri; i18n hazır değilse anahtarı döndürür. */
export function gt(key: string, named?: Record<string, unknown>): string {
  if (!_global) return key
  return named ? _global.t(key, named) : _global.t(key)
}

// Panel çok dilli (TR varsayılan + EN). @nuxtjs/i18n modülü Rolldown/Vite 8 ile
// çakıştığı için vue-i18n doğrudan kuruluyor. Dil tercihi localStorage'da.
export default defineNuxtPlugin((nuxtApp) => {
  let initial = 'tr'
  if (import.meta.client) {
    try {
      const s = localStorage.getItem('zorven_lang')
      if (s === 'en' || s === 'tr') initial = s
    } catch {
      /* gizli sekme/erişim yok */
    }
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
