<script setup lang="ts">
import type { Policy, PolicyRule, PolicyAction, Tunnel, Hostname } from '~/types/api'

const api = useApi()
const toast = useToast()
const { t } = useI18n()

const ACTION_TYPES: PolicyAction['type'][] = [
  'deny', 'rate_limit', 'set_header', 'redirect', 'require_mtls', 'verify_webhook', 'waf',
]
const WEBHOOK_PROVIDERS = ['github', 'stripe', 'gitlab', 'shopify', 'slack'] as const
const WAF_RULESETS = ['owasp-lite'] as const
// Suslu parantezler sablonda yorumlanmasin diye sabit olarak tutuluyor.
const SECRET_REF_PH = '{{secret:webhook-gizli}}'

const loading = ref(true)
const policies = ref<Policy[]>([])
const tunnels = ref<Tunnel[]>([])
const hostnames = ref<Hostname[]>([])

// Editor state
const showEditor = ref(false)
const editingId = ref<string | null>(null)
const formName = ref('')
const formPriority = ref(100)
const formEnabled = ref(true)
const formRules = ref<PolicyRule[]>([])
const saving = ref(false)

function newRule(): PolicyRule {
  return { match: { path_prefix: '' }, action: { type: 'deny', status: 403 } }
}

function openCreate() {
  editingId.value = null
  formName.value = ''
  formPriority.value = 100
  formEnabled.value = true
  formRules.value = [newRule()]
  showEditor.value = true
}

function openEdit(p: Policy) {
  editingId.value = p.id
  formName.value = p.name
  formPriority.value = p.priority
  formEnabled.value = p.enabled
  formRules.value = structuredClone(toRaw(p.config?.rules?.length ? p.config.rules : [newRule()]))
  showEditor.value = true
}

function addRule() { formRules.value.push(newRule()) }
function removeRule(i: number) { formRules.value.splice(i, 1) }

// match.methods CSV <-> array yardimcilari
function methodsCsv(r: PolicyRule): string { return (r.match.methods || []).join(', ') }
function setMethodsCsv(r: PolicyRule, v: string) {
  const arr = v.split(',').map(s => s.trim().toUpperCase()).filter(Boolean)
  r.match.methods = arr.length ? arr : undefined
}

// Geo/liste koşulları da CSV girilir. Boş dizi yerine undefined yazıyoruz:
// backend "boş bırakılan koşulu dikkate almaz" diyor, boş dizi de aynı anlama
// gelir ama undefined JSON'u temiz tutar ve kaydedilmiş kural okunaklı kalır.
function csv(v?: (string | number)[]): string { return (v || []).join(', ') }
function setCsvUpper(r: PolicyRule, field: 'country' | 'continent', v: string) {
  const arr = v.split(',').map(s => s.trim().toUpperCase()).filter(Boolean)
  r.match[field] = arr.length ? arr : undefined
}
function setCsvLower(r: PolicyRule, field: 'reputation', v: string) {
  const arr = v.split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
  r.match[field] = arr.length ? arr : undefined
}
function setIspCsv(r: PolicyRule, v: string) {
  const arr = v.split(',').map(s => s.trim()).filter(Boolean)
  r.match.isp = arr.length ? arr : undefined
}
function setAsnCsv(r: PolicyRule, v: string) {
  const arr = v.split(',').map(s => Number(s.trim())).filter(n => Number.isFinite(n) && n > 0)
  r.match.asn = arr.length ? arr : undefined
}
function setPatternsCsv(r: PolicyRule, v: string) {
  const arr = v.split('\n').map(s => s.trim()).filter(Boolean)
  r.action.patterns = arr.length ? arr : undefined
}
function patternsText(r: PolicyRule): string { return (r.action.patterns || []).join('\n') }

// tor_exit / hosting üç durumlu: belirtilmedi · evet · hayır.
// 'false' ile 'belirtilmedi' aynı şey DEĞİL — ilki "Tor olmayan" demek.
type TriState = '' | 'true' | 'false'
function triGet(r: PolicyRule, field: 'tor_exit' | 'hosting'): TriState {
  const v = r.match[field]
  return v === undefined ? '' : (v ? 'true' : 'false')
}
function triSet(r: PolicyRule, field: 'tor_exit' | 'hosting', v: TriState) {
  r.match[field] = v === '' ? undefined : v === 'true'
}

