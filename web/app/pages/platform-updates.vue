<script setup lang="ts">
const api = useApi()
const toast = useToast()
const { platformAdmin, platformDomain } = useAuth()
const { t, locale } = useI18n()

type Post = {
  id: string; slug: string; kind: string; version: string
  title_tr: string; body_tr: string; title_en: string; body_en: string; title_ja: string; body_ja: string
  maintenance_start: string | null; maintenance_end: string | null
  published_at: string | null; emailed_at: string | null
  email_stats: { total: number, sent: number, failed: number, done: boolean, interrupted?: boolean }
}

const posts = ref<Post[]>([])
const pending = ref(true)
const editing = ref(false)
const editId = ref<string | null>(null)
const saving = ref(false)
const lang = ref<'tr' | 'en' | 'ja'>('tr')

const blank = () => ({
  slug: '', kind: 'update', version: '',
  title_tr: '', body_tr: '', title_en: '', body_en: '', title_ja: '', body_ja: '',
  mStart: '', mEnd: '',
})
const form = reactive(blank())
const current = computed(() => posts.value.find(p => p.id === editId.value) || null)

// Tarayici yerel saati <-> ISO (UTC) donusumu (datetime-local)
function toLocalInput(iso: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}
function toISO(local: string): string | null {
  if (!local) return null
  const d = new Date(local)
  return isNaN(d.getTime()) ? null : d.toISOString()
}

async function load(silent = false) {
  if (!silent) pending.value = true
  try {
    posts.value = (await api.adminListUpdates()).updates ?? []
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.loadFailed'))
  } finally {
    pending.value = false
  }
}

function openNew() {
  Object.assign(form, blank())
  editId.value = null
  lang.value = 'tr'
  editing.value = true
}
function openEdit(p: Post) {
  Object.assign(form, {
    slug: p.slug, kind: p.kind, version: p.version,
    title_tr: p.title_tr, body_tr: p.body_tr, title_en: p.title_en, body_en: p.body_en,
    title_ja: p.title_ja, body_ja: p.body_ja,
    mStart: toLocalInput(p.maintenance_start), mEnd: toLocalInput(p.maintenance_end),
  })
  editId.value = p.id
  lang.value = 'tr'
  editing.value = true
}

function payload() {
  return {
    slug: form.slug.trim().toLowerCase(), kind: form.kind, version: form.version.trim(),
    title_tr: form.title_tr, body_tr: form.body_tr, title_en: form.title_en, body_en: form.body_en,
    title_ja: form.title_ja, body_ja: form.body_ja,
    maintenance_start: form.kind === 'maintenance' ? toISO(form.mStart) : null,
    maintenance_end: form.kind === 'maintenance' ? toISO(form.mEnd) : null,
  }
}

async function save(): Promise<boolean> {
  saving.value = true
  try {
    const saved = await api.adminSaveUpdate(editId.value, payload())
    editId.value = saved.id
    toast.success(t('updatesAdmin.saved'))
    await load()
    return true
  } catch (e: any) {
    const code = e?.data?.error?.code
    toast.error(code === 'slug_taken' ? t('updatesAdmin.slugTaken') : (e?.data?.error?.message || t('updatesAdmin.saveFailed')))
    return false
  } finally {
    saving.value = false
  }
}

async function togglePublish() {
  if (!editId.value) return
  const publish = !current.value?.published_at
  try {
    await api.adminPublishUpdate(editId.value, publish)
    toast.success(t(publish ? 'updatesAdmin.publishedOk' : 'updatesAdmin.unpublishedOk'))
    await load()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.saveFailed'))
  }
}

async function remove() {
  const p = current.value
  if (!p) return
  if (!confirm(t('updatesAdmin.deleteConfirm', { title: p.title_tr }))) return
  try {
    await api.adminDeleteUpdate(p.id)
    toast.success(t('updatesAdmin.deleted'))
    editing.value = false
    await load()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.saveFailed'))
  }
}

