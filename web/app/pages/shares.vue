<script setup lang="ts">
// Zorven Net — Paylaşımlar (WireGuard Mod A): bir cihazın portunu seçilen
// kişi/grup/cihaza uçtan uca şifreli paylaşır. Sunucu yalnız şifreli paketleri
// iletir. Alıcı `zorven access` ile yerel porttan bağlanır.
import type { NetShare, NetGrantee, Client, TeamMember } from '~/types/api'

const api = useApi()
const toast = useToast()
const { t } = useI18n()

const loading = ref(true)
const unavailable = ref('')
const shares = ref<NetShare[]>([])
const clients = ref<Client[]>([])
const members = ref<TeamMember[]>([])

function errMsg(e: any, fallback: string): string {
  return e?.data?.error?.message || e?.data?.error || e?.message || fallback
}

const mine = computed(() => shares.value.filter(s => s.can_manage))
const incoming = computed(() => shares.value.filter(s => s.relation === 'grantee'))

async function load() {
  loading.value = true
  unavailable.value = ''
  try {
    const [sh, cl] = await Promise.all([api.listNetShares(), api.listClients()])
    shares.value = sh
    clients.value = cl
    try {
      members.value = (await api.listTeamMembers()).members.filter(m => m.status !== 'pending' && m.user_id)
    } catch { members.value = [] }
  } catch (e: any) {
    unavailable.value = e?.status === 503 || e?.statusCode === 503 ? t('shares.unavailable') : errMsg(e, t('shares.loadError'))
  } finally {
    loading.value = false
  }
}

function granteeLabel(g: NetGrantee): string {
  if (g.type === 'user') {
    const m = members.value.find(x => x.user_id === g.id)
    return m ? (m.name || m.email) : g.id
  }
  if (g.type === 'device') return clients.value.find(c => c.id === g.id)?.name || g.id
  const key: Record<string, string> = {
    'everyone': 'groupEveryone', 'role:owner': 'groupOwners', 'role:admin': 'groupAdmins', 'role:member': 'groupMembers',
  }
  return key[g.id] ? t(`shares.${key[g.id]}`) : g.id
}

function fmtExp(s: NetShare): string {
  return s.expires_at ? new Date(s.expires_at).toLocaleString() : t('shares.never')
}

// --- Oluşturma ---
const fClient = ref('')
const fPort = ref<number | null>(null)
const fProto = ref<'tcp' | 'udp'>('tcp')
const fName = ref('')
const fExp = ref(0)
const fGrantees = ref<NetGrantee[]>([])
const fType = ref<'user' | 'group' | 'device'>('user')
const fPick = ref('')
const saving = ref(false)

const pickOptions = computed<{ id: string, label: string }[]>(() => {
  if (fType.value === 'user') return members.value.map(m => ({ id: m.user_id, label: m.name || m.email }))
  if (fType.value === 'device') return clients.value.map(c => ({ id: c.id, label: c.name }))
  return [
    { id: 'everyone', label: t('shares.groupEveryone') },
    { id: 'role:owner', label: t('shares.groupOwners') },
    { id: 'role:admin', label: t('shares.groupAdmins') },
    { id: 'role:member', label: t('shares.groupMembers') },
  ]
})

function addGrantee() {
  if (!fPick.value) return
  if (!fGrantees.value.some(g => g.type === fType.value && g.id === fPick.value)) {
    fGrantees.value.push({ type: fType.value, id: fPick.value })
  }
  fPick.value = ''
}

async function create() {
  if (!fClient.value || !fPort.value) return
  if (!fGrantees.value.length) { toast.error(t('shares.needGrantee')); return }
  saving.value = true
  try {
    await api.createNetShare({
      client_id: fClient.value, port: fPort.value, proto: fProto.value,
      ...(fName.value.trim() ? { name: fName.value.trim().toLowerCase() } : {}),
      to: fGrantees.value, ...(fExp.value ? { expires_in_sec: fExp.value } : {}),
    })
    fPort.value = null
    fName.value = ''
    fGrantees.value = []
    await load()
    toast.success(t('shares.created'))
  } catch (e: any) {
    toast.error(errMsg(e, t('shares.actionFailed')))
  } finally {
    saving.value = false
  }
}

