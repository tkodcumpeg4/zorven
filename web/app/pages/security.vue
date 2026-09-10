<script setup lang="ts">
import QRCode from 'qrcode'

const {
  method,
  twoFactorEnabled,
  enableTwoFactor,
  confirmTotpSetup,
  disableTwoFactor,
  refreshTwoFactorState,
} = useAuth()
const toast = useToast()
const { t } = useI18n()

type View = 'loading' | 'disabled' | 'setup' | 'enabled'
const view = ref<View>('loading')

// Enable akışı
const password = ref('')
const setupCode = ref('')
const totpURI = ref('')
const qrDataUrl = ref('')
const backupCodes = ref<string[]>([])
const busy = ref(false)
const err = ref('')

// Disable akışı
const disablePassword = ref('')
const showDisable = ref(false)

const manualSecret = computed(() => {
  try { return new URL(totpURI.value).searchParams.get('secret') || '' } catch { return '' }
})

onMounted(async () => {
  if (method.value !== 'better-auth') { view.value = 'disabled'; return }
  await refreshTwoFactorState()
  view.value = twoFactorEnabled.value ? 'enabled' : 'disabled'
})

watch(twoFactorEnabled, (v) => {
  if (view.value !== 'setup') view.value = v ? 'enabled' : 'disabled'
})

async function startEnable() {
  err.value = ''
  if (!password.value) { err.value = t('security.errPassword'); return }
  busy.value = true
  try {
    const { totpURI: uri, backupCodes: codes } = await enableTwoFactor(password.value)
    totpURI.value = uri
    backupCodes.value = codes
    qrDataUrl.value = uri ? await QRCode.toDataURL(uri, { margin: 1, width: 200 }) : ''
    view.value = 'setup'
    password.value = ''
  } catch (e: any) {
    err.value = e.message || t('security.errEnableFailed')
  } finally {
    busy.value = false
  }
}

async function confirmSetup() {
  err.value = ''
  const code = setupCode.value.trim()
  if (!code) { err.value = t('security.errEnterCode'); return }
  busy.value = true
  try {
    await confirmTotpSetup(code)
    toast.success(t('security.enabledToast'))
    setupCode.value = ''
    view.value = 'enabled'
  } catch (e: any) {
    err.value = e.message || t('security.errCodeFailed')
  } finally {
    busy.value = false
  }
}

async function doDisable() {
  err.value = ''
  if (!disablePassword.value) { err.value = t('security.errPassword'); return }
  busy.value = true
  try {
    await disableTwoFactor(disablePassword.value)
    toast.success(t('security.disabledToast'))
    disablePassword.value = ''
    showDisable.value = false
    view.value = 'disabled'
  } catch (e: any) {
    err.value = e.message || t('security.errDisableFailed')
  } finally {
    busy.value = false
  }
}

