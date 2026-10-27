<script setup lang="ts">
// Tunel "Guvenlik" sekmesi (ZTNA Z3): WAF (off/log/block), ulke (Geo) kurali ve
// engellenen / gozlenen istekler listesi.
import type { TunnelSecurity, RequestLog } from '~/types/api'

const props = defineProps<{ tunnelId: string, privileged: boolean, proto: string }>()
const { t } = useI18n()
const api = useApi()
const toast = useToast()

const isRaw = computed(() => props.proto === 'tcp' || props.proto === 'udp')
const loading = ref(true)
const saving = ref(false)
const geoDbLoaded = ref(true)
const planOk = ref(true)
const catalog = ref<{ id: string, category: string }[]>([])

const form = reactive({
  wafMode: 'off' as 'off' | 'log' | 'block',
  wafBody: false,
  disabled: [] as string[],
  geoMode: 'off' as 'off' | 'allow' | 'deny',
  countries: '',
  unknownIs: 'allow' as 'allow' | 'deny',
  // Bot koruması
  botMode: 'off' as 'off' | 'suspicious' | 'always',
  botDifficulty: 18,
  botTtl: 60,
  botExclude: [] as string[],
  botVerified: true,
  botEmptyUa: false,
  // Hız sınırı
  rateEnabled: false,
  rateRequests: 120,
  rateWindow: 60,
  rateAction: 'block' as 'block' | 'challenge',
  rateExclude: [] as string[],
})

const DIFFS = [{ id: 'light', bits: 16 }, { id: 'normal', bits: 18 }, { id: 'strong', bits: 20 }]
const TTLS = [15, 60, 720, 1440, 10080]
const diffOptions = computed(() => {
  const base = DIFFS.map(d => ({ ...d, label: t('tunbot.diff_' + d.id) }))
  if (DIFFS.some(d => d.bits === form.botDifficulty)) return base
  return [...base, { id: 'custom', bits: form.botDifficulty, label: t('tunbot.diffCustom', { bits: form.botDifficulty }) }]
})
const diffHint = computed(() => {
  const d = DIFFS.find(x => x.bits === form.botDifficulty)
  return d ? t('tunbot.diffHint_' + d.id) : ''
})
const ttlOptions = computed(() => (TTLS.includes(form.botTtl) ? TTLS : [...TTLS, form.botTtl].sort((a, b) => a - b))
  .map(m => ({ m, label: TTLS.includes(m) ? t('tunbot.ttl_' + m) : t('tunbot.ttlCustom', { n: m }) })))

const categories = computed(() => [...new Set(catalog.value.map(r => r.category))])

function apply(s: TunnelSecurity) {
  form.wafMode = (s.waf?.mode || 'off') as typeof form.wafMode
  form.wafBody = !!s.waf?.body
  form.disabled = [...(s.waf?.disabled_rules || [])]
  form.geoMode = (s.geo?.mode || 'off') as typeof form.geoMode
  form.countries = (s.geo?.countries || []).join(', ')
  form.unknownIs = (s.geo?.unknown_is || 'allow') as typeof form.unknownIs
  form.botMode = (s.bot?.mode || 'off') as typeof form.botMode
  form.botDifficulty = s.bot?.difficulty || 18
  form.botTtl = s.bot?.ttl_minutes || 60
  form.botExclude = [...(s.bot?.exclude_paths || [])]
  form.botVerified = s.bot?.allow_verified_bots ?? true
  form.botEmptyUa = !!s.bot?.block_empty_ua
  form.rateEnabled = !!s.rate?.enabled
  form.rateRequests = s.rate?.requests || 120
  form.rateWindow = s.rate?.window_seconds || 60
  form.rateAction = (s.rate?.action || 'block') as typeof form.rateAction
  form.rateExclude = [...(s.rate?.exclude_paths || [])]
  geoDbLoaded.value = s.geo_db_loaded
  planOk.value = s.plan_ok
  catalog.value = s.rules || []
}

