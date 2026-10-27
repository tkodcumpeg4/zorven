/**
 * Kayit formu CAPTCHA'si (Zorven CAPTCHA, kendi widget'imiz).
 *
 * Akis: GET /api/v1/captcha/platform-config -> {enabled, sitekey}. enabled ise
 * /captcha/v1/api.js (ayni origin; dev'de nitro devProxy) yuklenir ve widget
 * verilen kapsayiciya cizilir. Jeton `X-Zorven-Captcha` basligiyla sign-up
 * istegine eklenir (web/server/lib/signup-guard.ts dogrular). Sosyal giris
 * CAPTCHA'siz kalir.
 */

interface ZorvenCaptchaApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string
  reset: (id: string) => void
  remove: (id: string) => void
}

declare global {
  interface Window {
    zorvenCaptcha?: ZorvenCaptchaApi
  }
}

const SCRIPT_SRC = '/captcha/v1/api.js'
let scriptPromise: Promise<void> | null = null

function loadScript(): Promise<void> {
  if (window.zorvenCaptcha) return Promise.resolve()
  if (scriptPromise) return scriptPromise
  scriptPromise = new Promise<void>((resolve, reject) => {
    const s = document.createElement('script')
    s.src = SCRIPT_SRC
    s.async = true
    s.onload = () => resolve()
    s.onerror = () => {
      scriptPromise = null
      reject(new Error('captcha script'))
    }
    document.head.appendChild(s)
  })
  return scriptPromise
}

export function useSignupCaptcha() {
  const { locale } = useI18n()
  const enabled = ref(false)
  /** 'idle' | 'loading' | 'ready' | 'error' */
  const status = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const token = ref('')
  let sitekey = ''
  let widgetId = ''
  let configLoaded = false

  async function loadConfig() {
    if (configLoaded) return
    try {
      const r = await $fetch<{ enabled?: boolean, sitekey?: string }>('/api/v1/captcha/platform-config')
      enabled.value = !!(r?.enabled && r.sitekey)
      sitekey = r?.sitekey ?? ''
    } catch {
      // Ayar okunamazsa CAPTCHA kapali sayilir; sunucu kancasi yine de dogrular.
      enabled.value = false
    }
    configLoaded = true
  }

  /** Widget'i kapsayiciya cizer (kayit sekmesi acildiginda cagrilir). */
  async function mount(el: HTMLElement | null) {
    await loadConfig()
    if (!enabled.value || !el) return
    status.value = 'loading'
    try {
      await loadScript()
      if (widgetId && window.zorvenCaptcha) {
        try { window.zorvenCaptcha.remove(widgetId) } catch { /* yoksay */ }
      }
      token.value = ''
      widgetId = window.zorvenCaptcha!.render(el, {
        sitekey,
        theme: 'auto',
        lang: locale.value === 'ja' || locale.value === 'en' || locale.value === 'tr' ? locale.value : 'auto',
        callback: (t: string) => { token.value = t },
        'expired-callback': () => { token.value = '' },
        'error-callback': () => { token.value = ''; status.value = 'error' },
      })
      status.value = 'ready'
    } catch {
      status.value = 'error'
    }
  }

  /** Jeton tek kullanimliktir: hata sonrasi widget sifirlanir. */
  function reset() {
    token.value = ''
    if (widgetId && window.zorvenCaptcha) {
      try { window.zorvenCaptcha.reset(widgetId) } catch { /* yoksay */ }
    }
  }

  function unmount() {
    if (widgetId && window.zorvenCaptcha) {
      try { window.zorvenCaptcha.remove(widgetId) } catch { /* yoksay */ }
    }
    widgetId = ''
    token.value = ''
  }

  return { enabled, status, token, loadConfig, mount, reset, unmount }
}
