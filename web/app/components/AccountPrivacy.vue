<script setup lang="ts">
// Profil sayfasi: "Verilerim" (kisisel veriyi JSON indir) ve "Tehlikeli bolge" (hesabi sil).
//
// Hesap silme akisi (sunucu: web/server/lib/account-delete.ts, Go: server/api/account.go):
//   1. Pencere acilinca Go'dan onizleme alinir: neler silinecek, hangi organizasyonlardan cikilacak,
//      silmeyi engelleyen bir durum var mi (baska uyeli org'un tek sahibi, platform yoneticisi).
//   2. Parola (parolali hesaplarda) + "anliyorum" onayi ile onay e-postasi istenir.
//   3. E-postadaki 30 dk gecerli baglanti tiklaninca hesap silinir.
import type { AccountDeletionPlan, AccountOrg } from '~/types/api'
import { DELETE_LINK_MINUTES, accountErrorKey, contentCounts, errorCode, sessionIsFresh } from '~/lib/account'

const props = defineProps<{ email: string }>()

const { t } = useI18n()
const toast = useToast()
const api = useApi()
const account = useAccount()
const { logout } = useAuth()
const route = useRoute()
const router = useRouter()

const LINK_MINUTES = DELETE_LINK_MINUTES

// --- Verilerim ---------------------------------------------------------------
const exporting = ref(false)
async function doExport() {
  exporting.value = true
  try {
    await account.exportData()
    toast.success(t('account.data.done'))
  } catch (e: any) {
    toast.error(t(accountErrorKey(errorCode(e) || e?.code)))
  } finally {
    exporting.value = false
  }
}

// --- Hesabi sil ----------------------------------------------------------------
const open = ref(false)
const step = ref<'form' | 'sent'>('form')
const plan = ref<AccountDeletionPlan | null>(null)
const planLoading = ref(false)
const planFailed = ref(false)
const hasPw = ref<boolean | null>(null)
const sessionCreated = ref<string | null>(null)
const forcedStale = ref(false)
const password = ref('')
const understood = ref(false)
const busy = ref(false)
const error = ref('')
const pwInput = ref<HTMLInputElement | null>(null)
const dialogEl = ref<HTMLElement | null>(null)
const titleId = useId()

// Parola alani: hesabin parolasi varsa (ya da bilinmiyorsa) gosterilir.
const needsPassword = computed(() => hasPw.value !== false)
// Yeniden giris gerekir mi: oturum yasi bilinmiyorsa sunucu karar verir (taze say).
const stale = computed(() => forcedStale.value || (sessionCreated.value !== null && !sessionIsFresh(sessionCreated.value)))
const canSubmit = computed(() =>
  !!plan.value?.can_delete && understood.value && !stale.value && !busy.value
  && (!needsPassword.value || password.value.length > 0))

function orgLabel(o: AccountOrg): string {
  return o.name && o.name !== o.slug ? `${o.name} (${o.slug})` : o.slug
}
function counts(o: AccountOrg): string {
  const c = contentCounts(o)
  return c ? t('account.delete.counts', c) : ''
}

async function loadPlan() {
  planLoading.value = true
  planFailed.value = false
  try {
    plan.value = await api.getAccountDeletionPreview()
  } catch {
    plan.value = null
    planFailed.value = true
  } finally {
    planLoading.value = false
  }
}

async function openDialog() {
  step.value = 'form'
  password.value = ''
  understood.value = false
  error.value = ''
  forcedStale.value = false
  plan.value = null
  open.value = true
  loadPlan()
  hasPw.value = await account.hasPassword()
  sessionCreated.value = await account.sessionCreatedAt()
  await nextTick()
  ;(pwInput.value ?? dialogEl.value)?.focus()
}

function close() {
  if (busy.value) return
  open.value = false
}

async function submit() {
  if (!canSubmit.value) return
  busy.value = true
  error.value = ''
  try {
    await account.requestDeletion(needsPassword.value ? password.value : '')
    password.value = ''
    step.value = 'sent'
  } catch (e: any) {
    const code = errorCode(e) || e?.code || ''
    error.value = t(accountErrorKey(code))
    if (code === 'ACCOUNT_DELETE_SESSION_NOT_FRESH') forcedStale.value = true
    // Engel arada olustuysa (baska uye eklendi...) plani tazele.
    if (code === 'ACCOUNT_DELETE_OWNER_BLOCKED' || code === 'ACCOUNT_DELETE_PLATFORM_BLOCKED') loadPlan()
  } finally {
    busy.value = false
  }
}

async function signOutAndRetry() {
  open.value = false
  await logout()
}

