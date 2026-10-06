<script setup lang="ts">
// FAZ 3 / F17 — Zorven Network: özel kaynaklar.
//
// Özel kaynak internete hiç açılmaz; yetkili kullanıcı kendi bilgisayarında
// `zorven connect` çalıştırır ve yerel SOCKS5 proxy üzerinden kaynağa adıyla
// (ör. db.internal:5432) ulaşır. Erişim, kaynağı yayınlayan cihaza erişim
// hakkıyla aynıdır (Cihaz erişimi politikaları).
import type { Tunnel, Client } from '~/types/api'

const api = useApi()
const toast = useToast()
const { t } = useI18n()
const { user, platformAdmin } = useAuth()

const canManage = computed(() => {
  if (platformAdmin.value) return true
  const r = (user.value?.role || '').toLowerCase()
  return r === 'owner' || r === 'admin'
})

const loading = ref(true)
const unavailable = ref('') // plan kapısı vb. — sayfa yerine açıklama gösterilir
const resources = ref<Tunnel[]>([])
const clients = ref<Client[]>([])

function errMsg(e: any, fallback: string): string {
  return e?.data?.error?.message || e?.data?.error || e?.message || fallback
}

async function load() {
  loading.value = true
  unavailable.value = ''
  try {
    const [res, cl] = await Promise.all([api.listNetworkResources(), api.listClients()])
    resources.value = res
    clients.value = cl
  } catch (e: any) {
    unavailable.value = errMsg(e, t('network.loadError'))
  } finally {
    loading.value = false
  }
}

function clientName(id: string): string {
  return clients.value.find(c => c.id === id)?.name || id
}
function clientOnline(id: string): boolean {
  return clients.value.find(c => c.id === id)?.status === 'online'
}
// Alt ağ kaynağı (F20): hedef "subnet:192.168.1.0/24".
function subnetOf(target: string): string {
  return target.startsWith('subnet:') ? target.slice(7) : ''
}
function port(target: string): string {
  const m = target.match(/:(\d+)\/?$/)
  return m ? m[1]! : ''
}

const fName = ref('')
const fClient = ref('')
const fTarget = ref('')
const fKind = ref<'single' | 'subnet'>('single')
const saving = ref(false)

async function create() {
  if (!fName.value.trim() || !fClient.value || !fTarget.value.trim()) return
  saving.value = true
  try {
    await api.createNetworkResource({
      name: fName.value.trim().toLowerCase(),
      client_id: fClient.value,
      ...(fKind.value === 'subnet' ? { subnet: fTarget.value.trim() } : { target: fTarget.value.trim() }),
    })
    fName.value = ''
    fTarget.value = ''
    await load()
    toast.success(t('network.created'))
  } catch (e: any) {
    toast.error(errMsg(e, t('network.actionFailed')))
  } finally {
    saving.value = false
  }
}

