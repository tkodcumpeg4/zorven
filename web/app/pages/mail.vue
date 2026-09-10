<script setup lang="ts">
import type { MailInfo, MailMessage } from '~/types/api'

const api = useApi()
const toast = useToast()
const { t } = useI18n()
const { relativeTime } = useFormat()

const info = ref<MailInfo | null>(null)
const box = ref<'inbox' | 'sent'>('inbox')
const messages = ref<MailMessage[]>([])
const selected = ref<MailMessage | null>(null)
const loading = ref(true)
const loadingMsg = ref(false)
const copied = ref(false)

// Yeni mesaj / yanit
const showCompose = ref(false)
const sending = ref(false)
interface ComposeAtt { filename: string; content_type: string; content_base64: string; size: number }
const compose = reactive({
  to: '', subject: '', body: '', in_reply_to: '', from: '',
  attachments: [] as ComposeAtt[],
})
const MAX_ATTACH_TOTAL = 20 * 1024 * 1024 // 20 MB toplam sınır

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

async function onFilePick(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  for (const f of files) {
    const total = compose.attachments.reduce((a, x) => a + x.size, 0) + f.size
    if (total > MAX_ATTACH_TOTAL) { toast.warn(t('mail.attachTooLarge')); break }
    const b64 = await fileToBase64(f)
    compose.attachments.push({ filename: f.name, content_type: f.type || 'application/octet-stream', content_base64: b64, size: f.size })
  }
  input.value = ''
}

function fileToBase64(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result).split(',')[1] ?? '')
    r.onerror = reject
    r.readAsDataURL(f)
  })
}

function removeAttachment(i: number) {
  compose.attachments.splice(i, 1)
}

