<script setup lang="ts">
import type { RequestLog, Tunnel, LogFilter } from '~/types/api'

const api = useApi()
const { duration, bytes, clock } = useFormat()
const { t } = useI18n()

const rows = ref<RequestLog[]>([])
const tunnels = ref<Tunnel[]>([])
const pending = ref(true)
const live = ref(true)
const selected = ref<RequestLog | null>(null)

// --- Gelişmiş filtreler ---
const fMethod = ref('')
const fStatus = ref('')          // '' | 2xx | 3xx | 4xx | 5xx | kesin kod
const fHost = ref('')
const fQ = ref('')
const fMinDur = ref<number | ''>('')

const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD']
const statusOpts = [
  { v: '', l: t('requests.statusAll') },
  { v: '2xx', l: t('requests.status2xx') },
  { v: '3xx', l: t('requests.status3xx') },
  { v: '4xx', l: t('requests.status4xx') },
  { v: '5xx', l: t('requests.status5xx') },
]

const hostOf = (id: string) => tunnels.value.find(t => t.id === id)?.hostnames?.[0]?.fqdn ?? id
const hostnamesList = computed(() => {
  const s = new Set<string>()
  tunnels.value.forEach(t => t.hostnames?.forEach(h => s.add(h.fqdn)))
  return [...s]
})

function buildFilter(): LogFilter {
  const f: LogFilter = { limit: 500 }
  if (fMethod.value) f.method = fMethod.value
  if (fStatus.value) f.status = fStatus.value
  if (fHost.value) f.hostname = fHost.value
  if (fQ.value.trim()) f.q = fQ.value.trim()
  if (fMinDur.value !== '' && Number(fMinDur.value) > 0) f.min_dur = Number(fMinDur.value)
  return f
}

async function applyFilters() {
  pending.value = true
  rows.value = await api.listRequests(buildFilter())
  selected.value = null
  pending.value = false
}

function clearFilters() {
  fMethod.value = ''; fStatus.value = ''; fHost.value = ''; fQ.value = ''; fMinDur.value = ''
  applyFilters()
}

// Canlı akan yeni kaydın mevcut filtreye uyup uymadığı (client-side eşleşme).
function matches(r: RequestLog): boolean {
  if (fMethod.value && r.method !== fMethod.value) return false
  if (fStatus.value) {
    if (/^\dxx$/.test(fStatus.value)) {
      const d = +fStatus.value[0]!
      if (!(r.status >= d * 100 && r.status < d * 100 + 100)) return false
    } else if (r.status !== +fStatus.value) return false
  }
  if (fHost.value && (r.hostname || hostOf(r.tunnel_id)) !== fHost.value) return false
  if (fQ.value.trim() && !r.path.includes(fQ.value.trim())) return false
  if (fMinDur.value !== '' && r.duration_ms < Number(fMinDur.value)) return false
  return true
}

let stopStream: (() => void) | undefined
onMounted(async () => {
  tunnels.value = await api.listTunnels()
  await applyFilters()
  stopStream = api.streamRequests((r) => {
    if (live.value && matches(r)) rows.value = [r, ...rows.value].slice(0, 1000)
  })
})
onUnmounted(() => stopStream?.())

