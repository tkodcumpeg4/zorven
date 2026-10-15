<script setup lang="ts">
// ACIK SURUM override (scripts/oss/overrides): yalniz TR + EN.
// Dil secici acilir menu: klavye (Enter/Bosluk/oklar/Esc/Home/End) ve disari tiklama destekli.
const { locale } = useI18n()

const LANGS = [
  { code: 'tr', short: 'TR', name: 'Türkçe' },
  { code: 'en', short: 'EN', name: 'English' },
] as const

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)
const items = ref<HTMLElement[]>([])
const uid = useId()

const current = computed(() => LANGS.find(l => l.code === locale.value) ?? LANGS[0])

function setLang(next: string) {
  locale.value = next as any
  try { localStorage.setItem('zorven_lang', next) } catch { /* erisim yok */ }
}

function focusItem(i: number) {
  const n = LANGS.length
  items.value[((i % n) + n) % n]?.focus()
}

async function openMenu(focusIdx?: number) {
  open.value = true
  await nextTick()
  const idx = focusIdx ?? Math.max(0, LANGS.findIndex(l => l.code === locale.value))
  focusItem(idx)
}

function close(returnFocus = true) {
  if (!open.value) return
  open.value = false
  if (returnFocus) trigger.value?.focus()
}

function onTriggerKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') { e.preventDefault(); openMenu() }
  else if (e.key === 'ArrowUp') { e.preventDefault(); openMenu(LANGS.length - 1) }
}

function onMenuKey(e: KeyboardEvent) {
  const i = items.value.findIndex(el => el === document.activeElement)
  if (e.key === 'ArrowDown') { e.preventDefault(); focusItem(i + 1) }
  else if (e.key === 'ArrowUp') { e.preventDefault(); focusItem(i - 1) }
  else if (e.key === 'Home') { e.preventDefault(); focusItem(0) }
  else if (e.key === 'End') { e.preventDefault(); focusItem(LANGS.length - 1) }
  else if (e.key === 'Escape') { e.preventDefault(); close() }
  else if (e.key === 'Tab') close(false)
}

function pick(code: string) {
  setLang(code)
  close()
}

function onDocDown(e: Event) {
  if (open.value && root.value && !root.value.contains(e.target as Node)) close(false)
}
onMounted(() => document.addEventListener('pointerdown', onDocDown))
onBeforeUnmount(() => document.removeEventListener('pointerdown', onDocDown))
</script>

<template>
  <div ref="root" class="relative">
    <button
      ref="trigger"
      type="button"
      class="flex h-8 cursor-pointer items-center gap-1.5 rounded-lg border border-line px-2 font-mono text-[11px] font-semibold
             text-fg-muted transition-colors duration-200 hover:border-accent/40 hover:bg-surface hover:text-fg"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="`${uid}-menu`"
      :aria-label="`Language: ${current.name}`"
      :title="current.name"
      @click="open ? close() : openMenu()"
      @keydown="onTriggerKey"
    >
      <Icon name="lucide:languages" class="size-3.5" aria-hidden="true" />
      <span>{{ current.short }}</span>
      <Icon name="lucide:chevron-down" class="size-3 transition-transform duration-200" :class="open ? 'rotate-180' : ''" aria-hidden="true" />
    </button>

    <div
      v-if="open"
      :id="`${uid}-menu`"
      role="menu"
      aria-label="Language"
      class="absolute right-0 top-full z-50 mt-2 min-w-36 rounded-lg border border-line bg-surface p-1 shadow-lg"
      @keydown="onMenuKey"
    >
      <button
        v-for="(l, i) in LANGS"
        :key="l.code"
        :ref="el => { if (el) items[i] = el as HTMLElement }"
        type="button"
        role="menuitemradio"
        :aria-checked="locale === l.code"
        :lang="l.code"
        class="flex w-full cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-1.5 text-left text-xs
               transition-colors duration-150 hover:bg-line/40 focus:bg-line/40 focus:outline-none"
        :class="locale === l.code ? 'text-fg' : 'text-fg-muted hover:text-fg'"
        @click="pick(l.code)"
      >
        <span>{{ l.name }}</span>
        <Icon v-if="locale === l.code" name="lucide:check" class="size-3.5 text-accent" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