async function revoke(s: NetShare) {
  if (!confirm(t('shares.revokeConfirm', { name: s.name }))) return
  try {
    await api.deleteNetShare(s.id)
    await load()
    toast.success(t('shares.revoked'))
  } catch (e: any) {
    toast.error(errMsg(e, t('shares.actionFailed')))
  }
}

const copied = ref('')
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = text
    setTimeout(() => { if (copied.value === text) copied.value = '' }, 1500)
  } catch { /* pano erişimi yok */ }
}
function accessCmd(s: NetShare): string {
  return `ZORVEN_API_TOKEN=zrv_api_... zorven access ${s.name} --local ${s.port}`
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { load(); timer = setInterval(() => { if (!saving.value) load() }, 15000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <div class="mx-auto max-w-4xl space-y-6 px-4 py-6 sm:px-5">
    <div>
      <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
        <Icon name="lucide:share-2" class="size-5 text-accent" />
        {{ t('shares.title') }}
      </h1>
      <p class="mt-1 text-sm text-fg-muted">{{ t('shares.subtitle') }}</p>
    </div>

    <div v-if="loading && !shares.length && !unavailable" class="py-12 text-center text-fg-muted">
      <Icon name="lucide:loader-circle" class="mx-auto size-6 animate-spin" />
    </div>

    <div v-else-if="unavailable" class="rounded-xl border border-line bg-surface p-8 text-center">
      <Icon name="lucide:lock" class="mx-auto size-8 text-fg-muted" />
      <p class="mt-3 text-sm text-fg">{{ unavailable }}</p>
    </div>

    <template v-else>
      <section class="rounded-xl border border-line bg-surface p-5">
        <h2 class="text-sm font-semibold text-fg">{{ t('shares.howTitle') }}</h2>
        <ul class="mt-3 list-disc space-y-1.5 pl-5 text-xs leading-relaxed text-fg-muted">
          <li>{{ t('shares.howShare') }}</li>
          <li>{{ t('shares.howAccess') }}</li>
        </ul>
        <p class="mt-3 flex items-start gap-1.5 text-[11px] leading-relaxed text-fg-subtle">
          <Icon name="lucide:shield-check" class="mt-px size-3.5 shrink-0" />
          <span>{{ t('shares.relayNote') }}</span>
        </p>
      </section>

      <!-- Yönettiklerim -->
      <section class="rounded-xl border border-line bg-surface p-5">
        <h2 class="text-sm font-semibold text-fg">{{ t('shares.mine') }}</h2>
        <div v-if="!mine.length" class="mt-4 rounded-lg border border-dashed border-line p-6 text-center text-xs text-fg-muted">
          {{ t('shares.empty') }}
        </div>
        <div v-else class="mt-4 space-y-2">
          <div v-for="s in mine" :key="s.id" class="rounded-lg border border-line px-3 py-2.5 text-sm">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="font-mono text-fg">
                  {{ s.name }}
                  <span class="ml-1 rounded bg-accent/15 px-1.5 py-0.5 font-sans text-[10px] font-medium uppercase text-accent">{{ s.proto }}/{{ s.port }}</span>
                </div>
                <div class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-fg-subtle">
                  <span class="inline-flex items-center gap-1">
                    <span class="size-1.5 rounded-full" :class="s.owner_online ? 'bg-success' : 'bg-fg-subtle'" />
                    {{ s.owner_name || s.owner_client_id }}
                    <span class="text-fg-subtle">({{ s.owner_online ? t('shares.ownerOnline') : t('shares.ownerOffline') }})</span>
                  </span>
                  <span>{{ t('shares.expires') }}: {{ fmtExp(s) }}</span>
                </div>
                <div class="mt-1.5 flex flex-wrap gap-1">
                  <span v-for="g in s.grantees || []" :key="g.type + g.id" class="rounded-full border border-line px-2 py-0.5 text-[11px] text-fg-muted">
                    {{ granteeLabel(g) }}
                  </span>
                </div>
                <div class="mt-1.5 text-[11px] text-fg-subtle">
                  {{ t('shares.connected') }}:
                  <template v-if="s.connected.length">
                    <span v-for="n in s.connected" :key="n.id" class="mr-1.5 font-mono">{{ n.name }}</span>
                  </template>
                  <template v-else>{{ t('shares.noneConnected') }}</template>
                </div>
              </div>
              <button class="shrink-0 rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:border-danger hover:text-danger" @click="revoke(s)">
                {{ t('shares.revoke') }}
              </button>
            </div>
          </div>
        </div>

        <form class="mt-5 space-y-3 border-t border-line pt-4" @submit.prevent="create">
          <div class="text-xs font-medium text-fg-muted">{{ t('shares.create') }}</div>
          <div class="grid gap-2 sm:grid-cols-4">
            <select v-model="fClient" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent sm:col-span-2">
              <option value="" disabled>{{ t('shares.devicePh') }}</option>
              <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
            <input v-model.number="fPort" type="number" min="1" max="65535" :placeholder="t('shares.port')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
            <select v-model="fProto" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </select>
          </div>
          <div class="grid gap-2 sm:grid-cols-2">
            <input v-model="fName" type="text" :placeholder="t('shares.name') + ' — ' + t('shares.namePh')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
            <select v-model.number="fExp" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
              <option :value="0">{{ t('shares.expNone') }}</option>
              <option :value="3600">{{ t('shares.exp1h') }}</option>
              <option :value="86400">{{ t('shares.exp24h') }}</option>
              <option :value="604800">{{ t('shares.exp7d') }}</option>
            </select>
          </div>
          <div>
            <div class="mb-1 text-[11px] text-fg-subtle">{{ t('shares.grantees') }}</div>
            <div class="flex flex-wrap gap-2">
              <select v-model="fType" class="rounded-lg border border-line bg-bg px-2 py-2 text-sm text-fg outline-none focus:border-accent" @change="fPick = ''">
                <option value="user">{{ t('shares.granteeUser') }}</option>
                <option value="group">{{ t('shares.granteeGroup') }}</option>
                <option value="device">{{ t('shares.granteeDevice') }}</option>
              </select>
              <select v-model="fPick" class="min-w-40 flex-1 rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent" @change="addGrantee">
                <option value="" disabled>—</option>
                <option v-for="o in pickOptions" :key="o.id" :value="o.id">{{ o.label }}</option>
              </select>
            </div>
            <div v-if="fGrantees.length" class="mt-2 flex flex-wrap gap-1">
              <span v-for="g in fGrantees" :key="g.type + g.id" class="inline-flex items-center gap-1 rounded-full border border-line px-2 py-0.5 text-[11px] text-fg">
                {{ granteeLabel(g) }}
                <button type="button" class="text-fg-muted hover:text-danger" @click="fGrantees = fGrantees.filter(x => x !== g)">
                  <Icon name="lucide:x" class="size-3" />
                </button>
              </span>
            </div>
          </div>
          <button type="submit" class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving || !fClient || !fPort">
            <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
            <Icon v-else name="lucide:plus" class="size-4" />
            {{ t('shares.create') }}
          </button>
        </form>
      </section>

      <!-- Bana açılanlar -->
      <section class="rounded-xl border border-line bg-surface p-5">
        <h2 class="text-sm font-semibold text-fg">{{ t('shares.incoming') }}</h2>
        <div v-if="!incoming.length" class="mt-4 rounded-lg border border-dashed border-line p-6 text-center text-xs text-fg-muted">
          {{ t('shares.emptyIncoming') }}
        </div>
        <div v-else class="mt-4 space-y-2">
          <div v-for="s in incoming" :key="s.id" class="rounded-lg border border-line px-3 py-2.5 text-sm">
            <div class="font-mono text-fg">
              {{ s.name }}
              <span class="ml-1 rounded bg-accent/15 px-1.5 py-0.5 font-sans text-[10px] font-medium uppercase text-accent">{{ s.proto }}/{{ s.port }}</span>
            </div>
            <div class="mt-0.5 text-[11px] text-fg-subtle">{{ s.owner_name }} · {{ t('shares.expires') }}: {{ fmtExp(s) }}</div>
            <div class="mt-1.5 flex items-center gap-2">
              <code class="flex-1 truncate rounded bg-bg px-2 py-1.5 font-mono text-[11px] text-fg">{{ accessCmd(s) }}</code>
              <button class="rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:text-fg" @click="copy(accessCmd(s))">
                {{ copied === accessCmd(s) ? t('shares.copied') : t('shares.copy') }}
              </button>
            </div>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>
