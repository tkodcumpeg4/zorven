<script setup lang="ts">
// Tunel "MCP" modu ayarlari (M3/M4): kimlik dogrulama, baglanti URL'si, arac
// izin/ret listesi, hiz siniri, anahtarlar, baglantiyi sina ve analitik.
import type { TunnelMCPConfig, MCPKey, MCPKeyCreated, MCPCall, MCPStats, MCPTestResult } from '~/types/api'

const props = defineProps<{ tunnelId: string, privileged: boolean }>()
const { t } = useI18n()
const api = useApi()
const toast = useToast()

const loading = ref(true)
const saving = ref(false)
const enabled = ref(false)
const oauthAvailable = ref(true)
const urls = ref<string[]>([])
const form = reactive({
  auth: 'oauth' as TunnelMCPConfig['auth'],
  ackNoAuth: false,
  path: '/mcp',
  transport: 'auto' as TunnelMCPConfig['transport'],
  allowOrgMembers: true,
  emails: '',
  allowTools: '',
  denyTools: '',
  rate: 0,
  headTimeout: 300,
  idleTimeout: 900,
  maxStreams: 20,
  logLevel: 'meta' as TunnelMCPConfig['log_level'],
})

const lines = (s: string) => s.split(/[\n,]+/).map(x => x.trim()).filter(Boolean)

function applyConfig(c: TunnelMCPConfig) {
  form.auth = c.auth
  form.ackNoAuth = !!c.ack_no_auth
  form.path = c.path
  form.transport = c.transport
  form.allowOrgMembers = c.allow_org_members
  form.emails = (c.allowed_emails || []).join('\n')
  form.allowTools = (c.allow_tools || []).join('\n')
  form.denyTools = (c.deny_tools || []).join('\n')
  form.rate = c.rate_limit_per_min
  form.headTimeout = c.head_timeout_sec
  form.idleTimeout = c.idle_timeout_sec
  form.maxStreams = c.max_streams
  form.logLevel = c.log_level
}

async function load() {
  loading.value = true
  try {
    const m = await api.getTunnelMCP(props.tunnelId)
    enabled.value = m.enabled
    urls.value = m.urls
    oauthAvailable.value = m.oauth_available
    applyConfig(m.config)
    if (props.privileged && form.auth === 'apikey') void loadKeys()
    void loadStats()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('mcpTunnel.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const res = await api.setTunnelMCP(props.tunnelId, {
      enabled: enabled.value,
      config: {
        auth: form.auth, ack_no_auth: form.auth === 'none' ? form.ackNoAuth : false,
        path: form.path, transport: form.transport,
        allow_org_members: form.allowOrgMembers, allowed_emails: lines(form.emails),
        allow_tools: lines(form.allowTools), deny_tools: lines(form.denyTools),
        rate_limit_per_min: Number(form.rate) || 0,
        head_timeout_sec: Number(form.headTimeout) || 300, idle_timeout_sec: Number(form.idleTimeout) || 900,
        max_streams: Number(form.maxStreams) || 20, log_level: form.logLevel,
      },
    })
    urls.value = res.urls
    applyConfig(res.config)
    toast.success(t('mcpTunnel.saved'))
    if (form.auth === 'apikey') void loadKeys()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('mcpTunnel.saveFailed'))
  } finally {
    saving.value = false
  }
}

// --- baglanti URL'si / kopyalama ---
const primaryUrl = computed(() => urls.value[0] || '')
function copy(text: string) {
  navigator.clipboard.writeText(text)
  toast.success(t('common.copied'))
}
const cliSnippet = computed(() => {
  const h = form.auth === 'apikey' ? ' --header "Authorization: Bearer YOUR_KEY"' : ''
  return `claude mcp add --transport http my-server ${primaryUrl.value || 'https://<host>/mcp'}${h}`
})

