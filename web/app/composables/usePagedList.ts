import type { Ref } from 'vue'
import type { PagedResult } from '~/composables/useApi'

export const PAGE_SIZE = 50

export interface UsePagedListOptions<F> {
  /** Rota sorgu anahtari (?page=N). Bir sayfada birden cok liste varsa her birine ayri anahtar ver. */
  queryKey?: string
  pageSize?: number
  /** Filtre/arama degerleri; degisince sayfa 1'e doner. */
  filters?: () => F
  /** Filtre degisiminde bekleme (ms). Arama kutulari icin 300 onerilir. */
  debounceMs?: number
  /** false: mount'ta otomatik yukleme yapma (cagiran reload() cagirir). */
  immediate?: boolean
}

/**
 * Sayfali liste durumu. fetcher({limit, offset}, filters) -> {items,total,hasMore}.
 * Sayfa numarasi rota sorgusunda tutulur; eski (gec gelen) yanitlar yok sayilir.
 */
export function usePagedList<T, F = Record<string, never>>(
  fetcher: (p: { limit: number, offset: number }, filters: F) => Promise<PagedResult<T>>,
  opts: UsePagedListOptions<F> = {},
) {
  const size = opts.pageSize ?? PAGE_SIZE
  const key = opts.queryKey ?? 'page'
  const route = useRoute()
  const router = useRouter()

  const readPage = () => {
    const raw = route.query[key]
    const v = Number(Array.isArray(raw) ? raw[0] : raw)
    return Number.isFinite(v) && v >= 1 ? Math.floor(v) : 1
  }

  const items = ref<T[]>([]) as Ref<T[]>
  const total = ref<number | null>(null)
  const hasMore = ref(false)
  const page = ref(readPage())
  const loading = ref(true)
  const error = ref<unknown>(null)
  const pageCount = computed(() => total.value != null
    ? Math.max(1, Math.ceil(total.value / size))
    : (hasMore.value ? page.value + 1 : page.value))

  let seq = 0
  async function load(silent = false) {
    const my = ++seq
    if (!silent) loading.value = true
    try {
      const r = await fetcher({ limit: size, offset: (page.value - 1) * size }, (opts.filters?.() ?? {}) as F)
      if (my !== seq) return // eski yanit
      error.value = null
      // Silinmeler sonrasi bos kalan sayfadan geri don.
      if (r.items.length === 0 && page.value > 1 && r.total != null) {
        setPage(Math.max(1, Math.ceil(r.total / size)))
        return
      }
      items.value = r.items
      total.value = r.total
      hasMore.value = r.hasMore
    } catch (e) {
      if (my !== seq) return
      error.value = e
    } finally {
      if (my === seq) loading.value = false
    }
  }

  function setPage(n: number) {
    const p = Math.max(1, Math.floor(n))
    if (p === page.value) return
    page.value = p
    const q = { ...route.query }
    if (p > 1) q[key] = String(p); else delete q[key]
    router.replace({ query: q })
    load()
  }

  // Geri/ileri tusu veya dis degisiklik.
  watch(() => route.query[key], () => {
    const p = readPage()
    if (p !== page.value) { page.value = p; load() }
  })

  // Filtre/arama degisimi: sayfa 1'e don, (debounce) yukle. Ilk degerde calismaz.
  if (opts.filters) {
    let timer: ReturnType<typeof setTimeout> | undefined
    watch(() => JSON.stringify(opts.filters!()), () => {
      if (timer) clearTimeout(timer)
      const run = () => {
        if (page.value !== 1) setPage(1); else load()
      }
      if (opts.debounceMs) timer = setTimeout(run, opts.debounceMs); else run()
    })
    onScopeDispose(() => { if (timer) clearTimeout(timer) })
  }

  onScopeDispose(() => { seq++ })
  if (opts.immediate !== false) onMounted(() => load())

  return {
    items, total, hasMore, page, pageCount, loading, error, pageSize: size,
    setPage,
    reload: (silent = false) => load(silent),
  }
}
