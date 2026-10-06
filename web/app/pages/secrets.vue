<script setup lang="ts">
import type { Secret } from '~/types/api'

const api = useApi()
const toast = useToast()
const { relativeTime } = useFormat()
const { platformAdmin, user: currentUser } = useAuth()
const { t } = useI18n()

// Secret yazma (olustur/sil) yalnizca owner/admin; okuma member'a acik.
const canManage = computed(() => {
  if (platformAdmin.value) return true
  const r = (currentUser.value?.role || '').toLowerCase()
  return r === 'owner' || r === 'admin'
})

// Hata govdesi {error:{code,message}} ya da duz dizge olabilir; toast'a nesne gitmesin.
function errMsg(e: any, fallback: string): string {
  const err = e?.data?.error
  return (typeof err === 'string' ? err : err?.message) || e?.message || fallback
}

const loading = ref(true)
const secrets = ref<Secret[]>([])

const showCreate = ref(false)
const newName = ref('')
const newValue = ref('')
const creating = ref(false)
const deletingId = ref<string | null>(null)

// Olusturma sonrasi deger BIR KEZ gosterilir.
const revealed = ref<{ name: string, value: string } | null>(null)

// {{secret:ad}} literali; sablonda dogrudan yazilirsa mustache'i erken kapatir.
const secretSyntax = '{{secret:ad}}'

async function load() {
  loading.value = true
  try {
    const res = await api.listSecrets()
    secrets.value = res.secrets
  } catch (e: any) {
    toast.error(errMsg(e, t('secrets.loadError')))
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!canManage.value) return
  if (!newName.value.trim() || !newValue.value) return
  creating.value = true
  try {
    const res = await api.createSecret(newName.value.trim(), newValue.value)
    revealed.value = { name: res.secret.name, value: res.value }
    showCreate.value = false
    newName.value = ''
    newValue.value = ''
    await load()
  } catch (e: any) {
    toast.error(errMsg(e, t('secrets.createError')))
  } finally {
    creating.value = false
  }
}

async function remove(s: Secret) {
  if (!confirm(t('secrets.deleteConfirm', { name: s.name }))) return
  deletingId.value = s.id
  try {
    await api.deleteSecret(s.id)
    await load()
    toast.success(t('secrets.deleted'))
  } catch (e: any) {
    toast.error(errMsg(e, t('secrets.deleteError')))
  } finally {
    deletingId.value = null
  }
}

function copyRevealed() {
  if (!revealed.value) return
  navigator.clipboard?.writeText(revealed.value.value)
  toast.success(t('secrets.copied'))
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-5xl px-4 py-6 sm:px-5">
    <div class="mb-6 flex items-start justify-between gap-4">
      <div>
        <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
          <Icon name="lucide:key-round" class="size-5 text-accent" />
          {{ t('secrets.title') }}
        </h1>
        <p class="mt-1 text-sm text-fg-muted">{{ t('secrets.subtitle') }}</p>
      </div>
      <button
        v-if="canManage"
        class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-accent/90 active:scale-95"
        @click="showCreate = true"
      >
        <Icon name="lucide:plus" class="size-4" />
        {{ t('secrets.create') }}
      </button>
    </div>

      <div v-if="loading" class="py-12 text-center text-fg-muted">
        <Icon name="lucide:loader-circle" class="mx-auto size-6 animate-spin" />
      </div>

      <div v-else-if="secrets.length === 0" class="rounded-xl border border-dashed border-line bg-surface p-10 text-center">
        <Icon name="lucide:key-round" class="mx-auto size-8 text-fg-muted" />
        <p class="mt-3 text-sm text-fg-muted">{{ t('secrets.empty') }}</p>
      </div>

      <div v-else class="overflow-hidden rounded-xl border border-line bg-surface">
        <table class="w-full text-sm">
          <thead class="border-b border-line bg-surface-2 text-left text-xs uppercase tracking-wide text-fg-muted">
            <tr>
              <th class="px-4 py-3 font-medium">{{ t('secrets.colName') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('secrets.colValue') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('secrets.colKeyVersion') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('secrets.colCreated') }}</th>
              <th class="px-4 py-3" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in secrets" :key="s.id" class="border-b border-line last:border-0">
              <td class="px-4 py-3 font-mono text-fg">{{ s.name }}</td>
              <td class="px-4 py-3 font-mono text-fg-muted">••••••••</td>
              <td class="px-4 py-3 text-fg-muted">v{{ s.key_version }}</td>
              <td class="px-4 py-3 text-fg-muted">{{ relativeTime(s.created_at) }}</td>
              <td class="px-4 py-3 text-right">
                <button
                  v-if="canManage"
                  class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-fg-muted transition-colors hover:text-danger disabled:opacity-50"
                  :disabled="deletingId === s.id"
                  @click="remove(s)"
                >
                  <Icon name="lucide:trash-2" class="size-3.5" />
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="mt-3 text-xs text-fg-muted">
        {{ t('secrets.usageHint') }} <code class="rounded bg-surface-2 px-1 py-0.5 font-mono">{{ secretSyntax }}</code>
      </p>

    <!-- Olusturma modali -->
    <div v-if="showCreate" class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4" @click.self="showCreate = false">
      <div class="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="text-lg font-semibold text-fg">{{ t('secrets.create') }}</h2>
        <div class="mt-4 space-y-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('secrets.colName') }}</label>
            <input v-model="newName" type="text" placeholder="github-prod" class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent">
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('secrets.colValue') }}</label>
            <textarea v-model="newValue" rows="3" class="w-full rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent" />
          </div>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <button class="rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg" @click="showCreate = false">{{ t('common.cancel') }}</button>
          <button
            class="inline-flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
            :disabled="creating || !newName.trim() || !newValue"
            @click="create"
          >
            <Icon v-if="creating" name="lucide:loader-circle" class="size-4 animate-spin" />
            {{ t('secrets.create') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Deger bir kez gosterilir -->
    <div v-if="revealed" class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4" @click.self="revealed = null">
      <div class="w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-lg">
        <h2 class="flex items-center gap-2 text-lg font-semibold text-fg">
          <Icon name="lucide:eye" class="size-5 text-accent" />
          {{ t('secrets.revealTitle') }}
        </h2>
        <p class="mt-1 text-sm text-fg-muted">{{ t('secrets.revealHint') }}</p>
        <div class="mt-4 rounded-lg border border-line bg-bg p-3">
          <p class="text-xs text-fg-muted">{{ revealed.name }}</p>
          <p class="mt-1 break-all font-mono text-sm text-fg">{{ revealed.value }}</p>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <button class="inline-flex items-center gap-1.5 rounded-lg border border-line px-3 py-2 text-sm text-fg-muted hover:text-fg" @click="copyRevealed">
            <Icon name="lucide:copy" class="size-4" />
            {{ t('common.copy') }}
          </button>
          <button class="rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white" @click="revealed = null">{{ t('common.done') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
