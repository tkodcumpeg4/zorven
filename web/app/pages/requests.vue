<script setup lang="ts">
import type { RequestLog, Tunnel, LogFilter, RequestDetail, ReplayResult, ReplayOverrides } from '~/types/api'

const api = useApi()
const toast = useToast()
const { duration, bytes, clock } = useFormat()
const { t } = useI18n()
const { isPrivileged } = useRole()

const rows = ref<RequestLog[]>([])
const tunnels = ref<Tunnel[]>([])
const pending = ref(true)
const live = ref(true)
const selected = ref<RequestLog | null>(null)

// --- FAZ 2: İstek inspector (yakalama + detay + replay) ---
const captureEnabled = ref(false)
const captureBusy = ref(false)
const detail = ref<RequestDetail | null>(null)
const detailLoading = ref(false)
const replaying = ref(false)
const replayResult = ref<ReplayResult | null>(null)

async function toggleCapture() {
  captureBusy.value = true
  try {
    captureEnabled.value = await api.setCaptureEnabled(!captureEnabled.value)
    toast.info(captureEnabled.value ? t('requests.captureOn') : t('requests.captureOff'))
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('requests.captureFailed'))
  } finally {
    captureBusy.value = false
  }
}

// --- F08: düzenle & replay ---
const editing = ref(false)
const edMethod = ref('')
const edPath = ref('')
const edQuery = ref('')
const edBody = ref('')
const edHeaders = ref('')        // "Ad: deger" satirlari
const edRemoveHeaders = ref('')  // virgulle ayrilmis
const edTunnelID = ref('')

// Formu yakalamadan doldur. Düzenleme açılınca orijinali göstermek, kullanıcının
// neyi değiştirdiğini görmesini sağlar; boş formda ne değiştiği belirsiz kalırdı.
function resetEditor() {
  const d = detail.value
  edMethod.value = d?.method || ''
  edPath.value = d?.path || ''
  edQuery.value = d?.query || ''
  edBody.value = d?.req_body || ''
  edHeaders.value = headerLines(d?.req_headers)
  edRemoveHeaders.value = ''
  edTunnelID.value = d?.tunnel_id || ''
}

function toggleEditor() {
  editing.value = !editing.value
  if (editing.value) resetEditor()
}

// "Ad: deger" satirlarini haritaya cevirir. Ad bos veya iki nokta yoksa satir atlanir.
function parseHeaderLines(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const i = line.indexOf(':')
    if (i <= 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k) out[k] = v
  }
  return out
}

// Yalnizca GERCEKTEN degisen alanlari gonderiyoruz: degismemis alani gondermek
// sunucuda gereksiz dogrulama yapar ve "neyi degistirdim" bilgisini bulanik birakir.
function buildOverrides(): ReplayOverrides | undefined {
  const d = detail.value
  if (!d) return undefined
  const ov: ReplayOverrides = {}
  if (edMethod.value.trim() && edMethod.value.trim().toUpperCase() !== (d.method || '').toUpperCase()) {
    ov.method = edMethod.value.trim()
  }
  if (edPath.value !== (d.path || '')) ov.path = edPath.value
  if (edQuery.value !== (d.query || '')) ov.query = edQuery.value
  if (edBody.value !== (d.req_body || '')) ov.body = edBody.value
  if (edTunnelID.value.trim() && edTunnelID.value.trim() !== d.tunnel_id) ov.tunnel_id = edTunnelID.value.trim()

  const headers = parseHeaderLines(edHeaders.value)
  const original = parseHeaderLines(headerLines(d.req_headers))
  const changed: Record<string, string> = {}
  for (const [k, v] of Object.entries(headers)) {
    if (original[k] !== v) changed[k] = v
  }
  if (Object.keys(changed).length) ov.headers = changed

  const remove = edRemoveHeaders.value.split(',').map(x => x.trim()).filter(Boolean)
  // Formdan tamamen silinen basliklar da kaldirilmali; aksi halde kullanici
  // satiri sildigi halde baslik gitmezdi.
  for (const k of Object.keys(original)) {
    if (!(k in headers) && !remove.includes(k)) remove.push(k)
  }
  if (remove.length) ov.remove_headers = remove

  return Object.keys(ov).length ? ov : undefined
}

// Seçim değişince tam detayı getir (yakalama varsa).
watch(selected, async (r) => {
  detail.value = null
  replayResult.value = null
  editing.value = false
  if (!r) return
  detailLoading.value = true
  try {
    detail.value = await api.getRequestDetail(r.id)
  } catch {
    detail.value = null // yakalama kapalı veya kayıt düşmüş
  } finally {
    detailLoading.value = false
  }
})

