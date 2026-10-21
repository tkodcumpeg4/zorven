<script setup lang="ts">
// Panel hata ekranı: bilinmeyen rota (404) ve beklenmeyen hatalar. Nuxt bunu
// layout dışında çizer; tema ve dil burada ayrıca hazırlanır.
import type { NuxtError } from '#app'

const props = defineProps<{ error: NuxtError }>()
const route = useRoute()
const { t, locale } = useI18n()
const { initTheme } = useTheme()
const { platformDomain } = useAuth()
onMounted(() => { initTheme() })

const is404 = computed(() => Number(props.error?.statusCode) === 404)
const site = computed(() => {
  const d = platformDomain.value || (import.meta.client ? location.host.replace(/^(panel|app)\./, '') : 'zorven.app')
  const pre = locale.value === 'en' ? '/en/' : locale.value === 'ja' ? '/ja/' : '/'
  return `https://${d}${pre}`
})
useHead({ title: computed(() => `${is404.value ? t('notFound.title') : t('notFound.errorTitle')} · Zorven`) })

const home = () => clearError({ redirect: '/' })
const back = () => { if (import.meta.client && history.length > 1) history.back(); else home() }
</script>

<template>
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden px-4">
    <StarField />
    <main class="relative z-10 w-full max-w-md text-center">
      <div class="mb-6 flex items-center justify-center gap-2"><Logo class="size-10 shrink-0" /><span class="text-lg font-semibold text-fg">zorven</span></div>
      <p class="font-mono text-7xl font-bold tracking-tight text-accent sm:text-8xl" aria-hidden="true">
        {{ is404 ? '404' : (error?.statusCode || 500) }}
      </p>
      <h1 class="mt-4 text-xl font-semibold text-fg sm:text-2xl">
        {{ is404 ? t('notFound.title') : t('notFound.errorTitle') }}
      </h1>
      <p class="mt-2 text-sm text-fg-muted">
        {{ is404 ? t('notFound.desc') : t('notFound.errorDesc') }}
      </p>
      <p v-if="is404" class="mt-3 truncate font-mono text-xs text-fg-subtle">{{ route.fullPath }}</p>
      <div class="mt-8 flex flex-wrap items-center justify-center gap-2">
        <button class="inline-flex cursor-pointer items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90" @click="home">
          <Icon name="lucide:layout-dashboard" class="size-4" /> {{ t('notFound.home') }}
        </button>
        <button class="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-surface" @click="back">
          <Icon name="lucide:arrow-left" class="size-4" /> {{ t('notFound.back') }}
        </button>
        <a :href="site" class="inline-flex items-center gap-1.5 rounded-md px-4 py-2 text-sm text-fg-muted hover:text-fg">
          <Icon name="lucide:globe" class="size-4" /> {{ t('notFound.site') }}
        </a>
      </div>
    </main>
  </div>
</template>
