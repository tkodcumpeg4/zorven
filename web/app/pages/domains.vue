<script setup lang="ts">
import type { Hostname, Tunnel, VerificationInstructions, PathRoute } from '~/types/api'

const api = useApi()
const { t } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const { platformAdmin, tenantSlug, platformDomain } = useAuth()

const hostnames = ref<Hostname[]>([])
const tunnels = ref<Tunnel[]>([])
const pending = ref(true)
const saving = ref(false)
const formError = ref('')

// Sekme seçimi: Platform subdomain veya Özel harici domain
const activeTab = ref<'platform' | 'custom'>('platform')

// Form durumu
const form = reactive({
  name: '',          // Platform subdomain etiketi (ör: 'api')
  kind: 'scoped' as 'scoped' | 'short', // scoped: ad--kiracı.platform | short: ad.platform
  custom_domain: '', // Tam FQDN (ör: 'api.alanadiniz.com')
})

// Doğrulama ve talimat modalı durumu
const verifyingId = ref<string | null>(null)
const actionMessage = ref<{ text: string; isError: boolean } | null>(null)
const selectedInstructionsDomain = ref<Hostname | null>(null)
// Doğrulanmış (sahipliği kanıtlanmış) ama DNS'i henüz platforma yönlenmemiş ad için canlı kontrol.
const dnsChecking = ref(false)
const dnsResult = ref<{ id: string, pointed: boolean } | null>(null)
async function checkDNS(h: Hostname) {
  dnsChecking.value = true
  try {
    const r = await api.dnsCheckHostname(h.id)
    dnsResult.value = { id: h.id, pointed: r.pointed }
  } catch (e: any) {
    toast.error(e?.data?.error?.message || t('domains.addFailed'))
  } finally {
    dnsChecking.value = false
  }
}
const copiedKey = ref<string | null>(null)

// FAZ 6.5 — yol tabanlı yönlendirme (hostname bazlı, açılır satır)
const pathOpenId = ref<string | null>(null)
const pathRoutes = ref<PathRoute[]>([])
const pathLoading = ref(false)
const pathSaving = ref(false)
const pathForm = reactive({ path_prefix: '', tunnel_id: '' })

async function togglePaths(h: Hostname) {
  if (pathOpenId.value === h.id) { pathOpenId.value = null; return }
  pathOpenId.value = h.id
  pathRoutes.value = []
  pathForm.path_prefix = ''
  pathForm.tunnel_id = ''
  pathLoading.value = true
  try {
    pathRoutes.value = await api.listPathRoutes(h.id)
  } catch { /* sessizce */ } finally {
    pathLoading.value = false
  }
}
async function addPath(h: Hostname) {
  if (!pathForm.path_prefix.trim() || !pathForm.tunnel_id) return
  pathSaving.value = true
  try {
    const pr = await api.addPathRoute(h.id, { path_prefix: pathForm.path_prefix.trim(), tunnel_id: pathForm.tunnel_id })
    pathRoutes.value.unshift(pr)
    pathForm.path_prefix = ''
    pathForm.tunnel_id = ''
    toast.success(t('domains.pathAdded'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('domains.pathAddFailed'))
  } finally {
    pathSaving.value = false
  }
}
async function removePath(h: Hostname, routeID: string) {
  try {
    await api.deletePathRoute(h.id, routeID)
    pathRoutes.value = pathRoutes.value.filter(p => p.id !== routeID)
    toast.info(t('domains.pathRemoved'))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('domains.pathRemoveFailed'))
  }
}

onMounted(async () => {
  try {
    ;[hostnames.value, tunnels.value] = await Promise.all([
      api.listHostnames(), api.listTunnels(),
    ])
  } catch (e) {
    // Başarısız yükleme sonsuz spinner bırakmasın.
    toast.error(hostnameError(e, t('domains.loadFailed')))
  } finally {
    pending.value = false
  }
})

const tunnelLabel = (id?: string | null) => {
  if (!id) return t('domains.idle')
  const tn = tunnels.value.find(x => x.id === id)
  return tn ? `${tn.target} (${tn.id})` : id
}