async function doReplay() {
  if (!selected.value) return
  replaying.value = true
  replayResult.value = null
  try {
    replayResult.value = await api.replayRequest(selected.value.id, editing.value ? buildOverrides() : undefined)
    toast.success(t('requests.replayDone'))
  } catch (e: any) {
    toast.error(e?.data?.error?.message || e?.message || t('requests.replayFailed'))
  } finally {
    replaying.value = false
  }
}

function headerLines(h?: Record<string, string[]>): string {
  if (!h) return ''
  return Object.entries(h).map(([k, vs]) => `${k}: ${(vs || []).join(', ')}`).join('\n')
}

// --- Gelişmiş filtreler ---
const fMethod = ref('')
const fStatus = ref('')          // '' | 2xx | 3xx | 4xx | 5xx | kesin kod
const fHost = ref('')
const fQ = ref('')
const fMinDur = ref<number | ''>('')
const fRejected = ref(false)

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
  if (fRejected.value) f.rejected = 'true'
  return f
}

// Hata olursa spinner kalici olmasin; kullaniciya toast gosterilir.
async function applyFilters() {
  pending.value = true
  try {
    rows.value = await api.listRequests(buildFilter())
    selected.value = null
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('requests.loadFailed'))
  } finally {
    pending.value = false
  }
}

function clearFilters() {
  fMethod.value = ''; fStatus.value = ''; fHost.value = ''; fQ.value = ''; fMinDur.value = ''; fRejected.value = false
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
  if (fRejected.value && !r.reject_reason) return false
  return true
}

let stopStream: (() => void) | undefined
// Kullanici await sirasinda sayfadan cikarsa akis unmount'tan sonra acilmasin.
let unmounted = false
onUnmounted(() => {
  unmounted = true
  stopStream?.()
})
onMounted(async () => {
  try { tunnels.value = await api.listTunnels() } catch { /* hostname listesi bos kalir */ }
  try { captureEnabled.value = await api.getCaptureEnabled() } catch { /* yok say */ }
  await applyFilters()
  if (unmounted) return
  stopStream = api.streamRequests((r) => {
    if (live.value && matches(r)) rows.value = [r, ...rows.value].slice(0, 1000)
  })
})

