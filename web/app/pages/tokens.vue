<script setup lang="ts">
import type { APIToken, CreateAPITokenResponse, IPAllowlistRule, Tunnel } from '~/types/api'

const api = useApi()
const toast = useToast()
const route = useRoute()
const router = useRouter()
const { relativeTime } = useFormat()
const { subscription, currentPlan, openUpgrade, loadBillingData, loading: billingLoading } = useBilling()
const { platformAdmin } = useAuth()
const { t } = useI18n()

// Gating: Pro, Team, Enterprise plan check. Platform Admin her zaman erişir.
const hasSecurityAccess = computed(() => {
  if (platformAdmin.value) return true
  const p = subscription.value?.plan
  return p === 'pro' || p === 'team' || p === 'enterprise'
})

// Tab state: 'tokens' or 'ip'
const activeTab = ref<'tokens' | 'ip'>((route.query.tab as 'tokens' | 'ip') || 'tokens')

// Watch query param
watch(() => route.query.tab, (tab) => {
  if (tab === 'tokens' || tab === 'ip') {
    activeTab.value = tab
  }
})

function switchTab(tab: 'tokens' | 'ip') {
  activeTab.value = tab
  router.replace({ query: { ...route.query, tab } })
}

// Global data
const loading = ref(true)
const tunnels = ref<Tunnel[]>([])

// --- API Tokens State ---
const tokens = ref<APIToken[]>([])
const showCreateTokenModal = ref(false)
const createTokenName = ref('')
const createTokenExpiry = ref(30) // days
const createTokenAllScopes = ref(true)
const selectedScopes = ref<string[]>([
  'tunnels:read', 'tunnels:write',
  'hostnames:read', 'hostnames:write',
  'clients:read', 'clients:write'
])
const creatingToken = ref(false)

// Token Secret Display Modal
const issuedSecret = ref<{ name: string; token: string; prefix: string } | null>(null)
const copiedSecret = ref(false)
const revokingTokenId = ref<string | null>(null)

// Available scope options
const availableScopes = [
  { id: 'tunnels:read', label: t('tokens.scopeTunnelsRead'), desc: t('tokens.scopeTunnelsReadDesc') },
  { id: 'tunnels:write', label: t('tokens.scopeTunnelsWrite'), desc: t('tokens.scopeTunnelsWriteDesc') },
  { id: 'hostnames:read', label: t('tokens.scopeHostnamesRead'), desc: t('tokens.scopeHostnamesReadDesc') },
  { id: 'hostnames:write', label: t('tokens.scopeHostnamesWrite'), desc: t('tokens.scopeHostnamesWriteDesc') },
  { id: 'clients:read', label: t('tokens.scopeClientsRead'), desc: t('tokens.scopeClientsReadDesc') },
  { id: 'clients:write', label: t('tokens.scopeClientsWrite'), desc: t('tokens.scopeClientsWriteDesc') },
]

// --- IP Allowlist State ---
const ipRules = ref<IPAllowlistRule[]>([])
const selectedTunnelFilter = ref<string>((route.query.tunnel as string) || '')
const showCreateRuleModal = ref(false)
const ruleCidr = ref('')
const ruleTunnelId = ref<string>((route.query.tunnel as string) || '')
const ruleDescription = ref('')
const creatingRule = ref(false)
const updatingRuleId = ref<string | null>(null)
const deletingRuleId = ref<string | null>(null)

// Client detected public IP
const detectedIp = ref<string>('')
const detectingIp = ref(false)

onMounted(async () => {
  await loadBillingData()
  if (!hasSecurityAccess.value) {
    openUpgrade(t('tokens.upgradeMsg'), 'api_access')
    router.replace('/')
    return
  }

  await fetchData()
  detectClientIP()
})

async function fetchData() {
  loading.value = true
  try {
    const [tokensRes, rulesRes, tunnelsRes] = await Promise.all([
      api.listAPITokens().catch(() => ({ tokens: [], count: 0 })),
      api.listIPRules().catch(() => ({ rules: [], count: 0 })),
      api.listTunnels().catch(() => []),
    ])
    tokens.value = tokensRes.tokens || []
    ipRules.value = rulesRes.rules || []
    tunnels.value = tunnelsRes || []
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.dataLoadFailed'))
  } finally {
    loading.value = false
  }
}

