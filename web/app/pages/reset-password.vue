<script setup lang="ts">
definePageMeta({ layout: false })

const route = useRoute()
const router = useRouter()
const { resetPassword } = useAuth()
const { initTheme } = useTheme()
const { t } = useI18n()

const token = computed(() => String(route.query.token ?? ''))
const pw = ref('')
const pw2 = ref('')
const busy = ref(false)
const error = ref('')
const done = ref(false)

onMounted(() => { initTheme() })

async function submit() {
  error.value = ''
  if (!token.value) { error.value = t('reset.errInvalidToken'); return }
  if (pw.value.length < 8) { error.value = t('reset.errPasswordMin'); return }
  if (pw.value !== pw2.value) { error.value = t('reset.errMismatch'); return }
  busy.value = true
  try {
    await resetPassword(token.value, pw.value)
    done.value = true
    setTimeout(() => router.push('/'), 1800)
  } catch (e: any) {
    error.value = e.message || t('reset.errFailed')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid min-h-screen place-items-center bg-bg px-5">
    <div class="w-full max-w-md rounded-xl border border-line bg-surface/80 p-6 shadow-xl backdrop-blur-md">
      <div class="flex items-center gap-3 border-b border-line pb-4">
        <Logo class="size-10 shrink-0" />
        <div>
          <h1 class="text-base font-bold tracking-tight text-fg">{{ t('reset.title') }}</h1>
          <p class="text-xs text-fg-muted">{{ t('reset.subtitle') }}</p>
        </div>
      </div>

      <div v-if="done" class="mt-5 flex items-start gap-2 rounded-lg border border-accent/30 bg-accent/10 p-3 text-sm text-accent">
        <Icon name="lucide:check-circle" class="size-5 shrink-0 mt-0.5" />
        <span>{{ t('reset.done') }}</span>
      </div>

      <template v-else>
        <div v-if="error" class="mt-4 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
          <Icon name="lucide:alert-circle" class="size-4 shrink-0 mt-0.5" />
          <span>{{ error }}</span>
        </div>

        <form class="mt-4 space-y-3.5" @submit.prevent="submit">
          <div>
            <label class="mb-1 block font-mono text-xs text-fg-muted">{{ t('reset.newPassword') }}</label>
            <input v-model="pw" type="password" autocomplete="new-password" placeholder="••••••••"
              class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none" />
          </div>
          <div>
            <label class="mb-1 block font-mono text-xs text-fg-muted">{{ t('reset.newPasswordAgain') }}</label>
            <input v-model="pw2" type="password" autocomplete="new-password" placeholder="••••••••"
              class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
              @keyup.enter="submit" />
          </div>
          <button type="submit" :disabled="busy"
            class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50">
            <Icon v-if="busy" name="lucide:loader-circle" class="size-4 animate-spin" />
            {{ busy ? t('reset.saving') : t('reset.updateBtn') }}
          </button>
          <div class="text-center">
            <NuxtLink to="/" class="text-xs text-fg-muted hover:text-accent hover:underline">{{ t('reset.backToSignin') }}</NuxtLink>
          </div>
        </form>
      </template>
    </div>
  </div>
</template>