function exportData(fmt: 'csv' | 'json') {
  let content: string, mime: string, ext: string
  if (fmt === 'json') {
    content = JSON.stringify(rows.value, null, 2); mime = 'application/json'; ext = 'json'
  } else {
    const head = 'ts,method,hostname,path,status,duration_ms,client_ip,bytes_in,bytes_out,reject_reason'
    const esc = (s: string) => `"${String(s).replace(/"/g, '""')}"`
    const lines = rows.value.map(r => [
      r.ts, r.method, r.hostname || hostOf(r.tunnel_id), esc(r.path),
      r.status, r.duration_ms, r.client_ip || '', r.bytes_in, r.bytes_out, r.reject_reason || '',
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
          class="flex cursor-pointer items-center gap-1.5 rounded border px-2.5 py-1.5 font-mono text-[11px] transition-colors disabled:opacity-50"
          :class="captureEnabled ? 'border-accent/40 bg-accent/10 text-accent' : 'border-line text-fg-muted hover:bg-surface-2'"
          :aria-pressed="captureEnabled" :disabled="captureBusy || !isPrivileged" :title="isPrivileged ? t('requests.captureHint') : t('common.adminOnly')" @click="toggleCapture"
        >
          <Icon :name="captureEnabled ? 'lucide:circle-dot' : 'lucide:circle'" class="size-3" />
          {{ captureEnabled ? t('requests.captureActive') : t('requests.captureIdle') }}
        </button>
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
      <label class="flex cursor-pointer items-center gap-1.5 pb-1.5 text-xs text-fg-muted">
        <input v-model="fRejected" type="checkbox" @change="applyFilters">
        {{ t('requests.rejectedOnly') }}
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
                <td class="px-3 py-2">
                  <span class="inline-flex items-center gap-1.5"><StatusCode :code="r.status" /><RejectBadge :reason="r.reject_reason" /></span>
                </td>
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
          <div v-if="selected.reject_reason"><dt class="label-sys mb-1">{{ t('requests.rejectReason') }}</dt><dd class="flex items-center gap-2 text-xs"><RejectBadge :reason="selected.reject_reason" /><span class="text-fg-muted">{{ t(`requests.reject.${selected.reject_reason}`) }}</span></dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.colPath') }}</dt><dd class="break-all font-mono text-xs">{{ selected.path }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.colDuration') }}</dt><dd class="font-mono text-xs tabular-nums">{{ duration(selected.duration_ms) }}</dd></div>
          <div><dt class="label-sys mb-1">{{ t('requests.clientIp') }}</dt><dd class="font-mono text-xs text-fg-muted">{{ selected.client_ip || '—' }}</dd></div>
          <div class="flex gap-6">
            <div><dt class="label-sys mb-1">{{ t('requests.bytesIn') }}</dt><dd class="font-mono text-xs tabular-nums text-fg-subtle">{{ bytes(selected.bytes_in) }}</dd></div>
            <div><dt class="label-sys mb-1">{{ t('requests.bytesOut') }}</dt><dd class="font-mono text-xs tabular-nums text-fg-subtle">{{ bytes(selected.bytes_out) }}</dd></div>
          </div>
          <div><dt class="label-sys mb-1">{{ t('requests.tunnelId') }}</dt><dd class="break-all font-mono text-[11px] text-fg-subtle">{{ selected.tunnel_id }}</dd></div>

          <!-- FAZ 2: tam yakalama (header + gövde) + Replay -->
          <div class="border-t border-line pt-3">
            <p v-if="detailLoading" class="text-xs text-fg-muted">{{ t('common.loading') }}</p>
            <div v-else-if="!detail" class="rounded border border-dashed border-line p-2.5 text-[11px] text-fg-subtle">
              {{ captureEnabled ? t('requests.notCaptured') : t('requests.enableCaptureHint') }}
            </div>
            <div v-else class="space-y-3">
              <div class="flex gap-1.5">
                <button
                  :disabled="replaying"
                  class="flex flex-1 cursor-pointer items-center justify-center gap-1.5 rounded bg-accent px-3 py-1.5 text-xs font-semibold text-on-accent transition hover:opacity-90 disabled:opacity-50"
                  @click="doReplay"
                >
                  <Icon name="lucide:repeat-2" class="size-3.5" :class="{ 'animate-spin': replaying }" />
                  {{ replaying ? t('requests.replaying') : (editing ? t('requests.replayEdited') : t('requests.replay')) }}
                </button>
                <button
                  class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 text-xs text-fg-muted transition hover:text-fg"
                  :class="editing ? 'border-accent text-fg' : ''"
                  @click="toggleEditor"
                >
                  <Icon name="lucide:pencil" class="size-3.5" />
                  {{ t('requests.edit') }}
                </button>
              </div>

              <!-- F08: duzenle & replay -->
              <div v-if="editing" class="space-y-1.5 rounded border border-accent/40 bg-surface-1 p-2">
                <div class="flex items-center justify-between">
                  <span class="label-sys">{{ t('requests.editSection') }}</span>
                  <button class="text-[10px] text-fg-muted hover:text-fg" @click="resetEditor">{{ t('requests.resetEdits') }}</button>
                </div>
                <div class="grid grid-cols-3 gap-1.5">
                  <input v-model="edMethod" type="text" placeholder="GET" class="rounded border border-line bg-bg px-2 py-1 font-mono text-[11px] text-fg outline-none focus:border-accent">
                  <input v-model="edPath" type="text" placeholder="/api/x" class="col-span-2 rounded border border-line bg-bg px-2 py-1 font-mono text-[11px] text-fg outline-none focus:border-accent">
                </div>
                <input v-model="edQuery" type="text" :placeholder="t('requests.queryPh')" class="w-full rounded border border-line bg-bg px-2 py-1 font-mono text-[11px] text-fg outline-none focus:border-accent">
                <textarea v-model="edHeaders" rows="3" :placeholder="t('requests.headersPh')" class="w-full rounded border border-line bg-bg px-2 py-1 font-mono text-[10px] text-fg outline-none focus:border-accent" />
                <input v-model="edRemoveHeaders" type="text" :placeholder="t('requests.removeHeadersPh')" class="w-full rounded border border-line bg-bg px-2 py-1 font-mono text-[11px] text-fg outline-none focus:border-accent">
                <textarea v-model="edBody" rows="4" :placeholder="t('requests.bodyPh')" class="w-full rounded border border-line bg-bg px-2 py-1 font-mono text-[10px] text-fg outline-none focus:border-accent" />
                <input v-model="edTunnelID" type="text" :placeholder="t('requests.targetTunnelPh')" class="w-full rounded border border-line bg-bg px-2 py-1 font-mono text-[11px] text-fg outline-none focus:border-accent">
                <p class="text-[10px] leading-relaxed text-fg-subtle">{{ t('requests.editHint') }}</p>
              </div>

              <div v-if="replayResult" class="rounded border border-line bg-surface-1 p-2">
                <div class="mb-1 flex items-center gap-2"><span class="label-sys">{{ t('requests.replayResult') }}</span><StatusCode :code="replayResult.status" /></div>
                <pre class="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono text-[10px] text-fg-muted">{{ replayResult.body }}{{ replayResult.body_truncated ? '…' : '' }}</pre>
              </div>

              <!-- F09: orijinal vs replay farki -->
              <div v-if="replayResult?.diff" class="rounded border border-line bg-surface-1 p-2">
                <div class="mb-1.5 flex items-center gap-2">
                  <span class="label-sys">{{ t('requests.diffSection') }}</span>
                  <span v-if="replayResult.diff.truncated" class="text-[10px] text-warn">({{ t('requests.truncated') }})</span>
                </div>

                <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
                  <span class="flex items-center gap-1">
                    <span class="text-fg-subtle">{{ t('requests.status') }}</span>
                    <StatusCode :code="replayResult.diff.old_status" />
                    <Icon name="lucide:arrow-right" class="size-3 text-fg-subtle" />
                    <StatusCode :code="replayResult.diff.new_status" />
                  </span>
                  <span class="font-mono tabular-nums text-fg-subtle">
                    {{ replayResult.diff.old_duration_ms }}ms &rarr; {{ replayResult.diff.new_duration_ms }}ms
                  </span>
                </div>

                <p v-if="!replayResult.diff.status_changed && !replayResult.diff.headers.length && !replayResult.diff.body.length"
                   class="text-[11px] text-fg-subtle">{{ t('requests.diffNone') }}</p>

                <div v-if="replayResult.diff.headers.length" class="mb-2">
                  <span class="label-sys">{{ t('requests.diffHeaders') }}</span>
                  <div class="mt-1 space-y-0.5">
                    <div v-for="d in replayResult.diff.headers" :key="'h-' + d.path" class="font-mono text-[10px]">
                      <span class="text-fg-muted">{{ d.path }}</span>
                      <span v-if="d.old" class="text-danger"> &minus; {{ d.old }}</span>
                      <span v-if="d.new" class="text-success"> + {{ d.new }}</span>
                    </div>
                  </div>
                </div>

                <div v-if="replayResult.diff.body.length">
                  <span class="label-sys">{{ t('requests.diffBody') }} ({{ replayResult.diff.body_kind }})</span>
                  <div class="mt-1 max-h-40 space-y-0.5 overflow-auto">
                    <div v-for="d in replayResult.diff.body" :key="'b-' + d.path" class="font-mono text-[10px]">
                      <span class="text-fg-muted">{{ d.path }}</span>
                      <span v-if="d.old" class="text-danger"> &minus; {{ d.old }}</span>
                      <span v-if="d.new" class="text-success"> + {{ d.new }}</span>
                    </div>
                  </div>
                </div>
              </div>

              <details open>
                <summary class="cursor-pointer text-xs font-medium text-fg">{{ t('requests.requestSection') }}</summary>
                <div class="mt-1.5 space-y-1.5">
                  <pre v-if="headerLines(detail.req_headers)" class="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono text-[10px] text-fg-muted">{{ headerLines(detail.req_headers) }}</pre>
                  <div v-if="detail.req_body">
                    <span class="label-sys">{{ t('requests.body') }}<span v-if="detail.req_body_truncated" class="text-warn"> ({{ t('requests.truncated') }})</span></span>
                    <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono text-[10px]">{{ detail.req_body }}</pre>
                  </div>
                </div>
              </details>

              <details open>
                <summary class="cursor-pointer text-xs font-medium text-fg">{{ t('requests.responseSection') }}</summary>
                <div class="mt-1.5 space-y-1.5">
                  <pre v-if="headerLines(detail.resp_headers)" class="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono text-[10px] text-fg-muted">{{ headerLines(detail.resp_headers) }}</pre>
                  <div v-if="detail.resp_body">
                    <span class="label-sys">{{ t('requests.body') }}<span v-if="detail.resp_body_truncated" class="text-warn"> ({{ t('requests.truncated') }})</span></span>
                    <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-bg p-2 font-mono text-[10px]">{{ detail.resp_body }}</pre>
                  </div>
                </div>
              </details>
            </div>
          </div>
        </dl>
      </PanelFrame>
    </div>
  </div>
</template>
