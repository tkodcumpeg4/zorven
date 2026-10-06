import type { Subscription, TenantUsage, Plan } from '~/types/api'
import { gt } from '~/plugins/i18n'

// ACIK CEKIRDEK (open-core): faturalama/plan yukseltme yoktur. Bu composable,
// panel sayfalarinin kullandigi kaynak sayaclarini (kullanim) saglar; limitler
// sinirsizdir (null), "yukselt" cagrilari no-op'tur.

const subscription = ref<Subscription | null>(null)
const usage = ref<TenantUsage | null>(null)
const plans = ref<Plan[]>([])
const loading = ref(false)
const error = ref('')

export function useBilling() {
  const api = useApi()

  async function loadBillingData() {
    loading.value = true
    error.value = ''
    try {
      const [subRes, plansList] = await Promise.all([
        api.getSubscription(),
        api.listPlans(),
      ])
      subscription.value = subRes.subscription
      usage.value = subRes.usage
      plans.value = plansList
    } catch (err: any) {
      error.value = err?.data?.error?.message || err?.message || String(err)
    } finally {
      loading.value = false
    }
  }

  // Plan yukseltme yok: cagrilari kirmamak icin imza korunur, hicbir sey yapmaz.
  function openUpgrade(_reason = '', _feature = '') {}

  const currentPlan = computed<Plan | null>(() => subscription.value?.plan_details ?? null)

  // Acik surumde her kaynak sinirsizdir.
  const effectiveMaxClients = computed<number | null>(() => null)
  const effectiveBandwidthBytes = computed<number | null>(() => null)
  const isThrottled = computed(() => false)

  function formatBytes(bytes?: number | null): string {
    if (bytes === null || bytes === undefined) return gt('common.unlimited')
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    const val = (bytes / Math.pow(1024, i)).toFixed(i >= 3 ? 1 : 0)
    return `${val} ${units[i]}`
  }

  return {
    subscription,
    usage,
    plans,
    loading,
    error,
    currentPlan,
    effectiveMaxClients,
    effectiveBandwidthBytes,
    isThrottled,
    loadBillingData,
    openUpgrade,
    formatBytes,
  }
}
