<script setup lang="ts">
// Zorven Net — Tam ağ katmanı (Mod B, OS TUN): Mod B düğümleri, IP/DNS adları,
// ağ aralığı ve cihazdan cihaza erişim kuralları (varsayılan hepsi kapalı).
// Kural/aralık yönetimi yalnızca sahip/yönetici içindir (API 403 → gizlenir).
import type { NetNode, NetTunRule, NetNetwork, Client } from '~/types/api'

const props = defineProps<{ clients: Client[] }>()

const api = useApi()
const toast = useToast()
const { t } = useI18n()

const nodes = ref<NetNode[]>([])
const rules = ref<NetTunRule[]>([])
const network = ref<NetNetwork | null>(null)
const canManage = ref(true)
const loadError = ref('')

function errMsg(e: any, fallback: string): string {
  return e?.data?.error?.message || e?.data?.error || e?.message || fallback
}

const tunNodes = computed(() => nodes.value.filter(n => n.kind === 'device' && n.tun))
const otherCount = computed(() => nodes.value.filter(n => !(n.kind === 'device' && n.tun)).length)

function dnsName(n: NetNode): string {
  return `${n.name}.zorven.internal`
}

async function load() {
  loadError.value = ''
  try {
    nodes.value = await api.listNetNodes()
    network.value = await api.getNetNetwork()
  } catch (e: any) {
    loadError.value = errMsg(e, t('nettun.loadError'))
    return
  }
  try {
    rules.value = await api.listNetTunRules()
    canManage.value = true
  } catch (e: any) {
    // 403: sahip/yönetici değil -> kural yönetimi gizlenir
    canManage.value = false
    rules.value = []
  }
}

function devName(id: string, fallback?: string): string {
  return props.clients.find(c => c.id === id)?.name || fallback || id
}
function srcLabel(r: NetTunRule): string {
  return r.src.type === 'group' ? t('nettun.srcEveryone') : devName(r.src.id, r.src_name)
}
function dstLabel(r: NetTunRule): string {
  return r.dst === '*' ? t('nettun.dstAll') : devName(r.dst, r.dst_name)
}
function protoLabel(p: string): string {
  return p === 'any' ? t('nettun.protoAny') : p.toUpperCase()
}

// --- Kural oluşturma ---
const fSrc = ref('*') // '*' = tüm cihazlar
const fDst = ref('*')
const fProto = ref<'any' | 'tcp' | 'udp' | 'icmp'>('tcp')
const fPorts = ref('')
const saving = ref(false)

async function addRule() {
  saving.value = true
  try {
    await api.createNetTunRule({
      src: fSrc.value === '*' ? { type: 'group', id: 'everyone' } : { type: 'device', id: fSrc.value },
      dst: fDst.value,
      proto: fProto.value,
      ...(fProto.value !== 'icmp' && fPorts.value.trim() ? { ports: fPorts.value.trim() } : {}),
    })
    fPorts.value = ''
    await load()
    toast.success(t('nettun.ruleAdded'))
  } catch (e: any) {
    toast.error(errMsg(e, t('nettun.actionFailed')))
  } finally {
    saving.value = false
  }
}

async function removeRule(r: NetTunRule) {
  if (!confirm(t('nettun.ruleDeleteConfirm'))) return
  try {
    await api.deleteNetTunRule(r.id)
    await load()
    toast.success(t('nettun.ruleDeleted'))
  } catch (e: any) {
    toast.error(errMsg(e, t('nettun.actionFailed')))
  }
}