async function changeAttachment(h: Hostname, newTunnelID: string) {
  try {
    const updated = await api.updateHostname(h.id, newTunnelID || null)
    h.tunnel_id = updated.tunnel_id
    actionMessage.value = {
      text: newTunnelID
        ? t('domains.attachSuccess', { fqdn: h.fqdn })
        : t('domains.detachSuccess', { fqdn: h.fqdn }),
      isError: false,
    }
  } catch (e) {
    actionMessage.value = {
      text: hostnameError(e, t('domains.attachFailed')),
      isError: true,
    }
  }
}

const platformHost = computed(() => platformDomain.value || 'zorven.app')
const previewLabel = computed(() => form.name.trim().toLowerCase() || t('domains.previewPlaceholder'))
// Gerçekte oluşacak adres: kiracı slug'ı panelde auth/me'den gelir.
const previewFqdn = computed(() =>
  form.kind === 'short'
    ? `${previewLabel.value}.${platformHost.value}`
    : `${previewLabel.value}--${tenantSlug.value || t('domains.previewTenant')}.${platformHost.value}`,
)
function pickKind(k: 'scoped' | 'short') {
  form.kind = k
  formError.value = ''
}

const TYPE_META: Record<Hostname['type'], { label: string, title: string, cls: string }> = {
  scoped: {
    label: t('domains.typeScopedLabel'),
    title: t('domains.typeScopedTitle'),
    cls: 'border-accent/30 text-accent',
  },
  global: {
    label: t('domains.typeGlobalLabel'),
    title: t('domains.typeGlobalTitle'),
    cls: 'border-line text-fg-muted',
  },
  legacy: {
    label: t('domains.typeLegacyLabel'),
    title: t('domains.typeLegacyTitle'),
    cls: 'border-line text-fg-subtle',
  },
  custom: {
    label: t('domains.typeCustomLabel'),
    title: t('domains.typeCustomTitle'),
    cls: 'border-line bg-surface-2 text-fg font-medium',
  },
}

async function submit() {
  formError.value = ''
  actionMessage.value = null

  saving.value = true
  try {
    if (activeTab.value === 'platform') {
      if (!form.name.trim()) { formError.value = t('domains.errNameRequired'); saving.value = false; return }
      const newH = await api.createHostname(form.name.trim(), undefined, form.kind)
      hostnames.value.push(newH)
      form.name = ''
      toast.success(t('domains.added', { fqdn: newH.fqdn }))
    } else {
      if (!form.custom_domain.trim()) { formError.value = t('domains.errDomainRequired'); saving.value = false; return }
      const hst = await api.createCustomHostname(form.custom_domain.trim())
      hostnames.value.push(hst)
      form.custom_domain = ''
      if (hst.auto_verified && hst.dns_pointed) {
        // Üst zone doğrulanmış VE ad zaten platforma yönleniyor → hemen aktif.
        toast.success(t('domains.autoVerified', { fqdn: hst.fqdn, zone: hst.parent_zone || '' }))
      } else if (hst.auto_verified) {
        // Sahiplik doğrulandı ama trafik için alt alan adının CNAME kaydı gerekir.
        dnsResult.value = { id: hst.id, pointed: false }
        selectedInstructionsDomain.value = hst
        toast.success(t('domains.autoVerifiedNeedsDns', { fqdn: hst.fqdn }))
      } else {
        // Yeni eklenen özel domain için DNS talimat modalını doğrudan aç
        selectedInstructionsDomain.value = hst
        toast.success(t('domains.addedCustom', { fqdn: hst.fqdn }))
      }
    }
  } catch (e: any) {
    const msg = hostnameError(e, t('domains.addFailed'))
    formError.value = msg
    toast.error(formError.value)
  } finally {
    saving.value = false
  }
}

async function verify(h: Hostname) {
  verifyingId.value = h.id
  actionMessage.value = null
  try {
    const res = await api.verifyHostname(h.id)
    if (res.verified) {
      h.verified = true
      actionMessage.value = { text: t('domains.verifiedMsg', { fqdn: h.fqdn }), isError: false }
      toast.success(t('domains.verifiedToast', { fqdn: h.fqdn }))
    } else {
      actionMessage.value = { text: t('domains.verifyPending', { fqdn: h.fqdn }), isError: true }
      toast.warn(t('domains.verifyPendingToast'))
    }
  } catch (e) {
    actionMessage.value = { text: hostnameError(e, t('domains.verifyFailed')), isError: true }
    toast.error(actionMessage.value.text)
  } finally {
    verifyingId.value = null
  }
}