// --- anahtarlar ---
const keys = ref<MCPKey[]>([])
const newLabel = ref('')
const newDays = ref(0)
const createdKey = ref<MCPKeyCreated | null>(null)
async function loadKeys() {
  try { keys.value = await api.listMCPKeys(props.tunnelId) } catch { keys.value = [] }
}
async function createKey() {
  try {
    createdKey.value = await api.createMCPKey(props.tunnelId, { label: newLabel.value, expires_in_days: Number(newDays.value) || undefined })
    newLabel.value = ''
    await loadKeys()
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('mcpTunnel.keyFailed'))
  }
}
async function removeKey(k: MCPKey) {
  if (!confirm(t('mcpTunnel.keyDeleteConfirm', { name: k.label }))) return
  try { await api.deleteMCPKey(props.tunnelId, k.id); await loadKeys() } catch { toast.error(t('mcpTunnel.keyFailed')) }
}

// --- sina ---
const testing = ref(false)
const testRes = ref<MCPTestResult | null>(null)
async function runTest() {
  testing.value = true
  try { testRes.value = await api.testTunnelMCP(props.tunnelId) }
  catch (e: any) { toast.error(e?.data?.error?.message || t('mcpTunnel.testFailed')) }
  finally { testing.value = false }
}
const toolNames = computed(() => testRes.value?.probe?.tool_names ?? [])
function addTool(list: 'allowTools' | 'denyTools', name: string) {
  const cur = lines(form[list])
  if (!cur.includes(name)) form[list] = [...cur, name].join('\n')
}

async function revokeSessions() {
  if (!confirm(t('mcpTunnel.revokeConfirm'))) return
  try {
    const r = await api.revokeMCPSessions(props.tunnelId)
    toast.success(t('mcpTunnel.revoked', { n: r.revoked }))
  } catch { toast.error(t('mcpTunnel.saveFailed')) }
}

// --- analitik ---
const statsWindow = ref('24h')
const stats = ref<MCPStats | null>(null)
const calls = ref<MCPCall[]>([])
const callStatus = ref('')
async function loadStats() {
  try {
    stats.value = await api.getMCPStats(props.tunnelId, statsWindow.value)
    calls.value = await api.listMCPCalls(props.tunnelId, { status: callStatus.value || undefined, window: statsWindow.value, limit: 30 })
  } catch { stats.value = null; calls.value = [] }
}
watch([statsWindow, callStatus], loadStats)
const errRate = (s: { calls: number, errors: number }) => (s.calls ? Math.round((s.errors / s.calls) * 100) : 0)
const fmtTime = (ts: string) => new Date(ts).toLocaleString()
const statusClass = (s: string) => s === 'ok' ? 'text-accent' : s === 'error' ? 'text-danger' : 'text-warn'

onMounted(load)
</script>

