<script setup lang="ts">
// Ekip daveti kabul sayfası. Davet e-postasındaki link buraya gelir
// (/invite?id=...). Oturum yoksa AdminKeyGate giriş/kayıt gösterir; kayıt
// sonrası doğrulama linki de bu sayfaya döner. Davet yalnızca gönderildiği
// e-postanın sahibi tarafından kabul edilebilir (sunucu zorlar).
import type { TeamInvitation } from '~/types/api'

const api = useApi()
const toast = useToast()
const route = useRoute()
const { t } = useI18n()
const { user, switchOrganization } = useAuth()
const { clearActiveProject } = useActiveProject()

const id = computed(() => String(route.query.id || ''))
const loading = ref(true)
const busy = ref(false)
const inv = ref<TeamInvitation | null>(null)
const errorCode = ref('')
const errorMsg = ref('')
const done = ref<'accepted' | 'declined' | ''>('')

async function load() {
  loading.value = true
  errorCode.value = ''
  errorMsg.value = ''
  if (!id.value) {
    errorCode.value = 'invitation_not_found'
    loading.value = false
    return
  }
  try {
    inv.value = await api.getInvitation(id.value)
  } catch (e: any) {
    errorCode.value = e?.data?.error?.code || 'unknown'
    errorMsg.value = e?.data?.error?.message || e?.message || ''
  } finally {
    loading.value = false
  }
}

async function accept() {
  if (!inv.value) return
  busy.value = true
  try {
    const res = await api.acceptInvitation(inv.value.id)
    done.value = 'accepted'
    // Yeni organizasyona geç; proje seçimi kiracıya özeldir, taşınmamalı.
    clearActiveProject()
    try {
      await switchOrganization(res.organization_id)
    } catch {
      // Geçiş başarısız olsa da üyelik oluştu; kullanıcı org menüsünden seçebilir.
    }
    toast.success(t('invite.accepted', { org: res.organization_name }))
    window.location.href = '/'
  } catch (e: any) {
    errorCode.value = e?.data?.error?.code || 'unknown'
    errorMsg.value = e?.data?.error?.message || e?.message || ''
  } finally {
    busy.value = false
  }
}

async function decline() {
  if (!inv.value) return
  busy.value = true
  try {
    await api.declineInvitation(inv.value.id)
    done.value = 'declined'
  } catch (e: any) {
    errorCode.value = e?.data?.error?.code || 'unknown'
    errorMsg.value = e?.data?.error?.message || e?.message || ''
  } finally {
    busy.value = false
  }
}

const errorText = computed(() => {
  switch (errorCode.value) {
    case 'invitation_not_found': return t('invite.errNotFound')
    case 'invitation_email_mismatch': return t('invite.errEmailMismatch', { email: user.value?.email || '' })
    case 'invitation_expired': return t('invite.errExpired')
    case 'invitation_closed': return t('invite.errClosed')
    case 'user_session_required': return t('invite.errSession')
    default: return errorMsg.value || t('invite.errGeneric')
  }
})

const roleLabel = computed(() => inv.value?.role === 'admin' ? t('team.roleAdmin') : t('team.roleMember'))

onMounted(load)
</script>

<template>
  <div class="mx-auto flex min-h-[60vh] max-w-md items-center px-4 py-10">
    <div class="w-full rounded-xl border border-line bg-surface p-6 shadow-sm">
      <div class="mb-4 flex items-center gap-2">
        <Icon name="lucide:mail-open" class="size-5 text-accent" />
        <h1 class="text-lg font-semibold text-fg">{{ t('invite.title') }}</h1>
      </div>

      <div v-if="loading" class="py-8 text-center text-fg-muted">
        <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
      </div>

      <div v-else-if="done === 'declined'" class="space-y-4">
        <p class="text-sm text-fg-muted">{{ t('invite.declined') }}</p>
        <NuxtLink to="/" class="inline-flex rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg">
          {{ t('invite.goHome') }}
        </NuxtLink>
      </div>

      <div v-else-if="errorCode" class="space-y-4">
        <div class="flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 p-3 text-sm text-fg">
          <Icon name="lucide:alert-triangle" class="mt-0.5 size-4 shrink-0 text-warn" />
          <span>{{ errorText }}</span>
        </div>
        <NuxtLink to="/" class="inline-flex rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg">
          {{ t('invite.goHome') }}
        </NuxtLink>
      </div>

      <div v-else-if="inv" class="space-y-5">
        <p class="text-sm leading-relaxed text-fg-muted">
          {{ t('invite.body', { who: inv.inviter_name || t('invite.someone'), org: inv.organization_name, role: roleLabel }) }}
        </p>
        <div class="rounded-lg border border-line bg-surface-2 p-3 text-xs text-fg-muted">
          <div class="flex items-center gap-1.5">
            <Icon name="lucide:at-sign" class="size-3.5" />
            <span class="font-mono">{{ inv.email }}</span>
          </div>
          <p class="mt-1.5">{{ t('invite.accessNote') }}</p>
        </div>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg disabled:opacity-50"
            :disabled="busy"
            @click="decline"
          >
            {{ t('invite.decline') }}
          </button>
          <button
            type="button"
            class="inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-on-accent hover:opacity-90 disabled:opacity-50"
            :disabled="busy"
            @click="accept"
          >
            <Icon v-if="busy" name="lucide:loader-2" class="size-4 animate-spin" />
            <Icon v-else name="lucide:check" class="size-4" />
            {{ t('invite.accept') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