// Geo/liste koşulu kullanan kurallar için uyarı gösterilir.
function usesGeoOrLists(r: PolicyRule): boolean {
  const m = r.match
  return !!(m.country?.length || m.continent?.length || m.asn?.length || m.isp?.length
    || m.reputation?.length || m.tor_exit !== undefined || m.hosting !== undefined)
}

// Eylem tipi degisince o tipe ait olmayan alanlar temizlenir; aksi halde eski
// bir 'deny' status'u kaydedilen 'waf' kuralinin icinde kalirdi.
function onActionTypeChange(r: PolicyRule) {
  const t = r.action.type
  const keep: Record<string, string[]> = {
    deny: ['status', 'message'],
    redirect: ['location', 'status'],
    rate_limit: ['key', 'requests', 'window_sec', 'burst'],
    set_header: ['request', 'response'],
    require_mtls: [],
    verify_webhook: ['provider', 'secret_ref', 'tolerance_sec', 'status'],
    waf: ['ruleset', 'patterns', 'status'],
  }
  const allowed = new Set(keep[t] || [])
  for (const k of Object.keys(r.action)) {
    if (k !== 'type' && !allowed.has(k)) delete (r.action as any)[k]
  }
  if (t === 'verify_webhook' && !r.action.provider) r.action.provider = 'github'
  if (t === 'waf' && r.action.ruleset === undefined) r.action.ruleset = 'owasp-lite'
}

