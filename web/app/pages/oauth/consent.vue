<script setup lang="ts">
// OAuth onay sayfasi. Go AS'in /oauth/authorize ucu dogrulanmis istegi buraya
// yonlendirir (parametreler URL'de). Oturum yoksa yerlesimdeki AdminKeyGate
// giris/kayit gosterir. Kararlar /oauth/consent/info ve /decision uclariyla
// (panel oturum cerezi) verilir; sunucu her seyi yeniden dogrular.
definePageMeta({ layout: 'bare' })

interface ScopeInfo { id: string, write: boolean, destructive?: boolean }
interface Org { id: string, name: string, role: string }
interface ConsentInfo {
  client_id: string
  client_name: string
  redirect_host: string
  verified: boolean
  scopes: ScopeInfo[]
  resource: string
  orgs: Org[]
  user_email: string
  // 'tunnel': kullanicinin kendi MCP sunucusu (tunel); 'management': Zorven yonetim MCP'si.
  resource_kind?: 'tunnel' | 'management'
  resource_host?: string
  allowed?: boolean
  // Tam erisim (full): ayri bolum, varsayilan KAPALI; step-up icin oturum tazeligi sunucuca bildirilir.
  full_available?: boolean
  full_requested?: boolean
  fresh_auth?: boolean
}

const { t } = useI18n()
const route = useRoute()
const { tenant, logout } = useAuth()

const PARAMS = ['client_id', 'redirect_uri', 'response_type', 'code_challenge', 'code_challenge_method', 'state', 'scope', 'resource']
const params = computed(() => {
  const out: Record<string, string> = {}
  for (const k of PARAMS) {
    const v = route.query[k]
    if (typeof v === 'string') out[k] = v
  }
  return out
})

const loading = ref(true)
const busy = ref(false)
const redirecting = ref(false)
const info = ref<ConsentInfo | null>(null)
const errorText = ref('')
const tenantId = ref('')
const readOnly = ref(true)
// Yikici (silme) kapsamlar VARSAYILAN KAPALI; kullanici tek tek acar.
const destructiveOn = reactive<Record<string, boolean>>({})
// Tam erisim de VARSAYILAN KAPALI; istemci istese bile kullanici acikca isaretler.
const fullOn = ref(false)
const reauthing = ref(false)

function errCode(e: any): string {
  return e?.data?.error || ''
}
function errDesc(e: any): string {
  return e?.data?.error_description || e?.message || ''
}

async function load() {
  loading.value = true
  errorText.value = ''
  try {
    const res = await $fetch<ConsentInfo>('/oauth/consent/info', { query: params.value })
    info.value = res
    const prefer = res.orgs.find(o => o.id === tenant.value?.id) || res.orgs[0]
    tenantId.value = prefer?.id || ''
    if (res.resource_kind === 'tunnel') tenantId.value = 'tunnel'
  } catch (e: any) {
    if (errCode(e) === 'login_required') errorText.value = t('oauth.consent.errLogin')
    else if (e?.statusCode === 400) errorText.value = t('oauth.consent.errInvalid', { msg: errDesc(e) })
    else errorText.value = t('oauth.consent.errGeneric')
  } finally {
    loading.value = false
  }
}

const isTunnel = computed(() => info.value?.resource_kind === 'tunnel')
const allRequested = computed(() => info.value?.scopes ?? [])
const destructive = computed(() => allRequested.value.filter(s => s.destructive))
const requested = computed(() => allRequested.value.filter(s => !s.destructive))
const hasWrite = computed(() => requested.value.some(s => s.write))
const readIds = computed(() => requested.value.filter(s => !s.write).map(s => s.id))
// Salt okuma secenegi yalniz anlamliysa sunulur: okunur bir karsiligi var.
const canReadOnly = computed(() => hasWrite.value && (readIds.value.length > 0 || requested.value.some(s => s.id === 'mcp:manage')))

const showFull = computed(() => !isTunnel.value && !!info.value?.full_available)
// Tam erisim acikken diger kapsamlar zaten icinde; yine de acikca secilenler gonderilir.
const needReauth = computed(() => fullOn.value && info.value?.fresh_auth === false)

const granted = computed<string[]>(() => {
  const ids = requested.value.map(s => s.id)
  const extra = destructive.value.filter(s => destructiveOn[s.id]).map(s => s.id)
  const full = showFull.value && fullOn.value ? ['full'] : []
  if (!canReadOnly.value || !readOnly.value) return [...ids, ...extra, ...full]
  return [...(readIds.value.length > 0 ? readIds.value : ['mcp:read']), ...extra, ...full]
})

// Step-up: oturumu kapat; yerlesimdeki giris kapisi ayni sayfada (ayni URL, ayni
// parametreler) giris formunu gosterir, giristen sonra onay sayfasi yeniden yuklenir.
// Disari yonlendirme yok.
async function reauth() {
  reauthing.value = true
  try {
    await logout()
  } finally {
    reauthing.value = false
  }
}

