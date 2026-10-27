<script setup lang="ts">
// Yol listesi editoru (bot korumasi / hiz siniri "hariç tutulan yollar"): '/' ile baslar,
// '*' yalniz sonda, en fazla 32 kayit.
const model = defineModel<string[]>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

const MAX = 32
const draft = ref('')
const error = ref('')

function add() {
  const v = draft.value.trim()
  if (!v) return
  if (!v.startsWith('/')) { error.value = t('tunbot.excludeErrStart'); return }
  if (/\s/.test(v)) { error.value = t('tunbot.excludeErrSpace'); return }
  const star = v.indexOf('*')
  if (star >= 0 && star !== v.length - 1) { error.value = t('tunbot.excludeErrStar'); return }
  if (model.value.includes(v)) { error.value = t('tunbot.excludeErrDup'); return }
  if (model.value.length >= MAX) { error.value = t('tunbot.excludeErrMax'); return }
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
      <input v-model="draft" type="text" :placeholder="t('tunbot.excludePlaceholder')"
             class="w-full rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-xs focus:border-accent"
             @keydown.enter.prevent="add">
      <button type="button" class="shrink-0 rounded border border-line px-3 py-1.5 text-xs text-fg hover:border-accent" @click="add">
        {{ t('tunbot.excludeAdd') }}
      </button>
    </div>
    <p v-if="error" class="mt-1 text-[11px] text-danger">{{ error }}</p>
  </div>
</template>
