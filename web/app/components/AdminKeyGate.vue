<script setup lang="ts">
const {
  authed,
  ready,
  githubEnabled,
  needs2FA,
  init,
  loginWithEmail,
  signUpWithEmail,
  loginWithBetterAuthGithub,
  loginWithKey,
  verifyTotp,
  sendEmailOtp,
  verifyEmailOtp,
  verifyBackupCode,
  requestPasswordReset,
} = useAuth()
const { t } = useI18n()
const route = useRoute()

type Tab = 'signin' | 'signup' | 'key'
const activeTab = ref<Tab>('signin')

// Form states
const email = ref('')
const password = ref('')
const name = ref('')
const keyInput = ref('')

const error = ref('')
const loading = ref(false)
// Kayıt sonrası e-posta doğrulama beklerken dolu olur (oturum henüz açılmadı).
const signedUpEmail = ref('')
// Şifremi unuttum akışı
const forgotSent = ref(false)

async function handleForgot() {
  error.value = ''
  if (!email.value.trim()) { error.value = t('auth.errForgotEmailFirst'); return }
  loading.value = true
  try {
    await requestPasswordReset(email.value.trim())
    forgotSent.value = true
  } catch (err: any) {
    error.value = err.message || t('auth.errForgotFailed')
  } finally {
    loading.value = false
  }
}

// --- 2FA (giriş ikinci adımı) ---
type TwoFaMethod = 'totp' | 'email' | 'backup'
const twoFaMethod = ref<TwoFaMethod>('totp')
const twoFaCode = ref('')
const twoFaError = ref('')
const twoFaLoading = ref(false)
const emailOtpSent = ref(false)

async function switchTwoFa(m: TwoFaMethod) {
  twoFaMethod.value = m
  twoFaCode.value = ''
  twoFaError.value = ''
  if (m === 'email' && !emailOtpSent.value) {
    twoFaLoading.value = true
    try {
      await sendEmailOtp()
      emailOtpSent.value = true
    } catch (e: any) {
      twoFaError.value = e.message || t('auth.errOtpSend')
    } finally {
      twoFaLoading.value = false
    }
  }
}

async function resendEmailOtp() {
  twoFaError.value = ''
  twoFaLoading.value = true
  try {
    await sendEmailOtp()
    emailOtpSent.value = true
  } catch (e: any) {
    twoFaError.value = e.message || t('auth.errOtpSend')
  } finally {
    twoFaLoading.value = false
  }
}

async function handleTwoFa() {
  twoFaError.value = ''
  const code = twoFaCode.value.trim()
  if (!code) { twoFaError.value = t('auth.errCode'); return }
  twoFaLoading.value = true
  try {
    if (twoFaMethod.value === 'totp') await verifyTotp(code)
    else if (twoFaMethod.value === 'email') await verifyEmailOtp(code)
    else await verifyBackupCode(code)
    // Başarılıysa needs2FA false olur ve slot render edilir.
  } catch (e: any) {
    twoFaError.value = e.message || t('auth.errCodeFailed')
  } finally {
    twoFaLoading.value = false
  }
}

const AUTH_ERROR_CODES = ['forbidden', 'state', 'exchange', 'user', 'disabled', 'code', 'server']

onMounted(async () => {
  const code = String(route.query.auth_error ?? '')
  if (code && AUTH_ERROR_CODES.includes(code)) error.value = t('auth.github_' + code)
  await init()
})

async function handleSignIn() {
  error.value = ''
  if (!email.value.trim() || !password.value) {
    error.value = t('auth.errEmailPassword')
    return
  }
  loading.value = true
  try {
    await loginWithEmail(email.value.trim(), password.value)
  } catch (err: any) {
    error.value = err.message || t('auth.errSigninFailed')
  } finally {
    loading.value = false
  }
}

