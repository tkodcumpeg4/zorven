<script setup lang="ts">
// Etkin oturumlar (Guvenlik sayfasi): cihaz/tarayici, IP, acilis ve son etkinlik, "bu oturum"
// rozeti; tek oturumu ya da bu cihaz disindaki tum oturumlari sonlandirma.
// Uclar: web/server/lib/account-plugin.ts (jeton donmez; oturum `id` ile yonetilir).
import type { AccountSession } from '~/composables/useAccount'
import { accountErrorKey } from '~/lib/account'
import { parseUserAgent, deviceLabel } from '~/lib/userAgent'
import { dateLocale } from '~/plugins/i18n'

const { t } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const account = useAccount()

const sessions = ref<AccountSession[]>([])
const loading = ref(true)
const loadError = ref(false)
const busyId = ref('')
const busyOthers = ref(false)

const others = computed(() => sessions.value.filter(s => !s.current))

async function load() {
  loading.value = true
  loadError.value = false
  try {
    sessions.value = await account.listSessions()
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

function label(s: AccountSession): string {
  return deviceLabel(s.userAgent) || t('account.sessions.unknownDevice')
}
function isMobile(s: AccountSession): boolean {
  return !!parseUserAgent(s.userAgent)?.mobile
}
function abs(iso: string): string {
  return new Date(iso).toLocaleString(dateLocale())
}

async function revoke(s: AccountSession) {
  if (!confirm(t('account.sessions.revokeConfirm'))) return
  busyId.value = s.id
  try {
    await account.revokeSession(s.id)
    sessions.value = sessions.value.filter(x => x.id !== s.id)
    toast.success(t('account.sessions.revoked'))
  } catch (e: any) {
    toast.error(t(e?.code ? accountErrorKey(e.code) : 'account.sessions.revokeFailed'))
  } finally {
    busyId.value = ''
  }
}

async function revokeOthers() {
  if (!confirm(t('account.sessions.revokeOthersConfirm'))) return
  busyOthers.value = true
  try {
    await account.revokeOtherSessions()
    sessions.value = sessions.value.filter(s => s.current)
    toast.success(t('account.sessions.revokedOthers'))
  } catch {
    toast.error(t('account.sessions.revokeFailed'))
  } finally {
    busyOthers.value = false
  }
}
</script>

<template>
  <PanelFrame :label="t('account.sessions.title')" :meta="sessions.length ? String(sessions.length) : undefined">
    <p class="px-4 pt-3 text-xs text-fg-muted">{{ t('account.sessions.intro') }}</p>

    <p v-if="loading" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>

    <div v-else-if="loadError" class="flex flex-wrap items-center gap-3 px-4 py-6 text-sm text-danger">
      <Icon name="lucide:alert-circle" class="size-4 shrink-0" aria-hidden="true" />
      <span>{{ t('account.sessions.loadFailed') }}</span>
      <button class="cursor-pointer rounded-md border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-bg" @click="load">
        {{ t('account.sessions.retry') }}
      </button>
    </div>

    <ul v-else class="divide-y divide-line">
      <li v-for="s in sessions" :key="s.id" class="flex flex-wrap items-center gap-3 px-4 py-3">
        <span class="grid size-9 shrink-0 place-items-center rounded-lg bg-surface-2 text-fg-muted">
          <Icon v-if="isMobile(s)" name="lucide:smartphone" class="size-4" aria-hidden="true" />
          <Icon v-else name="lucide:monitor" class="size-4" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm font-medium text-fg">{{ label(s) }}</span>
            <span v-if="s.current" class="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium text-accent">
              {{ t('account.sessions.thisSession') }}
            </span>
          </div>
          <div class="mt-0.5 flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-fg-muted">
            <span class="break-all font-mono">{{ s.ipAddress || '—' }}</span>
            <span :title="abs(s.createdAt)">{{ t('account.sessions.created') }}: {{ relativeTime(s.createdAt) }}</span>
            <span :title="abs(s.updatedAt)">{{ t('account.sessions.lastActive') }}: {{ relativeTime(s.updatedAt) }}</span>
          </div>
        </div>
        <button
          v-if="!s.current"
          :disabled="busyId === s.id || busyOthers"
          class="cursor-pointer rounded-md border border-danger/40 px-3 py-1.5 text-xs font-medium text-danger hover:bg-danger/10 disabled:cursor-not-allowed disabled:opacity-50"
          @click="revoke(s)"
        >
          {{ busyId === s.id ? '...' : t('account.sessions.revoke') }}
        </button>
      </li>
    </ul>

    <div v-if="!loading && !loadError" class="flex flex-wrap items-center justify-between gap-3 border-t border-line px-4 py-3">
      <p class="text-xs text-fg-subtle">{{ others.length ? '' : t('account.sessions.onlyThis') }}</p>
      <button
        v-if="others.length"
        :disabled="busyOthers || !!busyId"
        class="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-danger/40 px-3 py-1.5 text-xs font-medium text-danger hover:bg-danger/10 disabled:cursor-not-allowed disabled:opacity-50"
        @click="revokeOthers"
      >
        <Icon name="lucide:log-out" class="size-3.5" aria-hidden="true" />
        {{ busyOthers ? '...' : t('account.sessions.revokeOthers') }}
      </button>
    </div>
  </PanelFrame>
</template>