// Gönderilebilecek adresler: kiracının kendi adresi + (owner ise) sistem kutuları.
const fromOptions = computed<string[]>(() => {
  const own = info.value?.address ? [info.value.address] : []
  const sys = info.value?.system_addresses ?? []
  return [...own, ...sys]
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    info.value = await api.mailInfo()
    if (info.value?.enabled) await loadBox()
  } catch (e: any) {
    toast.error(e?.data?.error?.error || e?.message || t('mail.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function loadBox() {
  selected.value = null
  loadingMsg.value = true
  try {
    messages.value = await api.listMail(box.value)
  } catch (e: any) {
    toast.error(e?.data?.error?.error || e?.message || t('mail.loadFailed'))
  } finally {
    loadingMsg.value = false
  }
}

function switchBox(b: 'inbox' | 'sent') {
  if (box.value === b) return
  box.value = b
  loadBox()
}

async function openMessage(m: MailMessage) {
  const wasUnseen = m.direction === 'inbound' && !m.seen
  try {
    selected.value = await api.getMail(m.id)
    m.seen = true
    if (wasUnseen && info.value) info.value.unseen = Math.max(0, (info.value.unseen || 0) - 1)
  } catch (e: any) {
    toast.error(e?.data?.error?.error || e?.message || t('mail.loadFailed'))
  }
}

async function removeMessage(m: MailMessage) {
  if (!confirm(t('mail.deleteConfirm'))) return
  try {
    await api.deleteMail(m.id)
    messages.value = messages.value.filter(x => x.id !== m.id)
    if (selected.value?.id === m.id) selected.value = null
    toast.info(t('mail.deleted'))
  } catch (e: any) {
    toast.error(e?.data?.error?.error || e?.message || t('mail.deleteFailed'))
  }
}

function addrOnly(s: string): string {
  const m = s.match(/<([^>]+)>/)
  return m ? m[1] : s.trim()
}

function openCompose() {
  compose.to = ''
  compose.subject = ''
  compose.body = ''
  compose.in_reply_to = ''
  compose.from = info.value?.address || ''
  compose.attachments = []
  showCompose.value = true
}

function replyTo(m: MailMessage) {
  compose.to = addrOnly(m.from)
  compose.subject = m.subject.toLowerCase().startsWith('re:') ? m.subject : `Re: ${m.subject}`
  compose.body = ''
  compose.in_reply_to = m.message_id || ''
  // Hangi kutuya geldiyse ondan yanıtla (sistem adresi ise onu koru, seçilebilirse).
  const toAddr = addrOnly(m.to || '')
  compose.from = fromOptions.value.includes(toAddr) ? toAddr : (info.value?.address || '')
  compose.attachments = []
  showCompose.value = true
}

async function send() {
  if (!compose.to.trim() || !compose.to.includes('@')) { toast.warn(t('mail.warnRecipient')); return }
  if (!compose.body.trim()) { toast.warn(t('mail.warnBody')); return }
  sending.value = true
  try {
    await api.sendMail({
      to: compose.to.trim(),
      subject: compose.subject.trim(),
      body: compose.body,
      in_reply_to: compose.in_reply_to || undefined,
      from: (compose.from && compose.from !== info.value?.address) ? compose.from : undefined,
      attachments: compose.attachments.length
        ? compose.attachments.map(a => ({ filename: a.filename, content_type: a.content_type, content_base64: a.content_base64 }))
        : undefined,
    })
    toast.success(t('mail.sentToast'))
    showCompose.value = false
    if (box.value === 'sent') loadBox()
  } catch (e: any) {
    toast.error(e?.data?.error?.error || e?.message || t('mail.sendFailed'))
  } finally {
    sending.value = false
  }
}

async function copyAddress() {
  if (!info.value?.address) return
  try {
    await navigator.clipboard.writeText(info.value.address)
    copied.value = true
    toast.success(t('mail.addressCopied'))
    setTimeout(() => { copied.value = false }, 2000)
  } catch { /* pano yok */ }
}
</script>

<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('mail.title') }}</h1>
        <p class="mt-0.5 text-sm text-fg-muted">{{ t('mail.subtitle') }}</p>
      </div>
      <button
        v-if="info?.enabled"
        type="button"
        class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-semibold text-on-accent transition-all hover:opacity-90 active:scale-95"
        @click="openCompose"
      >
        <Icon name="lucide:pencil" class="size-4" />
        <span>{{ t('mail.compose') }}</span>
      </button>
    </header>

    <p v-if="loading" class="rounded-lg border border-line bg-surface px-4 py-6 text-sm text-fg-muted">
      {{ t('common.loading') }}
    </p>

    <!-- Webmail kapali -->
    <div v-else-if="!info?.enabled" class="rounded-xl border border-line bg-surface/60 p-5 text-sm text-fg-muted">
      <div class="flex items-start gap-2.5">
        <Icon name="lucide:mail-x" class="size-5 shrink-0 text-fg-subtle" />
        <p>{{ t('mail.disabled') }}</p>
      </div>
    </div>

    <template v-else>
      <!-- Adres seridi -->
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-accent/30 bg-accent/5 px-4 py-2.5">
        <div class="flex items-center gap-2 text-sm">
          <Icon name="lucide:at-sign" class="size-4 text-accent" />
          <span class="text-fg-muted">{{ t('mail.yourAddress') }}</span>
          <code class="font-mono font-semibold text-accent">{{ info.address }}</code>
        </div>
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1 text-xs text-accent hover:underline"
          @click="copyAddress"
        >
          <Icon :name="copied ? 'lucide:check' : 'lucide:copy'" class="size-3.5" />
          {{ copied ? t('common.copied') : t('common.copy') }}
        </button>
      </div>

      <!-- Kutu sekmeleri -->
      <div class="flex gap-2 border-b border-line pb-3">
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
          :class="box === 'inbox' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
          @click="switchBox('inbox')"
        >
          <Icon name="lucide:inbox" class="size-3.5" />
          <span>{{ t('mail.inbox') }}</span>
          <span v-if="info.unseen" class="rounded-full bg-accent px-1.5 py-0.2 text-[10px] font-bold text-on-accent">{{ info.unseen }}</span>
        </button>
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
          :class="box === 'sent' ? 'bg-surface-2 text-fg border border-line' : 'text-fg-muted hover:text-fg'"
          @click="switchBox('sent')"
        >
          <Icon name="lucide:send" class="size-3.5" />
          <span>{{ t('mail.sent') }}</span>
        </button>
      </div>

      <div class="grid gap-4 lg:grid-cols-[minmax(0,360px)_1fr]">
        <!-- Liste -->
        <PanelFrame :label="box === 'inbox' ? t('mail.inbox') : t('mail.sent')" :meta="String(messages.length)">
          <p v-if="loadingMsg" class="px-4 py-6 text-sm text-fg-muted">{{ t('common.loading') }}</p>
          <p v-else-if="!messages.length" class="px-4 py-8 text-center text-sm text-fg-muted">{{ t('mail.empty') }}</p>
          <div v-else class="max-h-[70vh] divide-y divide-line overflow-auto">
            <button
              v-for="m in messages"
              :key="m.id"
              type="button"
              class="flex w-full cursor-pointer flex-col gap-0.5 px-4 py-3 text-left transition-colors"
              :class="[
                selected?.id === m.id ? 'bg-accent/10' : 'hover:bg-surface-2/40',
                m.direction === 'inbound' && !m.seen ? 'font-semibold' : '',
              ]"
              @click="openMessage(m)"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="truncate text-sm text-fg">{{ box === 'inbox' ? (m.from || '—') : (m.to || '—') }}</span>
                <span class="shrink-0 font-mono text-[10px] text-fg-subtle">{{ relativeTime(m.received_at) }}</span>
              </div>
              <div class="flex items-center gap-1.5">
                <span v-if="m.direction === 'inbound' && !m.seen" class="size-1.5 shrink-0 rounded-full bg-accent" />
                <span class="truncate text-xs text-fg-muted">{{ m.subject || t('mail.noSubject') }}</span>
                <span
                  v-if="box === 'inbox' && m.to && info && m.to.toLowerCase() !== (info.address || '').toLowerCase()"
                  class="shrink-0 rounded border border-line bg-surface-2 px-1 font-mono text-[9px] text-fg-subtle"
                  :title="m.to"
                >{{ m.to.split('@')[0] }}</span>
              </div>
              <span v-if="m.text_body" class="truncate text-[11px] text-fg-subtle">{{ m.text_body }}</span>
            </button>
          </div>
        </PanelFrame>

        <!-- Okuma paneli -->
        <PanelFrame :label="t('mail.message')">
          <div v-if="!selected" class="px-4 py-16 text-center text-sm text-fg-muted">
            {{ t('mail.selectMessage') }}
          </div>
          <div v-else class="flex h-full flex-col">
            <div class="border-b border-line p-4">
              <div class="flex items-start justify-between gap-3">
                <h2 class="text-base font-semibold text-fg">{{ selected.subject || t('mail.noSubject') }}</h2>
                <div class="flex shrink-0 items-center gap-1.5">
                  <button
                    v-if="selected.direction === 'inbound'"
                    type="button"
                    class="flex cursor-pointer items-center gap-1 rounded-lg border border-line px-2.5 py-1 text-xs text-fg-muted transition-colors hover:border-accent/40 hover:text-accent"
                    @click="replyTo(selected)"
                  >
                    <Icon name="lucide:reply" class="size-3.5" />
                    {{ t('mail.reply') }}
                  </button>
                  <button
                    type="button"
                    class="cursor-pointer rounded-lg border border-line p-1.5 text-fg-muted transition-colors hover:border-danger/40 hover:text-danger"
                    :title="t('common.delete')"
                    @click="removeMessage(selected)"
                  >
                    <Icon name="lucide:trash-2" class="size-3.5" />
                  </button>
                </div>
              </div>
              <div class="mt-2 space-y-0.5 font-mono text-[11px] text-fg-muted">
                <div><span class="text-fg-subtle">{{ t('mail.from') }}:</span> {{ selected.from || '—' }}</div>
                <div><span class="text-fg-subtle">{{ t('mail.to') }}:</span> {{ selected.to || '—' }}</div>
                <div><span class="text-fg-subtle">{{ t('mail.date') }}:</span> {{ new Date(selected.received_at).toLocaleString() }}</div>
              </div>
            </div>
            <div v-if="selected.attachments && selected.attachments.length" class="flex flex-wrap gap-2 border-b border-line px-4 py-3">
              <a
                v-for="att in selected.attachments"
                :key="att.id"
                :href="api.mailAttachmentUrl(att.id)"
                download
                class="flex items-center gap-2 rounded-lg border border-line bg-surface-2 px-2.5 py-1.5 text-xs text-fg transition-colors hover:border-accent/40 hover:text-accent"
              >
                <Icon name="lucide:paperclip" class="size-3.5 text-accent" />
                <span class="max-w-[180px] truncate">{{ att.filename }}</span>
                <span class="text-[10px] text-fg-subtle">{{ fmtSize(att.size_bytes) }}</span>
                <Icon name="lucide:download" class="size-3.5" />
              </a>
            </div>
            <div class="flex-1 overflow-auto p-4">
              <iframe
                v-if="selected.html_body"
                :srcdoc="selected.html_body"
                sandbox=""
                class="h-[55vh] w-full rounded border border-line bg-white"
              />
              <pre v-else class="whitespace-pre-wrap break-words font-sans text-sm text-fg">{{ selected.text_body || t('mail.emptyBody') }}</pre>
            </div>
          </div>
        </PanelFrame>
      </div>
    </template>

    <!-- Yeni mesaj / yanit modali -->
    <div
      v-if="showCompose"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showCompose = false"
    >
      <div class="w-full max-w-lg rounded-xl border border-line bg-surface p-5 shadow-2xl">
        <div class="flex items-center justify-between border-b border-line pb-3">
          <h3 class="flex items-center gap-2 text-sm font-semibold text-fg">
            <Icon name="lucide:pencil" class="size-4 text-accent" />
            {{ compose.in_reply_to ? t('mail.reply') : t('mail.compose') }}
          </h3>
          <button type="button" class="text-fg-muted hover:text-fg" @click="showCompose = false">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>
        <form class="mt-4 space-y-3" @submit.prevent="send">
          <div v-if="fromOptions.length > 1">
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('mail.from') }}</label>
            <select
              v-model="compose.from"
              class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            >
              <option v-for="addr in fromOptions" :key="addr" :value="addr">{{ addr }}</option>
            </select>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('mail.to') }}</label>
            <input
              v-model="compose.to"
              type="email"
              placeholder="ornek@alanadi.com"
              class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            >
            <p v-if="info && info.can_send_external === false" class="mt-1 flex items-center gap-1 text-[11px] text-fg-subtle">
              <Icon name="lucide:info" class="size-3 shrink-0" />
              {{ t('mail.internalOnly', { domain: '@' + (info.domain || 'mail.zorven.app') }) }}
            </p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('mail.subject') }}</label>
            <input
              v-model="compose.subject"
              type="text"
              class="w-full rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            >
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('mail.body') }}</label>
            <textarea
              v-model="compose.body"
              rows="8"
              class="w-full resize-y rounded-md border border-line bg-bg px-3 py-2 text-sm text-fg focus:border-accent focus:outline-none"
            />
          </div>
          <div v-if="compose.attachments.length" class="flex flex-wrap gap-2">
            <span
              v-for="(att, i) in compose.attachments"
              :key="i"
              class="flex items-center gap-1.5 rounded-lg border border-line bg-surface-2 px-2 py-1 text-xs text-fg"
            >
              <Icon name="lucide:paperclip" class="size-3 text-accent" />
              <span class="max-w-[160px] truncate">{{ att.filename }}</span>
              <span class="text-[10px] text-fg-subtle">{{ fmtSize(att.size) }}</span>
              <button type="button" class="cursor-pointer text-fg-muted hover:text-danger" @click="removeAttachment(i)">
                <Icon name="lucide:x" class="size-3" />
              </button>
            </span>
          </div>

          <div class="flex items-center justify-between gap-2 border-t border-line pt-3">
            <label class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-line px-3 py-1.5 text-xs text-fg-muted transition-colors hover:border-accent/40 hover:text-accent">
              <Icon name="lucide:paperclip" class="size-3.5" />
              <span>{{ t('mail.attach') }}</span>
              <input type="file" multiple class="hidden" @change="onFilePick">
            </label>
            <div class="flex gap-2">
            <button
              type="button"
              class="cursor-pointer rounded-lg border border-line px-3 py-1.5 text-xs text-fg-muted hover:bg-surface-2"
              @click="showCompose = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="sending"
              class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-xs font-semibold text-on-accent hover:opacity-90 disabled:opacity-50"
            >
              <Icon v-if="sending" name="lucide:loader-2" class="size-3.5 animate-spin" />
              <Icon v-else name="lucide:send" class="size-3.5" />
              <span>{{ sending ? t('mail.sending') : t('mail.send') }}</span>
            </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