async function remove(r: Tunnel) {
  if (!confirm(t('network.deleteConfirm', { name: r.private_name || r.id }))) return
  try {
    await api.deleteTunnel(r.id)
    await load()
  } catch (e: any) {
    toast.error(errMsg(e, t('network.actionFailed')))
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

const connectCmd = 'ZORVEN_API_TOKEN=zrv_api_... zorven connect'

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-4xl space-y-6 px-4 py-6 sm:px-5">
    <div>
      <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
        <Icon name="lucide:network" class="size-5 text-accent" />
        {{ t('network.title') }}
      </h1>
      <p class="mt-1 text-sm text-fg-muted">{{ t('network.subtitle') }}</p>
    </div>

    <div v-if="loading" class="py-12 text-center text-fg-muted">
      <Icon name="lucide:loader-circle" class="mx-auto size-6 animate-spin" />
    </div>

    <div v-else-if="unavailable" class="rounded-xl border border-line bg-surface p-8 text-center">
      <Icon name="lucide:lock" class="mx-auto size-8 text-fg-muted" />
      <p class="mt-3 text-sm text-fg">{{ unavailable }}</p>
    </div>

    <template v-else>
      <!-- Nasıl bağlanılır -->
      <section class="rounded-xl border border-line bg-surface p-5">
        <h2 class="text-sm font-semibold text-fg">{{ t('network.howTitle') }}</h2>
        <ol class="mt-3 list-decimal space-y-2 pl-5 text-xs leading-relaxed text-fg-muted">
          <li>{{ t('network.step1') }}</li>
          <li>
            {{ t('network.step2') }}
            <div class="mt-1.5 flex items-center gap-2">
              <code class="flex-1 rounded bg-bg px-2 py-1.5 font-mono text-[11px] text-fg">{{ connectCmd }}</code>
              <button class="rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:text-fg" @click="copy(connectCmd)">
                {{ copied === connectCmd ? t('network.copied') : t('network.copy') }}
              </button>
            </div>
          </li>
          <li>{{ t('network.step3') }}</li>
        </ol>
        <p class="mt-3 flex items-start gap-1.5 text-[11px] leading-relaxed text-fg-subtle">
          <Icon name="lucide:info" class="mt-px size-3.5 shrink-0" />
          <span>{{ t('network.limits') }}</span>
        </p>
      </section>

      <!-- Kaynaklar -->
      <section class="rounded-xl border border-line bg-surface p-5">
        <h2 class="text-sm font-semibold text-fg">{{ t('network.resources') }}</h2>

        <div v-if="!resources.length" class="mt-4 rounded-lg border border-dashed border-line p-6 text-center text-xs text-fg-muted">
          {{ t('network.empty') }}
        </div>
        <div v-else class="mt-4 space-y-1.5">
          <div
            v-for="r in resources"
            :key="r.id"
            class="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2 text-sm"
          >
            <div class="min-w-0">
              <div v-if="subnetOf(r.target)" class="font-mono text-fg">
                {{ r.private_name }}
                <span class="ml-1 rounded bg-accent/15 px-1.5 py-0.5 font-sans text-[10px] font-medium text-accent">{{ t('network.subnetBadge') }}</span>
              </div>
              <div v-else class="font-mono text-fg">{{ r.private_name }}<span class="text-fg-subtle">:{{ port(r.target) }}</span></div>
              <div class="mt-0.5 flex flex-wrap items-center gap-1.5 text-[11px] text-fg-subtle">
                <span class="inline-flex items-center gap-1">
                  <span class="size-1.5 rounded-full" :class="clientOnline(r.client_id) ? 'bg-success' : 'bg-fg-subtle'" />
                  {{ clientName(r.client_id) }}
                </span>
                <span>→</span>
                <span class="font-mono">{{ subnetOf(r.target) || r.target }}</span>
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <button
                v-if="!subnetOf(r.target)"
                class="rounded-md border border-line px-2 py-1 text-[11px] text-fg-muted hover:text-fg"
                @click="copy(`${r.private_name}:${port(r.target)}`)"
              >
                {{ copied === `${r.private_name}:${port(r.target)}` ? t('network.copied') : t('network.copyAddr') }}
              </button>
              <button v-if="canManage" class="rounded-md px-2 py-1 text-fg-muted hover:text-danger" :title="t('network.delete')" @click="remove(r)">
                <Icon name="lucide:trash-2" class="size-3.5" />
              </button>
            </div>
          </div>
        </div>

        <!-- Ekleme (owner/admin; sunucu da zorlar) -->
        <form v-if="canManage" class="mt-5 space-y-2 border-t border-line pt-4" @submit.prevent="create">
          <div class="flex items-center justify-between gap-2">
            <div class="text-xs font-medium text-fg-muted">{{ t('network.add') }}</div>
            <div class="flex gap-1 text-[11px]">
              <button type="button" class="rounded-md border px-2 py-1" :class="fKind === 'single' ? 'border-accent text-fg' : 'border-line text-fg-muted'" @click="fKind = 'single'">{{ t('network.kindSingle') }}</button>
              <button type="button" class="rounded-md border px-2 py-1" :class="fKind === 'subnet' ? 'border-accent text-fg' : 'border-line text-fg-muted'" @click="fKind = 'subnet'">{{ t('network.kindSubnet') }}</button>
            </div>
          </div>
          <div class="grid gap-2 sm:grid-cols-3">
            <input v-model="fName" type="text" :placeholder="t('network.namePh')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
            <select v-model="fClient" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
              <option value="" disabled>{{ t('network.devicePh') }}</option>
              <option v-for="c in clients" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
            <input v-model="fTarget" type="text" :placeholder="fKind === 'subnet' ? '192.168.1.0/24' : t('network.targetPh')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
          </div>
          <p class="text-[11px] leading-relaxed text-fg-subtle">{{ fKind === 'subnet' ? t('network.subnetHint') : t('network.addHint') }}</p>
          <button type="submit" class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50" :disabled="saving || !fName.trim() || !fClient || !fTarget.trim()">
            <Icon v-if="saving" name="lucide:loader-circle" class="size-4 animate-spin" />
            <Icon v-else name="lucide:plus" class="size-4" />
            {{ t('network.add') }}
          </button>
        </form>
      </section>
    </template>
  </div>
</template>
