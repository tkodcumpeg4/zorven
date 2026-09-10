<script setup lang="ts">
const api = useApi()
const route = useRoute()
const { t } = useI18n()

const code = ref(String(route.query.code ?? '').toUpperCase())
const state = ref<'idle' | 'loading' | 'ok' | 'error'>('idle')
const message = ref('')
const deviceName = ref('')

async function approve() {
  if (!code.value.trim()) { state.value = 'error'; message.value = t('device.errEmpty'); return }
  state.value = 'loading'
  try {
    const res = await api.deviceApprove(code.value.trim())
    if (res.ok) {
      state.value = 'ok'
      deviceName.value = res.name ?? t('device.defaultDevice')
    } else {
      state.value = 'error'; message.value = t('device.errFailed')
    }
  } catch (e: unknown) {
    state.value = 'error'
    message.value = (e as { data?: { error?: { message?: string } } })?.data?.error?.message ?? t('device.errInvalid')
  }
}
</script>

<template>
  <div class="mx-auto max-w-md space-y-6 py-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('device.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('device.subtitle') }}</p>
    </header>

    <div class="rounded-lg border border-line bg-surface p-5">
      <template v-if="state === 'ok'">
        <div class="space-y-2 text-center">
          <div class="mx-auto grid size-12 place-items-center rounded-full bg-accent/15 text-accent"><Icon name="lucide:check" class="size-6" /></div>
          <h2 class="text-lg font-semibold">{{ t('device.connected') }}</h2>
          <p class="text-sm text-fg-muted">{{ t('device.connectedDesc', { name: deviceName }) }}</p>
        </div>
      </template>

      <template v-else>
        <label class="label-sys mb-2 block">{{ t('device.deviceCode') }}</label>
        <input
          v-model="code"
          placeholder="XXXX-XXXX"
          class="w-full rounded border border-line bg-surface-2 px-3 py-2.5 text-center font-mono text-lg tracking-widest uppercase"
          @keyup.enter="approve"
        >
        <p v-if="state === 'error'" class="mt-2 text-sm text-danger">{{ message }}</p>
        <button
          class="mt-4 w-full cursor-pointer rounded bg-accent px-4 py-2.5 font-semibold text-on-accent transition hover:opacity-90 disabled:opacity-50"
          :disabled="state === 'loading'"
          @click="approve"
        >
          {{ state === 'loading' ? t('device.approving') : t('device.approveConnect') }}
        </button>
        <p class="mt-3 text-center text-xs text-fg-subtle">{{ t('device.footer') }}</p>
      </template>
    </div>
  </div>
</template>
