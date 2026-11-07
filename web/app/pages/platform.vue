<script setup lang="ts">
import type { AdminGlobalStats, TenantWithCounts, ClientWithTenant, HostnameWithTenant, AbuseReport } from '~/types/api'

const api = useApi()
const toast = useToast()
const { platformAdmin, method, switchOrganization, organizations, check } = useAuth()
const { setAdminTenant, clearAdminTenant } = useAdminTenant()
const { clearActiveProject } = useActiveProject()
const { relativeTime } = useFormat()
const { t } = useI18n()

const stats = ref<AdminGlobalStats | null>(null)
// Her sekme ayri sayfalayici (50/sayfa); sayfa numarasi URL'de (?tnpage, ?clpage, ?hnpage, ?abpage).
const tenantsPager = usePagedList<TenantWithCounts>(p => api.pageAdminTenants(p), { queryKey: 'tnpage', immediate: false })
const clientsPager = usePagedList<ClientWithTenant>(p => api.pageAdminClients(p), { queryKey: 'clpage', immediate: false })
const hostnamesPager = usePagedList<HostnameWithTenant>(p => api.pageAdminHostnames(p), { queryKey: 'hnpage', immediate: false })
const abusePager = usePagedList<AbuseReport>(p => api.pageAdminAbuseReports(p), { queryKey: 'abpage', immediate: false })
const tenants = tenantsPager.items
const clients = clientsPager.items
const hostnames = hostnamesPager.items
const abuseReports = abusePager.items

const pending = ref(true)
const switching = ref<string | null>(null)
const freezing = ref<string | null>(null)
const activeTab = ref<'tenants' | 'clients' | 'hostnames' | 'abuse'>('tenants')
const error = ref('')

async function loadData() {
  pending.value = true
  error.value = ''
  try {
    const [st] = await Promise.all([
      api.adminGetStats(),
      tenantsPager.reload(true), clientsPager.reload(true), hostnamesPager.reload(true),
      abusePager.reload(true),
    ])
    stats.value = st
    // Ana listeler (kiracilar/istemciler/alan adlari/kotuye kullanim) basarisizsa hata goster.
    for (const pg of [tenantsPager, clientsPager, hostnamesPager, abusePager]) {
      if (pg.error.value) throw pg.error.value
    }
  } catch (err: any) {
    error.value = err?.data?.error?.message || err?.message || t('platform.loadFailed')
    toast.error(error.value)
  } finally {
    pending.value = false
  }
}

async function handleSwitchToTenant(ten: TenantWithCounts) {
  switching.value = ten.id
  try {
    await api.adminSwitchTenant(ten.id)
    // Admin anahtarinin oturumu yok: secim X-Tenant-ID basligi ile tasinir.
    if (method.value === 'key') {
      if (ten.id === 'ten_default') clearAdminTenant()
      else setAdminTenant(ten.id)
    }
    toast.success(t('platform.switchedTo', { slug: ten.slug }))
    // Proje slug'i kiraciya ozeldir; yeni kiraciya tasinmamali.
    clearActiveProject()
    await check()
    // Plan/proje/abonelik state'i yeni kiraciya gore tazelensin diye tam yenileme.
    window.location.assign('/')
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('platform.switchFailed'))
  } finally {
    switching.value = null
  }
}

async function handleFreeze(fqdn: string, frozen: boolean) {
  freezing.value = fqdn
  try {
    await api.adminFreeze({ fqdn, frozen })
    toast.success(frozen ? t('platform.frozenOk', { fqdn }) : t('platform.unfrozenOk', { fqdn }))
    await loadData()
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('platform.freezeFailed'))
  } finally {
    freezing.value = null
  }
}

onMounted(() => {
  // Platform admin degilse /admin/* istegi atilmaz; ana sayfaya yonlendir (API de 403 verir).
  if (!platformAdmin.value) {
    navigateTo('/', { replace: true })
    return
  }
  loadData()
})
</script>