// Esc ile kapat (pencere acikken).
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) close()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// E-posta baglantisi basarisiz olursa sunucu buraya ?accountDeleteError=<KOD> ile doner.
const urlError = ref('')
onMounted(() => {
  const q = route.query.accountDeleteError
  const code = Array.isArray(q) ? q[0] : q
  if (!code) return
  urlError.value = t(accountErrorKey(code))
  const { accountDeleteError: _drop, ...rest } = route.query
  router.replace({ query: rest })
})
</script>

<template>
  <!-- Verilerim -->
  <section class="rounded-xl border border-line bg-surface/60 p-5">
    <h3 class="text-sm font-semibold text-fg">{{ t('account.data.title') }}</h3>
    <p class="mt-0.5 text-xs text-fg-muted">{{ t('account.data.hint') }}</p>
    <div class="mt-3 flex flex-wrap items-center gap-3">
      <button
        :disabled="exporting"
        class="inline-flex cursor-pointer items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        @click="doExport"
      >
        <Icon :name="exporting ? 'lucide:loader-circle' : 'lucide:download'" class="size-4" :class="exporting ? 'animate-spin' : ''" aria-hidden="true" />
        {{ exporting ? t('account.data.preparing') : t('account.data.button') }}
      </button>
      <span class="text-[11px] text-fg-subtle">{{ t('account.data.limit') }}</span>
    </div>
  </section>

  <!-- Tehlikeli bolge -->
  <section class="rounded-xl border border-danger/40 bg-danger/5 p-5">
    <h3 class="flex items-center gap-2 text-sm font-semibold text-danger">
      <Icon name="lucide:triangle-alert" class="size-4" aria-hidden="true" />
      {{ t('account.delete.title') }}
    </h3>
    <p class="mt-2 text-xs leading-relaxed text-fg-muted">{{ t('account.delete.explain') }}</p>
    <div v-if="urlError" role="alert" class="mt-3 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
      <Icon name="lucide:alert-circle" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>{{ urlError }}</span>
    </div>
    <button
      class="mt-3 inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-danger px-3 py-2 text-sm font-medium text-white hover:opacity-90"
      @click="openDialog"
    >
      <Icon name="lucide:trash-2" class="size-4" aria-hidden="true" />
      {{ t('account.delete.button') }}
    </button>
  </section>

  <!-- Onay penceresi -->
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/75 p-4 backdrop-blur-sm sm:items-center"
      @click.self="close"
    >
      <div
        ref="dialogEl"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        tabindex="-1"
        class="relative my-8 w-full max-w-lg rounded-2xl border border-danger/40 bg-surface p-6 shadow-2xl outline-none"
      >
        <div class="flex items-start justify-between gap-3">
          <h2 :id="titleId" class="flex items-center gap-2 text-base font-semibold text-fg">
            <Icon name="lucide:triangle-alert" class="size-5 text-danger" aria-hidden="true" />
            {{ t('account.delete.dialogTitle') }}
          </h2>
          <button class="cursor-pointer rounded-md p-1 text-fg-muted hover:text-fg" :aria-label="t('common.close')" :disabled="busy" @click="close">
            <Icon name="lucide:x" class="size-4" aria-hidden="true" />
          </button>
        </div>

        <!-- Gonderildi -->
        <div v-if="step === 'sent'" class="mt-5 text-center">
          <div class="mx-auto grid size-12 place-items-center rounded-full bg-accent/15 text-accent">
            <Icon name="lucide:mail-check" class="size-6" aria-hidden="true" />
          </div>
          <h3 class="mt-3 text-sm font-semibold text-fg">{{ t('account.delete.sentTitle') }}</h3>
          <p class="mt-2 text-sm text-fg-muted">{{ t('account.delete.sentBody', { email: props.email, minutes: LINK_MINUTES }) }}</p>
          <button class="mt-5 cursor-pointer rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-bg" @click="close">
            {{ t('account.delete.close') }}
          </button>
        </div>

        <!-- Form -->
        <template v-else>
          <p v-if="planLoading || (!plan && !planFailed)" class="mt-5 flex items-center gap-2 text-sm text-fg-muted">
            <Icon name="lucide:loader-circle" class="size-4 animate-spin" aria-hidden="true" />
            {{ t('account.delete.loadingPlan') }}
          </p>
          <p v-else-if="planFailed" class="mt-5 text-sm text-danger">{{ t('account.delete.planFailed') }}</p>

          <!-- Engel -->
          <div v-else-if="plan && !plan.can_delete" role="alert" class="mt-5 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
            <h3 class="font-semibold">{{ t('account.delete.blockedTitle') }}</h3>
            <ul class="mt-1.5 list-disc space-y-1 pl-4">
              <li v-for="o in plan.blocking_orgs" :key="o.id">
                {{ o.reason === 'platform' ? t('account.delete.blockedPlatform') : t('account.delete.blockedOwner', { org: orgLabel(o) }) }}
              </li>
            </ul>
          </div>

          <!-- Neler silinecek -->
          <div v-else-if="plan" class="mt-5 space-y-4 text-sm">
            <div>
              <h3 class="text-xs font-semibold uppercase tracking-wide text-fg-muted">{{ t('account.delete.willDelete') }}</h3>
              <ul class="mt-2 list-disc space-y-2 pl-5 text-fg-muted">
                <li>{{ t('account.delete.willDeleteAccount') }}</li>
                <li v-if="plan.delete_orgs.length">
                  {{ t('account.delete.willDeleteOrgs') }}
                  <ul class="mt-1 space-y-1">
                    <li v-for="o in plan.delete_orgs" :key="o.id" class="text-fg">
                      <span class="font-medium">{{ orgLabel(o) }}</span>
                      <span v-if="counts(o)" class="block text-xs text-fg-muted">{{ counts(o) }}</span>
                    </li>
                  </ul>
                </li>
                <li v-if="plan.leave_orgs.length">
                  {{ t('account.delete.willLeaveOrgs') }}
                  <ul class="mt-1 space-y-1">
                    <li v-for="o in plan.leave_orgs" :key="o.id" class="font-medium text-fg">{{ orgLabel(o) }}</li>
                  </ul>
                </li>
              </ul>
              <p class="mt-3 text-xs leading-relaxed text-fg-subtle">{{ t('account.delete.kept') }}</p>
            </div>

            <p class="text-xs leading-relaxed text-fg-muted">{{ t('account.delete.emailNote', { email: props.email, minutes: LINK_MINUTES }) }}</p>

            <!-- Yeniden giris gerekiyor -->
            <div v-if="stale" role="alert" class="rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-warn">
              <p>{{ t('account.delete.reauthNeeded') }}</p>
              <button class="mt-2 inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-warn/40 px-3 py-1.5 font-medium hover:bg-warn/10" @click="signOutAndRetry">
                <Icon name="lucide:log-out" class="size-3.5" aria-hidden="true" />
                {{ t('account.delete.reauthButton') }}
              </button>
            </div>

            <form v-else class="space-y-3" @submit.prevent="submit">
              <div v-if="needsPassword">
                <label :for="`${titleId}-pw`" class="mb-1 block text-xs font-medium text-fg-muted">{{ t('account.delete.passwordLabel') }}</label>
                <input
                  :id="`${titleId}-pw`"
                  ref="pwInput"
                  v-model="password"
                  type="password"
                  autocomplete="current-password"
                  :placeholder="t('account.delete.passwordHint')"
                  class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-danger focus:outline-none"
                >
              </div>
              <p v-else class="text-xs text-fg-muted">{{ t('account.delete.noPassword') }}</p>

              <label class="flex cursor-pointer items-start gap-2 text-xs text-fg">
                <input v-model="understood" type="checkbox" class="mt-0.5 size-4 accent-[var(--color-danger,#e5484d)]">
                <span>{{ t('account.delete.understand') }}</span>
              </label>

              <p v-if="error" role="alert" class="flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/10 p-3 text-xs text-danger">
                <Icon name="lucide:alert-circle" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>{{ error }}</span>
              </p>

              <div class="flex flex-wrap justify-end gap-2 pt-1">
                <button type="button" class="cursor-pointer rounded-md border border-line px-4 py-2 text-sm text-fg-muted hover:bg-bg" :disabled="busy" @click="close">
                  {{ t('common.cancel') }}
                </button>
                <button
                  type="submit"
                  :disabled="!canSubmit"
                  class="cursor-pointer rounded-md bg-danger px-4 py-2 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {{ busy ? t('account.delete.sending') : t('account.delete.send') }}
                </button>
              </div>
            </form>
          </div>

          <div v-if="plan && !plan.can_delete" class="mt-4 flex justify-end">
            <button class="cursor-pointer rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-bg" @click="close">
              {{ t('account.delete.close') }}
            </button>
          </div>
          <div v-else-if="planFailed" class="mt-4 flex justify-end gap-2">
            <button class="cursor-pointer rounded-md border border-line px-4 py-2 text-sm text-fg-muted hover:bg-bg" @click="loadPlan">
              {{ t('account.sessions.retry') }}
            </button>
            <button class="cursor-pointer rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-bg" @click="close">
              {{ t('account.delete.close') }}
            </button>
          </div>
        </template>
      </div>
    </div>
  </Teleport>
</template>
