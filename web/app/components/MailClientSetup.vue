<script setup lang="ts">
import type { MailAppPassword, MailAppPasswordCreated, MailInfo } from '~/types/api'

// "Mail uygulamalarında kullan": IMAP/SMTP sunucu ayarları, uygulama parolası
// yönetimi ve Gmail / Outlook / Apple Mail / Thunderbird kurulum rehberleri.
const props = defineProps<{ info: MailInfo }>()

const api = useApi()
const toast = useToast()
const { t } = useI18n()
const { relativeTime } = useFormat()

const access = computed(() => props.info.client_access)

// Parola yönetilecek kutu: kendi adres + (owner/platform admin ise) sistem kutuları.
const boxes = computed<string[]>(() => {
  const own = props.info.address ? [props.info.address] : []
  return [...own, ...(props.info.system_addresses ?? [])]
})
const mailbox = ref<string>(props.info.address || '')

const passwords = ref<MailAppPassword[]>([])
const loading = ref(false)
const creating = ref(false)
const label = ref('')
const created = ref<MailAppPasswordCreated | null>(null)
const copiedKey = ref('')

function errMsg(e: any, fallback: string): string {
  return e?.data?.error?.message || (typeof e?.data?.error === 'string' ? e.data.error : '') || e?.message || fallback
}

async function loadPasswords() {
  if (!access.value?.enabled || !mailbox.value) return
  loading.value = true
  try {
    passwords.value = await api.listMailAppPasswords(mailbox.value)
  } catch (e: any) {
    toast.error(errMsg(e, t('mailApps.loadFailed')))
  } finally {
    loading.value = false
  }
}

watch(mailbox, () => { created.value = null; loadPasswords() })
onMounted(loadPasswords)

async function createPassword() {
  creating.value = true
  try {
    created.value = await api.createMailAppPassword(label.value.trim(), mailbox.value)
    label.value = ''
    await loadPasswords()
  } catch (e: any) {
    toast.error(errMsg(e, t('mailApps.createFailed')))
  } finally {
    creating.value = false
  }
}

async function revoke(p: MailAppPassword) {
  if (!confirm(t('mailApps.revokeConfirm', { label: p.label || p.id }))) return
  try {
    await api.revokeMailAppPassword(p.id)
    if (created.value?.id === p.id) created.value = null
    toast.info(t('mailApps.revoked'))
    await loadPasswords()
  } catch (e: any) {
    toast.error(errMsg(e, t('mailApps.revokeFailed')))
  }
}

async function copy(text: string, key: string) {
  try {
    await navigator.clipboard.writeText(text)
    copiedKey.value = key
    toast.success(t('common.copied'))
    setTimeout(() => { if (copiedKey.value === key) copiedKey.value = '' }, 1800)
  } catch { /* pano yok */ }
}

// Sunucu ayarları tablosu
interface Row { key: string; label: string; value: string; note?: string }
const rows = computed<Row[]>(() => {
  const a = access.value
  if (!a?.enabled) return []
  const host = a.host || ''
  const out: Row[] = [
    { key: 'imap', label: t('mailApps.imapServer'), value: host, note: `${t('mailApps.port')} ${a.imap_port} · SSL/TLS` },
  ]
  if (a.submission_port) {
    out.push({ key: 'smtp', label: t('mailApps.smtpServer'), value: host, note: `${t('mailApps.port')} ${a.submission_port} · STARTTLS` })
  }
  if (a.submissions_port) {
    out.push({ key: 'smtps', label: t('mailApps.smtpServerAlt'), value: host, note: `${t('mailApps.port')} ${a.submissions_port} · SSL/TLS` })
  }
  out.push({ key: 'user', label: t('mailApps.username'), value: mailbox.value, note: t('mailApps.usernameNote') })
  return out
})

// Kurulum rehberleri
type GuideId = 'gmail' | 'outlook' | 'apple' | 'thunderbird'
const guides: { id: GuideId; icon: string; steps: number }[] = [
  { id: 'gmail', icon: 'lucide:mail', steps: 6 },
  { id: 'outlook', icon: 'lucide:inbox', steps: 5 },
  { id: 'apple', icon: 'lucide:smartphone', steps: 5 },
  { id: 'thunderbird', icon: 'lucide:bird', steps: 4 },
]
const openGuide = ref<GuideId>('gmail')
const vars = computed(() => ({
  address: mailbox.value,
  host: access.value?.host || '',
  imap: String(access.value?.imap_port || 993),
  smtp: String(access.value?.submission_port || access.value?.submissions_port || 587),
}))
const mobileConfigUrl = computed(() => api.mailMobileConfigUrl(mailbox.value))
</script>