// Canli onizleme: sunucu tarafli guvenli render (debounce)
const bodyOf = computed(() => (lang.value === 'tr' ? form.body_tr : lang.value === 'en' ? form.body_en : form.body_ja))
const previewHtml = ref('')
let timer: ReturnType<typeof setTimeout> | null = null
watch([bodyOf, editing], () => {
  if (timer) clearTimeout(timer)
  if (!editing.value) return
  timer = setTimeout(async () => {
    const src = bodyOf.value
    if (!src.trim()) { previewHtml.value = ''; return }
    try { previewHtml.value = await api.adminPreviewUpdate(src) } catch { /* onizleme opsiyonel */ }
  }, 350)
}, { immediate: true })
onBeforeUnmount(() => { if (timer) clearTimeout(timer) })

// Yazi tanitim sitesinde yayinlanir (panel hostunda yok): mutlak, panel diline gore adres.
const siteOrigin = computed(() => {
  const d = platformDomain.value || (import.meta.client ? location.host.replace(/^(panel|app)\./, '') : 'zorven.app')
  return `https://${d}`
})
const publicPath = computed(() => {
  const pre = locale.value === 'en' ? '/en' : locale.value === 'ja' ? '/ja' : ''
  return `${siteOrigin.value}${pre}/updates/${form.slug}`
})

async function sendTest() {
  if (!editId.value) return
  try {
    const r = await api.adminTestUpdateEmail(editId.value, lang.value)
    toast.success(t('updatesAdmin.testSent', { email: r.sent_to }))
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.testFailed'))
  }
}

// Toplu gonderim. Kipler: send (ilk gonderim), resume (yalniz gonderilmemisler),
// retry (yalniz basarisizlar), force (herkese yeniden; onay metni gerekir).
type SendMode = 'send' | 'resume' | 'retry' | 'force'
const FORCE_WORD = 'RESEND'
const confirmOpen = ref(false)
const subscriberCount = ref(0)
const sending = ref(false)
const sendMode = ref<SendMode>('send')
const forceText = ref('')

type Phase = 'none' | 'sending' | 'interrupted' | 'failed' | 'done'
function phaseOf(p: Post | null): Phase {
  if (!p?.emailed_at) return 'none'
  const st = p.email_stats
  if (!st?.done) return st?.interrupted ? 'interrupted' : 'sending'
  return st.failed > 0 ? 'failed' : 'done'
}
const phase = computed(() => phaseOf(current.value))
const progressPct = computed(() => {
  const st = current.value?.email_stats
  if (!st || !st.total) return 0
  return Math.min(100, Math.round(((st.sent + st.failed) / st.total) * 100))
})

async function openSend(mode: SendMode) {
  if (!current.value?.published_at) { toast.warn(t('updatesAdmin.sendNeedsPublish')); return }
  try {
    subscriberCount.value = await api.adminUpdateSubscribers()
    sendMode.value = mode
    forceText.value = ''
    confirmOpen.value = true
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.sendFailed'))
  }
}
// Varsayilan eylem: gonderim yarim kaldiysa devam et; basarisiz varsa yeniden dene; hic gonderilmediyse gonder.
function primaryMode(): SendMode {
  switch (phase.value) {
    case 'interrupted': return 'resume'
    case 'failed': return 'retry'
    default: return 'send'
  }
}
async function doSend() {
  if (!editId.value) return
  if (sendMode.value === 'force' && forceText.value.trim() !== FORCE_WORD) return
  sending.value = true
  try {
    const m = sendMode.value
    const r = await api.adminSendUpdate(editId.value, m === 'force'
      ? { force: true, confirm: FORCE_WORD }
      : { action: m === 'resume' ? 'resume' : m === 'retry' ? 'retry_failed' : 'send' })
    toast.success(t('updatesAdmin.queued', { n: r.queued }))
    confirmOpen.value = false
    await load(true)
  } catch (e: any) {
    const code = e?.data?.error?.code
    toast.error(code === 'already_emailed' ? t('updatesAdmin.alreadyEmailed')
      : code === 'nothing_to_resume' ? t('updatesAdmin.nothingToResume')
      : (e?.data?.error?.message || t('updatesAdmin.sendFailed')))
    await load(true)
  } finally {
    sending.value = false
  }
}