// --- Ağ aralığı ---
const editNet = ref(false)
const fCidr = ref('')
async function saveNet() {
  saving.value = true
  try {
    await api.setNetNetwork(fCidr.value.trim())
    editNet.value = false
    await load()
    toast.success(t('nettun.networkSaved'))
  } catch (e: any) {
    toast.error(errMsg(e, t('nettun.actionFailed')))
  } finally {
    saving.value = false
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

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { load(); timer = setInterval(() => { if (!saving.value) load() }, 15000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="rounded-xl border border-line bg-surface p-5">
    <h2 class="flex items-center gap-2 text-sm font-semibold text-fg">
      <Icon name="lucide:network" class="size-4 text-accent" />
      {{ t('nettun.title') }}
    </h2>
    <p class="mt-1 text-xs leading-relaxed text-fg-muted">{{ t('nettun.subtitle') }}</p>
    <ul class="mt-3 list-disc space-y-1 pl-5 text-xs leading-relaxed text-fg-muted">
      <li>{{ t('nettun.howUp') }}</li>
      <li>{{ t('nettun.howDown') }}</li>
    </ul>

    <p v-if="loadError" class="mt-4 text-xs text-danger">{{ loadError }}</p>

    <template v-else>
      <!-- Mod B düğümleri -->
      <h3 class="mt-5 text-xs font-medium text-fg-muted">{{ t('nettun.nodesTitle') }}</h3>
      <div v-if="!tunNodes.length" class="mt-2 rounded-lg border border-dashed border-line p-5 text-center text-xs text-fg-muted">
        {{ t('nettun.nodesEmpty') }}
      </div>
      <div v-else class="mt-2 overflow-x-auto rounded-lg border border-line">
        <table class="w-full text-left text-xs">
          <thead class="text-[11px] text-fg-subtle">
            <tr>
              <th class="px-3 py-2 font-medium">{{ t('nettun.colDevice') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('nettun.colIp') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('nettun.colDns') }}</th>
              <th class="px-3 py-2 font-medium">{{ t('nettun.colStatus') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="n in tunNodes" :key="n.id" class="border-t border-line">
              <td class="px-3 py-2 text-fg">{{ n.name }}</td>
              <td class="px-3 py-2 font-mono text-fg">{{ n.ip }}</td>
              <td class="px-3 py-2">
                <button class="inline-flex items-center gap-1 font-mono text-fg-muted hover:text-fg" :title="t('nettun.copy')" @click="copy(dnsName(n))">
                  {{ dnsName(n) }}
                  <Icon :name="copied === dnsName(n) ? 'lucide:check' : 'lucide:copy'" class="size-3" />
                </button>
              </td>
              <td class="px-3 py-2">
                <span class="inline-flex items-center gap-1.5">
                  <span class="size-1.5 rounded-full" :class="n.online ? 'bg-success' : 'bg-fg-subtle'" />
                  {{ n.online ? t('nettun.online') : t('nettun.offline') }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="otherCount" class="mt-1.5 text-[11px] text-fg-subtle">{{ t('nettun.otherNodes', { n: otherCount }) }}</p>

      <!-- Ağ aralığı -->
      <div v-if="network" class="mt-5 border-t border-line pt-4">
        <h3 class="text-xs font-medium text-fg-muted">{{ t('nettun.networkTitle') }}</h3>
        <p class="mt-1 text-[11px] leading-relaxed text-fg-subtle">
          {{ t('nettun.networkHint', { cidr: network.cidr, resolver: network.resolver }) }}
        </p>
        <div v-if="canManage" class="mt-2">
          <button v-if="!editNet" class="rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:text-fg" @click="editNet = true; fCidr = network.cidr">
            {{ t('nettun.networkChange') }}
          </button>
          <form v-else class="space-y-1.5" @submit.prevent="saveNet">
            <div class="flex flex-wrap gap-2">
              <input v-model="fCidr" type="text" :placeholder="t('nettun.networkPh')" class="min-w-40 flex-1 rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
              <button type="submit" class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving || !fCidr.trim()">
                <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
                {{ t('nettun.networkSave') }}
              </button>
            </div>
            <p class="text-[11px] text-fg-subtle">{{ t('nettun.networkLocked') }}</p>
          </form>
        </div>
      </div>

      <!-- Erişim kuralları -->
      <div class="mt-5 border-t border-line pt-4">
        <h3 class="text-xs font-medium text-fg-muted">{{ t('nettun.rulesTitle') }}</h3>
        <p v-if="!canManage" class="mt-2 flex items-start gap-1.5 text-[11px] text-fg-subtle">
          <Icon name="lucide:lock" class="mt-px size-3.5 shrink-0" />
          <span>{{ t('nettun.adminOnly') }}</span>
        </p>
        <template v-else>
          <p class="mt-1 text-[11px] leading-relaxed text-fg-subtle">{{ t('nettun.rulesIntro') }}</p>
          <div v-if="!rules.length" class="mt-2 rounded-lg border border-dashed border-line p-5 text-center text-xs text-fg-muted">
            {{ t('nettun.rulesEmpty') }}
          </div>
          <div v-else class="mt-2 space-y-1.5">
            <div v-for="r in rules" :key="r.id" class="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2 text-xs">
              <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-fg">
                <span class="font-medium">{{ srcLabel(r) }}</span>
                <Icon name="lucide:arrow-right" class="size-3.5 text-fg-subtle" />
                <span class="font-medium">{{ dstLabel(r) }}</span>
                <span class="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium uppercase text-accent">
                  {{ protoLabel(r.proto) }}<template v-if="r.proto !== 'icmp'"> · {{ r.ports || t('nettun.allPorts') }}</template>
                </span>
              </div>
              <button class="shrink-0 rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:border-danger hover:text-danger" @click="removeRule(r)">
                {{ t('nettun.ruleDelete') }}
              </button>
            </div>
          </div>

          <form class="mt-3 space-y-2" @submit.prevent="addRule">
            <div class="grid gap-2 sm:grid-cols-4">
              <label class="text-[11px] text-fg-subtle">{{ t('nettun.ruleSrc') }}
                <select v-model="fSrc" class="mt-0.5 w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
                  <option value="*">{{ t('nettun.srcEveryone') }}</option>
                  <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
                </select>
              </label>
              <label class="text-[11px] text-fg-subtle">{{ t('nettun.ruleDst') }}
                <select v-model="fDst" class="mt-0.5 w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
                  <option value="*">{{ t('nettun.dstAll') }}</option>
                  <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
                </select>
              </label>
              <label class="text-[11px] text-fg-subtle">{{ t('nettun.ruleProto') }}
                <select v-model="fProto" class="mt-0.5 w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                  <option value="icmp">ICMP</option>
                  <option value="any">{{ t('nettun.protoAny') }}</option>
                </select>
              </label>
              <label class="text-[11px] text-fg-subtle">{{ t('nettun.rulePorts') }}
                <input v-model="fPorts" type="text" :disabled="fProto === 'icmp'" :placeholder="t('nettun.portsPh')" class="mt-0.5 w-full rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent disabled:opacity-50">
              </label>
            </div>
            <button type="submit" class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving">
              <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
              <Icon v-else name="lucide:plus" class="size-4" />
              {{ t('nettun.ruleAdd') }}
            </button>
          </form>
        </template>
      </div>

      <p class="mt-4 flex items-start gap-1.5 text-[11px] leading-relaxed text-fg-subtle">
        <Icon name="lucide:shield-check" class="mt-px size-3.5 shrink-0" />
        <span>{{ t('nettun.dnsNote') }}</span>
      </p>
    </template>
  </section>
</template>