function exportData(fmt: 'csv' | 'json') {
  let content: string, mime: string, ext: string
  if (fmt === 'json') {
    content = JSON.stringify(rows.value, null, 2); mime = 'application/json'; ext = 'json'
  } else {
    const head = 'ts,method,hostname,path,status,duration_ms,client_ip,bytes_in,bytes_out'
    const esc = (s: string) => `"${String(s).replace(/"/g, '""')}"`
    const lines = rows.value.map(r => [
      r.ts, r.method, r.hostname || hostOf(r.tunnel_id), esc(r.path),
      r.status, r.duration_ms, r.client_ip || '', r.bytes_in, r.bytes_out,
    ].join(','))
    content = [head, ...lines].join('\n'); mime = 'text/csv'; ext = 'csv'
  }
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement('a')
  a.href = url; a.download = `zorven-loglar-${Date.now()}.${ext}`; a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="space-y-4">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('requests.title') }}</h1>
        <p class="mt-0.5 text-sm text-fg-muted">{{ t('requests.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors hover:bg-surface-2"
          :class="live ? 'text-accent' : 'text-fg-muted'" :aria-pressed="live" @click="live = !live"
        >
          <span class="size-1.5 rounded-full" :class="live ? 'animate-pulse bg-accent' : 'bg-fg-subtle'" />
          {{ live ? t('requests.live') : t('requests.paused') }}
        </button>
        <button class="cursor-pointer rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors hover:bg-surface-2" @click="exportData('csv')">CSV</button>
        <button class="cursor-pointer rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors hover:bg-surface-2" @click="exportData('json')">JSON</button>
      </div>
    </header>

    <!-- Gelişmiş filtre çubuğu -->
    <div class="flex flex-wrap items-end gap-2 rounded-lg border border-line bg-surface p-3">
      <label class="flex flex-col gap-1">
        <span class="label-sys">{{ t('requests.method') }}</span>
        <select v-model="fMethod" class="rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs">
          <option value="">{{ t('requests.all') }}</option>
          <option v-for="m in methods" :key="m" :value="m">{{ m }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1">
        <span class="label-sys">{{ t('requests.status') }}</span>
        <select v-model="fStatus" class="rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs">
          <option v-for="o in statusOpts" :key="o.v" :value="o.v">{{ o.l }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1">
        <span class="label-sys">{{ t('requests.hostname') }}</span>
        <select v-model="fHost" class="rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs">
          <option value="">{{ t('requests.all') }}</option>
          <option v-for="h in hostnamesList" :key="h" :value="h">{{ h }}</option>
        </select>
      </label>
      <label class="flex flex-1 flex-col gap-1" style="min-width:180px">
        <span class="label-sys">{{ t('requests.pathSearch') }}</span>
        <input v-model="fQ" placeholder="/api/…" class="rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs" @keyup.enter="applyFilters">
      </label>
      <label class="flex flex-col gap-1">
        <span class="label-sys">{{ t('requests.minDur') }}</span>
        <input v-model="fMinDur" type="number" min="0" placeholder="0" class="w-24 rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs" @keyup.enter="applyFilters">
      </label>
      <div class="flex gap-2">
        <button class="cursor-pointer rounded bg-accent px-3 py-1.5 text-xs font-semibold text-on-accent transition hover:opacity-90" @click="applyFilters">{{ t('requests.apply') }}</button>
        <button class="cursor-pointer rounded border border-line px-3 py-1.5 text-xs text-fg-muted transition hover:bg-surface-2" @click="clearFilters">{{ t('requests.clear') }}</button>
      </div>
    </div>

    <!-- İki panel: sol liste + sağ detay (Rufus tarzı) -->
    <div class="grid gap-4 lg:grid-cols-[1fr_340px]">
      <PanelFrame :label="t('requests.requestLog')" :meta="t('requests.records', { n: rows.length })">
        <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
        <p v-else-if="!rows.length" class="px-4 py-8 text-center text-sm text-fg-muted">{{ t('requests.noMatch') }}</p>
        <div v-else class="max-h-[70vh] overflow-auto">
          <table class="w-full text-sm">
            <thead class="sticky top-0 bg-surface">
              <tr class="border-b border-line text-left">
                <th class="label-sys px-3 py-2 font-normal">{{ t('requests.colTime') }}</th>
                <th class="label-sys px-3 py-2 font-normal">{{ t('requests.method') }}</th>
                <th class="label-sys px-3 py-2 font-normal">{{ t('requests.colPath') }}</th>
                <th class="label-sys px-3 py-2 font-normal">{{ t('requests.status') }}</th>
                <th class="label-sys px-3 py-2 font-normal">{{ t('requests.colDuration') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-line">
              <tr
                v-for="r in rows" :key="r.id"
                class="cursor-pointer transition-colors"
                :class="selected?.id === r.id ? 'bg-accent/10' : 'hover:bg-surface-2/40'"
                @click="selected = r"
              >
                <td class="px-3 py-2 font-mono text-[11px] text-fg-subtle">{{ clock(r.ts) }}</td>
                <td class="px-3 py-2"><MethodBadge :method="r.method" /></td>
                <td class="max-w-[280px] truncate px-3 py-2 font-mono text-xs" :title="r.path">{{ r.path }}</td>
                <td class="px-3 py-2"><StatusCode :code="r.status" /></td>
                <td class="px-3 py-2 font-mono text-[11px] tabular-nums text-fg-muted">{{ duration(r.duration_ms) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </PanelFrame>

      <!-- Detay paneli -->
      <PanelFrame :label="t('requests.detail')">
        <div v-if="!selected" class="px-4 py-10 text-center text-sm text-fg-muted">{{ t('requests.selectRequest') }}</div>
        <dl v-else class="space-y-3 p-4 text-sm">
          <div><dt class="label-sys mb-1">{{ t('requests.time') }}</dt><dd class="font-mono text-xs">{{ new Date(selected.ts).toLocaleString('tr-TR') }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.methodStatus') }}</dt><dd class="flex items-center gap-2"><MethodBadge :method="selected.method" /><StatusCode :code="selected.status" /></dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.hostname') }}</dt><dd class="break-all font-mono text-xs text-fg-muted">{{ selected.hostname || hostOf(selected.tunnel_id) }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.colPath') }}</dt><dd class="break-all font-mono text-xs">{{ selected.path }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.colDuration') }}</dt><dd class="font-mono text-xs tabular-nums">{{ duration(selected.duration_ms) }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.clientIp') }}</dt><dd class="font-mono text-xs text-fg-muted">{{ selected.client_ip || '—' }}</dd></div>
          <div class="flex gap-6">
            <div><dt class="label-sys mb-1">{{ t('requests.bytesIn') }}</dt><dd class="font-mono text-xs tabular-nums text-fg-subtle">{{ bytes(selected.bytes_in) }}</dd></div>
            <div><dt class="label-sys mb-1">{{ t('requests.bytesOut') }}</dt><dd class="font-mono text-xs tabular-nums text-fg-subtle">{{ bytes(selected.bytes_out) }}</dd></div>
          </div>
          <div><dt class="label-sys mb-1">{{ t('requests.tunnelId') }}</dt><dd class="break-all font-mono text-[11px] text-fg-subtle">{{ selected.tunnel_id }}</dd></div>
        </dl>
      </PanelFrame>
    </div>
  </div>
</template>
