import type { Subscription, TenantUsage, Plan } from '~/types/api'
import { gt } from '~/plugins/i18n'

// Global state paylasimi (sayfalar arasi senkron)
const subscription = ref<Subscription | null>(null)
const usage = ref<TenantUsage | null>(null)
const plans = ref<Plan[]>([])
const loading = ref(false)
const error = ref('')

const showUpgradeModal = ref(false)
const upgradeReason = ref('')
const upgradeFeature = ref('')

export function useBilling() {
  const api = useApi()
  const toast = useToast()

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
      error.value = err?.data?.error?.message || err?.message || gt('billing.loadFailed')
    } finally {
      loading.value = false
    }
  }

  // Acik cekirdek (open-core): plan yukseltme yok — self-host sinirsizdir.
  // Cagrilari kirmamak icin imza korunur ama modal acilmaz (no-op).
  function openUpgrade(_reason = '', _feature = '') {
    showUpgradeModal.value = false
  }

  function closeUpgrade() {
    showUpgradeModal.value = false
    upgradeReason.value = ''
    upgradeFeature.value = ''
  }

  const currentPlan = computed<Plan | null>(() => {
    if (subscription.value?.plan_details) {
      return subscription.value.plan_details
    }
    const currentId = subscription.value?.plan || 'free'
    return plans.value.find(p => p.id === currentId) || null
  })

  // Open-core: bant genisligi kisitlama yok.
  const isThrottled = computed(() => false)

  function formatBytes(bytes?: number | null): string {
    if (bytes === null || bytes === undefined) return gt('common.unlimited')
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    const val = (bytes / Math.pow(1024, i)).toFixed(i >= 3 ? 1 : 0)
    return `${val} ${units[i]}`
  }

  function formatCurrency(cents?: number | null, planId?: string): string {
    if (planId && planId.toLowerCase() === 'enterprise') return gt('billing.custom')
    if (cents === null || cents === undefined || cents === 0) return gt('billing.free')
    const dollars = cents / 100
    return `$${dollars.toFixed(0)}/ay`
  }

  function getQuotaPercentage(used?: number, limit?: number | null): number {
    if (limit === null || limit === undefined || limit <= 0) return 0
    if (!used) return 0
    const pct = Math.round((used / limit) * 100)
    return Math.min(pct, 100)
  }

  return {
    subscription,
    usage,
    plans,
    loading,
    error,
    showUpgradeModal,
    upgradeReason,
    upgradeFeature,
    currentPlan,
    isThrottled,
    loadBillingData,
    openUpgrade,
    closeUpgrade,
    formatBytes,
    formatCurrency,
    getQuotaPercentage,
  }
}