// Gonderim suruyorken ilerlemeyi yokla (4 sn)
let poll: ReturnType<typeof setInterval> | null = null
watch(() => posts.value.some(p => phaseOf(p) === 'sending'), (any) => {
  if (any && !poll) poll = setInterval(() => load(true), 4000)
  else if (!any && poll) { clearInterval(poll); poll = null }
}, { immediate: true })
onBeforeUnmount(() => { if (poll) clearInterval(poll) })

// JSON ice / disa aktarma
type ImportRes = { index: number, slug: string, action: 'create' | 'update' | 'skip' | 'error', publish: boolean, messages?: string[] }
type ImportResp = { dry_run: boolean, counts: Record<string, number>, results: ImportRes[] }
const importOpen = ref(false)
const importText = ref('')
const importMode = ref<'create_only' | 'upsert'>('create_only')
const importPublish = ref(false)
const importBusy = ref(false)
const importPreviewed = ref(false)
const importResp = ref<ImportResp | null>(null)

function openImport() {
  importText.value = ''
  importResp.value = null
  importPreviewed.value = false
  importPublish.value = false
  importMode.value = 'create_only'
  importOpen.value = true
}
// Girdi/secenek degisince onizleme gecersiz olur
watch([importText, importMode, importPublish], () => { importPreviewed.value = false })

