<script setup lang="ts">
import type { Client } from '~/types/api'

const api = useApi()
const { t } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const { openUpgrade, loadBillingData } = useBilling()

const clients = ref<Client[]>([])
const pending = ref(true)
const saving = ref(false)
const newName = ref('')

/** Token yalnizca olusturma yanitinda bir kez doner (api_contract.md §1). */
const issuedToken = ref<{ name: string, token: string } | null>(null)
const copied = ref(false)
const selectedTab = ref<'linux' | 'windows' | 'cli'>('linux')

const origin = computed(() => {
  if (typeof window !== 'undefined') {
    return window.location.origin
  }
  return 'https://zorven.app'
})

onMounted(async () => {
  await fetchClients()
  // Her 10 saniyede bir metrikleri tazelemek icin polling
  const interval = setInterval(fetchClients, 10000)
  onUnmounted(() => clearInterval(interval))
})

async function fetchClients() {
  try {
    clients.value = await api.listClients()
  } catch (err) {
    // sessizce devam et
  } finally {
    pending.value = false
  }
}

async function create() {
  if (!newName.value.trim()) return
  saving.value = true
  try {
    const res = await api.createClient(newName.value.trim())
    clients.value.push(res.client)
    issuedToken.value = { name: res.client.name, token: res.token }
    copied.value = false
    newName.value = ''
    toast.success(t('clients.created'))
    loadBillingData()
  } catch (err: any) {
    const code = err?.data?.error?.code
    const msg = err?.data?.error?.message || err?.message || t('clients.createFailed')
    if (code === 'plan_limit_reached') {
      openUpgrade(msg, 'clients')
    } else {
      toast.error(msg)
    }
  } finally {
    saving.value = false
  }
}

async function copyText(txt: string) {
  if (!txt) return
  await navigator.clipboard.writeText(txt)
  copied.value = true
  toast.success(t('clients.cmdCopied'))
  setTimeout(() => (copied.value = false), 2000)
}

async function remove(c: Client) {
  try {
    await api.deleteClient(c.id)
    clients.value = clients.value.filter(x => x.id !== c.id)
    toast.info(t('clients.removed', { name: c.name }))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('clients.removeFailed'))
  }
}

const linuxCmd = computed(() => {
  if (!issuedToken.value) return ''
  return `curl -fsSL "${origin.value}/install.sh?token=${issuedToken.value.token}" | sudo bash`
})

const windowsCmd = computed(() => {
  if (!issuedToken.value) return ''
  return `irm "${origin.value}/install.ps1?token=${issuedToken.value.token}" | iex`
})

const cliCmd = computed(() => {
  if (!issuedToken.value) return ''
  return `zorven authtoken ${issuedToken.value.token}\nzorven 8080`
})

