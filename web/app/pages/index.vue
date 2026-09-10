<script setup lang="ts">
import type { Tunnel, Client, RequestLog } from '~/types/api'

const api = useApi()
const { t } = useI18n()
const { relativeTime, duration, clock } = useFormat()
const { subscription, usage, currentPlan, formatBytes, formatCurrency, openUpgrade } = useBilling()

const tunnels = ref<Tunnel[]>([])
const clients = ref<Client[]>([])
const requests = ref<RequestLog[]>([])
const pending = ref(true)

let stopStream: (() => void) | undefined

async function fetchDashboard() {
  try {
    const [t, c, r] = await Promise.all([
      api.listTunnels(),
      api.listClients(),
      api.listRequests(40),
    ])
    tunnels.value = t
    clients.value = c
    requests.value = r
  } catch {
    // sessizce devam et
  } finally {
    pending.value = false
  }
}

onMounted(async () => {
  await fetchDashboard()
  const interval = setInterval(fetchDashboard, 10000)

  // Canlı akış: yeni istekler başa eklenir, liste 40 kayıtta tutulur
  stopStream = api.streamRequests((r) => {
    requests.value = [r, ...requests.value].slice(0, 40)
  })

  onUnmounted(() => {
    clearInterval(interval)
    stopStream?.()
  })
})

const clientName = (id: string) => clients.value.find(c => c.id === id)?.name ?? id

const activeTunnels = computed(() => tunnels.value.filter(t => t.enabled).length)
const onlineClients = computed(() => clients.value.filter(c => c.status === 'online').length)
const errorRate = computed(() => {
  if (!requests.value.length) return '0%'
  const errors = requests.value.filter(r => r.status >= 400).length
  return `${Math.round((errors / requests.value.length) * 100)}%`
})
const p95 = computed(() => {
  if (!requests.value.length) return '—'
  const sorted = [...requests.value].map(r => r.duration_ms).sort((a, b) => a - b)
  return duration(sorted[Math.floor(sorted.length * 0.95)] ?? 0)
})