async function pickImportFile(ev: Event) {
  const f = (ev.target as HTMLInputElement).files?.[0]
  if (!f) return
  importText.value = await f.text()
  ;(ev.target as HTMLInputElement).value = ''
}
async function runImport(dryRun: boolean) {
  if (!importText.value.trim()) return
  importBusy.value = true
  try {
    const r: ImportResp = await api.adminImportUpdates(importText.value, {
      mode: importMode.value, dryRun, publish: importPublish.value,
    })
    importResp.value = r
    if (dryRun) {
      importPreviewed.value = true
    } else {
      toast.success(t('updatesAdmin.importDone', {
        create: r.counts.create ?? 0, update: r.counts.update ?? 0, skip: r.counts.skip ?? 0, error: r.counts.error ?? 0,
      }))
      importPreviewed.value = false
      await load(true)
    }
  } catch (e: any) {
    importResp.value = null
    toast.error(e?.data?.error?.message || t('updatesAdmin.importFailed'))
  } finally {
    importBusy.value = false
  }
}
async function exportJson() {
  try {
    const data = await api.adminExportUpdates()
    const blob = new Blob([JSON.stringify(data, null, 2) + '\n'], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `zorven-updates-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('updatesAdmin.exportFailed'))
  }
}
const actionKey: Record<string, string> = {
  create: 'updatesAdmin.importActCreate', update: 'updatesAdmin.importActUpdate',
  skip: 'updatesAdmin.importActSkip', error: 'updatesAdmin.importActError',
}
const importCanRun = computed(() => importPreviewed.value && !!importResp.value
  && ((importResp.value.counts.create ?? 0) + (importResp.value.counts.update ?? 0)) > 0)

function kindLabel(k: string) {
  return t(k === 'release' ? 'updatesAdmin.kindRelease' : k === 'maintenance' ? 'updatesAdmin.kindMaintenance' : 'updatesAdmin.kindUpdate')
}
function emailLabel(p: Post) {
  const st = p.email_stats
  switch (phaseOf(p)) {
    case 'none': return t('updatesAdmin.notEmailed')
    case 'sending': return t('updatesAdmin.sendingProgress', { sent: st.sent, failed: st.failed, total: st.total })
    case 'interrupted': return t('updatesAdmin.interruptedProgress', { sent: st.sent, failed: st.failed, total: st.total })
    default: return t('updatesAdmin.emailedAt', { n: st.sent, f: st.failed })
  }
}

onMounted(() => {
  if (!platformAdmin.value) {
    navigateTo('/', { replace: true })
    return
  }
  load()
})
</script>

<template>
  <div v-if="platformAdmin" class="space-y-6">
    <header class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="flex items-center gap-2">
          <Icon name="lucide:megaphone" class="size-6 text-accent" />
          <h1 class="text-xl font-semibold tracking-tight">{{ t('updatesAdmin.title') }}</h1>
        </div>
        <p class="mt-1 text-sm text-fg-muted">{{ t('updatesAdmin.subtitle') }}</p>
      </div>
      <div v-if="!editing" class="flex flex-wrap items-center gap-2">
        <button class="inline-flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-surface" @click="exportJson">
          <Icon name="lucide:download" class="size-4" /> {{ t('updatesAdmin.exportBtn') }}
        </button>
        <button class="inline-flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-surface" @click="openImport">
          <Icon name="lucide:upload" class="size-4" /> {{ t('updatesAdmin.importBtn') }}
        </button>
        <button class="inline-flex items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90" @click="openNew">
          <Icon name="lucide:plus" class="size-4" /> {{ t('updatesAdmin.new') }}
        </button>
      </div>
      <button v-else class="inline-flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-sm text-fg hover:bg-surface" @click="editing = false">
        <Icon name="lucide:arrow-left" class="size-4" /> {{ t('updatesAdmin.back') }}
      </button>
    </header>

    <!-- Liste -->
    <section v-if="!editing" class="rounded-xl border border-line bg-surface/60">
      <p v-if="pending" class="p-5 text-sm text-fg-muted">...</p>
      <p v-else-if="!posts.length" class="p-5 text-sm text-fg-muted">{{ t('updatesAdmin.empty') }}</p>
      <table v-else class="w-full text-sm">
        <thead class="text-left text-xs text-fg-muted">
          <tr>
            <th class="px-4 py-2">{{ t('updatesAdmin.colTitle') }}</th>
            <th class="px-4 py-2">{{ t('updatesAdmin.colKind') }}</th>
            <th class="px-4 py-2">{{ t('updatesAdmin.colStatus') }}</th>
            <th class="px-4 py-2">{{ t('updatesAdmin.colEmail') }}</th>
            <th class="px-4 py-2" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in posts" :key="p.id" class="border-t border-line">
            <td class="px-4 py-2.5">
              <div class="font-medium text-fg">{{ p.title_tr }}</div>
              <div class="text-xs text-fg-muted">/{{ p.slug }}<span v-if="p.version"> · {{ p.version }}</span></div>
            </td>
            <td class="px-4 py-2.5">{{ kindLabel(p.kind) }}</td>
            <td class="px-4 py-2.5">
              <span :class="p.published_at ? 'text-accent' : 'text-fg-muted'">{{ p.published_at ? t('updatesAdmin.published') : t('updatesAdmin.draft') }}</span>
            </td>
            <td class="px-4 py-2.5 text-xs text-fg-muted">{{ emailLabel(p) }}</td>
            <td class="px-4 py-2.5 text-right">
              <button class="rounded-md border border-line px-3 py-1 text-xs hover:bg-surface" @click="openEdit(p)">{{ t('updatesAdmin.edit') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- Duzenleyici -->
    <section v-else class="space-y-4">
      <div class="grid gap-4 rounded-xl border border-line bg-surface/60 p-5 sm:grid-cols-4">
        <label class="block text-xs text-fg-muted sm:col-span-2">{{ t('updatesAdmin.slug') }}
          <input v-model="form.slug" type="text" maxlength="80" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
          <span class="mt-0.5 block">{{ t('updatesAdmin.slugHint') }}</span>
        </label>
        <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.kind') }}
          <select v-model="form.kind" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg">
            <option value="release">{{ t('updatesAdmin.kindRelease') }}</option>
            <option value="update">{{ t('updatesAdmin.kindUpdate') }}</option>
            <option value="maintenance">{{ t('updatesAdmin.kindMaintenance') }}</option>
          </select>
        </label>
        <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.version') }}
          <input v-model="form.version" type="text" maxlength="40" placeholder="0.2.9" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
        </label>
        <template v-if="form.kind === 'maintenance'">
          <label class="block text-xs text-fg-muted sm:col-span-2">{{ t('updatesAdmin.mStart') }}
            <input v-model="form.mStart" type="datetime-local" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg">
          </label>
          <label class="block text-xs text-fg-muted sm:col-span-2">{{ t('updatesAdmin.mEnd') }}
            <input v-model="form.mEnd" type="datetime-local" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg">
          </label>
          <p class="text-xs text-fg-muted sm:col-span-4">{{ t('updatesAdmin.mHint') }}</p>
        </template>
      </div>

      <div class="rounded-xl border border-line bg-surface/60 p-5">
        <div class="flex gap-1 border-b border-line pb-2">
          <button v-for="l in (['tr', 'en', 'ja'] as const)" :key="l" type="button"
            class="rounded-md px-3 py-1 text-sm uppercase" :class="lang === l ? 'bg-accent text-on-accent' : 'text-fg-muted hover:bg-surface'"
            @click="lang = l">{{ l }}</button>
        </div>
        <p class="mt-2 text-xs text-fg-muted">{{ t('updatesAdmin.trRequired') }}</p>
        <div class="mt-3 grid gap-4 lg:grid-cols-2">
          <div class="space-y-3">
            <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.titleField') }}
              <input v-if="lang === 'tr'" v-model="form.title_tr" type="text" maxlength="200" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
              <input v-else-if="lang === 'en'" v-model="form.title_en" type="text" maxlength="200" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
              <input v-else v-model="form.title_ja" type="text" maxlength="200" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
            </label>
            <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.bodyField') }}
              <textarea v-if="lang === 'tr'" v-model="form.body_tr" rows="16" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-sm text-fg focus:border-accent focus:outline-none" />
              <textarea v-else-if="lang === 'en'" v-model="form.body_en" rows="16" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-sm text-fg focus:border-accent focus:outline-none" />
              <textarea v-else v-model="form.body_ja" rows="16" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-sm text-fg focus:border-accent focus:outline-none" />
              <span class="mt-0.5 block">{{ t('updatesAdmin.bodyHint') }}</span>
            </label>
          </div>
          <div>
            <div class="text-xs text-fg-muted">{{ t('updatesAdmin.preview') }}</div>
            <!-- Sunucu tarafli guvenli render: ham HTML kacirilir, yalniz http/https/mailto baglantilari -->
            <div v-if="previewHtml" class="prose prose-invert mt-1 max-w-none rounded-md border border-line bg-bg p-4 text-sm" v-html="previewHtml" />
            <p v-else class="mt-1 rounded-md border border-dashed border-line p-4 text-sm text-fg-muted">{{ t('updatesAdmin.previewEmpty') }}</p>
          </div>
        </div>
      </div>

      <div v-if="phase !== 'none' && current" class="rounded-xl border border-line bg-surface/60 p-4">
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span class="font-medium text-fg">{{ t('updatesAdmin.deliveryTitle') }}</span>
          <span class="text-xs" :class="phase === 'interrupted' ? 'text-amber-400' : 'text-fg-muted'">{{ emailLabel(current) }}</span>
        </div>
        <div class="mt-2 h-2 overflow-hidden rounded-full bg-bg">
          <div class="h-full bg-accent transition-all" :style="{ width: progressPct + '%' }" />
        </div>
        <p v-if="phase === 'interrupted'" class="mt-2 text-xs text-amber-400">{{ t('updatesAdmin.interruptedHint') }}</p>
        <p v-if="current.email_stats.failed > 0 && phase !== 'sending'" class="mt-2 text-xs text-fg-muted">{{ t('updatesAdmin.failedHint', { f: current.email_stats.failed }) }}</p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button :disabled="saving" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="save">
          {{ saving ? '...' : t('updatesAdmin.save') }}
        </button>
        <template v-if="editId">
          <button class="rounded-md border border-line px-4 py-2 text-sm hover:bg-surface" @click="togglePublish">
            {{ current?.published_at ? t('updatesAdmin.unpublish') : t('updatesAdmin.publish') }}
          </button>
          <a v-if="current?.published_at" :href="publicPath" target="_blank" rel="noopener" class="inline-flex items-center gap-1 rounded-md border border-line px-4 py-2 text-sm hover:bg-surface">
            <Icon name="lucide:external-link" class="size-4" /> {{ t('updatesAdmin.viewPublic') }}
          </a>
          <button class="inline-flex items-center gap-1 rounded-md border border-line px-4 py-2 text-sm hover:bg-surface" @click="sendTest">
            <Icon name="lucide:mail" class="size-4" /> {{ t('updatesAdmin.testEmail') }}
          </button>
          <button v-if="phase === 'none'" class="inline-flex items-center gap-1 rounded-md border border-accent px-4 py-2 text-sm text-accent hover:bg-accent/10" @click="openSend('send')">
            <Icon name="lucide:send" class="size-4" /> {{ t('updatesAdmin.send') }}
          </button>
          <button v-else-if="phase === 'interrupted'" class="inline-flex items-center gap-1 rounded-md border border-accent px-4 py-2 text-sm text-accent hover:bg-accent/10" @click="openSend(primaryMode())">
            <Icon name="lucide:play" class="size-4" /> {{ t('updatesAdmin.resume') }}
          </button>
          <button v-else-if="phase === 'failed'" class="inline-flex items-center gap-1 rounded-md border border-accent px-4 py-2 text-sm text-accent hover:bg-accent/10" @click="openSend(primaryMode())">
            <Icon name="lucide:rotate-cw" class="size-4" /> {{ t('updatesAdmin.retryFailed', { f: current?.email_stats.failed }) }}
          </button>
          <button v-if="phase === 'done' || phase === 'failed' || phase === 'interrupted'" class="inline-flex items-center gap-1 rounded-md border border-line px-4 py-2 text-sm text-red-400 hover:bg-surface" @click="openSend('force')">
            <Icon name="lucide:send" class="size-4" /> {{ t('updatesAdmin.forceResend') }}
          </button>
          <button class="ml-auto inline-flex items-center gap-1 rounded-md border border-line px-4 py-2 text-sm text-red-400 hover:bg-surface" @click="remove">
            <Icon name="lucide:trash-2" class="size-4" /> {{ t('updatesAdmin.delete') }}
          </button>
        </template>
      </div>
    </section>

    <!-- JSON ice aktarma -->
    <div v-if="importOpen" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" @click.self="importOpen = false">
      <div class="flex max-h-[90vh] w-full max-w-3xl flex-col gap-3 overflow-y-auto rounded-xl border border-line bg-bg p-5 shadow-xl">
        <h3 class="text-base font-semibold text-fg">{{ t('updatesAdmin.importTitle') }}</h3>
        <p class="text-sm text-fg-muted">{{ t('updatesAdmin.importHint') }}</p>
        <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.importPaste') }}
          <textarea v-model="importText" rows="9" spellcheck="false" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-xs text-fg focus:border-accent focus:outline-none" />
        </label>
        <div class="flex flex-wrap items-end gap-4">
          <label class="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-line px-3 py-2 text-sm text-fg hover:bg-surface">
            <Icon name="lucide:file-json" class="size-4" /> {{ t('updatesAdmin.importFile') }}
            <input type="file" accept=".json,application/json" class="hidden" @change="pickImportFile">
          </label>
          <label class="block text-xs text-fg-muted">{{ t('updatesAdmin.importMode') }}
            <select v-model="importMode" class="mt-1 block rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg">
              <option value="create_only">{{ t('updatesAdmin.importModeCreate') }}</option>
              <option value="upsert">{{ t('updatesAdmin.importModeUpsert') }}</option>
            </select>
          </label>
          <label class="flex items-center gap-2 text-sm text-fg">
            <input v-model="importPublish" type="checkbox" class="size-4 accent-[var(--accent,currentColor)]">
            <span>{{ t('updatesAdmin.importPublish') }}
              <span class="block text-xs text-fg-muted">{{ t('updatesAdmin.importPublishHint') }}</span></span>
          </label>
        </div>

        <div v-if="importResp" class="space-y-2">
          <p class="text-sm text-fg">{{ t('updatesAdmin.importCounts', { create: importResp.counts.create ?? 0, update: importResp.counts.update ?? 0, skip: importResp.counts.skip ?? 0, error: importResp.counts.error ?? 0 }) }}</p>
          <div class="overflow-x-auto rounded-md border border-line">
            <table class="w-full text-sm">
              <thead class="text-left text-xs text-fg-muted">
                <tr>
                  <th class="px-3 py-2">#</th>
                  <th class="px-3 py-2">{{ t('updatesAdmin.importColSlug') }}</th>
                  <th class="px-3 py-2">{{ t('updatesAdmin.importColAction') }}</th>
                  <th class="px-3 py-2">{{ t('updatesAdmin.importColNote') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in importResp.results" :key="r.index" class="border-t border-line align-top">
                  <td class="px-3 py-2 text-fg-muted">{{ r.index + 1 }}</td>
                  <td class="px-3 py-2 font-mono text-xs">{{ r.slug || '-' }}</td>
                  <td class="px-3 py-2" :class="r.action === 'error' ? 'text-red-400' : r.action === 'skip' ? 'text-fg-muted' : 'text-accent'">
                    {{ t(actionKey[r.action] || 'updatesAdmin.importActError') }}<span v-if="r.publish"> · {{ t('updatesAdmin.importWillPublish') }}</span>
                  </td>
                  <td class="px-3 py-2 text-xs text-fg-muted">
                    <div v-for="(m, i) in r.messages || []" :key="i">{{ m }}</div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <p v-else class="text-xs text-fg-muted">{{ t('updatesAdmin.importNeedPreview') }}</p>

        <div class="flex justify-end gap-2">
          <button class="rounded-md border border-line px-4 py-2 text-sm hover:bg-surface" @click="importOpen = false">{{ t('updatesAdmin.importClose') }}</button>
          <button :disabled="importBusy || !importText.trim()" class="rounded-md border border-line px-4 py-2 text-sm hover:bg-surface disabled:opacity-50" @click="runImport(true)">
            {{ importBusy ? '...' : t('updatesAdmin.importPreview') }}
          </button>
          <button :disabled="importBusy || !importCanRun" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="runImport(false)">
            {{ importBusy ? '...' : t('updatesAdmin.importRun') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Gonderim onayi -->
    <div v-if="confirmOpen" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" @click.self="confirmOpen = false">
      <div class="w-full max-w-md rounded-xl border border-line bg-bg p-5 shadow-xl">
        <h3 class="text-base font-semibold text-fg">{{ t(sendMode === 'force' ? 'updatesAdmin.forceTitle' : sendMode === 'resume' ? 'updatesAdmin.resumeTitle' : sendMode === 'retry' ? 'updatesAdmin.retryTitle' : 'updatesAdmin.sendConfirmTitle') }}</h3>
        <p class="mt-2 text-sm text-fg-muted">{{ t(sendMode === 'resume' ? 'updatesAdmin.resumeBody' : sendMode === 'retry' ? 'updatesAdmin.retryBody' : 'updatesAdmin.sendConfirmBody', { n: subscriberCount, f: current?.email_stats.failed ?? 0 }) }}</p>
        <template v-if="sendMode === 'force'">
          <p class="mt-2 text-sm text-amber-400">{{ t('updatesAdmin.sendConfirmForce') }}</p>
          <label class="mt-3 block text-xs text-fg-muted">{{ t('updatesAdmin.forceTypeHint', { word: FORCE_WORD }) }}
            <input v-model="forceText" type="text" autocomplete="off" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 font-mono text-sm text-fg focus:border-accent focus:outline-none">
          </label>
        </template>
        <div class="mt-4 flex justify-end gap-2">
          <button class="rounded-md border border-line px-4 py-2 text-sm hover:bg-surface" @click="confirmOpen = false">{{ t('updatesAdmin.cancel') }}</button>
          <button :disabled="sending || subscriberCount === 0 || (sendMode === 'force' && forceText.trim() !== FORCE_WORD)" class="rounded-md bg-accent px-4 py-2 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" @click="doSend">
            {{ sending ? '...' : sendMode === 'resume' ? t('updatesAdmin.resume') : sendMode === 'retry' ? t('updatesAdmin.retryFailedShort') : t('updatesAdmin.sendConfirmBtn', { n: subscriberCount }) }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
