<script setup lang="ts">
import type { CaptchaSite } from '~/types/api'

// Zorven CAPTCHA: site anahtarlari, alan listesi, zorluk, gecis cerezi, sayaclar ve
// gomme/dogrulama ornekleri. Yazma owner/admin; okuma member'a acik.
const api = useApi()
const toast = useToast()
const { platformAdmin, user: currentUser } = useAuth()
const { t } = useI18n()

const canManage = computed(() => {
  if (platformAdmin.value) return true
  const r = (currentUser.value?.role || '').toLowerCase()
  return r === 'owner' || r === 'admin'
})

function errMsg(e: any, fallback: string): string {
  const err = e?.data?.error
  return (typeof err === 'string' ? err : err?.message) || e?.message || fallback
}

const loading = ref(true)
const sites = ref<CaptchaSite[]>([])
const expanded = ref<string | null>(null)

async function load() {
  loading.value = true
  try {
    sites.value = (await api.listCaptchaSites()).sites ?? []
  } catch (e: any) {
    toast.error(errMsg(e, t('captcha.loadError')))
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(t('captcha.copied'))
  } catch { /* pano erisimi yok */ }
}

// ---- olustur / duzenle ----
const DIFFS = [15, 17, 20]
const TTLS = [30, 60, 1440, 10080, 43200]
const showForm = ref(false)
const editing = ref<CaptchaSite | null>(null)
const saving = ref(false)
const form = reactive({
  name: '',
  domains: [] as string[],
  difficulty: 17,
  passEnabled: false,
  passTtl: 1440,
})
const domainDraft = ref('')
const domainError = ref('')

function openCreate() {
  editing.value = null
  Object.assign(form, { name: '', domains: [], difficulty: 17, passEnabled: false, passTtl: 1440 })
  domainDraft.value = ''
  domainError.value = ''
  showForm.value = true
}
function openEdit(s: CaptchaSite) {
  editing.value = s
  Object.assign(form, {
    name: s.name, domains: [...s.domains], difficulty: s.difficulty,
    passEnabled: s.pass_cookie.enabled, passTtl: s.pass_cookie.ttl_minutes,
  })
  domainDraft.value = ''
  domainError.value = ''
  showForm.value = true
}