function copyBackup() {
  try {
    navigator.clipboard.writeText(backupCodes.value.join('\n'))
    toast.success(t('security.backupCopied'))
  } catch { /* pano yok */ }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('security.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('security.subtitle') }}</p>
    </header>

    <!-- Sadece Better Auth hesapları -->
    <div v-if="method !== 'better-auth'" class="rounded-xl border border-line bg-surface/60 p-5 text-sm text-fg-muted">{{ t('security.onlyEmailAccounts') }}</div>

    <div v-else class="rounded-xl border border-line bg-surface/60 p-5">
      <div v-if="err" class="mb-4 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
        <Icon name="lucide:alert-circle" class="size-4 shrink-0 mt-0.5" />
        <span>{{ err }}</span>
      </div>

      <div class="flex items-start justify-between gap-4">
        <div class="flex items-start gap-3">
          <span class="grid size-9 place-items-center rounded-lg bg-accent/15 text-accent shrink-0">
            <Icon name="lucide:shield-check" class="size-5" />
          </span>
          <div>
            <h3 class="text-sm font-semibold text-fg">{{ t('security.twoFaTitle') }}</h3>
            <p class="mt-0.5 text-xs text-fg-muted">{{ t('security.twoFaSubtitle') }}</p>
          </div>
        </div>
        <span class="rounded-full px-2.5 py-1 text-[11px] font-medium"
          :class="view === 'enabled' ? 'bg-accent/15 text-accent' : 'bg-surface-2 text-fg-muted border border-line'">
          {{ view === 'enabled' ? t('security.enabled') : t('security.off') }}
        </span>
      </div>

      <!-- KAPALI: aç -->
      <div v-if="view === 'disabled'" class="mt-5 border-t border-line pt-5">
        <label class="mb-1 block font-mono text-xs text-fg-muted">{{ t('security.verifyPassword') }}</label>
        <div class="flex gap-2">
          <input v-model="password" type="password" autocomplete="current-password" placeholder="••••••••"
            class="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            @keyup.enter="startEnable" />
          <button :disabled="busy" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="startEnable">
            {{ busy ? '...' : t('security.enable2fa') }}
          </button>
        </div>
      </div>

      <!-- KURULUM: QR + yedek kodlar + onay -->
      <div v-else-if="view === 'setup'" class="mt-5 border-t border-line pt-5 space-y-5">
        <div class="grid gap-5 sm:grid-cols-[auto_1fr] sm:items-start">
          <div class="rounded-lg border border-line bg-white p-2 w-fit">
            <img v-if="qrDataUrl" :src="qrDataUrl" alt="TOTP QR" width="180" height="180" />
          </div>
          <div class="space-y-2 text-sm">
            <p class="text-fg-muted">{{ t('security.scanQr') }}</p>
            <p class="text-fg-muted">{{ t('security.manualKey') }}</p>
            <code class="block break-all rounded bg-bg border border-line px-2 py-1.5 font-mono text-xs text-fg">{{ manualSecret }}</code>
          </div>
        </div>

        <!-- Yedek kodlar -->
        <div v-if="backupCodes.length" class="rounded-lg border border-warn/30 bg-warn/5 p-4">
          <div class="flex items-center justify-between">
            <h4 class="text-xs font-semibold text-fg flex items-center gap-1.5">
              <Icon name="lucide:key-round" class="size-3.5 text-warn" /> {{ t('security.backupCodes') }}
            </h4>
            <button class="text-[11px] text-accent hover:underline" @click="copyBackup">{{ t('common.copy') }}</button>
          </div>
          <p class="mt-1 text-[11px] text-fg-muted">{{ t('security.backupHint') }}</p>
          <div class="mt-2 grid grid-cols-2 gap-1.5 font-mono text-xs text-fg">
            <span v-for="c in backupCodes" :key="c" class="rounded bg-bg border border-line px-2 py-1">{{ c }}</span>
          </div>
        </div>

        <!-- Onay -->
        <div>
          <label class="mb-1 block font-mono text-xs text-fg-muted">{{ t('security.enterCode') }}</label>
          <div class="flex gap-2">
            <input v-model="setupCode" inputmode="numeric" autocomplete="one-time-code" placeholder="000000"
              class="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-center font-mono text-lg tracking-[0.3em] text-fg placeholder:tracking-normal focus:border-accent focus:outline-none"
              @keyup.enter="confirmSetup" />
            <button :disabled="busy" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="confirmSetup">
              {{ busy ? '...' : t('security.confirm') }}
            </button>
          </div>
        </div>
      </div>

      <!-- ETKIN: kapat -->
      <div v-else-if="view === 'enabled'" class="mt-5 border-t border-line pt-5">
        <p class="text-sm text-fg-muted">{{ t('security.enabledDesc') }}</p>
        <div v-if="!showDisable" class="mt-3">
          <button class="rounded-md border border-danger/40 px-3 py-1.5 text-xs font-medium text-danger hover:bg-danger/10" @click="showDisable = true">
            {{ t('security.disable2fa') }}
          </button>
        </div>
        <div v-else class="mt-3">
          <label class="mb-1 block font-mono text-xs text-fg-muted">{{ t('security.disableVerify') }}</label>
          <div class="flex gap-2">
            <input v-model="disablePassword" type="password" autocomplete="current-password" placeholder="••••••••"
              class="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
              @keyup.enter="doDisable" />
            <button :disabled="busy" class="rounded-md bg-danger px-4 py-2 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50" @click="doDisable">
              {{ busy ? '...' : t('security.disable') }}
            </button>
            <button class="rounded-md border border-line px-3 py-2 text-xs text-fg-muted hover:bg-bg" @click="showDisable = false; err = ''">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </div>

      <div v-else class="mt-5 text-sm text-fg-muted">{{ t('common.loading') }}</div>
    </div>
  </div>
</template>
