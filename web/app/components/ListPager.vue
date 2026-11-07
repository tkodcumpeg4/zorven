<script setup lang="ts">
// Ortak liste sayfalayici. Toplam biliniyorsa "1-50 / 312" + kisaltilmis sayfa
// numaralari; yalniz hasMore biliniyorsa "1-50" + onceki/sonraki.
// Kullanim: <ListPager :list="pager" />  (pager = usePagedList() donusu)
//   veya elle: <ListPager :page :total :has-more :count @update:page />
import type { Ref } from 'vue'
type PagerList = {
  page: Ref<number>, total: Ref<number | null>, hasMore: Ref<boolean>, items: Ref<unknown[]>,
  loading: Ref<boolean>, pageSize: number, setPage: (n: number) => void,
}
const input = withDefaults(defineProps<{
  list?: PagerList
  page?: number
  pageSize?: number
  /** Toplam kayit (bilinmiyorsa null). */
  total?: number | null
  hasMore?: boolean
  /** Bu sayfadaki kayit sayisi (ozet icin). */
  count?: number
  loading?: boolean
}>(), { list: undefined, page: 1, pageSize: 50, total: null, hasMore: false, count: 0, loading: false })

const emit = defineEmits<{ 'update:page': [number] }>()
const { t } = useI18n()
const root = ref<HTMLElement | null>(null)

const v = computed(() => input.list
  ? { page: input.list.page.value, size: input.list.pageSize, total: input.list.total.value, hasMore: input.list.hasMore.value, count: input.list.items.value.length, loading: input.list.loading.value }
  : { page: input.page, size: input.pageSize, total: input.total, hasMore: input.hasMore, count: input.count, loading: input.loading })

const pageCount = computed(() => v.value.total != null
  ? Math.max(1, Math.ceil(v.value.total / v.value.size))
  : (v.value.hasMore ? v.value.page + 1 : v.value.page))
const from = computed(() => v.value.count > 0 ? (v.value.page - 1) * v.value.size + 1 : 0)
const to = computed(() => (v.value.page - 1) * v.value.size + v.value.count)
const visible = computed(() => v.value.page > 1 || v.value.hasMore || (v.value.total != null && v.value.total > v.value.size))
const canPrev = computed(() => v.value.page > 1 && !v.value.loading)
const canNext = computed(() => !v.value.loading && (v.value.total != null ? v.value.page < pageCount.value : v.value.hasMore))

/** 1 … 4 5 6 … 20 */
const pages = computed<(number | '…')[]>(() => {
  if (v.value.total == null) return []
  const n = pageCount.value, p = v.value.page
  if (n <= 7) return Array.from({ length: n }, (_, i) => i + 1)
  const set = new Set<number>([1, n, p - 1, p, p + 1])
  if (p <= 3) { set.add(2); set.add(3); set.add(4) }
  if (p >= n - 2) { set.add(n - 1); set.add(n - 2); set.add(n - 3) }
  const sorted = [...set].filter(x => x >= 1 && x <= n).sort((a, b) => a - b)
  const out: (number | '…')[] = []
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i]! - sorted[i - 1]! > 1) out.push('…')
    out.push(sorted[i]!)
  }
  return out
})

function go(p: number) {
  if (p < 1 || p === v.value.page || v.value.loading) return
  if (v.value.total != null && p > pageCount.value) return
  if (input.list) input.list.setPage(p); else emit('update:page', p)
  // Liste ustune kaydir (sayfalayicinin hemen ustundeki blok).
  nextTick(() => {
    const target = root.value?.previousElementSibling ?? root.value?.parentElement
    target?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
  })
}
</script>

<template>
  <nav v-if="visible" ref="root" :aria-label="t('pager.label')"
    class="mt-3 flex flex-wrap items-center justify-between gap-3 text-sm text-fg-muted">
    <p class="tabular-nums" aria-live="polite">
      <template v-if="v.total != null">{{ t('pager.summary', { from, to, total: v.total }) }}</template>
      <template v-else>{{ t('pager.summaryMore', { from, to }) }}</template>
    </p>
    <div class="flex items-center gap-1">
      <button type="button" :disabled="!canPrev" :aria-label="t('pager.prev')"
        class="inline-flex h-8 items-center gap-1 rounded-md border border-line px-2 transition hover:bg-surface-2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
        @click="go(v.page - 1)">
        <Icon name="lucide:chevron-left" class="size-4" /><span class="hidden sm:inline">{{ t('pager.prev') }}</span>
      </button>
      <template v-for="(p, i) in pages" :key="i">
        <span v-if="p === '…'" class="px-1 text-fg-subtle" aria-hidden="true">…</span>
        <button v-else type="button" :disabled="v.loading" :aria-current="p === v.page ? 'page' : undefined"
          :aria-label="t('pager.goto', { page: p })"
          class="inline-flex h-8 min-w-8 items-center justify-center rounded-md border px-2 tabular-nums transition disabled:cursor-not-allowed"
          :class="p === v.page ? 'border-accent bg-accent/10 font-medium text-fg' : 'border-line hover:bg-surface-2 hover:text-fg'"
          @click="go(p)">{{ p }}</button>
      </template>
      <span v-if="v.total == null" class="px-2 tabular-nums">{{ t('pager.pageN', { page: v.page }) }}</span>
      <button type="button" :disabled="!canNext" :aria-label="t('pager.next')"
        class="inline-flex h-8 items-center gap-1 rounded-md border border-line px-2 transition hover:bg-surface-2 hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
        @click="go(v.page + 1)">
        <span class="hidden sm:inline">{{ t('pager.next') }}</span><Icon name="lucide:chevron-right" class="size-4" />
      </button>
    </div>
  </nav>
</template>