<template>
  <div v-if="platformAdmin" class="space-y-6">
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
        <span>{{ t('platform.tabTenants', { n: tenantsPager.total.value ?? tenants.length }) }}</span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'clients' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'clients'"
      >
        <Icon name="lucide:monitor-smartphone" class="size-3.5" />
        <span>{{ t('platform.tabClients', { n: clientsPager.total.value ?? clients.length }) }}</span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'hostnames' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'hostnames'"
      >
        <Icon name="lucide:globe" class="size-3.5" />
        <span>{{ t('platform.tabHostnames', { n: hostnamesPager.total.value ?? hostnames.length }) }}</span>
      </button>

      <button
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
        :class="activeTab === 'abuse' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
        @click="activeTab = 'abuse'"
      >
        <Icon name="lucide:shield-alert" class="size-3.5" :class="abuseReports.length ? 'text-danger' : ''" />
        <span>{{ t('platform.tabAbuse', { n: abusePager.total.value ?? abuseReports.length }) }}</span>
      </button>
    </div>

    <!-- 1. Kiracılar Tablosu -->
    <PanelFrame v-if="activeTab === 'tenants'" :label="t('platform.allTenants')" :meta="`${tenantsPager.total.value ?? tenants.length}`">
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
      <ListPager class="px-4 pb-3" :list="tenantsPager" />
    </PanelFrame>

    <!-- 2. Tüm İstemciler Tablosu -->
    <PanelFrame v-if="activeTab === 'clients'" :label="t('platform.allClients')" :meta="`${clientsPager.total.value ?? clients.length}`">
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
      <ListPager class="px-4 pb-3" :list="clientsPager" />
    </PanelFrame>

    <!-- 3. Tüm Domainler Tablosu -->
    <PanelFrame v-if="activeTab === 'hostnames'" :label="t('platform.allHostnames')" :meta="`${hostnamesPager.total.value ?? hostnames.length}`">
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
      <ListPager class="px-4 pb-3" :list="hostnamesPager" />
    </PanelFrame>

    <!-- 4. Kötüye Kullanım Bildirimleri -->
    <PanelFrame v-if="activeTab === 'abuse'" :label="t('platform.allAbuse')" :meta="`${abusePager.total.value ?? abuseReports.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <p v-else-if="!abuseReports.length" class="px-4 py-8 text-center text-sm text-fg-muted">
        {{ t('platform.abuseEmpty') }}
      </p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colFqdn') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colReason') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colReporter') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('platform.colCreated') }}</th>
              <th class="px-4 py-2 text-right"><span class="sr-only">{{ t('platform.colAction') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="rep in abuseReports" :key="rep.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5 font-mono text-xs">
                <a :href="`https://${rep.fqdn}`" target="_blank" rel="noopener noreferrer" class="font-medium text-fg hover:text-accent hover:underline">
                  {{ rep.fqdn }}
                </a>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted max-w-md">
                <span class="line-clamp-2">{{ rep.reason || '—' }}</span>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-subtle">
                <div class="font-mono">{{ rep.reporter_ip || '—' }}</div>
                <div v-if="rep.reporter_id" class="font-mono text-[10px]" :title="t('platform.reporterIdHint')">{{ t('platform.reporterId') }}: {{ rep.reporter_id.slice(0, 12) }}</div>
                <div v-if="rep.reporter_ua" class="max-w-[220px] truncate text-[10px]" :title="rep.reporter_ua + (rep.reporter_lang ? ' · ' + rep.reporter_lang : '') + (rep.reporter_tls ? ' · TLS ' + rep.reporter_tls : '')">{{ rep.reporter_ua }}</div>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(rep.created_at) }}</td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex items-center justify-end gap-1.5">
                  <button
                    type="button"
                    :disabled="freezing === rep.fqdn"
                    class="cursor-pointer inline-flex items-center gap-1 rounded border border-danger/30 px-2.5 py-1 text-xs text-danger hover:bg-danger/10 disabled:opacity-50"
                    @click="handleFreeze(rep.fqdn, true)"
                  >
                    <Icon v-if="freezing === rep.fqdn" name="lucide:loader-2" class="size-3 animate-spin" />
                    <Icon v-else name="lucide:snowflake" class="size-3" />
                    <span>{{ t('platform.freeze') }}</span>
                  </button>
                  <button
                    type="button"
                    :disabled="freezing === rep.fqdn"
                    class="cursor-pointer inline-flex items-center gap-1 rounded border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-emerald-500/40 hover:text-emerald-400 disabled:opacity-50"
                    @click="handleFreeze(rep.fqdn, false)"
                  >
                    <Icon name="lucide:sun" class="size-3" />
                    <span>{{ t('platform.unfreeze') }}</span>
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <ListPager class="px-4 pb-3" :list="abusePager" />
    </PanelFrame>
  </div>
</template>
