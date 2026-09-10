<script setup lang="ts">
import type { AdminGlobalStats, TenantWithCounts, ClientWithTenant, HostnameWithTenant } from '~/types/api'

const api = useApi()
const toast = useToast()
const { platformAdmin, switchOrganization, organizations, check } = useAuth()
const { relativeTime } = useFormat()
const { t } = useI18n()

const stats = ref<AdminGlobalStats | null>(null)
const tenants = ref<TenantWithCounts[]>([])
const clients = ref<ClientWithTenant[]>([])
const hostnames = ref<HostnameWithTenant[]>([])

const pending = ref(true)
const switching = ref<string | null>(null)
const activeTab = ref<'tenants' | 'clients' | 'hostnames'>('tenants')
const error = ref('')

async function loadData() {
  pending.value = true
  error.value = ''
  try {
    const [st, tn, cl, hn] = await Promise.all([
      api.adminGetStats(),
      api.adminListTenants(),
      api.adminListClients(),
      api.adminListHostnames(),
    ])
    stats.value = st
    tenants.value = tn
    clients.value = cl
    hostnames.value = hn
  } catch (err: any) {
    error.value = err?.data?.error?.message || err?.message || t('platform.loadFailed')
    toast.error(error.value)
  } finally {
    pending.value = false
  }
}

async function handleSwitchToTenant(t: TenantWithCounts) {
  switching.value = t.id
  try {
    await api.adminSwitchTenant(t.id)
    toast.success(t('platform.switchedTo', { slug: t.slug }))
    await check()
    await navigateTo('/')
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('platform.switchFailed'))
  } finally {
    switching.value = null
  }
}

const showPlanModal = ref(false)
const selectedTenant = ref<TenantWithCounts | null>(null)
const selectedPlan = ref('free')
const updatingPlan = ref(false)

const PLAN_OPTIONS = [
  { id: 'free', label: t('platform.planFree') },
  { id: 'hobby', label: t('platform.planHobby') },
  { id: 'pro', label: t('platform.planPro') },
  { id: 'team', label: t('platform.planTeam') },
  { id: 'enterprise', label: t('platform.planEnterprise') },
]

function openChangePlan(t: TenantWithCounts) {
  selectedTenant.value = t
  selectedPlan.value = t.plan || 'free'
  showPlanModal.value = true
}

async function handleSavePlan() {
  if (!selectedTenant.value) return
  updatingPlan.value = true
  try {
    await api.adminUpdateTenantPlan(selectedTenant.value.id, selectedPlan.value)
    selectedTenant.value.plan = selectedPlan.value
    toast.success(t('platform.planUpdated', { slug: selectedTenant.value.slug, plan: selectedPlan.value }))
    showPlanModal.value = false
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('platform.planUpdateFailed'))
  } finally {
    updatingPlan.value = false
  }
}

function getPlanBadgeClass(plan?: string) {
  switch (plan) {
    case 'enterprise':
      return 'border border-amber-300 bg-amber-50 text-amber-800 dark:bg-amber-500/20 dark:text-amber-300 dark:border-amber-500/30 font-semibold'
    case 'team':
      return 'border border-blue-300 bg-blue-50 text-blue-800 dark:bg-blue-500/20 dark:text-blue-300 dark:border-blue-500/30 font-semibold'
    case 'pro':
      return 'border border-purple-300 bg-purple-50 text-purple-800 dark:bg-purple-500/20 dark:text-purple-300 dark:border-purple-500/30 font-semibold'
    case 'hobby':
      return 'border border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-500/20 dark:text-emerald-300 dark:border-emerald-500/30 font-semibold'
    default:
      return 'bg-surface-2 text-fg-muted border border-line font-medium'
  }
}