async function load() {
  loading.value = true
  try {
    apply(await api.getTunnelSecurity(props.tunnelId))
    void loadBlocked()
    void loadStats()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('tunsec.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (form.rateEnabled) {
    const rq = Number(form.rateRequests)
    const w = Number(form.rateWindow)
    if (!Number.isInteger(rq) || rq < 1 || rq > 100000) { toast.error(t('tunbot.errRequests')); return }
    if (!Number.isInteger(w) || w < 1 || w > 3600) { toast.error(t('tunbot.errWindow')); return }
  }
  saving.value = true
  try {
    const countries = form.countries.split(/[\s,]+/).map(x => x.trim().toUpperCase()).filter(Boolean)
    await api.setTunnelSecurity(props.tunnelId, {
      waf: { mode: form.wafMode, body: form.wafBody, disabled_rules: form.disabled },
      geo: { mode: form.geoMode, countries, unknown_is: form.unknownIs },
      bot: {
        mode: form.botMode,
        difficulty: form.botDifficulty,
        ttl_minutes: form.botTtl,
        exclude_paths: form.botExclude,
        allow_verified_bots: form.botVerified,
        block_empty_ua: form.botEmptyUa,
      },
      rate: {
        enabled: form.rateEnabled,
        requests: Number(form.rateRequests),
        window_seconds: Number(form.rateWindow),
        action: form.rateAction,
        exclude_paths: form.rateExclude,
      },
    })
    toast.success(t('tunsec.saved'))
    await load()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('tunsec.saveFailed'))
  } finally {
    saving.value = false
  }
}

function toggleCat(c: string) {
  const i = form.disabled.indexOf(c)
  if (i >= 0) form.disabled.splice(i, 1)
  else form.disabled.push(c)
}

// --- Bot / hiz siniri sayaclari (son 24 saat; tek istek, istemcide gruplanir) ---
const STAT_KEYS = ['bot:challenge', 'bot:fail', 'bot:block', 'rate:block', 'rate:challenge'] as const
const STAT_LIMIT = 1000
const stats = ref<Record<string, number>>({})
const statsTruncated = ref(false)
const statsLoading = ref(false)
async function loadStats() {
  statsLoading.value = true
  try {
    const rows = await api.listRequests({
      tunnel_id: props.tunnelId,
      reason: 'bot:*,rate:*',
      since: new Date(Date.now() - 24 * 3600 * 1000).toISOString(),
      limit: STAT_LIMIT,
    })
    const c: Record<string, number> = {}
    for (const r of rows) if (r.reject_reason) c[r.reject_reason] = (c[r.reject_reason] || 0) + 1
    stats.value = c
    statsTruncated.value = rows.length >= STAT_LIMIT
  } catch { stats.value = {} } finally { statsLoading.value = false }
}
const statLabel = (k: string) => t('tunbot.stat_' + k.replace(':', '_'))

// --- Engellenen istekler ---
const blocked = ref<RequestLog[]>([])
const blockedLoading = ref(false)
async function loadBlocked() {
  blockedLoading.value = true
  try {
    blocked.value = await api.listRequests({
      tunnel_id: props.tunnelId,
      reason: 'waf:*,waf_log:*,geo:*,challenge,bot:*,rate:*',
      limit: 100,
    })
  } catch { blocked.value = [] } finally { blockedLoading.value = false }
}

function reasonLabel(r?: string): string {
  if (!r) return ''
  if (r.startsWith('waf_log:')) return t('tunsec.reasonWafLog', { rule: r.slice(8) })
  if (r.startsWith('waf:')) return t('tunsec.reasonWaf', { rule: r.slice(4) })
  if (r.startsWith('geo:')) return t('tunsec.reasonGeo', { country: r.slice(4) })
  if (r === 'challenge') return t('tunsec.reasonChallenge')
  if (r.startsWith('bot:') || r.startsWith('rate:')) return t('tunbot.stat_' + r.replace(':', '_'))
  return r
}
const observed = (r: RequestLog) => (r.reject_reason || '').startsWith('waf_log:')

onMounted(load)
</script>

