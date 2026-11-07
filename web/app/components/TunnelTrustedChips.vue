<script setup lang="ts">
// Guvenilir IP editoru: tek IP veya CIDR; istemci tarafi dogrulama sunucudakiyle ayni
// sinirlari uygular (en fazla 100; IPv4 icin /8, IPv6 icin /32'den genis olamaz).
const model = defineModel<string[]>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

const MAX = 100
const draft = ref('')
const error = ref('')

const V4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/
const V6 = /^[0-9a-f:.]+$/i

// Dondurur: normalize edilmis metin veya hata anahtari.
function check(raw: string): { v?: string, err?: string } {
  const [addr, bitsRaw, extra] = raw.split('/')
  if (extra !== undefined || !addr) return { err: 'trustedErrInvalid' }
  const hasBits = bitsRaw !== undefined
  if (hasBits && !/^\d{1,3}$/.test(bitsRaw!)) return { err: 'trustedErrInvalid' }
  const m = V4.exec(addr)
  if (m) {
    if ([m[1], m[2], m[3], m[4]].some(o => Number(o) > 255 || (o!.length > 1 && o!.startsWith('0')))) return { err: 'trustedErrInvalid' }
    const bits = hasBits ? Number(bitsRaw) : 32
    if (bits > 32) return { err: 'trustedErrInvalid' }
    if (bits < 8) return { err: 'trustedErrBroad' }
    return { v: hasBits ? raw : addr }
  }
  if (addr.includes(':') && V6.test(addr) && (addr.match(/::/g) || []).length <= 1 && addr.split(':').length <= 9) {
    const bits = hasBits ? Number(bitsRaw) : 128
    if (bits > 128) return { err: 'trustedErrInvalid' }
    if (bits < 32) return { err: 'trustedErrBroad' }
    return { v: hasBits ? raw : addr }
  }
  return { err: 'trustedErrInvalid' }
}

function add() {
  const raw = draft.value.trim()
  if (!raw) return
  const r = check(raw)
  if (r.err) { error.value = t('tunbot.' + r.err); return }
  const v = r.v!.toLowerCase()
  if (model.value.some(x => x.toLowerCase() === v)) { error.value = t('tunbot.trustedErrDup'); return }
  if (model.value.length >= MAX) { error.value = t('tunbot.trustedErrMax'); return }
  error.value = ''
  model.value = [...model.value, v]
  draft.value = ''
}
function remove(p: string) {
  model.value = model.value.filter(x => x !== p)
}
</script>

<template>
  <div>
    <div v-if="model.length" class="mb-1.5 flex flex-wrap gap-1.5">
      <span v-for="p in model" :key="p" class="flex items-center gap-1 rounded-full border border-line bg-surface-2 px-2.5 py-0.5 font-mono text-[11px] text-fg">
        {{ p }}
        <button v-if="!disabled" type="button" class="text-fg-subtle hover:text-fg" @click="remove(p)">
          <Icon name="lucide:x" class="size-3" />
        </button>
      </span>
    </div>
    <div v-if="!disabled" class="flex gap-2">
      <input v-model="draft" type="text" :placeholder="t('tunbot.trustedPlaceholder')"
             class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent"
             @keydown.enter.prevent="add">
      <button type="button" class="shrink-0 rounded border border-line px-3 py-1.5 text-xs text-fg hover:border-accent" @click="add">
        {{ t('tunbot.trustedAdd') }}
      </button>
    </div>
    <p v-if="error" class="mt-1 text-[11px] text-danger">{{ error }}</p>
  </div>
</template>