<template>
  <div class="space-y-5">
    <p class="text-xs text-fg-muted">{{ t('mcpTunnel.intro') }}</p>
    <p v-if="loading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>

    <template v-else>
      <label class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
        <span class="flex items-center gap-2 text-sm text-fg"><Icon name="lucide:plug-zap" class="size-4 text-accent" />{{ t('mcpTunnel.enable') }}</span>
        <input v-model="enabled" type="checkbox" :disabled="!privileged" class="accent-[var(--color-accent)]">
      </label>

      <!-- Baglanti URL'si -->
      <div v-if="primaryUrl" class="space-y-2 rounded border border-line bg-surface-2 p-3">
        <span class="label-sys block">{{ t('mcpTunnel.connectUrl') }}</span>
        <div v-for="u in urls" :key="u" class="flex items-center gap-2">
          <code class="flex-1 truncate rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs text-fg">{{ u }}</code>
          <button type="button" class="btn-ghost" :title="t('common.copy')" @click="copy(u)"><Icon name="lucide:copy" class="size-3.5" /></button>
        </div>
        <p class="text-xs text-fg-muted">{{ t('mcpTunnel.claudeSteps') }}</p>
        <div class="flex items-center gap-2">
          <code class="flex-1 truncate rounded border border-line bg-bg px-2 py-1.5 font-mono text-[11px] text-fg">{{ cliSnippet }}</code>
          <button type="button" class="btn-ghost" :title="t('common.copy')" @click="copy(cliSnippet)"><Icon name="lucide:copy" class="size-3.5" /></button>
        </div>
      </div>
      <p v-else class="text-xs text-warn">{{ t('mcpTunnel.noUrl') }}</p>

      <!-- Kimlik dogrulama -->
      <div class="space-y-3 rounded border border-line p-3">
        <span class="label-sys block">{{ t('mcpTunnel.auth') }}</span>
        <select v-model="form.auth" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent">
          <option value="oauth" :disabled="!oauthAvailable">{{ t('mcpTunnel.authOauth') }}</option>
          <option value="apikey">{{ t('mcpTunnel.authApikey') }}</option>
          <option value="none">{{ t('mcpTunnel.authNone') }}</option>
        </select>
        <p v-if="!oauthAvailable" class="text-xs text-warn">{{ t('mcpTunnel.oauthUnavailable') }}</p>

        <template v-if="form.auth === 'oauth'">
          <p class="text-xs text-fg-muted">{{ t('mcpTunnel.oauthHelp') }}</p>
          <label class="flex cursor-pointer items-center justify-between rounded border border-line px-3 py-2">
            <span class="text-sm text-fg">{{ t('mcpTunnel.orgMembers') }}</span>
            <input v-model="form.allowOrgMembers" type="checkbox" :disabled="!privileged" class="accent-[var(--color-accent)]">
          </label>
          <div>
            <span class="label-sys mb-1 block">{{ t('mcpTunnel.allowedEmails') }}</span>
            <textarea v-model="form.emails" rows="3" :disabled="!privileged" placeholder="ali@example.com&#10;*@example.com" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent" />
            <p class="mt-1 text-xs text-fg-muted">{{ t('mcpTunnel.allowedEmailsHelp') }}</p>
          </div>
          <div v-if="privileged">
            <button type="button" class="btn-ghost text-xs" @click="revokeSessions"><Icon name="lucide:log-out" class="mr-1 size-3.5" />{{ t('mcpTunnel.revokeSessions') }}</button>
          </div>
        </template>

        <template v-else-if="form.auth === 'apikey'">
          <p class="text-xs text-fg-muted">{{ t('mcpTunnel.apikeyHelp') }}</p>
          <ul v-if="keys.length" class="divide-y divide-line rounded border border-line text-xs">
            <li v-for="k in keys" :key="k.id" class="flex items-center justify-between gap-2 px-3 py-2">
              <span class="min-w-0 truncate"><span class="font-medium text-fg">{{ k.label }}</span> <code class="font-mono text-fg-muted">{{ k.prefix }}…</code></span>
              <span class="shrink-0 text-fg-muted">{{ k.expires_at ? t('mcpTunnel.expires', { d: fmtTime(k.expires_at) }) : t('mcpTunnel.noExpiry') }}</span>
              <button v-if="privileged" class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="removeKey(k)"><Icon name="lucide:trash-2" class="size-3.5" /></button>
            </li>
          </ul>
          <div v-if="privileged" class="flex flex-wrap gap-2">
            <input v-model="newLabel" :placeholder="t('mcpTunnel.keyLabel')" maxlength="64" class="min-w-0 flex-1 rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
            <input v-model.number="newDays" type="number" min="0" max="3650" :placeholder="t('mcpTunnel.keyDays')" class="w-28 rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
            <button type="button" :disabled="!newLabel.trim()" class="rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent disabled:opacity-50" @click="createKey">{{ t('mcpTunnel.keyCreate') }}</button>
          </div>
          <div v-if="createdKey" class="rounded border border-warn/40 bg-warn/10 p-3 text-xs">
            <p class="mb-1 font-medium text-fg">{{ t('mcpTunnel.keyShownOnce') }}</p>
            <div class="flex items-center gap-2">
              <code class="flex-1 break-all font-mono text-fg">{{ createdKey.key }}</code>
              <button type="button" class="btn-ghost" @click="copy(createdKey.key)"><Icon name="lucide:copy" class="size-3.5" /></button>
            </div>
          </div>
        </template>

        <template v-else>
          <div class="flex items-start gap-2 rounded border border-danger/40 bg-danger/10 p-3 text-xs text-fg">
            <Icon name="lucide:shield-alert" class="mt-0.5 size-4 shrink-0 text-danger" />
            <span>{{ t('mcpTunnel.noneWarn') }}</span>
          </div>
          <label class="flex cursor-pointer items-center gap-2 text-sm text-fg">
            <input v-model="form.ackNoAuth" type="checkbox" :disabled="!privileged" class="accent-[var(--color-danger)]">
            {{ t('mcpTunnel.noneAck') }}
          </label>
        </template>
      </div>

      <!-- Arac izin/ret listesi -->
      <div class="space-y-3 rounded border border-line p-3">
        <span class="label-sys block">{{ t('mcpTunnel.policy') }}</span>
        <p class="text-xs text-fg-muted">{{ t('mcpTunnel.policyHelp') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <span class="label-sys mb-1 block">{{ t('mcpTunnel.allowTools') }}</span>
            <textarea v-model="form.allowTools" rows="4" :disabled="!privileged" placeholder="search_*&#10;read_file" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent" />
          </div>
          <div>
            <span class="label-sys mb-1 block">{{ t('mcpTunnel.denyTools') }}</span>
            <textarea v-model="form.denyTools" rows="4" :disabled="!privileged" placeholder="delete_*&#10;run_shell" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent" />
          </div>
        </div>
        <div v-if="toolNames.length" class="flex flex-wrap gap-1.5">
          <span v-for="n in toolNames" :key="n" class="inline-flex items-center gap-1 rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-fg">
            {{ n }}
            <button v-if="privileged" type="button" :title="t('mcpTunnel.addAllow')" class="text-accent hover:opacity-80" @click="addTool('allowTools', n)"><Icon name="lucide:check" class="size-3" /></button>
            <button v-if="privileged" type="button" :title="t('mcpTunnel.addDeny')" class="text-danger hover:opacity-80" @click="addTool('denyTools', n)"><Icon name="lucide:ban" class="size-3" /></button>
          </span>
        </div>
        <p class="flex items-start gap-1.5 text-xs text-fg-subtle">
          <Icon name="lucide:info" class="mt-0.5 size-3.5 shrink-0" />{{ t('mcpTunnel.legacyNote') }}
        </p>
      </div>

      <!-- Sinirlar -->
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.rate') }}</span>
          <input v-model.number="form.rate" type="number" min="0" max="6000" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent"></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.maxStreams') }}</span>
          <input v-model.number="form.maxStreams" type="number" min="1" max="200" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent"></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.headTimeout') }}</span>
          <input v-model.number="form.headTimeout" type="number" min="1" max="900" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent"></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.idleTimeout') }}</span>
          <input v-model.number="form.idleTimeout" type="number" min="1" max="3600" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent"></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.path') }}</span>
          <input v-model="form.path" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-sm focus:border-accent"></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.transport') }}</span>
          <select v-model="form.transport" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent">
            <option value="auto">auto</option><option value="streamable">streamable HTTP</option><option value="sse">HTTP+SSE (2024-11-05)</option>
          </select></label>
        <label class="block"><span class="label-sys mb-1 block">{{ t('mcpTunnel.logLevel') }}</span>
          <select v-model="form.logLevel" :disabled="!privileged" class="w-full rounded border border-line bg-bg px-2 py-1.5 text-sm focus:border-accent">
            <option value="meta">{{ t('mcpTunnel.logMeta') }}</option><option value="none">{{ t('mcpTunnel.logNone') }}</option>
          </select></label>
      </div>

      <div v-if="privileged" class="flex flex-wrap items-center justify-end gap-2">
        <button type="button" :disabled="testing || !enabled" class="btn-ghost text-sm" @click="runTest">
          <Icon :name="testing ? 'lucide:loader-2' : 'lucide:activity'" class="mr-1 size-4" :class="{ 'animate-spin': testing }" />{{ t('mcpTunnel.test') }}
        </button>
        <button :disabled="saving" class="rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="save">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>

      <!-- Sina sonucu -->
      <ul v-if="testRes" class="space-y-1 rounded border border-line p-3 text-xs">
        <li class="flex items-center gap-2"><Icon :name="testRes.probe.agent_online ? 'lucide:check-circle' : 'lucide:x-circle'" class="size-3.5" :class="testRes.probe.agent_online ? 'text-accent' : 'text-danger'" />{{ t('mcpTunnel.chkAgent') }}</li>
        <li class="flex items-center gap-2"><Icon :name="testRes.checks.auth_ready ? 'lucide:check-circle' : 'lucide:x-circle'" class="size-3.5" :class="testRes.checks.auth_ready ? 'text-accent' : 'text-danger'" />{{ t('mcpTunnel.chkAuth') }}</li>
        <li class="flex items-center gap-2"><Icon :name="testRes.probe.initialize.ok ? 'lucide:check-circle' : 'lucide:x-circle'" class="size-3.5" :class="testRes.probe.initialize.ok ? 'text-accent' : 'text-danger'" />{{ t('mcpTunnel.chkInit') }}<span class="text-fg-muted">{{ testRes.probe.server_name }} {{ testRes.probe.initialize.error }}</span></li>
        <li class="flex items-center gap-2"><Icon :name="testRes.probe.tools_list.ok ? 'lucide:check-circle' : 'lucide:x-circle'" class="size-3.5" :class="testRes.probe.tools_list.ok ? 'text-accent' : 'text-danger'" />{{ t('mcpTunnel.chkTools', { n: testRes.probe.tool_count }) }}<span class="text-fg-muted">{{ testRes.probe.tools_list.error }}</span></li>
      </ul>

      <!-- Analitik -->
      <div class="space-y-3 rounded border border-line p-3">
        <div class="flex items-center justify-between">
          <span class="label-sys flex items-center gap-1.5"><Icon name="lucide:bar-chart-3" class="size-3.5" />{{ t('mcpTunnel.analytics') }}</span>
          <select v-model="statsWindow" class="rounded border border-line bg-bg px-2 py-1 text-xs">
            <option value="1h">1h</option><option value="24h">24h</option><option value="7d">7d</option><option value="30d">30d</option>
          </select>
        </div>
        <div v-if="stats && stats.tools.length" class="overflow-x-auto">
          <table class="w-full text-left text-xs">
            <thead class="text-fg-muted"><tr>
              <th class="py-1 pr-3 font-medium">{{ t('mcpTunnel.colTool') }}</th><th class="px-2 font-medium">{{ t('mcpTunnel.colCalls') }}</th>
              <th class="px-2 font-medium">{{ t('mcpTunnel.colErrors') }}</th><th class="px-2 font-medium">{{ t('mcpTunnel.colDenied') }}</th>
              <th class="px-2 font-medium">{{ t('mcpTunnel.colAvg') }}</th><th class="px-2 font-medium">p95</th><th class="px-2 font-medium">max</th>
            </tr></thead>
            <tbody>
              <tr v-for="s in stats.tools" :key="s.tool" class="border-t border-line">
                <td class="py-1 pr-3 font-mono text-fg">{{ s.tool }}</td><td class="px-2">{{ s.calls }}</td>
                <td class="px-2" :class="s.errors ? 'text-danger' : ''">{{ s.errors }} <span class="text-fg-muted">({{ errRate(s) }}%)</span></td>
                <td class="px-2" :class="s.denied ? 'text-warn' : ''">{{ s.denied }}</td>
                <td class="px-2">{{ Math.round(s.avg_ms) }} ms</td><td class="px-2">{{ s.p95_ms }} ms</td><td class="px-2">{{ s.max_ms }} ms</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="text-xs text-fg-muted">{{ t('mcpTunnel.noStats') }}</p>

        <div class="flex items-center justify-between pt-1">
          <span class="label-sys">{{ t('mcpTunnel.recentCalls') }}</span>
          <select v-model="callStatus" class="rounded border border-line bg-bg px-2 py-1 text-xs">
            <option value="">{{ t('common.all') }}</option><option value="ok">ok</option><option value="error">error</option>
            <option value="denied">denied</option><option value="rate_limited">rate_limited</option>
          </select>
        </div>
        <ul v-if="calls.length" class="divide-y divide-line text-xs">
          <li v-for="c in calls" :key="c.id" class="flex flex-wrap items-center gap-x-3 gap-y-0.5 py-1">
            <span class="text-fg-muted">{{ fmtTime(c.ts) }}</span>
            <span class="font-mono text-fg">{{ c.method }}<template v-if="c.tool"> · {{ c.tool }}</template></span>
            <span :class="statusClass(c.status)">{{ c.status }}</span>
            <span class="text-fg-muted">{{ c.duration_ms }} ms</span>
            <span class="truncate text-fg-subtle">{{ c.principal }}</span>
          </li>
        </ul>
        <p class="text-xs text-fg-subtle">{{ t('mcpTunnel.privacy') }}</p>
      </div>
    </template>
  </div>
</template>