function scopeLabel(id: string): string {
  return t(`oauth.scope.${id.replace(':', '_')}`)
}

async function decide(decision: 'allow' | 'deny') {
  if (!info.value || busy.value) return
  busy.value = true
  errorText.value = ''
  try {
    const res = await $fetch<{ redirect: string }>('/oauth/consent/decision', {
      method: 'POST',
      body: { ...params.value, decision, tenant_id: tenantId.value, scopes: decision === 'allow' ? granted.value : undefined },
    })
    redirecting.value = true
    window.location.href = res.redirect
  } catch (e: any) {
    if (errCode(e) === 'reauth_required' && info.value) {
      info.value.fresh_auth = false
      errorText.value = t('oauth.consent.errReauth')
      busy.value = false
      return
    }
    errorText.value = e?.statusCode === 400 || e?.statusCode === 403
      ? t('oauth.consent.errInvalid', { msg: errDesc(e) })
      : t('oauth.consent.errGeneric')
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto flex min-h-[60vh] max-w-lg items-center px-4 py-10">
    <div class="w-full rounded-xl border border-line bg-surface p-6 shadow-sm">
      <div class="mb-4 flex items-center gap-2">
        <Icon name="lucide:shield-check" class="size-5 text-accent" />
        <h1 class="text-lg font-semibold text-fg">{{ t('oauth.consent.title') }}</h1>
      </div>

      <div v-if="loading" class="py-8 text-center text-fg-muted">
        <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
        <p class="mt-2 text-xs">{{ t('oauth.consent.loading') }}</p>
      </div>

      <div v-else-if="redirecting" class="py-8 text-center text-fg-muted">
        <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
        <p class="mt-2 text-xs">{{ t('oauth.consent.redirecting') }}</p>
      </div>

      <div v-else-if="!info" class="flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 p-3 text-sm text-fg">
        <Icon name="lucide:alert-triangle" class="mt-0.5 size-4 shrink-0 text-warn" />
        <span>{{ errorText }}</span>
      </div>

      <div v-else class="space-y-5">
        <p class="text-sm leading-relaxed text-fg-muted">
          {{ t('oauth.consent.intro', { name: info.client_name }) }}
        </p>

        <div v-if="!info.verified" class="flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-fg">
          <Icon name="lucide:alert-triangle" class="mt-0.5 size-4 shrink-0 text-warn" />
          <span>{{ t('oauth.consent.unverified') }}</span>
        </div>

        <div class="rounded-lg border border-line bg-surface-2 p-3 text-xs text-fg-muted">
          <div class="flex items-center gap-1.5">
            <Icon name="lucide:at-sign" class="size-3.5" />
            <span>{{ t('oauth.consent.signedInAs', { email: info.user_email || '-' }) }}</span>
          </div>
          <div class="mt-1.5 flex items-center gap-1.5">
            <Icon name="lucide:globe" class="size-3.5" />
            <span>{{ t('oauth.consent.redirectTo', { host: info.redirect_host }) }}</span>
          </div>
        </div>

        <div v-if="isTunnel" class="rounded-lg border border-line p-3 text-sm text-fg">
          <div class="flex items-center gap-1.5 font-medium">
            <Icon name="lucide:server" class="size-4 text-accent" />
            <span>{{ t('oauth.consent.tunnelTarget', { host: info.resource_host || '' }) }}</span>
          </div>
          <p class="mt-1 text-xs text-fg-muted">{{ t('oauth.consent.tunnelHint') }}</p>
          <p v-if="info.allowed === false" class="mt-2 flex items-start gap-1.5 text-xs text-danger">
            <Icon name="lucide:shield-x" class="mt-0.5 size-3.5 shrink-0" />
            <span>{{ t('oauth.consent.tunnelDenied', { email: info.user_email || '-' }) }}</span>
          </p>
        </div>

        <div v-else-if="info.orgs.length === 0" class="rounded-lg border border-line p-3 text-sm text-fg-muted">
          {{ t('oauth.consent.noOrgs') }}
        </div>

        <template v-if="isTunnel || info.orgs.length > 0">
          <div v-if="!isTunnel">
            <label class="mb-1 block text-xs font-medium text-fg" for="oauth-org">{{ t('oauth.consent.org') }}</label>
            <select
              id="oauth-org"
              v-model="tenantId"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            >
              <option v-for="o in info.orgs" :key="o.id" :value="o.id">{{ o.name }} ({{ o.role }})</option>
            </select>
            <p class="mt-1 text-xs text-fg-subtle">{{ t('oauth.consent.orgHint') }}</p>
          </div>

          <div>
            <div class="mb-1 text-xs font-medium text-fg">{{ t('oauth.consent.permissions') }}</div>
            <ul class="space-y-1.5">
              <li v-for="id in granted" :key="id" class="flex items-start gap-2 text-sm text-fg">
                <Icon name="lucide:check" class="mt-0.5 size-4 shrink-0 text-accent" />
                <span>{{ scopeLabel(id) }}</span>
              </li>
            </ul>
            <p v-if="destructive.length === 0" class="mt-2 flex items-center gap-1.5 text-xs text-fg-subtle">
              <Icon name="lucide:shield-off" class="size-3.5" />
              {{ t('oauth.consent.noDelete') }}
            </p>
          </div>

          <div v-if="destructive.length > 0" class="space-y-2 rounded-lg border border-danger/30 bg-danger/5 p-3">
            <div class="flex items-center gap-1.5 text-xs font-medium text-danger">
              <Icon name="lucide:trash-2" class="size-3.5" />
              {{ t('oauth.consent.destructiveTitle') }}
            </div>
            <p class="text-xs text-fg-muted">{{ t('oauth.consent.destructiveHint') }}</p>
            <label v-for="s in destructive" :key="s.id" class="flex cursor-pointer items-start gap-2">
              <input v-model="destructiveOn[s.id]" type="checkbox" class="mt-0.5 size-4 accent-[var(--color-danger)]" />
              <span class="text-sm text-fg">{{ scopeLabel(s.id) }}</span>
            </label>
          </div>

          <div v-if="showFull" class="space-y-2 rounded-lg border border-danger/40 bg-danger/5 p-3">
            <div class="flex items-center gap-1.5 text-xs font-medium text-danger">
              <Icon name="lucide:shield-alert" class="size-3.5" />
              {{ t('oauth.consent.fullTitle') }}
            </div>
            <p class="text-xs font-medium text-fg">{{ t('oauth.consent.fullWarn') }}</p>
            <p v-if="info.full_requested" class="flex items-start gap-1.5 text-xs text-fg-muted">
              <Icon name="lucide:info" class="mt-0.5 size-3.5 shrink-0" />
              <span>{{ t('oauth.consent.fullRequested') }}</span>
            </p>
            <p class="text-xs text-fg-muted">{{ t('oauth.consent.fullLimits') }}</p>
            <label class="flex cursor-pointer items-start gap-2">
              <input v-model="fullOn" type="checkbox" class="mt-0.5 size-4 accent-[var(--color-danger)]" />
              <span class="text-sm font-medium text-fg">{{ t('oauth.consent.fullTick') }}</span>
            </label>
            <div v-if="needReauth" class="space-y-2 rounded-lg border border-warn/30 bg-warn/10 p-3">
              <div class="flex items-center gap-1.5 text-xs font-medium text-fg">
                <Icon name="lucide:key-round" class="size-3.5 text-warn" />
                {{ t('oauth.consent.fullReauthTitle') }}
              </div>
              <p class="text-xs text-fg-muted">{{ t('oauth.consent.fullReauthBody') }}</p>
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1.5 rounded-lg border border-line px-3 py-1.5 text-xs font-medium text-fg hover:border-accent disabled:opacity-50"
                :disabled="reauthing"
                @click="reauth"
              >
                <Icon v-if="reauthing" name="lucide:loader-2" class="size-3.5 animate-spin" />
                <Icon v-else name="lucide:log-in" class="size-3.5" />
                {{ t('oauth.consent.fullReauthButton') }}
              </button>
            </div>
            <p v-else-if="fullOn" class="flex items-center gap-1.5 text-xs text-fg-muted">
              <Icon name="lucide:check" class="size-3.5 text-accent" />
              {{ t('oauth.consent.fullFresh') }}
            </p>
          </div>

          <label v-if="canReadOnly" class="flex cursor-pointer items-start gap-2 rounded-lg border border-line p-3">
            <input v-model="readOnly" type="checkbox" class="mt-0.5 size-4 accent-[var(--color-accent)]" />
            <span>
              <span class="block text-sm font-medium text-fg">{{ t('oauth.consent.readOnly') }}</span>
              <span class="block text-xs text-fg-muted">{{ t('oauth.consent.readOnlyHint') }}</span>
            </span>
          </label>
        </template>

        <div v-if="errorText" class="flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-fg">
          <Icon name="lucide:alert-triangle" class="mt-0.5 size-4 shrink-0 text-warn" />
          <span>{{ errorText }}</span>
        </div>

        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg disabled:opacity-50"
            :disabled="busy"
            @click="decide('deny')"
          >
            {{ t('oauth.consent.deny') }}
          </button>
          <button
            type="button"
            class="inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-on-accent hover:opacity-90 disabled:opacity-50"
            :disabled="busy || !tenantId || granted.length === 0 || needReauth || (isTunnel && info.allowed === false)"
            @click="decide('allow')"
          >
            <Icon v-if="busy" name="lucide:loader-2" class="size-4 animate-spin" />
            <Icon v-else name="lucide:check" class="size-4" />
            {{ t('oauth.consent.allow') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