<template>
  <div class="space-y-4">
    <div v-if="!access?.enabled" class="rounded-xl border border-line bg-surface/60 p-5 text-sm text-fg-muted">
      <div class="flex items-start gap-2.5">
        <Icon name="lucide:plug-zap" class="size-5 shrink-0 text-fg-subtle" />
        <p>{{ t('mailApps.disabled') }}</p>
      </div>
    </div>

    <template v-else>
      <p class="text-sm text-fg-muted">{{ t('mailApps.intro') }}</p>

      <!-- Gmail web POP uyarisi -->
      <div class="flex items-start gap-2.5 rounded-lg border border-warn/30 bg-warn/5 px-4 py-3 text-xs text-fg-muted">
        <Icon name="lucide:info" class="mt-0.5 size-4 shrink-0 text-warn" />
        <p>{{ t('mailApps.gmailWebNote') }}</p>
      </div>

      <!-- Kutu secimi (yalnizca sistem kutusu olan owner/platform admin icin) -->
      <div v-if="boxes.length > 1" class="flex flex-wrap items-center gap-2 text-xs">
        <span class="text-fg-muted">{{ t('mail.mailbox') }}</span>
        <button
          v-for="b in boxes"
          :key="b"
          type="button"
          class="cursor-pointer rounded-lg border px-2.5 py-1.5 font-mono text-xs transition-colors"
          :class="mailbox === b ? 'border-accent bg-accent/10 text-accent' : 'border-line text-fg hover:border-accent/50'"
          @click="mailbox = b"
        >{{ b }}</button>
      </div>

      <!-- Sunucu ayarlari -->
      <PanelFrame :label="t('mailApps.serverSettings')">
        <div class="divide-y divide-line">
          <div v-for="r in rows" :key="r.key" class="flex flex-wrap items-center justify-between gap-2 px-4 py-2.5">
            <div class="min-w-0">
              <div class="text-xs text-fg-muted">{{ r.label }}</div>
              <div v-if="r.note" class="text-[11px] text-fg-subtle">{{ r.note }}</div>
            </div>
            <div class="flex items-center gap-2">
              <code class="break-all font-mono text-sm text-fg">{{ r.value }}</code>
              <button
                type="button"
                class="cursor-pointer text-fg-muted transition-colors hover:text-accent"
                :title="t('common.copy')"
                @click="copy(r.value, r.key)"
              >
                <Icon :name="copiedKey === r.key ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
              </button>
            </div>
          </div>
          <div class="px-4 py-2.5 text-xs text-fg-muted">
            <Icon name="lucide:key-round" class="mr-1 inline size-3.5 text-accent" />
            {{ t('mailApps.passwordNote') }}
          </div>
        </div>
      </PanelFrame>

      <!-- Uygulama parolalari -->
      <PanelFrame :label="t('mailApps.passwords')" :meta="String(passwords.filter(p => !p.revoked_at).length)">
        <div class="space-y-3 p-4">
          <form class="flex flex-wrap items-end gap-2" @submit.prevent="createPassword">
            <div class="min-w-[200px] flex-1">
              <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('mailApps.label') }}</label>
              <input
                v-model="label"
                type="text"
                maxlength="60"
                :placeholder="t('mailApps.labelPlaceholder')"
                class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
              >
            </div>
            <button
              type="submit"
              :disabled="creating"
              class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-semibold text-on-accent transition-all hover:opacity-90 disabled:opacity-50"
            >
              <Icon v-if="creating" name="lucide:loader-2" class="size-4 animate-spin" />
              <Icon v-else name="lucide:plus" class="size-4" />
              <span>{{ t('mailApps.create') }}</span>
            </button>
          </form>

          <!-- Olusturulan parola: yalnizca bir kez gosterilir -->
          <div v-if="created" class="rounded-lg border border-accent/40 bg-accent/5 p-3">
            <div class="flex items-center gap-1.5 text-xs font-semibold text-accent">
              <Icon name="lucide:shield-check" class="size-4" />
              {{ t('mailApps.createdTitle') }}
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <code class="select-all rounded-md border border-line bg-bg px-3 py-1.5 font-mono text-base tracking-wider text-fg">{{ created.password }}</code>
              <button
                type="button"
                class="flex cursor-pointer items-center gap-1 text-xs text-accent hover:underline"
                @click="copy(created.password, 'newpw')"
              >
                <Icon :name="copiedKey === 'newpw' ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
                {{ copiedKey === 'newpw' ? t('common.copied') : t('common.copy') }}
              </button>
            </div>
            <p class="mt-2 text-[11px] text-fg-muted">{{ t('mailApps.createdHint') }}</p>
          </div>

          <p v-if="loading" class="text-sm text-fg-muted">{{ t('common.loading') }}</p>
          <p v-else-if="!passwords.length" class="py-3 text-center text-sm text-fg-muted">{{ t('mailApps.noPasswords') }}</p>
          <div v-else class="overflow-x-auto">
            <table class="w-full min-w-[520px] text-left text-xs">
              <thead class="text-fg-subtle">
                <tr>
                  <th class="py-1.5 pr-3 font-medium">{{ t('mailApps.colLabel') }}</th>
                  <th class="py-1.5 pr-3 font-medium">{{ t('mailApps.colCreated') }}</th>
                  <th class="py-1.5 pr-3 font-medium">{{ t('mailApps.colLastUsed') }}</th>
                  <th class="py-1.5 pr-3 font-medium">{{ t('mailApps.colStatus') }}</th>
                  <th class="py-1.5" />
                </tr>
              </thead>
              <tbody class="divide-y divide-line">
                <tr v-for="p in passwords" :key="p.id" :class="p.revoked_at ? 'opacity-50' : ''">
                  <td class="py-2 pr-3 text-fg">{{ p.label || '—' }}</td>
                  <td class="py-2 pr-3 font-mono text-fg-muted">{{ relativeTime(p.created_at) }}</td>
                  <td class="py-2 pr-3 font-mono text-fg-muted">
                    <template v-if="p.last_used_at">{{ relativeTime(p.last_used_at) }}<span v-if="p.last_used_ip" class="text-fg-subtle"> · {{ p.last_used_ip }}</span></template>
                    <template v-else>{{ t('mailApps.neverUsed') }}</template>
                  </td>
                  <td class="py-2 pr-3">{{ p.revoked_at ? t('mailApps.statusRevoked') : t('mailApps.statusActive') }}</td>
                  <td class="py-2 text-right">
                    <button
                      v-if="!p.revoked_at"
                      type="button"
                      class="cursor-pointer rounded-lg border border-line px-2 py-1 text-fg-muted transition-colors hover:border-danger/40 hover:text-danger"
                      @click="revoke(p)"
                    >{{ t('mailApps.revoke') }}</button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </PanelFrame>

      <!-- Kurulum rehberleri -->
      <PanelFrame :label="t('mailApps.guides')">
        <div class="flex flex-wrap gap-2 border-b border-line px-4 py-3">
          <button
            v-for="g in guides"
            :key="g.id"
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
            :class="openGuide === g.id ? 'border-accent bg-accent/10 text-accent' : 'border-line text-fg-muted hover:text-fg'"
            @click="openGuide = g.id"
          >
            <Icon :name="g.icon" class="size-3.5" />
            {{ t(`mailApps.guide.${g.id}.title`) }}
          </button>
        </div>
        <div v-for="g in guides" v-show="openGuide === g.id" :key="g.id" class="p-4">
          <p v-if="t(`mailApps.guide.${g.id}.intro`)" class="mb-3 text-xs text-fg-muted">{{ t(`mailApps.guide.${g.id}.intro`) }}</p>
          <ol class="space-y-2 text-sm text-fg">
            <li v-for="n in g.steps" :key="n" class="flex gap-2.5">
              <span class="mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border border-line font-mono text-[10px] text-fg-muted">{{ n }}</span>
              <span class="min-w-0 break-words">{{ t(`mailApps.guide.${g.id}.s${n}`, vars) }}</span>
            </li>
          </ol>
          <a
            v-if="g.id === 'apple'"
            :href="mobileConfigUrl"
            download
            class="mt-4 inline-flex items-center gap-1.5 rounded-lg border border-accent/40 bg-accent/10 px-3 py-1.5 text-xs font-medium text-accent transition-colors hover:bg-accent/15"
          >
            <Icon name="lucide:download" class="size-3.5" />
            {{ t('mailApps.downloadProfile') }}
          </a>
          <p v-if="g.id === 'apple'" class="mt-2 text-[11px] text-fg-subtle">{{ t('mailApps.profileNote') }}</p>
        </div>
      </PanelFrame>
    </template>
  </div>
</template>