async function detectClientIP() {
  detectingIp.value = true
  try {
    const res = await fetch('https://api64.ipify.org?format=json', { signal: AbortSignal.timeout(4000) })
    if (res.ok) {
      const data = await res.json()
      if (data?.ip) {
        detectedIp.value = data.ip
      }
    }
  } catch {
    // Ignore fallback
  } finally {
    detectingIp.value = false
  }
}

function fillDetectedIP() {
  if (detectedIp.value) {
    ruleCidr.value = detectedIp.value.includes('/') ? detectedIp.value : `${detectedIp.value}/32`
  }
}

// --- API Token Handlers ---
async function handleCreateToken() {
  if (!createTokenName.value.trim()) {
    toast.warn(t('tokens.warnNameFirst'))
    return
  }

  creatingToken.value = true
  try {
    const scopes = createTokenAllScopes.value ? ['*'] : selectedScopes.value
    const res = await api.createAPIToken(
      createTokenName.value.trim(),
      scopes,
      createTokenExpiry.value
    )
    tokens.value.unshift(res.api_token)
    issuedSecret.value = {
      name: res.api_token.name,
      token: res.token,
      prefix: res.api_token.token_prefix,
    }
    showCreateTokenModal.value = false
    createTokenName.value = ''
    createTokenExpiry.value = 30
    createTokenAllScopes.value = true
    toast.success(t('tokens.tokenCreated'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.tokenCreateFailed'))
  } finally {
    creatingToken.value = false
  }
}

async function handleRevokeToken(tokenItem: APIToken) {
  if (!confirm(t('tokens.revokeConfirm', { name: tokenItem.name }))) {
    return
  }

  revokingTokenId.value = tokenItem.id
  try {
    await api.revokeAPIToken(tokenItem.id)
    tokens.value = tokens.value.filter(t => t.id !== tokenItem.id)
    toast.success(t('tokens.tokenRevoked'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.tokenRevokeFailed'))
  } finally {
    revokingTokenId.value = null
  }
}

function copyIssuedToken() {
  if (!issuedSecret.value) return
  navigator.clipboard.writeText(issuedSecret.value.token)
  copiedSecret.value = true
  toast.success(t('tokens.tokenCopied'))
  setTimeout(() => { copiedSecret.value = false }, 2500)
}

// --- IP Allowlist Handlers ---
const filteredIPRules = computed(() => {
  if (!selectedTunnelFilter.value) {
    return ipRules.value
  }
  return ipRules.value.filter(r => r.tunnel_id === selectedTunnelFilter.value)
})

async function handleCreateRule() {
  if (!ruleCidr.value.trim()) {
    toast.warn(t('tokens.warnCidrFirst'))
    return
  }

  creatingRule.value = true
  try {
    const newRule = await api.createIPRule(
      ruleCidr.value.trim(),
      ruleDescription.value.trim(),
      ruleTunnelId.value || undefined
    )
    ipRules.value.unshift(newRule)
    toast.success(t('tokens.ruleCreated'))
    showCreateRuleModal.value = false
    ruleCidr.value = ''
    ruleDescription.value = ''
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.ruleCreateFailed'))
  } finally {
    creatingRule.value = false
  }
}

async function handleToggleRule(rule: IPAllowlistRule) {
  updatingRuleId.value = rule.id
  try {
    const updated = await api.updateIPRule(rule.id, { enabled: !rule.enabled })
    rule.enabled = updated.enabled
    toast.success(t('tokens.ruleToggled', { state: rule.enabled ? t('tokens.ruleEnabled') : t('tokens.ruleDisabled') }))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.ruleUpdateFailed'))
  } finally {
    updatingRuleId.value = null
  }
}

async function handleDeleteRule(rule: IPAllowlistRule) {
  if (!confirm(t('tokens.deleteRuleConfirm', { cidr: rule.cidr }))) {
    return
  }

  deletingRuleId.value = rule.id
  try {
    await api.deleteIPRule(rule.id)
    ipRules.value = ipRules.value.filter(r => r.id !== rule.id)
    toast.success(t('tokens.ruleDeleted'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tokens.ruleDeleteFailed'))
  } finally {
    deletingRuleId.value = null
  }
}

function getTunnelDisplay(tunnelId?: string | null) {
  if (!tunnelId) return t('tokens.allTunnelsGlobal')
  const tn = tunnels.value.find(x => x.id === tunnelId)
  return tn ? `${tn.target} (${tn.id.slice(0, 10)})` : tunnelId
}

function isExpired(expiresAt?: string | null) {
  if (!expiresAt) return false
  return new Date(expiresAt).getTime() < Date.now()
}
</script>

<template>
  <div v-if="hasSecurityAccess" class="space-y-6">
    <!-- Sayfa Başlığı ve Sekmeler -->
    <div class="mb-6 flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
      <div>
        <h1 class="text-xl font-bold text-fg sm:text-2xl flex items-center gap-2.5">
          <Icon name="lucide:shield-check" class="size-6 text-accent" />
          {{ t('tokens.title') }}
        </h1>
        <p class="mt-1 text-xs text-fg-muted sm:text-sm">{{ t('tokens.subtitle') }}</p>
      </div>

      <div class="flex items-center gap-2.5">
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-surface px-3 py-2 text-xs font-semibold text-fg-muted transition-all duration-150 hover:bg-surface-2 hover:text-fg active:scale-95 shadow-sm"
          :title="t('tokens.refreshTitle')"
          :disabled="loading"
          @click="fetchData"
        >
          <Icon name="lucide:refresh-cw" class="size-3.5" :class="{ 'animate-spin': loading }" />
          <span class="hidden sm:inline">{{ t('common.refresh') }}</span>
        </button>

        <button
          v-if="activeTab === 'tokens'"
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-semibold text-on-accent transition-all duration-150 hover:opacity-90 active:scale-95 shadow-sm"
          @click="showCreateTokenModal = true"
        >
          <Icon name="lucide:key" class="size-4" />
          <span>{{ t('tokens.newToken') }}</span>
        </button>

        <button
          v-else
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-semibold text-on-accent transition-all duration-150 hover:opacity-90 active:scale-95 shadow-sm"
          @click="showCreateRuleModal = true"
        >
          <Icon name="lucide:plus" class="size-4" />
          <span>{{ t('tokens.newRule') }}</span>
        </button>
      </div>
    </div>

    <!-- Sekme Butonları -->
    <div class="flex border-b border-line gap-4">
      <button
        type="button"
        class="flex cursor-pointer items-center gap-2 pb-3 text-sm font-medium transition-colors border-b-2"
        :class="activeTab === 'tokens' ? 'border-accent text-fg font-semibold' : 'border-transparent text-fg-muted hover:text-fg'"
        @click="switchTab('tokens')"
      >
        <Icon name="lucide:key-round" class="size-4" :class="activeTab === 'tokens' ? 'text-accent' : ''" />
        <span>{{ t('tokens.tabTokens') }}</span>
        <span class="rounded-full bg-surface px-2 py-0.5 text-xs font-mono text-fg-muted border border-line">
          {{ tokens.length }}
        </span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-2 pb-3 text-sm font-medium transition-colors border-b-2"
        :class="activeTab === 'ip' ? 'border-accent text-fg font-semibold' : 'border-transparent text-fg-muted hover:text-fg'"
        @click="switchTab('ip')"
      >
        <Icon name="lucide:network" class="size-4" :class="activeTab === 'ip' ? 'text-accent' : ''" />
        <span>{{ t('tokens.tabIp') }}</span>
        <span class="rounded-full bg-surface px-2 py-0.5 text-xs font-mono text-fg-muted border border-line">
          {{ ipRules.length }}
        </span>
      </button>
    </div>

    <!-- TAB 1: REST API ANAHTARLARI -->
    <div v-if="activeTab === 'tokens'" class="space-y-6">
      <!-- Özet Kartları -->
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div class="card-interactive rounded-xl border border-line bg-surface p-4">
          <div class="label-sys mb-1 flex items-center gap-1.5">
            <Icon name="lucide:key" class="size-3.5 text-accent" />
            <span>{{ t('tokens.totalTokens') }}</span>
          </div>
          <div class="font-mono text-2xl font-bold text-fg">{{ tokens.length }}</div>
          <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tokens.totalTokensHint') }}</p>
        </div>

        <div class="card-interactive rounded-xl border border-line bg-surface p-4">
          <div class="label-sys mb-1 flex items-center gap-1.5">
            <Icon name="lucide:lock" class="size-3.5 text-accent" />
            <span>{{ t('tokens.cryptoSec') }}</span>
          </div>
          <div class="font-mono text-base font-semibold text-fg flex items-center gap-1.5 mt-1">
            <span class="text-emerald-700 dark:text-emerald-400 font-bold">{{ t('tokens.cryptoHash') }}</span>
          </div>
          <p class="mt-1 text-[11px] text-fg-muted font-medium">{{ t('tokens.cryptoHint') }}</p>
        </div>

        <div class="card-interactive rounded-xl border border-line bg-surface p-4">
          <div class="label-sys mb-1 flex items-center gap-1.5">
            <Icon name="lucide:badge-check" class="size-3.5 text-accent" />
            <span>{{ t('tokens.planScope') }}</span>
          </div>
          <div class="flex items-center gap-2 mt-1">
            <span class="rounded bg-accent/15 border border-accent/30 px-2 py-0.5 font-mono text-xs font-bold capitalize text-accent">
              {{ currentPlan?.name || subscription?.plan }}
            </span>
            <span class="text-xs text-fg-muted font-mono font-medium">{{ t('tokens.fullAuth') }}</span>
          </div>
          <p class="mt-1 text-[11px] text-fg-muted font-medium">{{ t('tokens.planHint') }}</p>
        </div>
      </div>

      <!-- Token Tablosu -->
      <div class="rounded-xl border border-line bg-surface overflow-hidden shadow-sm">
        <div class="p-4 border-b border-line flex items-center justify-between">
          <h2 class="text-sm font-semibold text-fg flex items-center gap-2">
            <Icon name="lucide:list" class="size-4 text-accent" />
            {{ t('tokens.activeTokens') }}
          </h2>
          <span class="text-xs text-fg-muted font-mono font-medium">{{ t('tokens.tokensRegistered', { n: tokens.length }) }}</span>
        </div>

        <div v-if="loading" class="p-8 text-center text-fg-muted">
          <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
          <span class="mt-2 block text-xs font-mono font-medium">{{ t('tokens.tokensLoading') }}</span>
        </div>

        <div v-else-if="tokens.length === 0" class="p-8 text-center">
          <Icon name="lucide:key" class="mx-auto size-8 text-fg-muted mb-2" />
          <p class="text-sm font-semibold text-fg">{{ t('tokens.noTokens') }}</p>
          <p class="text-xs text-fg-muted mt-1 max-w-md mx-auto">{{ t('tokens.noTokensHint') }}</p>
          <button
            type="button"
            class="mt-4 inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-on-accent"
            @click="showCreateTokenModal = true"
          >
            <Icon name="lucide:plus" class="size-3.5" />
            <span>{{ t('tokens.createFirstToken') }}</span>
          </button>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-xs">
            <thead class="border-b border-line bg-surface-2/90 text-[11px] font-bold text-fg-muted uppercase tracking-wider font-mono">
              <tr>
                <th class="py-3 px-4">{{ t('tokens.colTokenName') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colPrefix') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colScopes') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colLastUsed') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colExpiry') }}</th>
                <th class="py-3 px-4 text-right">{{ t('tokens.colAction') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-line">
              <tr v-for="tok in tokens" :key="tok.id" class="hover:bg-surface-2/60 transition-colors">
                <td class="py-3 px-4">
                  <div class="font-semibold text-fg flex items-center gap-2">
                    <Icon name="lucide:key" class="size-3.5 text-accent shrink-0" />
                    <span>{{ tok.name }}</span>
                    <span v-if="isExpired(tok.expires_at)" class="rounded bg-danger/15 text-danger border border-danger/40 px-1.5 py-0.2 text-[9px] font-mono font-bold">
                      {{ t('tokens.expired') }}
                    </span>
                  </div>
                  <div class="text-[10px] text-fg-muted font-mono mt-0.5 font-medium">{{ t('tokens.createdAt', { time: relativeTime(tok.created_at) }) }}</div>
                </td>
                <td class="py-3 px-4 font-mono text-fg">
                  <span class="rounded bg-surface-2 px-2 py-0.5 border border-line text-[11px] font-medium">
                    zrv_api_{{ tok.token_prefix }}••••••••
                  </span>
                </td>
                <td class="py-3 px-4">
                  <div class="flex flex-wrap gap-1 max-w-xs">
                    <span
                      v-for="s in tok.scopes"
                      :key="s"
                      class="rounded bg-accent/15 border border-accent/30 px-1.5 py-0.2 text-[10px] font-mono font-semibold text-accent"
                    >
                      {{ s === '*' ? t('tokens.fullScope') : s }}
                    </span>
                  </div>
                </td>
                <td class="py-3 px-4 font-mono text-fg">
                  <span v-if="tok.last_used_at" class="text-fg font-medium">
                    {{ relativeTime(tok.last_used_at) }}
                  </span>
                  <span v-else class="text-fg-muted italic">{{ t('tokens.neverUsed') }}</span>
                </td>
                <td class="py-3 px-4 font-mono text-fg">
                  <span v-if="tok.expires_at" :class="isExpired(tok.expires_at) ? 'text-danger font-bold' : 'text-fg font-medium'">
                    {{ new Date(tok.expires_at).toLocaleDateString('tr-TR') }}
                  </span>
                  <span v-else class="text-emerald-700 dark:text-emerald-400 font-bold">{{ t('tokens.noExpiry') }}</span>
                </td>
                <td class="py-3 px-4 text-right">
                  <button
                    type="button"
                    class="cursor-pointer rounded-lg border border-line px-2.5 py-1 text-[11px] font-medium text-fg-muted hover:border-danger/40 hover:text-danger hover:bg-surface-2 transition-colors"
                    :disabled="revokingTokenId === tok.id"
                    :title="t('tokens.revokeTitle')"
                    @click="handleRevokeToken(tok)"
                  >
                    <Icon v-if="revokingTokenId === tok.id" name="lucide:loader-2" class="size-3 animate-spin inline mr-1" />
                    <span>{{ t('tokens.revoke') }}</span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- TAB 2: INGRESS IP FILTRELEME (ALLOWLIST) -->
    <div v-else class="space-y-6">
      <!-- Bilgilendirme Bannerı -->
      <div class="rounded-xl border border-accent/30 bg-accent/5 p-4 flex items-start gap-3 shadow-sm">
        <Icon name="lucide:shield-alert" class="size-5 text-accent shrink-0 mt-0.5" />
        <div class="text-xs text-fg space-y-1">
          <p class="font-semibold text-accent">{{ t('tokens.ipHowTitle') }}</p>
          <p class="text-fg-muted leading-relaxed">{{ t('tokens.ipHowBody') }}</p>
        </div>
      </div>

      <!-- Filtreler ve Mevcut IP Tespit Bloğu -->
      <div class="flex flex-col sm:flex-row gap-3 justify-between items-stretch sm:items-center">
        <!-- Tünel Filtresi -->
        <div class="flex items-center gap-2">
          <label class="text-xs text-fg-muted shrink-0 font-medium">{{ t('tokens.filterByTunnel') }}</label>
          <select
            v-model="selectedTunnelFilter"
            class="rounded-lg border border-line bg-surface px-3 py-1.5 text-xs text-fg focus:border-accent focus:outline-none"
          >
            <option value="">{{ t('tokens.allGlobalTunnel') }}</option>
            <option v-for="t in tunnels" :key="t.id" :value="t.id">
              {{ t.target }} ({{ t.id.slice(0, 8) }})
            </option>
          </select>
        </div>

        <!-- Tespit Edilen IP Yardımcısı -->
        <div class="flex items-center gap-2 self-end sm:self-auto">
          <div v-if="detectedIp" class="flex items-center gap-2 rounded-lg border border-line bg-surface px-2.5 py-1 text-xs">
            <span class="text-fg-subtle">{{ t('tokens.yourIp') }}</span>
            <span class="font-mono font-bold text-accent">{{ detectedIp }}</span>
            <button
              type="button"
              class="cursor-pointer text-accent hover:underline text-[11px] ml-1 font-medium"
              @click="ruleCidr = detectedIp + '/32'; showCreateRuleModal = true"
            >
              {{ t('tokens.addRule') }}
            </button>
          </div>
          <div v-else-if="detectingIp" class="text-xs text-fg-subtle font-mono flex items-center gap-1">
            <Icon name="lucide:loader-2" class="size-3 animate-spin text-accent" />
            <span>{{ t('tokens.detectingIp') }}</span>
          </div>
        </div>
      </div>

      <!-- IP Kuralları Tablosu -->
      <div class="rounded-xl border border-line bg-surface overflow-hidden shadow-sm">
        <div class="p-4 border-b border-line flex items-center justify-between">
          <h2 class="text-sm font-semibold text-fg flex items-center gap-2">
            <Icon name="lucide:network" class="size-4 text-accent" />
            {{ t('tokens.definedRules') }}
          </h2>
          <span class="text-xs text-fg-subtle font-mono">{{ t('tokens.rulesListed', { n: filteredIPRules.length }) }}</span>
        </div>

        <div v-if="loading" class="p-8 text-center text-fg-muted">
          <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
          <span class="mt-2 block text-xs font-mono">{{ t('tokens.rulesLoading') }}</span>
        </div>

        <div v-else-if="filteredIPRules.length === 0" class="p-8 text-center">
          <Icon name="lucide:shield" class="mx-auto size-8 text-fg-subtle mb-2" />
          <p class="text-sm font-medium text-fg">{{ t('tokens.noRules') }}</p>
          <p class="text-xs text-fg-muted mt-1 max-w-md mx-auto">{{ t('tokens.noRulesHint') }}</p>
          <button
            type="button"
            class="mt-4 inline-flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-on-accent"
            @click="showCreateRuleModal = true"
          >
            <Icon name="lucide:plus" class="size-3.5" />
            <span>{{ t('tokens.createFirstRule') }}</span>
          </button>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-xs">
            <thead class="border-b border-line bg-surface-2/90 text-[11px] font-bold text-fg-muted uppercase tracking-wider font-mono">
              <tr>
                <th class="py-3 px-4">{{ t('tokens.colCidr') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colScope') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colDescription') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colStatus') }}</th>
                <th class="py-3 px-4">{{ t('tokens.colAddedAt') }}</th>
                <th class="py-3 px-4 text-right">{{ t('tokens.colAction') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-line">
              <tr v-for="rule in filteredIPRules" :key="rule.id" class="hover:bg-surface-2/60 transition-colors">
                <td class="py-3 px-4">
                  <div class="flex items-center gap-2">
                    <span class="rounded bg-surface-2 border border-line px-2 py-0.5 font-mono text-xs font-bold text-fg">
                      {{ rule.cidr }}
                    </span>
                  </div>
                </td>
                <td class="py-3 px-4">
                  <span
                    class="rounded px-2 py-0.5 text-[11px] font-semibold border"
                    :class="!rule.tunnel_id ? 'border-accent/40 bg-accent/15 text-accent' : 'border-line bg-surface-2 text-fg'"
                  >
                    {{ getTunnelDisplay(rule.tunnel_id) }}
                  </span>
                </td>
                <td class="py-3 px-4 text-fg font-medium">
                  {{ rule.description || '—' }}
                </td>
                <td class="py-3 px-4">
                  <button
                    type="button"
                    class="cursor-pointer inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[10px] font-semibold transition-colors border shadow-xs"
                    :class="rule.enabled
                      ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-500/40 dark:bg-emerald-950/30 dark:text-emerald-300'
                      : 'border-line bg-surface-2 text-fg-muted'"
                    :disabled="updatingRuleId === rule.id"
                    :title="t('tokens.toggleStatusTitle')"
                    @click="handleToggleRule(rule)"
                  >
                    <span class="size-1.5 rounded-full" :class="rule.enabled ? 'bg-emerald-600 dark:bg-emerald-400' : 'bg-fg-muted'" />
                    <span>{{ rule.enabled ? t('tokens.active') : t('tokens.disabled') }}</span>
                  </button>
                </td>
                <td class="py-3 px-4 font-mono text-fg-muted text-[11px] font-medium">
                  {{ relativeTime(rule.created_at) }}
                </td>
                <td class="py-3 px-4 text-right">
                  <button
                    type="button"
                    class="cursor-pointer rounded-lg border border-line px-2.5 py-1 text-[11px] font-medium text-fg-muted hover:border-danger/40 hover:text-danger hover:bg-surface-2 transition-colors"
                    :disabled="deletingRuleId === rule.id"
                    :title="t('tokens.deleteRuleTitle')"
                    @click="handleDeleteRule(rule)"
                  >
                    <Icon v-if="deletingRuleId === rule.id" name="lucide:loader-2" class="size-3 animate-spin inline mr-1" />
                    <span>{{ t('common.delete') }}</span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- MODAL: YENI API ANAHTARI OLUSTUR -->
    <div
      v-if="showCreateTokenModal"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showCreateTokenModal = false"
    >
      <div class="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-2xl space-y-4">
        <div class="flex items-center justify-between pb-3 border-b border-line">
          <div class="flex items-center gap-2">
            <Icon name="lucide:key" class="size-5 text-accent" />
            <h3 class="text-sm font-semibold text-fg">{{ t('tokens.modalCreateToken') }}</h3>
          </div>
          <button type="button" class="text-fg-muted hover:text-fg" @click="showCreateTokenModal = false">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <form @submit.prevent="handleCreateToken" class="space-y-4">
          <!-- İsim -->
          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('tokens.tokenNameLabel') }}</label>
            <input
              v-model="createTokenName"
              type="text"
              :placeholder="t('tokens.tokenNamePlaceholder')"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
              required
            />
          </div>

          <!-- Geçerlilik Süresi -->
          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('tokens.validityLabel') }}</label>
            <select
              v-model="createTokenExpiry"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-xs text-fg focus:border-accent focus:outline-none"
            >
              <option :value="30">{{ t('tokens.days30') }}</option>
              <option :value="60">{{ t('tokens.days60') }}</option>
              <option :value="90">{{ t('tokens.days90') }}</option>
              <option :value="365">{{ t('tokens.year1') }}</option>
              <option :value="0">{{ t('tokens.neverExpires') }}</option>
            </select>
          </div>

          <!-- Kapsam / Yetki Seçimi -->
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="text-xs font-medium text-fg">{{ t('tokens.accessScope') }}</label>
              <label class="flex items-center gap-1.5 cursor-pointer text-xs text-accent">
                <input
                  v-model="createTokenAllScopes"
                  type="checkbox"
                  class="rounded border-line bg-bg text-accent focus:ring-0"
                />
                <span>{{ t('tokens.fullScope') }}</span>
              </label>
            </div>

            <div v-if="!createTokenAllScopes" class="space-y-1.5 rounded-lg border border-line bg-bg/50 p-3 max-h-48 overflow-y-auto">
              <label
                v-for="s in availableScopes"
                :key="s.id"
                class="flex items-start gap-2 cursor-pointer p-1 rounded hover:bg-surface transition-colors"
              >
                <input
                  v-model="selectedScopes"
                  :value="s.id"
                  type="checkbox"
                  class="rounded border-line bg-bg text-accent focus:ring-0 mt-0.5"
                />
                <div>
                  <div class="text-xs font-medium text-fg">{{ s.label }}</div>
                  <div class="text-[10px] text-fg-subtle font-mono">{{ s.id }} — {{ s.desc }}</div>
                </div>
              </label>
            </div>
          </div>

          <!-- Butonlar -->
          <div class="pt-3 border-t border-line flex justify-end gap-2">
            <button
              type="button"
              class="cursor-pointer rounded-lg border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2"
              @click="showCreateTokenModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="cursor-pointer rounded-lg bg-accent px-4 py-1.5 text-xs font-semibold text-on-accent hover:opacity-90 disabled:opacity-50 flex items-center gap-1.5"
              :disabled="creatingToken"
            >
              <Icon v-if="creatingToken" name="lucide:loader-2" class="size-3.5 animate-spin" />
              <span>{{ t('tokens.createBtn') }}</span>
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- MODAL: OLUSTURULAN TOKENIN GIZLI DEGERINI GOSTER -->
    <div
      v-if="issuedSecret"
      class="fixed inset-0 z-50 grid place-items-center bg-black/70 p-4 backdrop-blur-sm"
    >
      <div class="w-full max-w-lg rounded-xl border border-line bg-surface p-6 shadow-2xl space-y-4">
        <div class="flex items-center gap-3">
          <div class="grid size-10 place-items-center rounded-full bg-accent/15 text-accent">
            <Icon name="lucide:check-circle" class="size-6" />
          </div>
          <div>
            <h3 class="text-base font-bold text-fg">{{ t('tokens.modalIssuedTitle') }}</h3>
            <p class="text-xs text-fg-muted">{{ issuedSecret.name }}</p>
          </div>
        </div>

        <!-- Kritik Uyarı -->
        <div class="rounded-lg border border-warn/40 bg-warn/10 p-3 flex items-start gap-2.5">
          <Icon name="lucide:alert-triangle" class="size-4 text-warn shrink-0 mt-0.5" />
          <div class="text-xs text-warn leading-relaxed">{{ t('tokens.issuedWarn') }}</div>
        </div>

        <!-- Token Box -->
        <div class="space-y-1.5">
          <label class="text-xs font-semibold text-fg">{{ t('tokens.secretLabel') }}</label>
          <div class="flex items-center gap-2 rounded-lg border border-accent/40 bg-bg p-2.5 font-mono text-xs">
            <span class="break-all select-all text-accent font-semibold">{{ issuedSecret.token }}</span>
            <button
              type="button"
              class="shrink-0 cursor-pointer rounded bg-accent px-2.5 py-1 text-xs font-semibold text-on-accent hover:opacity-90 active:scale-95"
              @click="copyIssuedToken"
            >
              <Icon :name="copiedSecret ? 'lucide:check' : 'lucide:copy'" class="size-3.5 inline mr-1" />
              <span>{{ copiedSecret ? t('common.copied') : t('common.copy') }}</span>
            </button>
          </div>
        </div>

        <!-- Örnek cURL Kullanımı -->
        <div class="space-y-1.5">
          <label class="text-[11px] font-semibold text-fg-subtle uppercase tracking-wider">{{ t('tokens.curlExample') }}</label>
          <pre class="rounded-lg border border-line bg-bg p-2.5 font-mono text-[11px] text-fg-muted overflow-x-auto">curl -H "Authorization: Bearer {{ issuedSecret.token }}" \
  https://zorven.app/api/v1/tunnels</pre>
        </div>

        <div class="pt-3 border-t border-line flex justify-end">
          <button
            type="button"
            class="cursor-pointer rounded-lg bg-accent px-5 py-2 text-xs font-semibold text-on-accent hover:opacity-90"
            @click="issuedSecret = null"
          >
            {{ t('tokens.savedCloseBtn') }}
          </button>
        </div>
      </div>
    </div>

    <!-- MODAL: YENI IP KURALI EKLE -->
    <div
      v-if="showCreateRuleModal"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showCreateRuleModal = false"
    >
      <div class="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-2xl space-y-4">
        <div class="flex items-center justify-between pb-3 border-b border-line">
          <div class="flex items-center gap-2">
            <Icon name="lucide:network" class="size-5 text-accent" />
            <h3 class="text-sm font-semibold text-fg">{{ t('tokens.modalCreateRule') }}</h3>
          </div>
          <button type="button" class="text-fg-muted hover:text-fg" @click="showCreateRuleModal = false">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <form @submit.prevent="handleCreateRule" class="space-y-4">
          <!-- CIDR / IP -->
          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="text-xs font-medium text-fg">{{ t('tokens.ipCidrLabel') }}</label>
              <button
                v-if="detectedIp"
                type="button"
                class="text-[11px] text-accent hover:underline cursor-pointer font-medium"
                @click="fillDetectedIP"
              >
                {{ t('tokens.useMyIp', { ip: detectedIp }) }}
              </button>
            </div>
            <input
              v-model="ruleCidr"
              type="text"
              :placeholder="t('tokens.ipCidrPlaceholder')"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-xs text-fg font-mono placeholder:text-fg-subtle focus:border-accent focus:outline-none"
              required
            />
            <p class="mt-1 text-[10px] text-fg-subtle">{{ t('tokens.ipCidrHint') }}</p>
          </div>

          <!-- Kapsam (Tünel Seçimi) -->
          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('tokens.scopeLabel') }}</label>
            <select
              v-model="ruleTunnelId"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-xs text-fg focus:border-accent focus:outline-none"
            >
              <option value="">{{ t('tokens.allTunnelsGlobalRule') }}</option>
              <option v-for="t in tunnels" :key="t.id" :value="t.id">
                {{ t.target }} (ID: {{ t.id.slice(0, 10) }})
              </option>
            </select>
          </div>

          <!-- Açıklama / Not -->
          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('tokens.descLabel') }}</label>
            <input
              v-model="ruleDescription"
              type="text"
              :placeholder="t('tokens.descPlaceholder')"
              class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-xs text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
          </div>

          <!-- Butonlar -->
          <div class="pt-3 border-t border-line flex justify-end gap-2">
            <button
              type="button"
              class="cursor-pointer rounded-lg border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2"
              @click="showCreateRuleModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="cursor-pointer rounded-lg bg-accent px-4 py-1.5 text-xs font-semibold text-on-accent hover:opacity-90 disabled:opacity-50 flex items-center gap-1.5"
              :disabled="creatingRule"
            >
              <Icon v-if="creatingRule" name="lucide:loader-2" class="size-3.5 animate-spin" />
              <span>{{ t('tokens.saveRuleBtn') }}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>

  <!-- Loading State -->
  <div v-else-if="billingLoading" class="flex min-h-[50vh] flex-col items-center justify-center gap-3">
    <Icon name="lucide:loader-2" class="size-6 animate-spin text-accent" />
    <span class="text-xs text-fg-subtle font-mono">{{ t('tokens.checkingAccess') }}</span>
  </div>

  <!-- Locked State (Non-eligible plan) -->
  <div v-else class="flex min-h-[50vh] flex-col items-center justify-center gap-3">
    <!-- UpgradeModal pop up triggered, router redirects to / -->
  </div>
</template>