// Canlı ortalama CPU yükü
const avgCpu = computed(() => {
  const online = clients.value.filter(c => c.status === 'online' && c.metrics?.cpu_percent != null)
  if (!online.length) return '—'
  const total = online.reduce((acc, c) => acc + (c.metrics?.cpu_percent || 0), 0)
  return `%${Math.round(total / online.length)}`
})
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('overview.title') }}</h1>
        <p class="mt-0.5 text-sm text-fg-muted">{{ t('overview.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2 text-xs text-fg-subtle">
        <span class="size-2 rounded-full bg-accent animate-pulse" />
        <span>{{ t('overview.liveSync') }}</span>
      </div>
    </header>

    <!-- Metrikler -->
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <StatTile index="01" :label="t('overview.statActiveTunnel')" :value="pending ? '—' : activeTunnels"
                :hint="t('overview.statDefined', { n: tunnels.length })" tone="accent" />
      <StatTile index="02" :label="t('overview.statConnectedClient')" :value="pending ? '—' : onlineClients"
                :hint="t('overview.statRegisteredDevices', { n: clients.length })" />
      <StatTile index="03" :label="t('overview.statCpuLoad')" :value="pending ? '—' : avgCpu"
                :hint="t('overview.statOnlineMachines')" />
      <StatTile index="04" :label="t('overview.statP95')" :value="pending ? '—' : p95" :hint="t('overview.statLast40')" />
    </div>

    <!-- Abonelik & Kaynak Kotaları Özeti -->
    <div class="rounded-xl border border-line bg-surface/60 p-4 backdrop-blur-sm">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <!-- Sol: Plan Rozeti ve Hız -->
        <div class="flex items-center gap-3">
          <span class="grid size-9 place-items-center rounded-lg bg-accent/15 text-accent shrink-0">
            <Icon name="lucide:sparkles" class="size-5" />
          </span>
          <div>
            <div class="flex items-center gap-2">
              <h3 class="text-sm font-semibold text-fg capitalize">
                {{ currentPlan?.name || subscription?.plan || 'Free' }} {{ t('overview.planWord') }}
              </h3>
              <span class="font-mono text-xs text-fg-muted">
                ({{ formatCurrency(currentPlan?.price_monthly, currentPlan?.id) }})
              </span>
            </div>
            <div class="flex items-center gap-2 mt-0.5 text-xs text-fg-subtle">
              <span>{{ t('overview.speed') }} <strong class="font-mono text-accent">{{ currentPlan?.bandwidth_normal_mbps || 10 }} Mbps</strong></span>
              <span>•</span>
              <span>{{ t('overview.throttled') }} <strong class="font-mono text-fg-muted">{{ currentPlan?.bandwidth_throttled_mbps || 1 }} Mbps</strong></span>
            </div>
          </div>
        </div>

        <!-- Orta: Kaynak Kotaları Grid'i -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 flex-1 max-w-2xl px-2">
          <QuotaBar
            :label="t('overview.quotaClient')"
            :used="usage?.clients_count || 0"
            :limit="currentPlan?.max_clients ?? 2"
          />
          <QuotaBar
            :label="t('overview.quotaTunnel')"
            :used="usage?.tunnels_count || 0"
            :limit="currentPlan?.max_tunnels ?? 2"
          />
          <QuotaBar
            :label="t('overview.quotaCustomDomain')"
            :used="usage?.custom_domains_count || 0"
            :limit="currentPlan?.max_custom_domains ?? 0"
          />
          <QuotaBar
            :label="t('overview.quotaMonthlyData')"
            :used="usage?.bandwidth_used_bytes || 0"
            :limit="currentPlan?.bandwidth_limit_bytes ?? (5 * 1024 * 1024 * 1024)"
            :custom-used-text="formatBytes(usage?.bandwidth_used_bytes || 0)"
            :custom-limit-text="formatBytes(currentPlan?.bandwidth_limit_bytes)"
          />
        </div>

        <!-- Sağ: Aksiyon Butonu -->
        <div class="flex items-center gap-2 shrink-0">
          <NuxtLink
            to="/billing"
            class="flex items-center gap-1.5 rounded-lg border border-line bg-surface-2 px-3 py-1.5 text-xs font-medium text-fg transition-colors hover:border-accent/40 hover:text-accent"
          >
            <span>{{ t('overview.quotasPlans') }}</span>
            <Icon name="lucide:arrow-right" class="size-3.5" />
          </NuxtLink>
        </div>
      </div>
    </div>

    <!-- Tüneller & İstek Akışı -->
    <div class="grid gap-4 xl:grid-cols-2">
      <!-- Tüneller -->
      <PanelFrame :label="t('overview.tunnelsTitle')" :meta="`${tunnels.length}`">
        <template #actions>
          <NuxtLink to="/tunnels"
                    class="cursor-pointer font-mono text-[11px] text-accent hover:underline">
            {{ t('overview.all') }}
          </NuxtLink>
        </template>

        <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>

        <ul v-else class="divide-y divide-line">
          <li v-for="tn in tunnels" :key="tn.id"
              class="flex items-center justify-between gap-4 px-4 py-3">
            <div class="min-w-0">
              <p class="truncate font-mono text-sm">
                {{ (tn.hostnames ?? []).map(h => h.fqdn).join(', ') || t('overview.noName') }}
              </p>
              <p class="mt-0.5 truncate font-mono text-[11px] text-fg-subtle">
                {{ clientName(tn.client_id) }} → {{ tn.target }}
              </p>
            </div>
            <StatusPill :status="tn.enabled ? 'enabled' : 'disabled'" />
          </li>
        </ul>
      </PanelFrame>

      <!-- Canlı istek akışı -->
      <PanelFrame :label="t('overview.liveRequests')">
        <template #actions>
          <span class="inline-flex items-center gap-1.5 font-mono text-[11px] text-accent">
            <span class="size-1.5 animate-pulse rounded-full bg-accent" />{{ t('overview.live') }}
          </span>
        </template>

        <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>

        <ul v-else class="max-h-[420px] divide-y divide-line overflow-y-auto">
          <li v-for="r in requests" :key="r.id"
              class="flex items-center gap-3 px-4 py-2 text-sm">
            <MethodBadge :method="r.method" class="w-14 shrink-0" />
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted">{{ r.path }}</span>
            <StatusCode :code="r.status" />
            <span class="w-14 shrink-0 text-right font-mono text-[11px] tabular-nums text-fg-subtle">
              {{ duration(r.duration_ms) }}
            </span>
            <span class="w-16 shrink-0 text-right font-mono text-[11px] text-fg-subtle">
              {{ clock(r.ts) }}
            </span>
          </li>
        </ul>
      </PanelFrame>
    </div>

    <!-- Bağlı Cihazlar & Canlı Sistem Telemetrisi -->
    <PanelFrame :label="t('overview.devicesTitle')" :meta="`${clients.length}`">
      <template #actions>
        <NuxtLink to="/clients" class="cursor-pointer font-mono text-[11px] text-accent hover:underline">
          {{ t('overview.allDevices') }}
        </NuxtLink>
      </template>

      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('overview.colDevice') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('overview.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('overview.colTelemetry') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('overview.colVersionNet') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('overview.colLastSeen') }}</th>
              <th class="px-4 py-2 text-right"><span class="sr-only">{{ t('overview.colAccess') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="c in clients" :key="c.id" class="hover:bg-surface-2/40 transition-colors">
              <td class="px-4 py-3">
                <div class="font-medium text-fg">{{ c.name }}</div>
                <div class="font-mono text-xs text-fg-subtle">{{ c.id }}</div>
              </td>
              <td class="px-4 py-3">
                <div class="flex flex-col gap-1 items-start">
                  <StatusPill :status="c.status" />
                  <span
                    v-if="c.status === 'online' && c.is_service"
                    class="inline-flex items-center gap-1 rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium text-accent"
                  >
                    <Icon name="lucide:cog" class="size-3 animate-spin-slow" />
                    {{ t('overview.service') }}
                  </span>
                  <span
                    v-else-if="c.status === 'online'"
                    class="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-fg-muted"
                  >
                    {{ t('overview.desktopCli') }}
                  </span>
                </div>
              </td>

              <!-- Canlı Sistem Telemetrisi -->
              <td class="px-4 py-3">
                <div v-if="c.status === 'online' && c.metrics" class="space-y-1.5 min-w-[170px] max-w-[210px]">
                  <!-- CPU -->
                  <div>
                    <div class="flex justify-between text-[11px] text-fg-muted mb-0.5">
                      <span>{{ t('overview.cpu') }}</span>
                      <span class="font-mono font-medium" :class="c.metrics.cpu_percent > 85 ? 'text-danger' : 'text-fg'">%{{ c.metrics.cpu_percent.toFixed(0) }}</span>
                    </div>
                    <div class="h-1.5 w-full rounded-full bg-surface-2 overflow-hidden">
                      <div
                        class="h-full transition-all duration-300 rounded-full"
                        :class="c.metrics.cpu_percent > 85 ? 'bg-danger' : c.metrics.cpu_percent > 60 ? 'bg-amber-500' : 'bg-accent'"
                        :style="{ width: `${Math.min(100, Math.max(2, c.metrics.cpu_percent))}%` }"
                      />
                    </div>
                  </div>

                  <!-- RAM -->
                  <div>
                    <div class="flex justify-between text-[11px] text-fg-muted mb-0.5">
                      <span>{{ t('overview.ram') }}</span>
                      <span class="font-mono font-medium" :class="c.metrics.memory_percent > 85 ? 'text-danger' : 'text-fg'">
                        %{{ c.metrics.memory_percent.toFixed(0) }}
                        <span v-if="c.metrics.memory_used_mb" class="text-[10px] text-fg-subtle font-normal">({{ (c.metrics.memory_used_mb / 1024).toFixed(1) }} GB)</span>
                      </span>
                    </div>
                    <div class="h-1.5 w-full rounded-full bg-surface-2 overflow-hidden">
                      <div
                        class="h-full transition-all duration-300 rounded-full"
                        :class="c.metrics.memory_percent > 85 ? 'bg-danger' : c.metrics.memory_percent > 60 ? 'bg-amber-500' : 'bg-accent'"
                        :style="{ width: `${Math.min(100, Math.max(2, c.metrics.memory_percent))}%` }"
                      />
                    </div>
                  </div>

                  <!-- Disk -->
                  <div v-if="c.metrics.disk_percent" class="text-[10px] text-fg-subtle">
                    {{ t('overview.disk') }}: %{{ c.metrics.disk_percent.toFixed(0) }}
                  </div>
                </div>
                <span v-else-if="c.status === 'online'" class="text-xs text-fg-subtle">{{ t('overview.waitingMetrics') }}</span>
                <span v-else class="text-xs text-fg-muted">—</span>
              </td>

              <td class="px-4 py-3">
                <div class="font-mono text-xs text-fg-muted">{{ c.version ?? '—' }}</div>
                <div class="font-mono text-xs text-fg-subtle">{{ c.remote_addr ?? '—' }}</div>
              </td>
              <td class="px-4 py-3 text-xs text-fg-muted">{{ relativeTime(c.last_seen_at) }}</td>
              <td class="px-4 py-3 text-right">
                <div class="flex items-center justify-end gap-1">
                  <NuxtLink
                    v-if="c.status === 'online'"
                    :to="`/terminal/${c.id}`"
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent"
                    :title="t('overview.openTerminal')"
                  >
                    <Icon name="lucide:terminal" class="size-4" />
                  </NuxtLink>
                  <NuxtLink
                    v-if="c.status === 'online'"
                    :to="`/screen/${c.id}`"
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent"
                    :title="t('overview.watchScreen')"
                  >
                    <Icon name="lucide:monitor" class="size-4" />
                  </NuxtLink>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>
  </div>
</template>
