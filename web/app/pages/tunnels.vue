<script setup lang="ts">
import type { Tunnel, Client, Hostname } from '~/types/api'

const api = useApi()
const { t } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const { openUpgrade, loadBillingData } = useBilling()

const tunnels = ref<Tunnel[]>([])
const clients = ref<Client[]>([])
const hostnames = ref<Hostname[]>([])
const pending = ref(true)
const saving = ref(false)
const formError = ref('')

// name TAM hostname değil, tek bir DNS etiketi: sunucu bunu platform
// domainiyle birleştirip kiracı kapsamlı adı otomatik verir.
const form = reactive({
  name: '',
  client_id: '',
  target: 'http://localhost:8000',
  hostname_id: '',
})

/** Tünelin gösterilecek adları. */
const namesOf = (tn: Tunnel) => (tn.hostnames ?? []).map(h => h.fqdn)

function hostnameError(err: any, fallback: string): string {
  const code = err?.data?.error?.code || err?.message || ''
  if (code === 'hostname_taken') return t('tunnels.hostnameTaken')
  if (code === 'hostname_reserved') return t('tunnels.hostnameReserved')
  if (code === 'invalid_hostname') return t('tunnels.invalidHostname')
  return err?.data?.error?.message || fallback
}

/** Henüz hiçbir tünele bağlanmamış boşta duran domainler */
const unattachedHostnames = computed(() => hostnames.value.filter(h => !h.tunnel_id))

onMounted(async () => {
  const [tns, c, h] = await Promise.all([
    api.listTunnels(),
    api.listClients(),
    api.listHostnames(),
  ])
  tunnels.value = tns
  clients.value = c
  hostnames.value = h
  if (clients.value[0]) form.client_id = clients.value[0].id
  pending.value = false
})

const clientById = (id: string) => clients.value.find(c => c.id === id)
const clientName = (id: string) => clientById(id)?.name ?? id
const clientStatusOf = (id: string) => clientById(id)?.status
/** Seçicide/dropdownda istemciyi durumuyla birlikte etiketle. */
const clientLabel = (c: Client) =>
  `${c.name} — ${c.status === 'online' ? t('tunnels.online') : t('tunnels.offline')}`

async function submit() {
  if (!form.name.trim() || !form.client_id || !form.target.trim()) return
  formError.value = ''
  saving.value = true
  try {
    const created = await api.createTunnel({
      name: form.name.trim(),
      client_id: form.client_id,
      target: form.target.trim(),
      hostname_id: form.hostname_id || undefined,
    })
    tunnels.value.push(created)
    form.name = ''
    form.hostname_id = ''
    hostnames.value = await api.listHostnames()
    toast.success(t('tunnels.created'))
    loadBillingData()
  } catch (e: any) {
    const code = e?.data?.error?.code
    const msg = hostnameError(e, t('tunnels.createFailed'))
    formError.value = msg
    if (code === 'plan_limit_reached') {
      openUpgrade(msg, 'tunnels')
    } else {
      toast.error(msg)
    }
  } finally {
    saving.value = false
  }
}