async function copyText(text: string, key: string) {
  await navigator.clipboard.writeText(text)
  copiedKey.value = key
  toast.success(t('domains.copied'))
  setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = null
  }, 2000)
}

async function remove(h: Hostname) {
  if (selectedInstructionsDomain.value?.id === h.id) {
    selectedInstructionsDomain.value = null
  }
  try {
    await api.deleteHostname(h.id)
    hostnames.value = hostnames.value.filter(x => x.id !== h.id)
    toast.info(t('domains.removed', { fqdn: h.fqdn }))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('domains.removeFailed'))
  }
}
</script>

<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight">{{ t('domains.title') }}</h1>
      <p class="mt-0.5 text-sm text-fg-muted">{{ t('domains.subtitle') }}</p>
    </header>

    <!-- Geri bildirim mesajı -->
    <div
      v-if="actionMessage"
      class="flex items-center justify-between rounded border px-4 py-2.5 text-sm"
      :class="actionMessage.isError ? 'border-danger/30 bg-danger/10 text-danger' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400'"
    >
      <span>{{ actionMessage.text }}</span>
      <button class="text-xs opacity-70 hover:opacity-100" @click="actionMessage = null">{{ t('common.close') }}</button>
    </div>

    <!-- Yeni Ad Ekleme Paneli -->
    <PanelFrame :label="t('domains.newDomain')">
      <div class="p-4">
        <!-- Sekmeler -->
        <div class="mb-4 flex gap-2 border-b border-line pb-3">
          <button
            type="button"
            class="rounded px-3 py-1 text-xs font-medium transition-colors"
            :class="activeTab === 'platform' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
            @click="activeTab = 'platform'; formError = ''"
          >
            {{ t('domains.tabPlatform') }}
          </button>
          <button
            type="button"
            class="rounded px-3 py-1 text-xs font-medium transition-colors"
            :class="activeTab === 'custom' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
            @click="activeTab = 'custom'; formError = ''"
          >
            {{ t('domains.tabCustom') }}
          </button>
        </div>

        <form class="grid gap-3 sm:grid-cols-[1fr_auto]" @submit.prevent="submit">
          <!-- Platform Subdomain Alanı -->
          <div v-if="activeTab === 'platform'">
            <div class="mb-3 grid gap-2 sm:grid-cols-2" role="radiogroup" :aria-label="t('domains.kindLabel')">
              <button
                type="button"
                role="radio"
                :aria-checked="form.kind === 'scoped'"
                class="rounded border px-3 py-2 text-left transition-colors"
                :class="form.kind === 'scoped' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'"
                @click="pickKind('scoped')"
              >
                <span class="flex items-center gap-1.5 text-xs font-medium text-fg">
                  <Icon :name="form.kind === 'scoped' ? 'lucide:circle-dot' : 'lucide:circle'" class="size-3.5" :class="form.kind === 'scoped' ? 'text-accent' : 'text-fg-subtle'" />
                  {{ t('domains.kindScoped') }}
                  <span class="rounded border border-accent/30 px-1 text-[10px] text-accent">{{ t('domains.recommended') }}</span>
                </span>
                <span class="mt-1 block font-mono text-[11px] text-fg-muted">{{ t('domains.kindScopedEx', { domain: platformHost }) }}</span>
              </button>
              <button
                type="button"
                role="radio"
                :aria-checked="form.kind === 'short'"
                class="rounded border px-3 py-2 text-left transition-colors"