async function handleSignUp() {
  error.value = ''
  if (!name.value.trim()) {
    error.value = t('auth.errName')
    return
  }
  if (!email.value.trim()) {
    error.value = t('auth.errValidEmail')
    return
  }
  if (password.value.length < 8) {
    error.value = t('auth.errPasswordMin')
    return
  }
  loading.value = true
  try {
    const r = await signUpWithEmail(email.value.trim(), password.value, name.value.trim())
    if (r.needsVerification) {
      // Hesap oluştu ama oturum açılmadı: e-posta doğrulama ekranını göster.
      signedUpEmail.value = email.value.trim()
    }
    // needsVerification false ise oturum açıldı; slot render edilir.
  } catch (err: any) {
    error.value = err.message || t('auth.errSignupFailed')
  } finally {
    loading.value = false
  }
}

async function handleSocialGithub() {
  error.value = ''
  try {
    await loginWithBetterAuthGithub()
  } catch {
    // Fallback to legacy server route if Better Auth social redirect fails
    window.location.href = '/api/v1/auth/github/login'
  }
}

async function handleKeySubmit() {
  error.value = ''
  const val = keyInput.value.trim()
  if (!val) {
    error.value = t('auth.errKeyEmpty')
    return
  }
  if (!val.startsWith('zrv_admin_') && !val.startsWith('zorven_admin_') && !val.startsWith('rpsh_admin_')) {
    error.value = t('auth.errKeyPrefix')
    return
  }
  loading.value = true
  const ok = await loginWithKey(val)
  loading.value = false
  if (ok) {
    keyInput.value = ''
  } else {
    error.value = t('auth.errKeyRejected')
  }
}
</script>