async function toggle(tn: Tunnel) {
  try {
    const updated = await api.updateTunnel(tn.id, { enabled: !tn.enabled })
    Object.assign(tn, updated)
    toast.info(tn.enabled ? t('tunnels.activated') : t('tunnels.stopped'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.updateFailed'))
  }
}

async function remove(tn: Tunnel) {
  try {
    await api.deleteTunnel(tn.id)
    tunnels.value = tunnels.value.filter(x => x.id !== tn.id)
    hostnames.value = await api.listHostnames()
    toast.info(t('tunnels.deleted'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('tunnels.deleteFailed'))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('tunnels.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('tunnels.subtitle') }}</p>
    </header>

    <PanelFrame :label="t('tunnels.newTunnel')">
      <form class="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-[1fr_160px_1fr_180px_auto]" @submit.prevent="submit">
        <div>
          <label for="name" class="label-sys mb-1 block">{{ t('tunnels.name') }}</label>
          <input id="name" v-model="form.name" placeholder="api"
                 class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm
                        placeholder:text-fg-subtle focus:border-accent">
        </div>

        <div>
          <label for="client" class="label-sys mb-1 block">{{ t('tunnels.client') }}</label>
          <select id="client" v-model="form.client_id" :disabled="!clients.length"
                  class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5
                         font-mono text-sm focus:border-accent disabled:cursor-not-allowed disabled:opacity-50">
            <option v-if="!clients.length" value="">—</option>
            <option v-for="c in clients" :key="c.id" :value="c.id">{{ clientLabel(c) }}</option>
          </select>
        </div>

        <div>
          <label for="target" class="label-sys mb-1 block">{{ t('tunnels.target') }}</label>
          <input id="target" v-model="form.target"
                 class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm
                        focus:border-accent">
        </div>

        <div>
          <label for="domainSelect" class="label-sys mb-1 block">{{ t('tunnels.domainOptional') }}</label>
          <select id="domainSelect" v-model="form.hostname_id"
                  class="w-full cursor-pointer rounded border border-line bg-bg px-2.5 py-1.5
                         font-mono text-sm focus:border-accent">
            <option value="">{{ t('tunnels.autoDomain') }}</option>
            <option v-for="h in unattachedHostnames" :key="h.id" :value="h.id">
              {{ h.fqdn }}
            </option>
          </select>
        </div>

        <div class="flex items-end sm:col-span-2 lg:col-span-1">
          <button type="submit" :disabled="saving"
                  class="w-full cursor-pointer rounded bg-accent px-4 py-1.5 text-sm font-medium
                         text-on-accent transition-opacity duration-150 hover:opacity-90
                         disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto">
            {{ saving ? t('tunnels.adding') : t('common.add') }}
          </button>
        </div>

        <p v-if="!pending && !clients.length" class="text-sm text-fg-muted sm:col-span-2 lg:col-span-5">{{ t('tunnels.noClients') }}</p>
        <p v-if="formError" class="text-sm text-danger sm:col-span-2 lg:col-span-5" role="alert">{{ formError }}</p>
      </form>
    </PanelFrame>

    <PanelFrame :label="t('tunnels.definedTunnels')" :meta="`${tunnels.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <p v-else-if="!tunnels.length" class="px-4 py-8 text-center text-sm text-fg-muted">{{ t('tunnels.noTunnels') }}</p>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colNames') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colClient') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colTarget') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('tunnels.colCreated') }}</th>
              <th class="px-4 py-2"><span class="sr-only">{{ t('tunnels.colActions') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="tn in tunnels" :key="tn.id" class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5 font-mono text-xs">
                <p v-for="fqdn in namesOf(tn)" :key="fqdn" class="truncate">{{ fqdn }}</p>
                <span v-if="!namesOf(tn).length" class="text-fg-subtle">{{ t('tunnels.noName') }}</span>
              </td>
              <td class="px-4 py-2.5">
                <span class="inline-flex items-center gap-1.5">
                  <span
                    class="size-1.5 rounded-full"
                    :class="clientStatusOf(tn.client_id) === 'online' ? 'bg-accent' : clientStatusOf(tn.client_id) === 'offline' ? 'bg-fg-subtle' : 'bg-danger'"
                    :title="clientStatusOf(tn.client_id) === 'online' ? t('tunnels.online') : clientStatusOf(tn.client_id) === 'offline' ? t('tunnels.offline') : t('tunnels.unknownClient')"
                  />
                  <span v-if="clientById(tn.client_id)">{{ clientName(tn.client_id) }}</span>
                  <span v-else class="font-mono text-xs text-fg-subtle">{{ tn.client_id }} <span class="text-danger">({{ t('tunnels.unknownClient') }})</span></span>
                </span>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs text-fg-muted">{{ tn.target }}</td>
              <td class="px-4 py-2.5"><StatusPill :status="tn.enabled ? 'enabled' : 'disabled'" /></td>
              <td class="px-4 py-2.5 text-xs text-fg-muted">{{ relativeTime(tn.created_at) }}</td>
              <td class="px-4 py-2.5">
                <div class="flex justify-end gap-1">
                  <NuxtLink
                    :to="`/tokens?tab=ip&tunnel=${tn.id}`"
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-surface-2 hover:text-accent"
                    :title="t('tunnels.configIp', { name: namesOf(tn)[0] ?? tn.id })"
                  >
                    <Icon name="lucide:shield" class="size-4" />
                  </NuxtLink>
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors
                           duration-150 hover:bg-surface-2 hover:text-fg"
                    :aria-label="tn.enabled ? t('tunnels.stopTunnel', { name: namesOf(tn)[0] ?? tn.id }) : t('tunnels.startTunnel', { name: namesOf(tn)[0] ?? tn.id })"
                    @click="toggle(tn)"
                  >
                    <Icon :name="tn.enabled ? 'lucide:pause' : 'lucide:play'" class="size-4" />
                  </button>
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors
                           duration-150 hover:bg-danger/10 hover:text-danger"
                    :aria-label="t('tunnels.deleteTunnel', { name: namesOf(tn)[0] ?? tn.id })"
                    @click="remove(tn)"
                  >
                    <Icon name="lucide:trash-2" class="size-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PanelFrame>
  </div>
</template>
