<script setup lang="ts">
import { authClient } from '~/lib/auth-client'

const { method, user, updateName, changeEmail, changePassword } = useAuth()
const toast = useToast()
const { t } = useI18n()

const loading = ref(true)
const currentName = ref('')
const currentEmail = ref('')
const emailVerified = ref(false)

// Form state
const nameInput = ref('')
const nameBusy = ref(false)

const newEmail = ref('')
const emailBusy = ref(false)
const emailSent = ref(false)

const curPass = ref('')
const newPass = ref('')
const newPass2 = ref('')
const passBusy = ref(false)
const revokeOther = ref(true)

// İstek sınırı / genel hata → kullanıcı dostu mesaj
function friendly(e: any): string {
  const msg = String(e?.message || '')
  if (/too many|rate.?limit|429/i.test(msg)) {
    return t('profile.errRate')
  }
  return msg || t('profile.errGeneric')
}

async function loadSession() {
  loading.value = true
  try {
    const s = await authClient.getSession()
    const u = (s?.data as any)?.user
    currentName.value = u?.name ?? user.value?.name ?? ''
    currentEmail.value = u?.email ?? user.value?.email ?? ''
    emailVerified.value = !!u?.emailVerified
    nameInput.value = currentName.value
  } catch {
    currentName.value = user.value?.name ?? ''
    currentEmail.value = user.value?.email ?? ''
    nameInput.value = currentName.value
  } finally {
    loading.value = false
  }
}

onMounted(loadSession)

async function saveName() {
  const n = nameInput.value.trim()
  if (!n) { toast.warn(t('profile.warnNameEmpty')); return }
  if (n === currentName.value) { toast.info(t('profile.infoNameSame')); return }
  nameBusy.value = true
  try {
    await updateName(n)
    currentName.value = n
    toast.success(t('profile.nameUpdated'))
  } catch (e) {
    toast.error(friendly(e))
  } finally {
    nameBusy.value = false
  }
}

async function saveEmail() {
  const em = newEmail.value.trim().toLowerCase()
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(em)) { toast.warn(t('profile.warnValidEmail')); return }
  if (em === currentEmail.value.toLowerCase()) { toast.warn(t('profile.warnEmailSame')); return }
  emailBusy.value = true
  try {
    await changeEmail(em)
    emailSent.value = true
    toast.success(t('profile.emailApprovalSent'))
  } catch (e) {
    toast.error(friendly(e))
  } finally {
    emailBusy.value = false
  }
}

async function savePassword() {
  if (!curPass.value) { toast.warn(t('profile.warnCurrentPassword')); return }
  if (newPass.value.length < 8) { toast.warn(t('profile.warnPasswordMin')); return }
  if (newPass.value !== newPass2.value) { toast.warn(t('profile.warnPasswordMismatch')); return }
  passBusy.value = true
  try {
    await changePassword(curPass.value, newPass.value, revokeOther.value)
    curPass.value = newPass.value = newPass2.value = ''
    toast.success(t('profile.passwordUpdated'))
  } catch (e) {
    toast.error(friendly(e))
  } finally {
    passBusy.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('profile.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('profile.subtitle') }}</p>
    </header>

    <div v-if="method !== 'better-auth'" class="rounded-xl border border-line bg-surface/60 p-5 text-sm text-fg-muted">{{ t('profile.onlyEmailAccounts') }}</div>

    <template v-else>
      <!-- Özet -->
      <div class="rounded-xl border border-line bg-surface/60 p-5">
        <div class="flex items-center gap-3">
          <span class="grid size-11 place-items-center rounded-full bg-accent/15 text-accent text-lg font-semibold">
            {{ (currentName || currentEmail || '?').charAt(0).toUpperCase() }}
          </span>
          <div class="min-w-0">
            <div class="font-medium text-fg truncate">{{ currentName || '—' }}</div>
            <div class="flex items-center gap-2 text-xs text-fg-muted">
              <span class="truncate">{{ currentEmail }}</span>
              <span v-if="emailVerified" class="inline-flex items-center gap-0.5 rounded bg-accent/15 px-1.5 py-0.5 text-[10px] text-accent">
                <Icon name="lucide:badge-check" class="size-3" /> {{ t('profile.verified') }}
              </span>
              <span v-else class="inline-flex items-center gap-0.5 rounded bg-warn/15 px-1.5 py-0.5 text-[10px] text-warn">
                <Icon name="lucide:alert-triangle" class="size-3" /> {{ t('profile.unverified') }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- İsim -->
      <section class="rounded-xl border border-line bg-surface/60 p-5">
        <h3 class="text-sm font-semibold text-fg">{{ t('profile.displayName') }}</h3>
        <p class="mt-0.5 text-xs text-fg-muted">{{ t('profile.displayNameHint') }}</p>
        <div class="mt-3 flex gap-2 max-w-md">
          <input v-model="nameInput" type="text" maxlength="60" :placeholder="t('profile.yourName')"
            class="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            @keyup.enter="saveName" />
          <button :disabled="nameBusy" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="saveName">
            {{ nameBusy ? '...' : t('common.save') }}
          </button>
        </div>
      </section>

      <!-- E-posta -->
      <section class="rounded-xl border border-line bg-surface/60 p-5">
        <h3 class="text-sm font-semibold text-fg">{{ t('profile.emailAddress') }}</h3>
        <p class="mt-0.5 text-xs text-fg-muted">{{ t('profile.emailHintPre') }} <b class="text-fg">{{ t('profile.emailHintCurrent') }}</b> {{ t('profile.emailHintPost', { email: currentEmail }) }}</p>
        <div v-if="emailSent" class="mt-3 flex items-start gap-2 rounded-lg border border-accent/30 bg-accent/10 p-3 text-xs text-accent">
          <Icon name="lucide:mail-check" class="size-4 shrink-0 mt-0.5" />
          <span>{{ t('profile.emailSentBox') }}</span>
        </div>
        <div class="mt-3 flex gap-2 max-w-md">
          <input v-model="newEmail" type="email" :placeholder="t('profile.newEmailPlaceholder')"
            class="flex-1 rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            @keyup.enter="saveEmail" />
          <button :disabled="emailBusy" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="saveEmail">
            {{ emailBusy ? '...' : t('profile.changeBtn') }}
          </button>
        </div>
      </section>

      <!-- Şifre -->
      <section class="rounded-xl border border-line bg-surface/60 p-5">
        <h3 class="text-sm font-semibold text-fg">{{ t('profile.passwordTitle') }}</h3>
        <p class="mt-0.5 text-xs text-fg-muted">{{ t('profile.passwordHint') }}</p>
        <div class="mt-3 space-y-2.5 max-w-md">
          <input v-model="curPass" type="password" autocomplete="current-password" :placeholder="t('profile.currentPassword')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none" />
          <input v-model="newPass" type="password" autocomplete="new-password" :placeholder="t('profile.newPassword')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none" />
          <input v-model="newPass2" type="password" autocomplete="new-password" :placeholder="t('profile.newPasswordAgain')"
            class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            @keyup.enter="savePassword" />
          <label class="flex items-center gap-2 text-xs text-fg-muted">
            <input v-model="revokeOther" type="checkbox" class="accent-accent" />{{ t('profile.revokeOther') }}</label>
          <button :disabled="passBusy" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="savePassword">
            {{ passBusy ? '...' : t('profile.changePasswordBtn') }}
          </button>
        </div>
      </section>
    </template>
  </div>
</template>
