import tailwindcss from '@tailwindcss/vite'

export default defineNuxtConfig({
  compatibilityDate: '2025-01-01',
  devtools: { enabled: true },

  modules: ['@nuxt/icon', '@nuxt/fonts'],

  // Panel çok dilli: TR (varsayılan) + EN. @nuxtjs/i18n yerine vue-i18n doğrudan
  // bir plugin ile kuruluyor (app/plugins/i18n.ts) — modülün bundler plugin'i
  // Rolldown/Vite 8 ile çakıştığı için (builtin:vite-json). Dil localStorage'da.

  // vue-i18n'in useI18n'ini global auto-import yap (modül olmadigi icin).
  imports: {
    presets: [{ from: 'vue-i18n', imports: ['useI18n'] }],
  },

  css: ['~/assets/css/main.css', '@xterm/xterm/css/xterm.css'],

  vite: {
    plugins: [tailwindcss()],

  },

  // Dev proxy: dashboard (:3000) -> tunel sunucusu (:8443), AYNI origin.
  //
  // Nitro'nun devProxy'si kullaniliyor, vite.server.proxy DEGIL: Nuxt dev'de
  // dinleyen sunucu Nitro'dur ve WebSocket upgrade olayini o karsilar.
  // vite.server.proxy'nin ws:true'su bu yuzden terminal WSS'ini yukseltemiyordu.
  // httpxy tabanli devProxy ws'i dogru proxy'ler.
  //
  // Uretimde dashboard'i tunel sunucusu servis eder; proxy yalnizca dev icindir.
  nitro: {
    // Uretimde Nuxt'u statik SPA yerine NODE SUNUCU olarak paketle. Boylece
    // Better Auth uclari (/api/auth/*) uretimde de calisir; tunel sunucusu (Go)
    // bu uclari `--auth-upstream http://web:3000` ile buraya proxy'ler.
    // (SPA dashboard'u yine Go gomulu webdist'ten servis eder; bu servis yalnizca
    // /api/auth/* icin gereklidir ama Nuxt tam uygulamayi da sunabilir.)
    preset: 'node-server',
    devProxy: {
      '/api/v1': {
        // NOT: nitro.devProxy eslesen '/api/v1' onekini KIRPAR ve kalani
        // hedefe ekler. Hedefe '/api/v1' ekliyoruz ki tam yol korunsun:
        // '/api/v1/health' -> hedef '/api/v1' + '/health' = '/api/v1/health'.
        target: (process.env.ZORVEN_PROXY_TARGET || 'https://localhost:8443') + '/api/v1',
        changeOrigin: true,
        secure: false, // lokal self-signed sertifika
        ws: true,
      },
    },
  },

  runtimeConfig: {
    public: {
      apiBase: '/api/v1',
      // Terminal WS'inin dogrudan baglanacagi sunucu tabani.
      // Bos ise ayni origin kullanilir (uretim: dashboard'i sunucu servis eder).
      // Dev'de dev proxy WS upgrade'i yukseltemedigi icin sunucuya dogrudan
      // baglaniyoruz; NUXT_PUBLIC_WS_BASE=wss://localhost:8443 ile ayarlanir.
      wsBase: '',
    },
  },

  // Dashboard tamamen kimlik doğrulamalı ve canlı veri odaklı — SSR'a gerek yok,
  // hydration mismatch riskini de sıfırlar (canlı durum, göreli zaman damgaları).
  ssr: false,

  app: {
    pageTransition: { name: 'page', mode: 'out-in' },
    head: {
      title: 'Zorven - Reverse Proxy & Tünel Platformu',
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' },
      ],
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'color-scheme', content: 'dark light' },
      ],
    },
  },
})
