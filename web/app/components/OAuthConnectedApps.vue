<script setup lang="ts">
// "Bagli uygulamalar": OAuth ile yetki verilen MCP istemcileri (Claude ozel
// baglayici vb.). Uyeler kendi baglantilarini, owner/admin kiracidakilerin
// tumunu gorur ve iptal eder. Uclar panel oturumu ister (/oauth/grants).
interface Grant {
  id: string
  client_name: string
  tenant_id: string
  tenant_name: string
  own: boolean
  scopes: string[]
  created_at: string
  last_used_at?: string
  // Tam erisim (full) grant'i ve onayin dayandigi taze giris zamani.
  full?: boolean
  step_up_at?: string
}

const { t } = useI18n()
const toast = useToast()
const { relativeTime } = useFormat()
const { method, tenant, platformAdmin, user } = useAuth()

const grants = ref<Grant[]>([])
const loading = ref(true)
const failed = ref(false)
const showAll = ref(false)
const revoking = ref('')

const needAccount = computed(() => method.value !== 'better-auth')
const privileged = computed(() => {
  if (platformAdmin.value) return true
  const r = (user.value?.role || '').toLowerCase()
  return r === 'owner' || r === 'admin'
})

async function load() {
  if (needAccount.value) {
    loading.value = false
    return
  }
  loading.value = true
  failed.value = false
  try {
    const query = showAll.value && tenant.value?.id ? { tenant: tenant.value.id } : undefined
    const res = await $fetch<{ grants: Grant[] }>('/oauth/grants', { query })
    grants.value = res.grants ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}

async function revoke(g: Grant) {
  if (!confirm(t('oauth.apps.revokeConfirm', { name: g.client_name }))) return
  revoking.value = g.id
  try {
    await $fetch(`/oauth/grants/${encodeURIComponent(g.id)}`, { method: 'DELETE' })
    grants.value = grants.value.filter(x => x.id !== g.id)
    toast.success(t('oauth.apps.revoked'))
  } catch {
    toast.error(t('oauth.apps.revokeFailed'))
  } finally {
    revoking.value = ''
  }
}

function scopeLabel(id: string): string {
  return t(`oauth.scope.${id.replace(':', '_')}`)
}

watch(showAll, load)
onMounted(load)
</script>

<template>
  <section class="space-y-4">
    <div class="rounded-xl border border-line bg-surface p-5">
      <div class="mb-2 flex items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <Icon name="lucide:plug-zap" class="size-4 text-accent" />
          <h2 class="text-sm font-semibold text-fg">{{ t('oauth.apps.title') }}</h2>
        </div>
        <label v-if="privileged && !needAccount" class="flex cursor-pointer items-center gap-2 text-xs text-fg-muted">
          <input v-model="showAll" type="checkbox" class="size-3.5 accent-[var(--color-accent)]" />
          {{ t('oauth.apps.showAll') }}
        </label>
      </div>
      <p class="text-xs leading-relaxed text-fg-muted">{{ t('oauth.apps.desc') }}</p>
    </div>

    <div v-if="needAccount" class="rounded-xl border border-line bg-surface p-5 text-sm text-fg-muted">
      {{ t('oauth.apps.needAccount') }}
    </div>

    <div v-else-if="loading" class="py-10 text-center">
      <Icon name="lucide:loader-2" class="mx-auto size-6 animate-spin text-accent" />
    </div>

    <div v-else-if="failed" class="rounded-xl border border-warn/30 bg-warn/10 p-4 text-sm text-fg">
      {{ t('oauth.apps.loadFailed') }}
    </div>

    <div v-else-if="grants.length === 0" class="rounded-xl border border-line bg-surface p-8 text-center">
      <Icon name="lucide:plug" class="mx-auto mb-2 size-6 text-fg-subtle" />
      <p class="text-sm font-medium text-fg">{{ t('oauth.apps.empty') }}</p>
      <p class="mt-1 text-xs text-fg-muted">{{ t('oauth.apps.emptyHint') }}</p>
    </div>

    <div v-else class="overflow-hidden rounded-xl border border-line bg-surface">
      <ul class="divide-y divide-line">
        <li v-for="g in grants" :key="g.id" class="flex flex-wrap items-start justify-between gap-3 p-4">
          <div class="min-w-0 space-y-1.5">
            <div class="flex items-center gap-2">
              <Icon name="lucide:bot" class="size-4 shrink-0 text-accent" />
              <span class="truncate text-sm font-semibold text-fg">{{ g.client_name }}</span>
              <span class="rounded-full border border-line px-2 py-0.5 text-[10px] text-fg-muted">
                {{ g.own ? t('oauth.apps.you') : t('oauth.apps.member') }}
              </span>
              <span
                v-if="g.full"
                class="inline-flex items-center gap-1 rounded-full border border-danger/40 bg-danger/10 px-2 py-0.5 text-[10px] font-medium text-danger"
              >
                <Icon name="lucide:shield-alert" class="size-3" />{{ t('oauth.apps.fullBadge') }}
              </span>
            </div>
            <div class="text-xs text-fg-muted">
              {{ t('oauth.apps.organization') }}: <span class="text-fg">{{ g.tenant_name || g.tenant_id }}</span>
            </div>
            <ul class="space-y-0.5 text-xs text-fg-muted">
              <li v-for="s in g.scopes" :key="s" class="flex items-center gap-1.5">
                <Icon name="lucide:check" class="size-3 text-accent" />{{ scopeLabel(s) }}
              </li>
            </ul>
            <div v-if="g.full && g.step_up_at" class="text-[11px] text-fg-subtle">
              {{ t('oauth.apps.fullSince', { when: relativeTime(g.step_up_at) }) }}
            </div>
            <div class="text-[11px] text-fg-subtle">
              {{ t('oauth.apps.connected') }}: {{ relativeTime(g.created_at) }}
              <span class="mx-1">·</span>
              {{ t('oauth.apps.lastUsed') }}: {{ g.last_used_at ? relativeTime(g.last_used_at) : t('oauth.apps.never') }}
            </div>
          </div>
          <button
            type="button"
            class="inline-flex cursor-pointer items-center gap-1.5 rounded-lg border border-line px-3 py-1.5 text-xs text-fg-muted hover:border-danger hover:text-danger disabled:opacity-50"
            :disabled="revoking === g.id"
            @click="revoke(g)"
          >
            <Icon v-if="revoking === g.id" name="lucide:loader-2" class="size-3.5 animate-spin" />
            <Icon v-else name="lucide:unplug" class="size-3.5" />
            {{ t('oauth.apps.revoke') }}
          </button>
        </li>
      </ul>
    </div>
  </section>
</template>