const currentCmd = computed(() => {
  if (selectedTab.value === 'linux') return linuxCmd.value
  if (selectedTab.value === 'windows') return windowsCmd.value
  return cliCmd.value
})
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('clients.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('clients.subtitle') }}</p>
    </header>

    <!-- Token ve Tek Komutla Kurulum Paneli -->
    <div
      v-if="issuedToken"
      class="rounded-lg border border-accent/40 bg-accent/5 p-5"
      role="status"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="flex items-center gap-2.5">
          <div class="flex size-8 items-center justify-center rounded-lg bg-accent/20 text-accent">
            <Icon name="lucide:terminal" class="size-5" />
          </div>
          <div>
            <p class="text-sm font-medium">
              <span class="font-mono text-accent-bright font-bold">{{ issuedToken.name }}</span> {{ t('clients.createdFor') }}
            </p>
            <p class="text-xs text-fg-muted">{{ t('clients.createdHint') }}</p>
          </div>
        </div>

        <button
          class="cursor-pointer rounded p-1 text-fg-muted transition-colors duration-150 hover:text-fg"
          :aria-label="t('common.close')"
          @click="issuedToken = null"
        >
          <Icon name="lucide:x" class="size-4" />
        </button>
      </div>

      <!-- İşletim Sistemi Sekmeleri -->
      <div class="mt-4 border-b border-line flex gap-2">
        <button
          type="button"
          class="cursor-pointer flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors duration-150 -mb-px"
          :class="selectedTab === 'linux' ? 'border-accent text-accent' : 'border-transparent text-fg-muted hover:text-fg'"
          @click="selectedTab = 'linux'"
        >
          <Icon name="simple-icons:linux" class="size-3.5" />
          {{ t('clients.tabLinux') }}
        </button>
        <button
          type="button"
          class="cursor-pointer flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors duration-150 -mb-px"
          :class="selectedTab === 'windows' ? 'border-accent text-accent' : 'border-transparent text-fg-muted hover:text-fg'"
          @click="selectedTab = 'windows'"
        >
          <Icon name="simple-icons:windows" class="size-3.5" />
          {{ t('clients.tabWindows') }}
        </button>
        <button
          type="button"
          class="cursor-pointer flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors duration-150 -mb-px"
          :class="selectedTab === 'cli' ? 'border-accent text-accent' : 'border-transparent text-fg-muted hover:text-fg'"
          @click="selectedTab = 'cli'"
        >
          <Icon name="lucide:zap" class="size-3.5" />
          {{ t('clients.tabCli') }}
        </button>
      </div>

      <!-- Komut Kutusu -->
      <div class="mt-3">
        <div class="flex items-center justify-between text-xs text-fg-muted mb-1.5">
          <span v-if="selectedTab === 'linux'">{{ t('clients.hintLinux') }}</span>
          <span v-else-if="selectedTab === 'windows'">{{ t('clients.hintWindows') }}</span>
          <span v-else>{{ t('clients.hintCli') }}</span>

          <button
            type="button"
            class="cursor-pointer flex items-center gap-1 text-accent hover:underline"
            @click="copyText(currentCmd)"
          >
            <Icon :name="copied ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
            {{ copied ? t('common.copied') : t('clients.copyCmd') }}
          </button>
        </div>

        <pre class="overflow-x-auto rounded border border-line bg-bg p-3 font-mono text-xs text-fg selection:bg-accent selection:text-on-accent whitespace-pre-wrap">{{ currentCmd }}</pre>

        <!-- Masaustu uygulamasi (Windows): CLI yerine hazir .exe kurulumu. -->
        <div v-if="selectedTab === 'windows'" class="mt-2.5 flex flex-wrap items-center gap-2">
          <a
            :href="`${origin}/bin/Zorven-Setup-windows-amd64.exe`"
            download
            class="inline-flex items-center gap-1.5 rounded border border-line bg-surface-2 px-3 py-1.5 text-xs font-medium text-fg transition-colors duration-150 hover:border-accent hover:text-accent"
          >
            <Icon name="lucide:monitor-down" class="size-3.5" />
            {{ t('clients.downloadDesktop') }}
          </a>
          <span class="text-[11px] text-fg-subtle">{{ t('clients.desktopHint') }}</span>
        </div>

        <div class="mt-2.5 flex items-center gap-2 text-[11px] text-fg-subtle">
          <Icon name="lucide:shield-check" class="size-3.5 text-accent shrink-0" />
          <span>{{ t('clients.installNote') }}</span>
        </div>
      </div>
    </div>

    <!-- Yeni İstemci Formu -->
    <PanelFrame :label="t('clients.newClient')">
      <form class="flex flex-col gap-3 p-4 sm:flex-row sm:items-end" @submit.prevent="create">
        <div class="flex-1">
          <label for="cname" class="label-sys mb-1 block">{{ t('clients.clientName') }}</label>
          <input
            id="cname"
            v-model="newName"
            placeholder="ev-pc veya prod-sunucu-1"
            class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm placeholder:text-fg-subtle focus:border-accent"
          >
        </div>
        <button
          type="submit"
          :disabled="saving || !newName.trim()"
          class="cursor-pointer rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity duration-150 hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {{ saving ? t('common.creating') : t('clients.createBtn') }}
        </button>
      </form>
    </PanelFrame>

    <!-- Kayıtlı İstemciler Listesi -->
    <PanelFrame :label="t('clients.registered')" :meta="String(clients.length)">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('clients.colName') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('clients.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('clients.colMetrics') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('clients.colVersion') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('clients.colLastSeen') }}</th>
              <th class="px-4 py-2"><span class="sr-only">{{ t('clients.colActions') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="c in clients" :key="c.id" class="hover:bg-surface-2/40">
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
                    :title="t('clients.systemServiceTitle')"
                  >
                    <Icon name="lucide:cog" class="size-3 animate-spin-slow" />
                    {{ t('clients.systemService') }}
                  </span>
                  <span
                    v-else-if="c.status === 'online'"
                    class="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-fg-muted"
                    :title="t('clients.cliSessionTitle')"
                  >
                    {{ t('clients.cliSession') }}
                  </span>
                </div>
              </td>

              <!-- Canlı Metrikler (CPU / RAM / Disk) -->
              <td class="px-4 py-3">
                <div v-if="c.status === 'online' && c.metrics" class="space-y-1.5 min-w-[160px] max-w-[200px]">
                  <!-- CPU -->
                  <div>
                    <div class="flex justify-between text-[11px] text-fg-muted mb-0.5">
                      <span>CPU</span>
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
                      <span>RAM</span>
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
                  <div v-if="c.metrics.disk_percent" class="text-[10px] text-fg-subtle">{{ t('clients.diskUsage', { p: c.metrics.disk_percent.toFixed(0) }) }}</div>
                </div>
                <span v-else-if="c.status === 'online'" class="text-xs text-fg-subtle">{{ t('clients.waitingMetrics') }}</span>
                <span v-else class="text-xs text-fg-muted">—</span>
              </td>

              <td class="px-4 py-3">
                <div class="font-mono text-xs text-fg-muted">{{ c.version ?? '—' }}</div>
                <div class="font-mono text-xs text-fg-subtle">{{ c.remote_addr ?? '—' }}</div>
              </td>
              <td class="px-4 py-3 text-xs text-fg-muted">{{ relativeTime(c.last_seen_at) }}</td>
              <td class="px-4 py-3">
                <div class="flex justify-end gap-1">
                  <NuxtLink
                    v-if="c.status === 'online'"
                    :to="`/terminal/${c.id}`"
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent-bright"
                    :aria-label="t('clients.openTerminalAria', { name: c.name })"
                    :title="t('clients.openTerminal')"
                  >
                    <Icon name="lucide:square-terminal" class="size-4" />
                  </NuxtLink>
                  <NuxtLink
                    v-if="c.status === 'online'"
                    :to="`/screen/${c.id}`"
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent-bright"
                    :aria-label="t('clients.watchScreenAria', { name: c.name })"
                    :title="t('clients.watchScreen')"
                  >
                    <Icon name="lucide:monitor" class="size-4" />
                  </NuxtLink>
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-danger/10 hover:text-danger"
                    :aria-label="t('clients.deleteAria', { name: c.name })"
                    @click="remove(c)"
                  >
                    <Icon name="lucide:trash-2" class="size-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>
  </div>
</template>