async function load() {
  loading.value = true
  try {
    const [pRes, tRes, hRes] = await Promise.all([api.listPolicies(), api.listTunnels(), api.listHostnames()])
    policies.value = pRes.policies
    tunnels.value = tRes
    hostnames.value = hRes
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.loadError')))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!formName.value.trim()) return
  saving.value = true
  const payload = { name: formName.value.trim(), config: { rules: toRaw(formRules.value) }, priority: formPriority.value }
  try {
    if (editingId.value) {
      await api.updatePolicy(editingId.value, { ...payload, enabled: formEnabled.value })
    } else {
      await api.createPolicy(payload)
    }
    showEditor.value = false
    await load()
    toast.success(t('policies.saved'))
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.saveError')))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(p: Policy) {
  try {
    await api.updatePolicy(p.id, { name: p.name, config: p.config, enabled: !p.enabled, priority: p.priority })
    await load()
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.saveError')))
  }
}

async function remove(p: Policy) {
  if (!confirm(t('policies.deleteConfirm', { name: p.name }))) return
  try {
    await api.deletePolicy(p.id)
    await load()
    toast.success(t('policies.deleted'))
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.deleteError')))
  }
}

// --- Binding ---
const bindingPolicy = ref<Policy | null>(null)
const bindTunnelId = ref('')

function openBind(p: Policy) {
  bindingPolicy.value = p
  bindTunnelId.value = ''
}

async function doBind() {
  if (!bindingPolicy.value || !bindTunnelId.value) return
  try {
    await api.bindPolicy(bindingPolicy.value.id, { tunnel_id: bindTunnelId.value })
    await load()
    bindingPolicy.value = policies.value.find(x => x.id === bindingPolicy.value?.id) || null
    toast.success(t('policies.bound'))
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.bindError')))
  }
}

async function doUnbind(p: Policy, tunnelId: string) {
  try {
    await api.unbindPolicy(p.id, { tunnel_id: tunnelId })
    await load()
    bindingPolicy.value = policies.value.find(x => x.id === p.id) || null
  } catch (e: any) {
    toast.error(hostnameError(e, t('policies.bindError')))
  }
}

function tunnelName(id: string): string {
  const tn = tunnels.value.find(x => x.id === id)
  if (!tn) return id
  return tn.hostnames?.[0]?.fqdn || tn.target || id
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-5xl px-4 py-6 sm:px-5">
    <div class="mb-6 flex items-start justify-between gap-4">
      <div>
        <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
          <Icon name="lucide:shield-half" class="size-5 text-accent" />
          {{ t('policies.title') }}
        </h1>
        <p class="mt-1 text-sm text-fg-muted">{{ t('policies.subtitle') }}</p>
      </div>
      <button
        class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-accent/90 active:scale-95"
        @click="openCreate"
      >
        <Icon name="lucide:plus" class="size-4" />
        {{ t('policies.create') }}
      </button>
    </div>

      <div v-if="loading" class="py-12 text-center text-fg-muted">
        <Icon name="lucide:loader-circle" class="mx-auto size-6 animate-spin" />
      </div>

      <div v-else-if="policies.length === 0" class="rounded-xl border border-dashed border-line bg-surface p-10 text-center">
        <Icon name="lucide:shield-half" class="mx-auto size-8 text-fg-muted" />
        <p class="mt-3 text-sm text-fg-muted">{{ t('policies.empty') }}</p>
      </div>

      <div v-else class="space-y-3">
        <div v-for="p in policies" :key="p.id" class="rounded-xl border border-line bg-surface p-4">
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="font-medium text-fg">{{ p.name }}</span>
                <span class="rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-mono text-fg-muted">{{ t('policies.priority') }} {{ p.priority }}</span>
                <span :class="p.enabled ? 'text-success' : 'text-fg-muted'" class="text-[10px] uppercase tracking-wide">
                  {{ p.enabled ? t('policies.enabled') : t('policies.disabled') }}
                </span>
              </div>
              <p class="mt-1 text-xs text-fg-muted">
                {{ (p.config?.rules?.length || 0) }} {{ t('policies.rulesCount') }} ·
                {{ (p.bindings?.length || 0) }} {{ t('policies.bindingsCount') }}
              </p>
            </div>
            <div class="flex items-center gap-1">
              <button class="rounded-md px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="openBind(p)">
                <Icon name="lucide:link" class="size-3.5" /> {{ t('policies.bind') }}
              </button>
              <button class="rounded-md px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="toggleEnabled(p)">
                <Icon :name="p.enabled ? 'lucide:pause' : 'lucide:play'" class="size-3.5" />
              </button>
              <button class="rounded-md px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="openEdit(p)">
                <Icon name="lucide:pencil" class="size-3.5" /> {{ t('common.edit') }}
              </button>
              <button class="rounded-md px-2 py-1 text-xs text-fg-muted hover:text-danger" @click="remove(p)">
                <Icon name="lucide:trash-2" class="size-3.5" />
              </button>
            </div>
          </div>
          <div v-if="p.bindings?.length" class="mt-2 flex flex-wrap gap-1.5">
            <span v-for="b in p.bindings" :key="(b.tunnel_id || '') + (b.hostname || '')" class="inline-flex items-center gap-1 rounded bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">
              <Icon name="lucide:route" class="size-3" />
              {{ b.tunnel_id ? tunnelName(b.tunnel_id) : b.hostname }}
            </span>
          </div>
        </div>
      </div>

    <!-- Editor -->
    <div v-if="showEditor" class="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/40 p-4" @click.self="showEditor = false">
      <div class="my-8 w-full max-w-2xl rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="text-lg font-semibold text-fg">{{ editingId ? t('policies.editTitle') : t('policies.create') }}</h2>

        <div class="mt-4 grid grid-cols-2 gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('policies.name') }}</label>
            <input v-model="formName" type="text" class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('policies.priority') }}</label>
            <input v-model.number="formPriority" type="number" class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
          </div>
        </div>

        <div class="mt-4 space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="text-sm font-medium text-fg">{{ t('policies.rules') }}</h3>
            <button class="inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="addRule">
              <Icon name="lucide:plus" class="size-3.5" /> {{ t('policies.addRule') }}
            </button>
          </div>

          <div v-for="(r, i) in formRules" :key="i" class="rounded-lg border border-line bg-bg p-3">
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-fg-muted">#{{ i + 1 }}</span>
              <button class="text-fg-muted hover:text-danger" @click="removeRule(i)"><Icon name="lucide:x" class="size-4" /></button>
            </div>

            <!-- MATCH -->
            <p class="mb-1.5 text-[11px] uppercase tracking-wide text-fg-muted">{{ t('policies.match') }}</p>
            <div class="grid grid-cols-3 gap-2">
              <input v-model="r.match.path_prefix" type="text" :placeholder="t('policies.pathPrefix')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input :value="methodsCsv(r)" type="text" placeholder="GET, POST" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setMethodsCsv(r, ($event.target as HTMLInputElement).value)">
              <div class="text-[11px] text-fg-muted self-center">{{ t('policies.matchHint') }}</div>
            </div>

            <!-- GEO / LISTE KOSULLARI -->
            <details class="mt-2 rounded-md border border-line bg-surface/60 px-2.5 py-1.5">
              <summary class="cursor-pointer select-none text-[11px] uppercase tracking-wide text-fg-muted">
                {{ t('policies.geoSection') }}
              </summary>
              <div class="mt-2 grid grid-cols-2 gap-2">
                <input :value="csv(r.match.country)" type="text" :placeholder="t('policies.countryPh')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setCsvUpper(r, 'country', ($event.target as HTMLInputElement).value)">
                <input :value="csv(r.match.continent)" type="text" :placeholder="t('policies.continentPh')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setCsvUpper(r, 'continent', ($event.target as HTMLInputElement).value)">
                <input :value="csv(r.match.asn)" type="text" :placeholder="t('policies.asnPh')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setAsnCsv(r, ($event.target as HTMLInputElement).value)">
                <input :value="csv(r.match.isp)" type="text" :placeholder="t('policies.ispPh')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setIspCsv(r, ($event.target as HTMLInputElement).value)">
                <label class="flex items-center gap-2 text-xs text-fg-muted">
                  {{ t('policies.torExit') }}
                  <select :value="triGet(r, 'tor_exit')" class="flex-1 rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @change="triSet(r, 'tor_exit', ($event.target as HTMLSelectElement).value as any)">
                    <option value="">{{ t('policies.triAny') }}</option>
                    <option value="true">{{ t('policies.triYes') }}</option>
                    <option value="false">{{ t('policies.triNo') }}</option>
                  </select>
                </label>
                <label class="flex items-center gap-2 text-xs text-fg-muted">
                  {{ t('policies.hosting') }}
                  <select :value="triGet(r, 'hosting')" class="flex-1 rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @change="triSet(r, 'hosting', ($event.target as HTMLSelectElement).value as any)">
                    <option value="">{{ t('policies.triAny') }}</option>
                    <option value="true">{{ t('policies.triYes') }}</option>
                    <option value="false">{{ t('policies.triNo') }}</option>
                  </select>
                </label>
                <input :value="csv(r.match.reputation)" type="text" :placeholder="t('policies.reputationPh')" class="col-span-2 rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @input="setCsvLower(r, 'reputation', ($event.target as HTMLInputElement).value)">
              </div>
              <p v-if="usesGeoOrLists(r)" class="mt-2 flex items-start gap-1.5 rounded-md bg-warning/10 px-2 py-1.5 text-[11px] leading-relaxed text-fg-muted">
                <Icon name="lucide:triangle-alert" class="mt-px size-3.5 shrink-0 text-warning" />
                <span>{{ t('policies.geoWarning') }}</span>
              </p>
            </details>

            <!-- ACTION -->
            <p class="mb-1.5 mt-3 text-[11px] uppercase tracking-wide text-fg-muted">{{ t('policies.action') }}</p>
            <select v-model="r.action.type" class="mb-2 w-full rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent" @change="onActionTypeChange(r)">
              <option v-for="at in ACTION_TYPES" :key="at" :value="at">{{ t('policies.action_' + at) }}</option>
            </select>

            <div v-if="r.action.type === 'deny'" class="grid grid-cols-2 gap-2">
              <input v-model.number="r.action.status" type="number" placeholder="403" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input v-model="r.action.message" type="text" :placeholder="t('policies.denyMessage')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
            </div>

            <div v-else-if="r.action.type === 'redirect'" class="grid grid-cols-2 gap-2">
              <input v-model="r.action.location" type="text" placeholder="/new" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input v-model.number="r.action.status" type="number" placeholder="308" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
            </div>

            <div v-else-if="r.action.type === 'rate_limit'" class="grid grid-cols-4 gap-2">
              <input v-model="r.action.key" type="text" placeholder="ip" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input v-model.number="r.action.requests" type="number" :placeholder="t('policies.requests')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input v-model.number="r.action.window_sec" type="number" :placeholder="t('policies.windowSec')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              <input v-model.number="r.action.burst" type="number" placeholder="burst" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
            </div>

            <div v-else-if="r.action.type === 'require_mtls'" class="text-xs text-fg-muted">
              {{ t('policies.mtlsHint') }}
            </div>

            <div v-else-if="r.action.type === 'set_header'" class="text-xs text-fg-muted">
              {{ t('policies.setHeaderHint') }}
            </div>

            <div v-else-if="r.action.type === 'verify_webhook'" class="space-y-2">
              <div class="grid grid-cols-3 gap-2">
                <select v-model="r.action.provider" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
                  <option v-for="wp in WEBHOOK_PROVIDERS" :key="wp" :value="wp">{{ wp }}</option>
                </select>
                <input v-model="r.action.secret_ref" type="text" :placeholder="SECRET_REF_PH" class="col-span-2 rounded-md border border-line bg-surface px-2 py-1.5 font-mono text-sm text-fg outline-none focus:border-accent">
              </div>
              <div class="grid grid-cols-2 gap-2">
                <input v-model.number="r.action.tolerance_sec" type="number" :placeholder="t('policies.tolerancePh')" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
                <input v-model.number="r.action.status" type="number" placeholder="401" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              </div>
              <p class="text-[11px] leading-relaxed text-fg-muted">{{ t('policies.webhookHint') }}</p>
            </div>

            <div v-else-if="r.action.type === 'waf'" class="space-y-2">
              <div class="grid grid-cols-2 gap-2">
                <select v-model="r.action.ruleset" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
                  <option v-for="rs in WAF_RULESETS" :key="rs" :value="rs">{{ rs }}</option>
                </select>
                <input v-model.number="r.action.status" type="number" placeholder="403" class="rounded-md border border-line bg-surface px-2 py-1.5 text-sm text-fg outline-none focus:border-accent">
              </div>
              <textarea :value="patternsText(r)" rows="3" :placeholder="t('policies.patternsPh')" class="w-full rounded-md border border-line bg-surface px-2 py-1.5 font-mono text-xs text-fg outline-none focus:border-accent" @input="setPatternsCsv(r, ($event.target as HTMLTextAreaElement).value)" />
              <p class="text-[11px] leading-relaxed text-fg-muted">{{ t('policies.wafHint') }}</p>
            </div>
          </div>
        </div>

        <div class="mt-5 flex justify-end gap-2">
          <button class="rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg" @click="showEditor = false">{{ t('common.cancel') }}</button>
          <button class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving || !formName.trim()" @click="save">
            <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
            {{ t('common.save') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Bind modal -->
    <div v-if="bindingPolicy" class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4" @click.self="bindingPolicy = null">
      <div class="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="text-lg font-semibold text-fg">{{ t('policies.bindTitle', { name: bindingPolicy.name }) }}</h2>
        <div class="mt-4 flex gap-2">
          <select v-model="bindTunnelId" class="flex-1 rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
            <option value="">{{ t('policies.selectTunnel') }}</option>
            <option v-for="tn in tunnels" :key="tn.id" :value="tn.id">{{ tunnelName(tn.id) }}</option>
          </select>
          <button class="rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="!bindTunnelId" @click="doBind">{{ t('policies.bind') }}</button>
        </div>
        <div v-if="bindingPolicy.bindings?.length" class="mt-4 space-y-1.5">
          <p class="text-xs font-medium text-fg-muted">{{ t('policies.currentBindings') }}</p>
          <div v-for="b in bindingPolicy.bindings" :key="(b.tunnel_id || '') + (b.hostname || '')" class="flex items-center justify-between rounded-md bg-surface-2 px-2.5 py-1.5 text-sm">
            <span class="text-fg">{{ b.tunnel_id ? tunnelName(b.tunnel_id) : b.hostname }}</span>
            <button v-if="b.tunnel_id" class="text-fg-muted hover:text-danger" @click="doUnbind(bindingPolicy, b.tunnel_id || '')"><Icon name="lucide:x" class="size-4" /></button>
          </div>
        </div>
        <div class="mt-5 flex justify-end">
          <button class="rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg" @click="bindingPolicy = null">{{ t('common.done') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