<template>
  <div v-if="!ready" class="grid min-h-[60vh] place-items-center px-5">
    <div class="flex items-center gap-2.5 text-sm text-fg-muted">
      <Icon name="lucide:loader-circle" class="size-4 animate-spin" aria-hidden="true" />
      {{ t('auth.sessionChecking') }}
    </div>
  </div>

  <!-- İki Adımlı Doğrulama (giriş 2. adımı) -->
  <div v-else-if="needs2FA" class="grid min-h-[60vh] place-items-center px-5">
    <div class="w-full max-w-md rounded-xl border border-line bg-surface/80 p-6 shadow-xl backdrop-blur-md">
      <div class="flex items-center gap-3 border-b border-line pb-4">
        <div class="grid size-10 shrink-0 place-items-center rounded-lg bg-accent/15 text-accent">
          <Icon name="lucide:shield-check" class="size-5" />
        </div>
        <div>
          <h1 class="text-base font-bold tracking-tight text-fg">{{ t('auth.twoFaTitle') }}</h1>
          <p class="text-xs text-fg-muted">{{ t('auth.twoFaSubtitle') }}</p>
        </div>
      </div>

      <!-- Yöntem sekmeleri -->
      <div class="mt-4 flex rounded-lg border border-line bg-bg/50 p-1">
        <button type="button" class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="twoFaMethod === 'totp' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="switchTwoFa('totp')">{{ t('auth.twoFaAuthenticator') }}</button>
        <button type="button" class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="twoFaMethod === 'email' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="switchTwoFa('email')">{{ t('auth.twoFaEmail') }}</button>
        <button type="button" class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="twoFaMethod === 'backup' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="switchTwoFa('backup')">{{ t('auth.twoFaBackup') }}</button>
      </div>

      <div v-if="twoFaError" class="mt-4 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
        <Icon name="lucide:alert-circle" class="size-4 shrink-0 mt-0.5" />
        <span>{{ twoFaError }}</span>
      </div>

      <form class="mt-4 space-y-3.5" @submit.prevent="handleTwoFa">
        <p class="text-xs text-fg-muted">
          <template v-if="twoFaMethod === 'totp'">{{ t('auth.twoFaTotpHint') }}</template>
          <template v-else-if="twoFaMethod === 'email'">
            {{ t('auth.twoFaEmailHint') }}
            <button type="button" class="text-accent hover:underline" :disabled="twoFaLoading" @click="resendEmailOtp">{{ t('auth.twoFaResend') }}</button>
          </template>
          <template v-else>{{ t('auth.twoFaBackupHint') }}</template>
        </p>

        <input
          v-model="twoFaCode"
          type="text"
          inputmode="numeric"
          autocomplete="one-time-code"
          :placeholder="twoFaMethod === 'backup' ? 'yedek-kod' : '000000'"
          class="w-full rounded-md border border-line bg-bg px-3 py-2 text-center font-mono text-lg tracking-[0.3em] text-fg placeholder:text-fg-subtle placeholder:tracking-normal focus:border-accent focus:outline-none"
        />

        <button type="submit" :disabled="twoFaLoading"
          class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50">
          <Icon v-if="twoFaLoading" name="lucide:loader-circle" class="size-4 animate-spin" />
          {{ twoFaLoading ? t('auth.twoFaVerifying') : t('auth.twoFaVerify') }}
        </button>
      </form>
    </div>
  </div>

  <!-- Kayıt sonrası: e-posta doğrulama bekleniyor -->
  <div v-else-if="signedUpEmail" class="grid min-h-[60vh] place-items-center px-5">
    <div class="w-full max-w-md rounded-xl border border-line bg-surface/80 p-6 shadow-xl backdrop-blur-md text-center">
      <div class="mx-auto grid size-12 place-items-center rounded-full bg-accent/15 text-accent">
        <Icon name="lucide:mail-check" class="size-6" />
      </div>
      <h1 class="mt-4 text-lg font-bold tracking-tight text-fg">{{ t('auth.verifyTitle') }}</h1>
      <p class="mt-2 text-sm text-fg-muted">{{ t('auth.verifyBody', { email: signedUpEmail }) }}</p>
      <p class="mt-3 text-xs text-fg-subtle">
        {{ t('auth.verifySpam') }}
      </p>
      <button
        class="mt-5 w-full cursor-pointer rounded-md border border-line px-4 py-2.5 text-sm font-medium text-fg transition-colors hover:bg-surface-2"
        @click="signedUpEmail = ''; activeTab = 'signin'"
      >
        {{ t('auth.backToSignin') }}
      </button>
    </div>
  </div>

  <div v-else-if="!authed" class="grid min-h-[60vh] place-items-center px-5">
    <div class="w-full max-w-md rounded-xl border border-line bg-surface/80 p-6 shadow-xl backdrop-blur-md">
      <!-- Header -->
      <div class="flex items-center gap-3 border-b border-line pb-4">
        <Logo class="size-10 shrink-0" />
        <div>
          <h1 class="text-base font-bold tracking-tight text-fg">Zorven</h1>
          <p class="text-xs text-fg-muted">{{ t('auth.subtitle') }}</p>
        </div>
      </div>

      <!-- Navigation Tabs -->
      <div class="mt-4 flex rounded-lg border border-line bg-bg/50 p-1">
        <button
          type="button"
          class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="activeTab === 'signin' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="activeTab = 'signin'; error = ''"
        >
          {{ t('auth.tabSignin') }}
        </button>
        <button
          type="button"
          class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="activeTab === 'signup' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="activeTab = 'signup'; error = ''"
        >
          {{ t('auth.tabSignup') }}
        </button>
        <button
          type="button"
          class="flex-1 rounded-md py-1.5 text-xs font-medium transition-colors"
          :class="activeTab === 'key' ? 'bg-surface text-fg shadow-sm border border-line/60' : 'text-fg-muted hover:text-fg'"
          @click="activeTab = 'key'; error = ''"
        >
          {{ t('auth.tabKey') }}
        </button>
      </div>

      <!-- Error Alert -->
      <div v-if="error" class="mt-4 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
        <Icon name="lucide:alert-circle" class="size-4 shrink-0 mt-0.5" />
        <span>{{ error }}</span>
      </div>

      <!-- Tab: Giriş Yap (Email & Password) -->
      <form v-if="activeTab === 'signin'" class="mt-4 space-y-3.5" @submit.prevent="handleSignIn">
        <div>
          <label for="signin-email" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.email') }}</label>
          <input
            id="signin-email"
            v-model="email"
            type="email"
            required
            autocomplete="email"
            :placeholder="t('auth.emailPlaceholder')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <div>
          <label for="signin-password" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.password') }}</label>
          <input
            id="signin-password"
            v-model="password"
            type="password"
            required
            autocomplete="current-password"
            placeholder="••••••••"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Icon v-if="loading" name="lucide:loader-circle" class="size-4 animate-spin" />
          {{ loading ? t('auth.signingIn') : t('auth.signinBtn') }}
        </button>

        <!-- Şifremi unuttum -->
        <div v-if="forgotSent" class="flex items-start gap-2 rounded-lg border border-accent/30 bg-accent/10 p-3 text-xs text-accent">
          <Icon name="lucide:mail-check" class="size-4 shrink-0 mt-0.5" />
          <span>{{ t('auth.forgotSent') }}</span>
        </div>
        <div v-else class="text-center">
          <button type="button" class="cursor-pointer text-xs text-fg-muted hover:text-accent hover:underline" @click="handleForgot">
            {{ t('auth.forgotPassword') }}
          </button>
        </div>

        <!-- GitHub Social Button -->
        <div v-if="githubEnabled" class="pt-2">
          <div class="relative my-3 text-center">
            <span class="absolute inset-0 flex items-center"><span class="w-full border-t border-line" /></span>
            <span class="relative bg-surface/80 px-2 text-[11px] text-fg-subtle">{{ t('auth.orContinue') }}</span>
          </div>
          <button
            type="button"
            class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md border border-line bg-bg px-4 py-2 text-xs font-medium text-fg transition-colors hover:bg-surface"
            @click="handleSocialGithub"
          >
            <Icon name="lucide:github" class="size-4" />
            {{ t('auth.githubContinue') }}
          </button>
        </div>
      </form>

      <!-- Tab: Kayıt Ol -->
      <form v-else-if="activeTab === 'signup'" class="mt-4 space-y-3.5" @submit.prevent="handleSignUp">
        <div>
          <label for="signup-name" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.nameLabel') }}</label>
          <input
            id="signup-name"
            v-model="name"
            type="text"
            required
            autocomplete="name"
            :placeholder="t('auth.namePlaceholder')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <div>
          <label for="signup-email" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.email') }}</label>
          <input
            id="signup-email"
            v-model="email"
            type="email"
            required
            autocomplete="email"
            :placeholder="t('auth.signupEmailPlaceholder')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <div>
          <label for="signup-password" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.passwordMin') }}</label>
          <input
            id="signup-password"
            v-model="password"
            type="password"
            required
            minlength="8"
            autocomplete="new-password"
            placeholder="••••••••"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Icon v-if="loading" name="lucide:loader-circle" class="size-4 animate-spin" />
          {{ loading ? t('auth.creatingAccount') : t('auth.createAccount') }}
        </button>

        <p class="pt-1 text-center text-[11px] text-fg-subtle">
          {{ t('auth.signupNote') }}
        </p>
      </form>

      <!-- Tab: Admin Anahtarı -->
      <form v-else-if="activeTab === 'key'" class="mt-4 space-y-3.5" @submit.prevent="handleKeySubmit">
        <p class="text-xs text-fg-muted">
          {{ t('auth.keyHint') }}
        </p>

        <div>
          <label for="adminkey" class="mb-1 block font-mono text-xs text-fg-muted">{{ t('auth.keyLabel') }}</label>
          <input
            id="adminkey"
            v-model="keyInput"
            type="password"
            autocomplete="off"
            spellcheck="false"
            placeholder="zorven_admin_…"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
          />
        </div>

        <button
          type="submit"
          :disabled="loading"
          class="flex w-full cursor-pointer items-center justify-center gap-2 rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Icon v-if="loading" name="lucide:loader-circle" class="size-4 animate-spin" />
          {{ loading ? t('auth.keyVerifying') : t('auth.keyConnect') }}
        </button>

        <p class="border-t border-line pt-3 text-[11px] text-fg-subtle">
          {{ t('auth.keyNote') }}
        </p>
      </form>
    </div>
  </div>

  <slot v-else />
</template>