// Alan adi: sema/port/yol atilir, "*.ornek.com" alt alanlar icin; sunucu da dogrular.
function normDomain(raw: string): string {
  let d = raw.trim().toLowerCase()
  d = d.replace(/^[a-z]+:\/\//, '').replace(/[/?#].*$/, '').replace(/:\d+$/, '').replace(/\.$/, '')
  return d
}
function addDomain() {
  const d = normDomain(domainDraft.value)
  if (!d) return
  if (!/^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/.test(d)) { domainError.value = t('captcha.domainErrInvalid'); return }
  if (form.domains.includes(d)) { domainError.value = t('captcha.domainErrDup'); return }
  if (form.domains.length >= 50) { domainError.value = t('captcha.domainErrMax'); return }
  domainError.value = ''
  form.domains.push(d)
  domainDraft.value = ''
}
function removeDomain(d: string) {
  form.domains = form.domains.filter(x => x !== d)
}

// Gizli anahtar BIR KEZ gosterilir.
const revealed = ref<{ name: string, siteKey: string, secret: string } | null>(null)

async function save() {
  if (!canManage.value || !form.name.trim()) return
  if (domainDraft.value.trim()) addDomain()
  saving.value = true
  const payload = {
    name: form.name.trim(),
    domains: [...form.domains],
    difficulty: form.difficulty,
    pass_cookie: { enabled: form.passEnabled, ttl_minutes: form.passTtl },
  }
  try {
    if (editing.value) {
      await api.updateCaptchaSite(editing.value.id, payload)
      toast.success(t('captcha.saved'))
    } else {
      const created = await api.createCaptchaSite(payload)
      revealed.value = { name: created.name, siteKey: created.site_key, secret: created.secret || '' }
    }
    showForm.value = false
    await load()
  } catch (e: any) {
    toast.error(errMsg(e, editing.value ? t('captcha.updateError') : t('captcha.createError')))
  } finally {
    saving.value = false
  }
}

const busyId = ref<string | null>(null)
async function rotate(s: CaptchaSite) {
  if (!confirm(t('captcha.rotateConfirm', { name: s.name }))) return
  busyId.value = s.id
  try {
    const r = await api.rotateCaptchaSecret(s.id)
    revealed.value = { name: r.name, siteKey: r.site_key, secret: r.secret || '' }
    await load()
  } catch (e: any) {
    toast.error(errMsg(e, t('captcha.rotateError')))
  } finally {
    busyId.value = null
  }
}
async function remove(s: CaptchaSite) {
  if (!confirm(t('captcha.deleteConfirm', { name: s.name }))) return
  busyId.value = s.id
  try {
    await api.deleteCaptchaSite(s.id)
    toast.success(t('captcha.deleted'))
    await load()
  } catch (e: any) {
    toast.error(errMsg(e, t('captcha.deleteError')))
  } finally {
    busyId.value = null
  }
}

// ---- gomme ve dogrulama ornekleri ----
const origin = computed(() => {
  if (!import.meta.client) return 'https://zorven.app'
  const host = location.hostname.replace(/^(panel|app)\./, '')
  return `${location.protocol}//${host}${location.port ? ':' + location.port : ''}`
})
const lang = ref<'curl' | 'node' | 'php' | 'go'>('curl')
const LANGS = ['curl', 'node', 'php', 'go'] as const

function embedSnippet(s: CaptchaSite): string {
  return `<script src="${origin.value}/captcha/v1/api.js" async defer><\/script>\n\n<form method="POST" action="/giris">\n  <div class="zorven-captcha" data-sitekey="${s.site_key}"></div>\n  <button type="submit">Gönder</button>\n</form>`
}
function verifySnippet(l: string): string {
  const u = `${origin.value}/api/v1/captcha/siteverify`
  if (l === 'curl') {
    return `curl -s ${u} \\\n  -d "secret=$ZORVEN_CAPTCHA_SECRET" \\\n  -d "response=$ZORVEN_CAPTCHA_RESPONSE"`
  }
  if (l === 'node') {
    return `const r = await fetch('${u}', {\n  method: 'POST',\n  headers: { 'Content-Type': 'application/json' },\n  body: JSON.stringify({\n    secret: process.env.ZORVEN_CAPTCHA_SECRET,\n    response: req.body['zorven-captcha-response'],\n  }),\n})\nconst out = await r.json()\nif (!out.success) return res.status(400).send('captcha')`
  }
  if (l === 'php') {
    return `$ch = curl_init('${u}');\ncurl_setopt_array($ch, [\n  CURLOPT_POST => true,\n  CURLOPT_RETURNTRANSFER => true,\n  CURLOPT_POSTFIELDS => http_build_query([\n    'secret'   => getenv('ZORVEN_CAPTCHA_SECRET'),\n    'response' => $_POST['zorven-captcha-response'] ?? '',\n  ]),\n]);\n$out = json_decode(curl_exec($ch), true);\nif (empty($out['success'])) { http_response_code(400); exit; }`
  }
  return `form := url.Values{\n\t"secret":   {os.Getenv("ZORVEN_CAPTCHA_SECRET")},\n\t"response": {r.FormValue("zorven-captcha-response")},\n}\nresp, err := http.PostForm("${u}", form)\nif err != nil { /* hata */ }\ndefer resp.Body.Close()\nvar out struct{ Success bool \`json:"success"\` }\njson.NewDecoder(resp.Body).Decode(&out)\nif !out.Success { http.Error(w, "captcha", 400); return }`
}

function diffLabel(d: number): string {
  if (d === 15) return t('captcha.diffLight')
  if (d === 17) return t('captcha.diffNormal')
  if (d === 20) return t('captcha.diffStrong')
  return t('captcha.diffCustom', { n: d })
}
function ttlLabel(m: number): string {
  const map: Record<number, string> = { 30: 'ttl30m', 60: 'ttl1h', 1440: 'ttl1d', 10080: 'ttl7d', 43200: 'ttl30d' }
  return map[m] ? t('captcha.' + map[m]) : t('captcha.ttlCustom', { n: m })
}
const ttlOptions = computed(() => (TTLS.includes(form.passTtl) ? TTLS : [...TTLS, form.passTtl].sort((a, b) => a - b)))
const diffOptions = computed(() => (DIFFS.includes(form.difficulty) ? DIFFS : [...DIFFS, form.difficulty].sort((a, b) => a - b)))
</script>

<template>
  <div class="mx-auto max-w-5xl px-4 py-6 sm:px-5">
    <div class="mb-6 flex items-start justify-between gap-4">
      <div>
        <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
          <Icon name="lucide:bot" class="size-5 text-accent" />
          {{ t('captcha.title') }}
        </h1>
        <p class="mt-1 text-sm text-fg-muted">{{ t('captcha.subtitle') }}</p>
      </div>
      <button
        v-if="canManage"
        class="inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-accent/90 active:scale-95"
        @click="openCreate"
      >
        <Icon name="lucide:plus" class="size-4" />
        {{ t('captcha.create') }}
      </button>
    </div>

    <div v-if="loading" class="py-12 text-center text-fg-muted">
      <Icon name="lucide:loader-circle" class="mx-auto size-6 animate-spin" />
    </div>

    <div v-else-if="sites.length === 0" class="rounded-xl border border-dashed border-line bg-surface p-10 text-center">
      <Icon name="lucide:bot" class="mx-auto size-8 text-fg-muted" />
      <p class="mt-3 text-sm text-fg-muted">{{ t('captcha.empty') }}</p>
    </div>

    <div v-else class="space-y-4">
      <div v-for="s in sites" :key="s.id" class="rounded-xl border border-line bg-surface p-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="truncate text-base font-semibold text-fg">{{ s.name }}</h2>
            <div class="mt-1 flex items-center gap-1.5">
              <span class="text-xs text-fg-muted">{{ t('captcha.colSiteKey') }}</span>
              <code class="break-all rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-fg">{{ s.site_key }}</code>
              <button type="button" class="text-fg-muted hover:text-fg" :title="t('common.copy')" @click="copy(s.site_key)">
                <Icon name="lucide:copy" class="size-3.5" />
              </button>
            </div>
          </div>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              class="inline-flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-accent hover:text-fg"
              @click="expanded = expanded === s.id ? null : s.id"
            >
              <Icon name="lucide:code-xml" class="size-3.5" />
              {{ t('captcha.integration') }}
            </button>
            <template v-if="canManage">
              <button class="inline-flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-accent hover:text-fg" @click="openEdit(s)">
                <Icon name="lucide:pencil" class="size-3.5" />
                {{ t('common.edit') }}
              </button>
              <button class="inline-flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-accent hover:text-fg disabled:opacity-50" :disabled="busyId === s.id" @click="rotate(s)">
                <Icon name="lucide:refresh-cw" class="size-3.5" />
                {{ t('captcha.rotate') }}
              </button>
              <button class="inline-flex items-center gap-1 rounded-lg border border-line px-2.5 py-1 text-xs text-fg-muted hover:border-danger/40 hover:text-danger disabled:opacity-50" :disabled="busyId === s.id" @click="remove(s)">
                <Icon name="lucide:trash-2" class="size-3.5" />
                {{ t('common.delete') }}
              </button>
            </template>
          </div>
        </div>

        <div class="mt-3 flex flex-wrap items-center gap-1.5 text-[11px]">
          <span class="rounded-full border border-line bg-surface-2 px-2.5 py-0.5 text-fg">{{ t('captcha.colDifficulty') }}: {{ diffLabel(s.difficulty) }} ({{ s.difficulty }})</span>
          <span v-if="s.pass_cookie.enabled" class="rounded-full border border-line bg-surface-2 px-2.5 py-0.5 text-fg">{{ t('captcha.passBadge', { ttl: ttlLabel(s.pass_cookie.ttl_minutes) }) }}</span>
          <span v-if="s.domains.length === 0" class="rounded-full border border-line bg-surface-2 px-2.5 py-0.5 text-fg-muted">{{ t('captcha.anyDomain') }}</span>
          <span v-for="d in s.domains" :key="d" class="rounded-full border border-line bg-surface-2 px-2.5 py-0.5 font-mono text-fg">{{ d }}</span>
        </div>

        <div v-if="s.stats" class="mt-3 grid gap-3 sm:grid-cols-2">
          <div v-for="(row, key) in { today: s.stats.today, last: s.stats.last_7_days }" :key="key" class="rounded-lg border border-line bg-bg p-3">
            <p class="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-fg-muted">{{ key === 'today' ? t('captcha.statToday') : t('captcha.stat7d') }}</p>
            <dl class="grid grid-cols-3 gap-x-3 gap-y-1.5 text-xs">
              <div><dt class="text-fg-muted">{{ t('captcha.statChallenge') }}</dt><dd class="font-mono text-fg">{{ row.challenge }}</dd></div>
              <div><dt class="text-fg-muted">{{ t('captcha.statSolve') }}</dt><dd class="font-mono text-fg">{{ row.solve }}</dd></div>
              <div><dt class="text-fg-muted">{{ t('captcha.statOk') }}</dt><dd class="font-mono text-fg">{{ row.verify_ok }}</dd></div>
              <div><dt class="text-fg-muted">{{ t('captcha.statFail') }}</dt><dd class="font-mono text-fg">{{ row.verify_fail }}</dd></div>
              <div><dt class="text-fg-muted">{{ t('captcha.statRenewals') }}</dt><dd class="font-mono text-fg">{{ row.renewals }}</dd></div>
              <div><dt class="text-fg-muted">{{ t('captcha.statPasses') }}</dt><dd class="font-mono text-fg">{{ row.unique_passes }}</dd></div>
            </dl>
          </div>
        </div>

        <!-- Entegrasyon -->
        <div v-if="expanded === s.id" class="mt-4 space-y-4 border-t border-line pt-4">
          <div>
            <div class="mb-1 flex items-center justify-between">
              <h3 class="text-sm font-medium text-fg">{{ t('captcha.embedTitle') }}</h3>
              <button class="inline-flex items-center gap-1 text-xs text-fg-muted hover:text-fg" @click="copy(embedSnippet(s))">
                <Icon name="lucide:copy" class="size-3.5" />{{ t('common.copy') }}
              </button>
            </div>
            <p class="mb-2 text-xs text-fg-muted">{{ t('captcha.embedHint') }}</p>
            <pre class="overflow-x-auto rounded-lg border border-line bg-bg p-3 font-mono text-xs text-fg">{{ embedSnippet(s) }}</pre>
          </div>
          <div>
            <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
              <h3 class="text-sm font-medium text-fg">{{ t('captcha.verifyTitle') }}</h3>
              <div class="flex items-center gap-1">
                <button
                  v-for="l in LANGS" :key="l"
                  class="rounded-md border px-2 py-0.5 text-xs"
                  :class="lang === l ? 'border-accent text-fg' : 'border-line text-fg-muted hover:text-fg'"
                  @click="lang = l"
                >{{ l === 'node' ? 'Node.js' : l === 'php' ? 'PHP' : l === 'go' ? 'Go' : 'curl' }}</button>
                <button class="ml-1 inline-flex items-center gap-1 text-xs text-fg-muted hover:text-fg" @click="copy(verifySnippet(lang))">
                  <Icon name="lucide:copy" class="size-3.5" />{{ t('common.copy') }}
                </button>
              </div>
            </div>
            <p class="mb-2 text-xs text-fg-muted">{{ t('captcha.verifyHint') }}</p>
            <pre class="overflow-x-auto rounded-lg border border-line bg-bg p-3 font-mono text-xs text-fg">{{ verifySnippet(lang) }}</pre>
          </div>
        </div>
      </div>
    </div>

    <p class="mt-4 text-xs text-fg-muted">{{ t('captcha.privacyNote') }}</p>

    <!-- Olustur / duzenle -->
    <div v-if="showForm" class="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/40 p-4" @click.self="showForm = false">
      <div class="w-full max-w-lg rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="text-lg font-semibold text-fg">{{ editing ? t('captcha.edit') : t('captcha.create') }}</h2>
        <div class="mt-4 space-y-4">
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('captcha.fieldName') }}</label>
            <input v-model="form.name" type="text" maxlength="100" class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('captcha.fieldDomains') }}</label>
            <div v-if="form.domains.length" class="mb-1.5 flex flex-wrap gap-1.5">
              <span v-for="d in form.domains" :key="d" class="flex items-center gap-1 rounded-full border border-line bg-surface-2 px-2.5 py-0.5 font-mono text-[11px] text-fg">
                {{ d }}
                <button type="button" class="text-fg-muted hover:text-fg" @click="removeDomain(d)"><Icon name="lucide:x" class="size-3" /></button>
              </span>
            </div>
            <div class="flex gap-2">
              <input v-model="domainDraft" type="text" :placeholder="t('captcha.domainPlaceholder')" class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent" @keydown.enter.prevent="addDomain">
              <button type="button" class="shrink-0 rounded border border-line px-3 py-1.5 text-xs text-fg hover:border-accent" @click="addDomain">{{ t('common.add') }}</button>
            </div>
            <p v-if="domainError" class="mt-1 text-[11px] text-danger">{{ domainError }}</p>
            <p v-else class="mt-1 text-[11px] text-fg-muted">{{ t('captcha.domainsHint') }}</p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('captcha.fieldDifficulty') }}</label>
            <select v-model.number="form.difficulty" class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
              <option v-for="d in diffOptions" :key="d" :value="d">{{ diffLabel(d) }} ({{ d }})</option>
            </select>
          </div>
          <div class="rounded-lg border border-line bg-bg p-3">
            <label class="flex items-start gap-2 text-sm text-fg">
              <input v-model="form.passEnabled" type="checkbox" class="mt-0.5">
              <span>
                {{ t('captcha.passEnable') }}
                <span class="mt-0.5 block text-xs text-fg-muted">{{ t('captcha.passHint') }}</span>
              </span>
            </label>
            <div v-if="form.passEnabled" class="mt-3">
              <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('captcha.passTtl') }}</label>
              <select v-model.number="form.passTtl" class="w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none">
                <option v-for="m in ttlOptions" :key="m" :value="m">{{ ttlLabel(m) }}</option>
              </select>
            </div>
          </div>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <button class="rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg" @click="showForm = false">{{ t('common.cancel') }}</button>
          <button class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving || !form.name.trim()" @click="save">
            <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
            {{ editing ? t('common.save') : t('common.create') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Gizli anahtar bir kez gosterilir -->
    <div v-if="revealed" class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4">
      <div class="w-full max-w-lg rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="flex items-center gap-2 text-lg font-semibold text-fg">
          <Icon name="lucide:key-round" class="size-5 text-accent" />
          {{ t('captcha.secretTitle') }}
        </h2>
        <div class="mt-3 flex items-start gap-2 rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm text-fg">
          <Icon name="lucide:triangle-alert" class="mt-0.5 size-4 shrink-0 text-warn" />
          <p>{{ t('captcha.secretWarn') }}</p>
        </div>
        <div class="mt-4 space-y-3">
          <div>
            <p class="mb-1 text-xs text-fg-muted">{{ t('captcha.siteKeyLabel') }} ({{ revealed.name }})</p>
            <div class="flex items-center gap-2 rounded-lg border border-line bg-bg p-2.5">
              <code class="flex-1 break-all font-mono text-xs text-fg">{{ revealed.siteKey }}</code>
              <button class="text-fg-muted hover:text-fg" @click="copy(revealed.siteKey)"><Icon name="lucide:copy" class="size-4" /></button>
            </div>
          </div>
          <div>
            <p class="mb-1 text-xs text-fg-muted">{{ t('captcha.secretLabel') }}</p>
            <div class="flex items-center gap-2 rounded-lg border border-line bg-bg p-2.5">
              <code class="flex-1 break-all font-mono text-xs text-fg">{{ revealed.secret }}</code>
              <button class="inline-flex items-center gap-1 text-xs text-fg-muted hover:text-fg" @click="copy(revealed.secret)">
                <Icon name="lucide:copy" class="size-4" />{{ t('common.copy') }}
              </button>
            </div>
          </div>
        </div>
        <div class="mt-5 flex justify-end">
          <button class="rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white" @click="revealed = null">{{ t('captcha.secretSaved') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