:class="form.kind === 'short' ? 'border-accent bg-accent/5' : 'border-line hover:border-fg-subtle'"
                @click="pickKind('short')"
              >
                <span class="flex items-center gap-1.5 text-xs font-medium text-fg">
                  <Icon :name="form.kind === 'short' ? 'lucide:circle-dot' : 'lucide:circle'" class="size-3.5" :class="form.kind === 'short' ? 'text-accent' : 'text-fg-subtle'" />
                  {{ t('domains.kindShort') }}
                </span>
                <span class="mt-1 block font-mono text-[11px] text-fg-muted">{{ t('domains.kindShortEx', { domain: platformHost }) }}</span>
              </button>
            </div>
            <label for="name" class="label-sys mb-1 block">{{ t('domains.shortName') }}</label>
            <input
              id="name"
              v-model="form.name"
              placeholder="benim-projem"
              class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm placeholder:text-fg-subtle focus:border-accent"
            >
            <p class="mt-1 text-[11px] text-fg-subtle">
              {{ t('domains.shortNameHint') }} <span class="text-fg-muted">{{ t('domains.previewLabel') }}</span>
              <code class="font-mono text-fg">{{ previewFqdn }}</code>
            </p>
          </div>

          <!-- Özel Domain Alanı -->
          <div v-else>
            <label for="custom_domain" class="label-sys mb-1 block">{{ t('domains.customFqdn') }}</label>
            <input
              id="custom_domain"
              v-model="form.custom_domain"
              placeholder="api.alanadiniz.com"
              class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-sm placeholder:text-fg-subtle focus:border-accent"
            >
            <p class="mt-1 text-[11px] text-fg-subtle">{{ t('domains.customHint') }}</p>
          </div>

          <div class="flex items-start pt-[22px]">
            <button
              type="submit"
              :disabled="saving"
              class="w-full cursor-pointer rounded bg-accent px-4 py-1.5 text-sm font-medium text-on-accent transition-opacity duration-150 hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
            >
              {{ saving ? t('tunnels.adding') : (activeTab === 'custom' ? t('domains.addVerify') : t('common.add')) }}
            </button>
          </div>

          <p v-if="formError" class="text-sm text-danger sm:col-span-2" role="alert">{{ formError }}</p>
        </form>
      </div>
    </PanelFrame>

    <!-- DNS Doğrulama Talimatları Modalı / Paneli -->
    <div
      v-if="selectedInstructionsDomain"
      class="rounded-lg border border-line bg-surface p-5 shadow-lg"
    >
      <div class="flex items-start justify-between">
        <div>
          <div class="flex items-center gap-2">
            <Icon name="lucide:shield-check" class="size-5 text-accent" />
            <h2 class="text-base font-semibold text-fg">
              {{ t('domains.dnsInstructions') }} <span class="font-mono text-accent">{{ selectedInstructionsDomain.fqdn }}</span>
            </h2>
          </div>
          <p class="mt-1 text-xs text-fg-muted">{{ selectedInstructionsDomain.verified ? t('domains.dnsOnlyIntro') : t('domains.dnsIntro') }}</p>
        </div>
        <button
          class="rounded p-1 text-fg-muted hover:bg-surface-2 hover:text-fg"
          @click="selectedInstructionsDomain = null"
        >
          <Icon name="lucide:x" class="size-4" />
        </button>
      </div>

      <div class="mt-4 grid gap-4" :class="selectedInstructionsDomain.verified ? '' : 'md:grid-cols-2'">
        <!-- Yöntem 1: CNAME -->
        <div class="rounded border border-line bg-surface-1 p-3">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-accent">{{ t('domains.method1') }}</span>
            <span class="text-[10px] text-fg-subtle">{{ t('domains.method1Sub') }}</span>
          </div>
          <div class="mt-2.5 space-y-2 text-xs">
            <div>
              <span class="text-fg-subtle block">{{ t('domains.recordType') }}</span>
              <code class="font-mono font-bold text-fg">CNAME</code>
            </div>
            <div>
              <span class="text-fg-subtle block">{{ t('domains.hostName') }}</span>
              <div class="flex items-center justify-between rounded bg-bg px-2 py-1 font-mono text-[11px]">
                <span>{{ selectedInstructionsDomain.instructions?.cname_record || selectedInstructionsDomain.fqdn }}</span>
                <button
                  class="text-fg-muted hover:text-fg"
                  @click="copyText(selectedInstructionsDomain.instructions?.cname_record || selectedInstructionsDomain.fqdn, 'cname-host')"
                >
                  <Icon :name="copiedKey === 'cname-host' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
                </button>
              </div>
            </div>
            <div>
              <span class="text-fg-subtle block">{{ t('domains.targetValue') }}</span>
              <div class="flex items-center justify-between rounded bg-bg px-2 py-1 font-mono text-[11px]">
                <span>{{ selectedInstructionsDomain.instructions?.cname_target || 'cname.zorven.app' }}</span>
                <button
                  class="text-fg-muted hover:text-fg"
                  @click="copyText(selectedInstructionsDomain.instructions?.cname_target || 'cname.zorven.app', 'cname-target')"
                >
                  <Icon :name="copiedKey === 'cname-target' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Yöntem 2: TXT Challenge (yalnızca sahiplik doğrulanmamışken) -->
        <div v-if="!selectedInstructionsDomain.verified" class="rounded border border-line bg-surface-1 p-3">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-fg">{{ t('domains.method2') }}</span>
            <span class="text-[10px] text-fg-subtle">{{ t('domains.method2Sub') }}</span>
          </div>
          <div class="mt-2.5 space-y-2 text-xs">
            <div>
              <span class="text-fg-subtle block">{{ t('domains.recordType') }}</span>
              <code class="font-mono font-bold text-fg">TXT</code>
            </div>
            <div>
              <span class="text-fg-subtle block">{{ t('domains.hostName') }}</span>
              <div class="flex items-center justify-between rounded bg-bg px-2 py-1 font-mono text-[11px]">
                <span>{{ selectedInstructionsDomain.instructions?.txt_record || ('_rpsh-challenge.' + selectedInstructionsDomain.fqdn) }}</span>
                <button
                  class="text-fg-muted hover:text-fg"
                  @click="copyText(selectedInstructionsDomain.instructions?.txt_record || ('_rpsh-challenge.' + selectedInstructionsDomain.fqdn), 'txt-host')"
                >
                  <Icon :name="copiedKey === 'txt-host' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
                </button>
              </div>
            </div>
            <div>
              <span class="text-fg-subtle block">{{ t('domains.value') }}</span>
              <div class="flex items-center justify-between rounded bg-bg px-2 py-1 font-mono text-[11px]">
                <span class="truncate max-w-[180px]">{{ selectedInstructionsDomain.instructions?.txt_value || selectedInstructionsDomain.verify_token || t('domains.tokenWaiting') }}</span>
                <button
                  v-if="selectedInstructionsDomain.instructions?.txt_value || selectedInstructionsDomain.verify_token"
                  class="text-fg-muted hover:text-fg"
                  @click="copyText(selectedInstructionsDomain.instructions?.txt_value || selectedInstructionsDomain.verify_token || '', 'txt-val')"
                >
                  <Icon :name="copiedKey === 'txt-val' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="mt-4 flex items-center justify-between border-t border-line/50 pt-3">
        <div class="text-xs text-fg-muted">
          {{ t('domains.statusLabel') }}
          <span v-if="selectedInstructionsDomain.verified" class="font-medium text-emerald-600 dark:text-emerald-400">{{ t('domains.verified') }}</span>
          <span v-else class="font-medium text-amber-700 dark:text-amber-400">{{ t('domains.pendingVerify') }}</span>
          <template v-if="dnsResult && dnsResult.id === selectedInstructionsDomain.id">
            ·
            <span v-if="dnsResult.pointed" class="font-medium text-emerald-600 dark:text-emerald-400">{{ t('domains.dnsPointed') }}</span>
            <span v-else class="font-medium text-amber-700 dark:text-amber-400">{{ t('domains.dnsNotPointed') }}</span>
          </template>
        </div>
        <div class="flex items-center gap-2">
          <button
            v-if="selectedInstructionsDomain.verified"
            :disabled="dnsChecking"
            class="flex items-center gap-1.5 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50"
            @click="checkDNS(selectedInstructionsDomain)"
          >
            <Icon name="lucide:refresh-cw" class="size-3.5" :class="{ 'animate-spin': dnsChecking }" />
            {{ dnsChecking ? t('domains.checking') : t('domains.dnsCheck') }}
          </button>
          <button
            v-if="!selectedInstructionsDomain.verified"
            :disabled="verifyingId === selectedInstructionsDomain.id"
            class="flex items-center gap-1.5 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50"
            @click="verify(selectedInstructionsDomain)"
          >
            <Icon
              name="lucide:refresh-cw"
              class="size-3.5"
              :class="{ 'animate-spin': verifyingId === selectedInstructionsDomain.id }"
            />
            {{ verifyingId === selectedInstructionsDomain.id ? t('domains.checking') : t('domains.verifyNow') }}
          </button>
          <button
            class="rounded border border-line px-3 py-1.5 text-xs text-fg-muted hover:text-fg"
            @click="selectedInstructionsDomain = null"
          >
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Domainler Tablosu -->
    <PanelFrame :label="t('domains.allDomains')" :meta="`${hostnames.length}`">
      <p v-if="pending" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
      <p v-else-if="!hostnames.length" class="px-4 py-8 text-center text-sm text-fg-muted">{{ t('domains.noDomains') }}</p>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-line text-left">
              <th class="label-sys px-4 py-2 font-normal">{{ t('domains.colFqdn') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('domains.colType') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('domains.colStatus') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('domains.colTunnel') }}</th>
              <th class="label-sys px-4 py-2 font-normal">{{ t('domains.colCreated') }}</th>
              <th class="px-4 py-2 text-right"><span class="sr-only">{{ t('domains.colActions') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <template v-for="h in hostnames" :key="h.id">
            <tr class="hover:bg-surface-2/40">
              <td class="px-4 py-2.5 font-mono text-xs">
                <span class="font-medium text-fg">{{ h.fqdn }}</span>
              </td>
              <td class="px-4 py-2.5">
                <span
                  class="inline-block whitespace-nowrap rounded border px-1.5 py-0.5 font-mono text-[10px]"
                  :class="TYPE_META[h.type]?.cls || 'border-line text-fg-muted'"
                  :title="TYPE_META[h.type]?.title"
                >
                  {{ TYPE_META[h.type]?.label || h.type }}
                </span>
              </td>
              <td class="px-4 py-2.5">
                <template v-if="h.type === 'custom'">
                  <span
                    v-if="h.verified"
                    class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-[10px] font-semibold
                           border-emerald-300 bg-emerald-50 text-emerald-800
                           dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-400"
                  >
                    <Icon name="lucide:check-circle" class="size-3 text-emerald-600 dark:text-emerald-400" />
                    {{ t('domains.verified') }}
                  </span>
                  <span
                    v-else
                    class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-[10px] font-semibold
                           border-amber-300 bg-amber-50 text-amber-800
                           dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-400"
                  >
                    <Icon name="lucide:clock" class="size-3 text-amber-600 dark:text-amber-400" />
                    {{ t('domains.pendingVerify') }}
                  </span>
                </template>
                <template v-else>
                  <span class="inline-flex items-center gap-1 text-[11px] font-semibold text-emerald-700 dark:text-emerald-400">
                    <Icon name="lucide:check" class="size-3" /> {{ t('domains.active') }}
                  </span>
                </template>
              </td>
              <td class="px-4 py-2.5 font-mono text-xs">
                <select
                  class="cursor-pointer rounded border border-line bg-surface px-2 py-1 text-xs font-mono text-fg transition-colors hover:border-accent focus:border-accent"
                  :value="h.tunnel_id || ''"
                  @change="changeAttachment(h, ($event.target as HTMLSelectElement).value)"
                >
                  <option value="">{{ t('domains.idle') }}</option>
                  <option v-for="t in tunnels" :key="t.id" :value="t.id">{{ t.target }} ({{ t.id }})</option>
                </select>
              </td>
              <td class="px-4 py-2.5 text-xs text-fg-muted font-medium">{{ relativeTime(h.created_at) }}</td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex items-center justify-end gap-1">
                  <!-- Custom domain işlemleri -->
                  <template v-if="h.type === 'custom'">
                    <button
                      class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors hover:bg-surface-2 hover:text-accent"
                      :title="t('domains.viewDns')"
                      @click="selectedInstructionsDomain = h"
                    >
                      <Icon name="lucide:file-text" class="size-4" />
                    </button>
                    <button
                      v-if="!h.verified"
                      :disabled="verifyingId === h.id"
                      class="cursor-pointer rounded p-1.5 text-amber-700 dark:text-amber-400 transition-colors hover:bg-amber-500/10 hover:text-amber-800 dark:hover:text-amber-300 disabled:opacity-50"
                      :title="t('domains.verifyDns')"
                      @click="verify(h)"
                    >
                      <Icon
                        name="lucide:refresh-cw"
                        class="size-4"
                        :class="{ 'animate-spin': verifyingId === h.id }"
                      />
                    </button>
                  </template>

                  <!-- Yol kuralları (path routing) -->
                  <button
                    class="cursor-pointer rounded p-1.5 transition-colors hover:bg-surface-2"
                    :class="pathOpenId === h.id ? 'text-accent' : 'text-fg-muted hover:text-accent'"
                    :title="t('domains.pathRoutes')"
                    @click="togglePaths(h)"
                  >
                    <Icon name="lucide:route" class="size-4" />
                  </button>

                  <!-- Silme butonu -->
                  <button
                    class="cursor-pointer rounded p-1.5 text-fg-muted transition-colors duration-150 hover:bg-danger/10 hover:text-danger"
                    :aria-label="t('domains.deleteDomain', { fqdn: h.fqdn })"
                    @click="remove(h)"
                  >
                    <Icon name="lucide:trash-2" class="size-4" />
                  </button>
                </div>
              </td>
            </tr>

            <!-- Yol kuralları açılır satırı (FAZ 6.5) -->
            <tr v-if="pathOpenId === h.id" class="bg-surface-2/30">
              <td colspan="6" class="px-4 py-3">
                <div class="space-y-2.5">
                  <div class="flex items-center gap-1.5 text-xs font-medium text-fg">
                    <Icon name="lucide:route" class="size-3.5 text-accent" />
                    <span>{{ t('domains.pathRoutesFor', { fqdn: h.fqdn }) }}</span>
                  </div>
                  <p class="text-[11px] text-fg-subtle">{{ t('domains.pathIntro') }}</p>

                  <div class="flex flex-wrap items-center gap-2">
                    <input v-model="pathForm.path_prefix" placeholder="/api" class="w-32 rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs focus:border-accent">
                    <span class="text-fg-subtle">→</span>
                    <select v-model="pathForm.tunnel_id" class="flex-1 min-w-[180px] cursor-pointer rounded border border-line bg-bg px-2 py-1.5 text-xs focus:border-accent">
                      <option value="">{{ t('domains.pathSelectTunnel') }}</option>
                      <option v-for="tn in tunnels" :key="tn.id" :value="tn.id">{{ tn.target }} ({{ tn.id }})</option>
                    </select>
                    <button :disabled="pathSaving || !pathForm.path_prefix.trim() || !pathForm.tunnel_id" class="shrink-0 rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent transition-opacity hover:opacity-90 disabled:opacity-50" @click="addPath(h)">
                      {{ t('common.add') }}
                    </button>
                  </div>

                  <p v-if="pathLoading" class="py-2 text-center text-xs text-fg-muted">{{ t('common.loading') }}</p>
                  <p v-else-if="!pathRoutes.length" class="rounded border border-dashed border-line py-3 text-center text-[11px] text-fg-muted">{{ t('domains.pathEmpty') }}</p>
                  <ul v-else class="divide-y divide-line rounded border border-line">
                    <li v-for="pr in pathRoutes" :key="pr.id" class="flex items-center justify-between gap-2 px-3 py-1.5">
                      <div class="flex items-center gap-2 font-mono text-xs">
                        <span class="rounded bg-surface px-1.5 py-0.5 text-fg">{{ pr.path_prefix }}</span>
                        <span class="text-fg-subtle">→</span>
                        <span class="text-fg-muted">{{ tunnelLabel(pr.tunnel_id) }}</span>
                      </div>
                      <button class="shrink-0 rounded p-1 text-fg-muted hover:bg-danger/10 hover:text-danger" :aria-label="t('common.delete')" @click="removePath(h, pr.id)">
                        <Icon name="lucide:trash-2" class="size-3.5" />
                      </button>
                    </li>
                  </ul>
                </div>
              </td>
            </tr>
            </template>
          </tbody>
        </table>
      </div>
    </PanelFrame>
  </div>
</template>