onMounted(() => {
  loadData()
})
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2">
          <Icon name="lucide:shield-check" class="size-6 text-accent" />
          <h1 class="text-xl font-semibold tracking-tight">{{ t('platform.title') }}</h1>
          <span class="rounded border border-line bg-surface-2 px-2 py-0.5 font-mono text-[10px] font-bold text-fg-muted">
            {{ t('platform.globalAuth') }}
          </span>
        </div>
        <p class="mt-0.5 text-sm text-fg-muted">{{ t('platform.subtitle') }}</p>
      </div>

      <div class="flex items-center gap-2">
        <button
          type="button"
          :disabled="pending"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-surface px-3 py-1.5 text-xs text-fg-muted transition-colors hover:text-fg disabled:opacity-50"
          @click="loadData"
        >
          <Icon name="lucide:refresh-cw" class="size-3.5" :class="{ 'animate-spin': pending }" />
          <span>{{ t('common.refresh') }}</span>
        </button>
      </div>
    </header>

    <!-- Hata Bildirimi -->
    <div v-if="error" class="rounded border border-danger/30 bg-danger/10 p-3 text-sm text-danger" role="alert">
      {{ error }}
    </div>

    <!-- Global İstatistik Kartları -->
    <div v-if="stats" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <PanelFrame :label="t('platform.statOrgs')">
        <div class="p-4">
          <div class="text-2xl font-bold tracking-tight text-fg">{{ stats.tenants_count }}</div>
          <div class="mt-1 text-xs text-fg-muted">{{ t('platform.statOrgsHint') }}</div>
        </div>
      </PanelFrame>

      <PanelFrame :label="t('platform.statClients')">
        <div class="p-4">
          <div class="flex items-baseline gap-1.5">
            <span class="text-2xl font-bold tracking-tight text-emerald-400">{{ stats.online_clients_count }}</span>
            <span class="text-sm text-fg-muted">/ {{ stats.clients_count }}</span>
          </div>
          <div class="mt-1 text-xs text-fg-muted">{{ t('platform.statClientsHint') }}</div>
        </div>
      </PanelFrame>

      <PanelFrame :label="t('platform.statTunnels')">
        <div class="p-4">
          <div class="flex items-baseline gap-1.5">
            <span class="text-2xl font-bold tracking-tight text-accent">{{ stats.active_tunnels_count }}</span>
            <span class="text-sm text-fg-muted">/ {{ stats.tunnels_count }}</span>
          </div>
          <div class="mt-1 text-xs text-fg-muted">{{ t('platform.statTunnelsHint') }}</div>
        </div>
      </PanelFrame>

      <PanelFrame :label="t('platform.statDomains')">
        <div class="p-4">
          <div class="flex items-baseline gap-1.5">
            <span class="text-2xl font-bold tracking-tight text-accent">{{ stats.custom_domains_count }}</span>
            <span class="text-sm text-fg-muted">/ {{ stats.hostnames_count }}</span>
          </div>
          <div class="mt-1 text-xs text-fg-muted">{{ t('platform.statDomainsHint') }}</div>
        </div>
      </PanelFrame>
    </div>

    <!-- Sekmeler -->
    <div class="flex gap-2 border-b border-line pb-3">
      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'tenants' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'tenants'"
      >
        <Icon name="lucide:building-2" class="size-3.5" />
        <span>{{ t('platform.tabTenants', { n: tenants.length }) }}</span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'clients' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'clients'"
      >
        <Icon name="lucide:monitor-smartphone" class="size-3.5" />
        <span>{{ t('platform.tabClients', { n: clients.length }) }}</span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'hostnames' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'hostnames'"
      >
        <Icon name="lucide:globe" class="size-3.5" />
        <span>{{ t('platform.tabHostnames', { n: hostnames.length }) }}</span>
      </button>
    </div>

    <!-- 1. Kiracılar Tablosu -->
    <PanelFrame v-if="activeTab === 'tenants'" :label="t('platform.allTenants')" :meta="`${tenants.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colSlugName') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colTenantId') }}</th>
              <th class="label-sys px-4 py-2 font-normal text-center">{{ t('platform.colClient') }}</th>
              <th class="label-sys px-4 py-2 font-normal text-center">{{ t('platform.colTunnel') }}</th>
              <th class="label-sys px-4 py-2 font-normal text-center">{{ t('platform.colDomain') }}</th>
              <th class="label-sys px-4 py-2 font-normal text-center">{{ t('platform.colPlan') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colRegDate') }}</th>
              <th class="px-4 py-2 text-right"><span class="sr-only">{{ t('platform.colAction') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="ten in tenants" :key="ten.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2">
                  <Icon name="lucide:building-2" class="size-4 text-accent" />
                  <span class="font-mono font-medium text-fg">{{ ten.slug }}</span>
                </div>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted">{{ ten.id }}</td>
              <td class="px-4 py-2.5 text-center font-mono text-xs">{{ ten.clients_count }}</td>
              <td class="px-4 py-2.5 text-center font-mono text-xs">{{ ten.tunnels_count }}</td>
              <td class="px-4 py-2.5 text-center font-mono text-xs">{{ ten.hostnames_count }}</td>
              <td class="px-4 py-2.5 text-center">
                <button
                  type="button"
                  class="cursor-pointer inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider transition-colors hover:opacity-80"
                  :class="getPlanBadgeClass(ten.plan)"
                  :title="t('platform.changePlanTitle')"
                  @click="openChangePlan(ten)"
                >
                  <span>{{ ten.plan || 'free' }}</span>
                  <Icon name="lucide:pencil" class="size-2.5 opacity-60" />
                </button>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(ten.created_at) }}</td>
              <td class="px-4 py-2.5 text-right">
                <button
                  type="button"
                  :disabled="switching === ten.id"
                  class="cursor-pointer rounded border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-accent/40 hover:text-accent disabled:opacity-50 inline-flex items-center gap-1.5"
                  @click="handleSwitchToTenant(ten)"
                >
                  <Icon v-if="switching === ten.id" name="lucide:loader-2" class="size-3 animate-spin" />
                  <span>{{ t('platform.switchToManage') }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>

    <!-- 2. Tüm İstemciler Tablosu -->
    <PanelFrame v-if="activeTab === 'clients'" :label="t('platform.allClients')" :meta="`${clients.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colClientName') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colTenantOrg') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colConnDetail') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colCreated') }}</th>
              <th class="px-4 py-2 text-right"><span class="sr-only">{{ t('platform.colAccess') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="c in clients" :key="c.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5">
                <div class="font-medium text-fg">{{ c.name }}</div>
                <div class="font-mono text-[11px] text-fg-subtle">{{ c.id }}</div>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted font-medium">{{ c.tenant_slug }}</td>
              <td class="px-4 py-2.5">
                <span
                  class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium"
                  :class="c.status === 'online' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-surface text-fg-subtle'"
                >
                  <span class="size-1.5 rounded-full" :class="c.status === 'online' ? 'bg-emerald-400' : 'bg-fg-subtle'" />
                  {{ c.status === 'online' ? t('platform.online') : t('platform.offline') }}
                </span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted">
                <div v-if="c.status === 'online'">
                  <div>{{ c.remote_addr || '—' }}</div>
                  <div class="text-[10px] text-fg-subtle">v{{ c.version || '?' }}</div>
                </div>
                <span v-else>—</span>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(c.created_at) }}</td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex items-center justify-end gap-1.5">
                  <NuxtLink
                    :to="`/terminal/${c.id}`"
                    class="inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-xs text-fg-muted hover:border-accent hover:text-accent"
                    :title="t('platform.openTerminal')"
                  >
                    <Icon name="lucide:terminal" class="size-3" />
                    <span class="hidden sm:inline">CLI</span>
                  </NuxtLink>
                  <NuxtLink
                    :to="`/screen/${c.id}`"
                    class="inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-xs text-fg-muted hover:border-accent hover:text-accent"
                    :title="t('platform.openScreen')"
                  >
                    <Icon name="lucide:monitor" class="size-3" />
                    <span class="hidden sm:inline">{{ t('platform.screen') }}</span>
                  </NuxtLink>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>

    <!-- 3. Tüm Domainler Tablosu -->
    <PanelFrame v-if="activeTab === 'hostnames'" :label="t('platform.allHostnames')" :meta="`${hostnames.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colFqdn') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colType') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colTenantOrg') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colTargetTunnel') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colCreated') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="h in hostnames" :key="h.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5 font-mono text-xs">
                <a :href="`https://${h.fqdn}`" target="_blank" rel="noopener noreferrer" class="font-medium text-fg hover:text-accent hover:underline">
                  {{ h.fqdn }}
                </a>
              </td>
              <td class="px-4 py-2.5">
                <span class="rounded border border-line px-1.5 py-0.5 font-mono text-[10px] text-fg-muted">
                  {{ h.type }}
                </span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted font-medium">{{ h.tenant_slug }}</td>
              <td class="px-4 py-2.5 font-mono text-xs">
                <span v-if="h.target" class="text-emerald-400">{{ h.target }}</span>
                <span v-else class="text-amber-400">{{ t('platform.idle') }}</span>
              </td>
              <td class="px-4 py-2.5">
                <span v-if="h.type === 'custom' && !h.verified" class="text-amber-400 text-xs">{{ t('platform.pending') }}</span>
                <span v-else class="text-emerald-400 text-xs">{{ t('platform.active') }}</span>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(h.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>

    <!-- Modal: Kiracı Planını Değiştir -->
    <div
      v-if="showPlanModal && selectedTenant"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showPlanModal = false"
    >
      <div class="w-full max-w-sm rounded-xl border border-line bg-surface p-5 shadow-2xl">
        <div class="flex items-center justify-between pb-3 border-b border-line">
          <h3 class="text-sm font-semibold text-fg">{{ t('platform.modalChangePlan') }}</h3>
          <button type="button" class="text-fg-muted hover:text-fg" @click="showPlanModal = false">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <div class="mt-4 space-y-3 text-xs">
          <div>
            <span class="text-fg-subtle">{{ t('platform.tenantLabel') }}</span>
            <span class="ml-1 font-mono font-medium text-fg">{{ selectedTenant.slug }} ({{ selectedTenant.id }})</span>
          </div>

          <div>
            <label class="block text-xs font-medium text-fg-muted mb-1.5">{{ t('platform.selectNewPlan') }}</label>
            <select
              v-model="selectedPlan"
              class="w-full rounded border border-line bg-bg px-3 py-2 text-xs font-medium text-fg focus:border-accent focus:outline-none"
            >
              <option v-for="opt in PLAN_OPTIONS" :key="opt.id" :value="opt.id">
                {{ opt.label }}
              </option>
            </select>
          </div>
        </div>

        <div class="flex justify-end gap-2 pt-4 border-t border-line mt-4">
          <button
            type="button"
            class="rounded px-3 py-1.5 text-xs text-fg-muted hover:bg-bg"
            @click="showPlanModal = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="updatingPlan"
            class="rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent hover:opacity-90 disabled:opacity-50"
            @click="handleSavePlan"
          >
            {{ updatingPlan ? t('platform.savingPlan') : t('platform.updatePlan') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