<template>
  <div class="space-y-5">
    <p class="text-xs text-fg-muted">{{ t('tunsec.intro') }}</p>
    <div v-if="loading" class="py-6 text-center text-sm text-fg-muted">{{ t('common.loading') }}</div>
    <template v-else>
      <p v-if="!planOk" class="flex items-start gap-1.5 rounded-md bg-warning/10 px-2 py-1.5 text-[11px] text-fg-muted">
        <Icon name="lucide:lock" class="mt-px size-3.5 shrink-0 text-warning" />
        <span>{{ t('tunsec.planNotice') }}</span>
      </p>

      <!-- WAF (yalniz HTTP) -->
      <section v-if="!isRaw" class="space-y-3">
        <div class="flex items-center gap-2">
          <Icon name="lucide:shield-alert" class="size-4 text-accent" />
          <span class="label-sys">{{ t('tunsec.wafTitle') }}</span>
        </div>
        <div class="grid gap-2 sm:grid-cols-3">
          <label v-for="m in (['off', 'log', 'block'] as const)" :key="m"
                 class="flex cursor-pointer items-start gap-2 rounded border p-2 text-xs"
                 :class="form.wafMode === m ? 'border-accent bg-accent/5' : 'border-line'">
            <input v-model="form.wafMode" type="radio" :value="m" :disabled="!privileged" class="mt-0.5 accent-[var(--color-accent)]">
            <span>
              <span class="block font-medium text-fg">{{ t('tunsec.mode_' + m) }}</span>
              <span class="text-fg-muted">{{ t('tunsec.modeHelp_' + m) }}</span>
            </span>
          </label>
        </div>
        <template v-if="form.wafMode !== 'off'">
          <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
            <input v-model="form.wafBody" type="checkbox" :disabled="!privileged" class="accent-[var(--color-accent)]">
            {{ t('tunsec.scanBody') }}
          </label>
          <p class="-mt-1 text-[11px] text-fg-subtle">{{ t('tunsec.scanBodyHint') }}</p>
          <div>
            <span class="label-sys mb-1 block">{{ t('tunsec.rulesTitle') }}</span>
            <div class="flex flex-wrap gap-1.5">
              <button v-for="c in categories" :key="c" type="button" :disabled="!privileged"
                      class="rounded-full border px-2.5 py-0.5 text-[11px] transition-colors"
                      :class="form.disabled.includes(c) ? 'border-line text-fg-subtle line-through' : 'border-accent/50 bg-accent/10 text-accent'"
                      @click="toggleCat(c)">
                {{ t('tunsec.cat_' + c) }}
              </button>
            </div>
            <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunsec.rulesHint') }}</p>
          </div>
        </template>
      </section>

      <!-- Geo -->
      <section class="space-y-3 border-t border-line pt-4">
        <div class="flex items-center gap-2">
          <Icon name="lucide:globe" class="size-4 text-accent" />
          <span class="label-sys">{{ t('tunsec.geoTitle') }}</span>
        </div>
        <p v-if="form.geoMode !== 'off' && !geoDbLoaded" class="flex items-start gap-1.5 rounded-md bg-warning/10 px-2 py-1.5 text-[11px] text-fg-muted">
          <Icon name="lucide:triangle-alert" class="mt-px size-3.5 shrink-0 text-warning" />
          <span>{{ t('tunsec.geoDbMissing') }}</span>
        </p>
        <p class="text-[11px] text-fg-subtle">
          {{ t('tunsec.geoAttribution') }}
          <a href="https://db-ip.com" target="_blank" rel="noopener" class="underline hover:text-fg">DB-IP</a>
        </p>
        <div class="grid gap-2 sm:grid-cols-3">
          <label v-for="m in (['off', 'allow', 'deny'] as const)" :key="m"
                 class="flex cursor-pointer items-start gap-2 rounded border p-2 text-xs"
                 :class="form.geoMode === m ? 'border-accent bg-accent/5' : 'border-line'">
            <input v-model="form.geoMode" type="radio" :value="m" :disabled="!privileged" class="mt-0.5 accent-[var(--color-accent)]">
            <span>
              <span class="block font-medium text-fg">{{ t('tunsec.geo_' + m) }}</span>
              <span class="text-fg-muted">{{ t('tunsec.geoHelp_' + m) }}</span>
            </span>
          </label>
        </div>
        <template v-if="form.geoMode !== 'off'">
          <div>
            <span class="label-sys mb-1 block">{{ t('tunsec.countries') }}</span>
            <input v-model="form.countries" type="text" :disabled="!privileged" placeholder="TR, DE, US"
                   class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs uppercase focus:border-accent">
            <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunsec.countriesHint') }}</p>
          </div>
          <label class="flex items-center gap-2 text-xs text-fg">
            <span>{{ t('tunsec.unknownIs') }}</span>
            <select v-model="form.unknownIs" :disabled="!privileged" class="rounded border border-line bg-bg px-2 py-1 text-xs">
              <option value="allow">{{ t('tunsec.unknownAllow') }}</option>
              <option value="deny">{{ t('tunsec.unknownDeny') }}</option>
            </select>
          </label>
          <p class="text-[11px] text-fg-subtle">{{ t('tunsec.geoNote') }}</p>
        </template>
      </section>

      <!-- Bot koruması (yalniz HTTP) -->
      <section v-if="!isRaw" class="space-y-3 border-t border-line pt-4">
        <div class="flex items-center gap-2">
          <Icon name="lucide:bot" class="size-4 text-accent" />
          <span class="label-sys">{{ t('tunbot.botTitle') }}</span>
        </div>
        <p class="text-[11px] text-fg-subtle">{{ t('tunbot.botIntro') }}</p>
        <p v-if="!planOk" class="flex items-start gap-1.5 rounded-md bg-warning/10 px-2 py-1.5 text-[11px] text-fg-muted">
          <Icon name="lucide:lock" class="mt-px size-3.5 shrink-0 text-warning" />
          <span>{{ t('tunbot.planNotice') }}</span>
        </p>
        <div class="grid gap-2 sm:grid-cols-3">
          <label v-for="m in (['off', 'suspicious', 'always'] as const)" :key="m"
                 class="flex items-start gap-2 rounded border p-2 text-xs"
                 :class="[form.botMode === m ? 'border-accent bg-accent/5' : 'border-line', (m === 'always' && !planOk) ? 'opacity-60' : 'cursor-pointer']">
            <input v-model="form.botMode" type="radio" :value="m" :disabled="!privileged || (m === 'always' && !planOk && form.botMode !== 'always')" class="mt-0.5 accent-[var(--color-accent)]">
            <span>
              <span class="flex items-center gap-1.5 font-medium text-fg">
                {{ t('tunbot.mode_' + m) }}
                <span v-if="m === 'always'" class="rounded bg-accent/10 px-1 py-px text-[10px] text-accent">{{ t('tunbot.pro') }}</span>
              </span>
              <span class="text-fg-muted">{{ t('tunbot.modeHelp_' + m) }}</span>
            </span>
          </label>
        </div>
        <template v-if="form.botMode !== 'off'">
          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <span class="label-sys mb-1 block">{{ t('tunbot.difficulty') }}</span>
              <select v-model.number="form.botDifficulty" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs">
                <option v-for="d in diffOptions" :key="d.id" :value="d.bits">{{ d.label }}</option>
              </select>
              <p v-if="diffHint" class="mt-1 text-[11px] text-fg-subtle">{{ diffHint }}</p>
            </div>
            <div>
              <span class="label-sys mb-1 block">{{ t('tunbot.ttl') }}</span>
              <select v-model.number="form.botTtl" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs">
                <option v-for="o in ttlOptions" :key="o.m" :value="o.m">{{ o.label }}</option>
              </select>
              <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunbot.ttlHint') }}</p>
            </div>
          </div>
          <div>
            <span class="label-sys mb-1 block">{{ t('tunbot.excludePaths') }}</span>
            <TunnelPathChips v-model="form.botExclude" :disabled="!privileged" />
            <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunbot.excludeHintBot') }}</p>
          </div>
          <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
            <input v-model="form.botVerified" type="checkbox" :disabled="!privileged" class="accent-[var(--color-accent)]">
            {{ t('tunbot.verifiedBots') }}
          </label>
          <p class="-mt-1 text-[11px] text-fg-subtle">{{ t('tunbot.verifiedBotsHint') }}</p>
          <label class="flex cursor-pointer items-center gap-2 text-xs text-fg">
            <input v-model="form.botEmptyUa" type="checkbox" :disabled="!privileged" class="accent-[var(--color-accent)]">
            {{ t('tunbot.blockEmptyUa') }}
          </label>
          <p class="-mt-1 text-[11px] text-fg-subtle">{{ t('tunbot.blockEmptyUaHint') }}</p>
          <p class="flex items-start gap-1.5 text-[11px] text-fg-subtle">
            <Icon name="lucide:info" class="mt-px size-3.5 shrink-0" />
            <span>{{ t('tunbot.visitorNote') }}</span>
          </p>
        </template>
      </section>

      <!-- Hiz siniri (yalniz HTTP) -->
      <section v-if="!isRaw" class="space-y-3 border-t border-line pt-4">
        <div class="flex items-center gap-2">
          <Icon name="lucide:gauge" class="size-4 text-accent" />
          <span class="label-sys">{{ t('tunbot.rateTitle') }}</span>
          <span class="rounded bg-accent/10 px-1 py-px text-[10px] text-accent">{{ t('tunbot.pro') }}</span>
        </div>
        <p class="text-[11px] text-fg-subtle">{{ t('tunbot.rateIntro') }}</p>
        <label class="flex items-center gap-2 text-xs text-fg" :class="(!planOk && !form.rateEnabled) ? 'opacity-60' : 'cursor-pointer'">
          <input v-model="form.rateEnabled" type="checkbox" :disabled="!privileged || (!planOk && !form.rateEnabled)" class="accent-[var(--color-accent)]">
          {{ t('tunbot.rateEnable') }}
        </label>
        <template v-if="form.rateEnabled">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="label-sys mr-1">{{ t('tunbot.presets') }}</span>
            <button v-for="p in [[60, 60], [120, 60], [600, 60]]" :key="p[0]" type="button" :disabled="!privileged"
                    class="rounded-full border px-2.5 py-0.5 text-[11px] transition-colors"
                    :class="form.rateRequests === p[0] && form.rateWindow === p[1] ? 'border-accent/50 bg-accent/10 text-accent' : 'border-line text-fg-muted hover:border-accent'"
                    @click="form.rateRequests = p[0]!; form.rateWindow = p[1]!">
              {{ t('tunbot.preset' + p[0]) }}
            </button>
          </div>
          <div class="grid gap-3 sm:grid-cols-3">
            <label class="block">
              <span class="label-sys mb-1 block">{{ t('tunbot.requests') }}</span>
              <input v-model.number="form.rateRequests" type="number" min="1" max="100000" :disabled="!privileged"
                     class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
            </label>
            <label class="block">
              <span class="label-sys mb-1 block">{{ t('tunbot.windowSeconds') }}</span>
              <input v-model.number="form.rateWindow" type="number" min="1" max="3600" :disabled="!privileged"
                     class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent">
            </label>
            <label class="block">
              <span class="label-sys mb-1 block">{{ t('tunbot.action') }}</span>
              <select v-model="form.rateAction" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-xs">
                <option value="block">{{ t('tunbot.action_block') }}</option>
                <option value="challenge">{{ t('tunbot.action_challenge') }}</option>
              </select>
            </label>
          </div>
          <div>
            <span class="label-sys mb-1 block">{{ t('tunbot.excludePaths') }}</span>
            <TunnelPathChips v-model="form.rateExclude" :disabled="!privileged" />
            <p class="mt-1 text-[11px] text-fg-subtle">{{ t('tunbot.excludeHintRate') }}</p>
          </div>
        </template>

        <!-- Sayaclar -->
        <div class="space-y-1.5 rounded border border-line p-2.5">
          <div class="flex items-center justify-between">
            <span class="label-sys">{{ t('tunbot.statsTitle') }}</span>
            <button type="button" class="flex items-center gap-1 text-xs text-accent hover:underline" @click="loadStats">
              <Icon name="lucide:refresh-cw" class="size-3" /> {{ t('tunbot.statsRefresh') }}
            </button>
          </div>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-5">
            <div v-for="k in STAT_KEYS" :key="k" class="rounded bg-surface-2 px-2 py-1.5">
              <div class="font-mono text-sm text-fg">{{ statsLoading ? '…' : ((stats[k] || 0) + (statsTruncated ? '+' : '')) }}</div>
              <div class="text-[10px] text-fg-muted">{{ statLabel(k) }}</div>
            </div>
          </div>
          <p class="text-[10px] text-fg-subtle">{{ t('tunbot.statsNote') }}</p>
        </div>
      </section>

      <div v-if="privileged" class="flex justify-end">
        <button :disabled="saving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="save">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>

      <!-- Engellenen istekler -->
      <section class="space-y-2 border-t border-line pt-4">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <Icon name="lucide:shield-off" class="size-4 text-warning" />
            <span class="label-sys">{{ t('tunsec.blockedTitle') }}</span>
          </div>
          <button type="button" class="flex items-center gap-1 text-xs text-accent hover:underline" @click="loadBlocked">
            <Icon name="lucide:refresh-cw" class="size-3" /> {{ t('common.refresh') }}
          </button>
        </div>
        <div v-if="blockedLoading" class="py-3 text-center text-xs text-fg-muted">{{ t('common.loading') }}</div>
        <p v-else-if="!blocked.length" class="py-3 text-center text-xs text-fg-muted">{{ t('tunsec.blockedEmpty') }}</p>
        <ul v-else class="max-h-72 divide-y divide-line overflow-y-auto rounded border border-line text-xs">
          <li v-for="r in blocked" :key="r.id" class="flex flex-wrap items-center gap-x-3 gap-y-0.5 px-2.5 py-1.5">
            <span class="font-mono text-fg-subtle">{{ new Date(r.ts).toLocaleString() }}</span>
            <span class="font-mono text-fg">{{ r.client_ip }}</span>
            <span class="font-mono text-fg-muted">{{ r.method }} {{ r.path }}</span>
            <span class="ml-auto rounded px-1.5 py-px font-mono text-[10px]" :class="observed(r) ? 'bg-surface-2 text-fg-muted' : 'bg-warning/10 text-warning'">
              {{ reasonLabel(r.reject_reason) }}
            </span>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>
