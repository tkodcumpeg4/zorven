<script setup lang="ts">
import type { Plan } from '~/types/api'

const {
  showUpgradeModal,
  closeUpgrade,
  upgradeReason,
  plans,
  subscription,
  currentPlan,
  formatBytes,
  formatCurrency,
  loadBillingData,
} = useBilling()

const { platformAdmin, tenant } = useAuth()
const api = useApi()
const toast = useToast()

const { t } = useI18n()
const updatingPlan = ref<string | null>(null)

function contactEnterprise() {
  window.open(
    'mailto:sales@zorven.app?subject=Zorven%20Enterprise%20Plan%20Talebi&body=Merhaba%2C%20Zorven%20Enterprise%20plan%C4%B1%20hakk%C4%B1nda%20bilgi%20ve%20teklif%20almak%20istiyorum.',
    '_blank',
  )
  toast.info(
    t('billing.enterpriseToast'),
    t('billing.contactUs'),
    7000,
  )
}

async function selectPlan(plan: Plan) {
  if (subscription.value?.plan === plan.id) {
    return
  }

  if (plan.id === 'enterprise') {
    contactEnterprise()
    return
  }

  // Eger platform admin ise veritabaninda plani aninda degistirebilir
  if (platformAdmin.value && tenant.value?.id) {
    updatingPlan.value = plan.id
    try {
      await api.adminUpdateTenantPlan(tenant.value.id, plan.id)
      toast.success(t('billing.planUpdated', { name: plan.name }))
      await loadBillingData()
      closeUpgrade()
    } catch (err: any) {
      toast.error(err?.data?.error?.message || err?.message || t('billing.planUpdateFailed'))
    } finally {
      updatingPlan.value = null
    }
    return
  }

  // Normal kullanici icin Faz 4 Checkout uyarisi
  toast.info(
    t('billing.checkoutToast', { name: plan.name }),
    t('billing.checkoutToastTitle'),
    7000,
  )
}
</script>

<template>
  <div
    v-if="showUpgradeModal"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm overflow-y-auto"
    @click.self="closeUpgrade"
  >
    <div
      class="relative my-8 w-full max-w-5xl rounded-2xl border border-line bg-surface p-6 shadow-2xl transition-all"
    >
      <!-- Header -->
      <div class="flex items-start justify-between pb-4 border-b border-line">
        <div>
          <div class="flex items-center gap-2">
            <span class="grid size-7 place-items-center rounded-lg bg-accent/20 text-accent">
              <Icon name="lucide:sparkles" class="size-4" />
            </span>
            <h2 class="text-lg font-semibold tracking-tight text-fg">
              {{ t('upgrade.title') }}
            </h2>
          </div>
          <p class="mt-1 text-xs text-fg-muted">{{ t('upgrade.subtitle') }}</p>

          <!-- Tetiklenme sebebi bildirimi -->
          <div
            v-if="upgradeReason"
            class="mt-3 flex items-center gap-2 rounded-lg border border-accent/30 bg-accent/10 px-3 py-1.5 text-xs text-accent"
          >
            <Icon name="lucide:info" class="size-3.5 shrink-0" />
            <span>{{ upgradeReason }}</span>
          </div>
        </div>

        <button
          type="button"
          class="cursor-pointer rounded-lg p-1.5 text-fg-muted transition-colors hover:bg-surface-2 hover:text-fg"
          @click="closeUpgrade"
        >
          <Icon name="lucide:x" class="size-5" />
        </button>
      </div>

      <!-- Plan Kartları Grid'i -->
      <div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
        <div
          v-for="plan in plans"
          :key="plan.id"
          class="relative flex flex-col justify-between rounded-xl border p-4 transition-all duration-150"
          :class="[
            subscription?.plan === plan.id
              ? 'border-accent bg-accent/5 shadow-lg shadow-accent/5 ring-1 ring-accent/30'
              : plan.id === 'pro'
                ? 'border-purple-500/40 bg-purple-950/10 hover:border-purple-500/60'
                : 'border-line bg-bg/60 hover:border-line-hover hover:bg-surface-2/30'
          ]"
        >
          <!-- Rozetler -->
          <div class="absolute -top-2.5 right-3">
            <span
              v-if="subscription?.plan === plan.id"
              class="rounded-full bg-accent px-2 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wider text-on-accent"
            >
              {{ t('upgrade.currentPlan') }}
            </span>
            <span
              v-else-if="plan.id === 'pro'"
              class="rounded-full bg-purple-600 px-2 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wider text-white shadow-sm"
            >
              {{ t('upgrade.mostPopular') }}
            </span>
          </div>

          <div>
            <!-- Başlık & Fiyat -->
            <div class="space-y-1">
              <h3 class="text-sm font-semibold text-fg">{{ plan.name }}</h3>
              <div class="flex items-baseline gap-1">
                <span class="text-2xl font-bold tracking-tight text-fg">
                  {{ formatCurrency(plan.price_monthly, plan.id) }}
                </span>
              </div>
            </div>

            <!-- Özellikler Listesi -->
            <ul class="mt-4 space-y-2 border-t border-line/50 pt-4 text-xs text-fg-muted">
              <li class="flex items-center gap-2">
                <Icon name="lucide:users" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.max_members ? plan.max_members + ' ' + t('upgrade.person') : (plan.id === 'free' || plan.id === 'hobby' ? t('upgrade.onePerson') : t('upgrade.unlimited')) }}</strong> {{ t('upgrade.teamMember') }}
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:monitor-smartphone" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.max_clients ?? t('upgrade.unlimited') }}</strong> {{ t('upgrade.client') }}
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:route" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.max_tunnels ?? t('upgrade.unlimited') }}</strong> {{ t('upgrade.tunnel') }}
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:globe" class="size-3.5 shrink-0" :class="plan.max_custom_domains !== 0 ? 'text-accent' : 'text-fg-subtle'" />
                <span :class="{ 'opacity-50 line-through': plan.max_custom_domains === 0 }">
                  <strong class="font-mono text-fg">{{ plan.max_custom_domains ?? t('upgrade.unlimited') }}</strong> {{ t('upgrade.customDomain') }}
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:arrow-up-down" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ formatBytes(plan.bandwidth_limit_bytes) }}</strong> {{ t('upgrade.quotaMonth') }}
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:gauge" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.bandwidth_normal_mbps }}</strong> {{ t('upgrade.speed') }}
                  <span class="text-[10px] text-fg-subtle">({{ plan.bandwidth_throttled_mbps }} {{ t('upgrade.throttleShort') }})</span>
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:monitor" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.max_screen_streams ?? t('upgrade.unlimited') }}</strong> {{ t('upgrade.screen') }}
                  <span class="text-[10px] text-fg-subtle">({{ plan.screen_max_fps }} fps)</span>
                </span>
              </li>

              <li class="flex items-center gap-2">
                <Icon name="lucide:clock" class="size-3.5 shrink-0 text-accent" />
                <span>
                  <strong class="font-mono text-fg">{{ plan.log_retention_days }}</strong> {{ t('upgrade.logDays') }}
                </span>
              </li>

              <li v-if="plan.has_api_access" class="flex items-center gap-2 text-accent font-medium">
                <Icon name="lucide:code-2" class="size-3.5 shrink-0 text-accent" />
                <span>{{ t('upgrade.apiAccess') }}</span>
              </li>

              <li v-if="plan.has_ip_allowlist" class="flex items-center gap-2 text-accent font-medium">
                <Icon name="lucide:shield" class="size-3.5 shrink-0 text-accent" />
                <span>{{ t('upgrade.ipAllowlist') }}</span>
              </li>
            </ul>
          </div>

          <!-- Seç / Yükselt Butonu -->
          <div class="mt-6 pt-2">
            <button
              type="button"
              :disabled="subscription?.plan === plan.id || updatingPlan === plan.id"
              class="w-full cursor-pointer rounded-lg px-3 py-2 text-center text-xs font-medium transition-all duration-150 disabled:cursor-default"
              :class="[
                subscription?.plan === plan.id
                  ? 'border border-line bg-surface-2/40 text-fg-subtle'
                  : plan.id === 'enterprise'
                    ? 'border border-accent/40 bg-accent/10 text-accent hover:bg-accent hover:text-on-accent'
                    : plan.id === 'pro'
                      ? 'bg-purple-600 text-white shadow hover:bg-purple-500'
                      : 'bg-accent text-on-accent hover:opacity-90'
              ]"
              @click="selectPlan(plan)"
            >
              <template v-if="updatingPlan === plan.id">
                {{ t('upgrade.updating') }}
              </template>
              <template v-else-if="subscription?.plan === plan.id">
                {{ t('upgrade.activePlan') }}
              </template>
              <template v-else-if="plan.id === 'enterprise'">
                {{ t('upgrade.contactUs') }}
              </template>
              <template v-else-if="platformAdmin">
                {{ t('upgrade.switchAdmin') }}
              </template>
              <template v-else>
                {{ t('upgrade.upgrade') }}
              </template>
            </button>
          </div>
        </div>
      </div>

      <!-- Alt Bilgilendirme -->
      <div class="mt-6 flex flex-col sm:flex-row items-center justify-between gap-3 rounded-lg border border-line bg-surface-2/40 p-3 text-xs text-fg-muted">
        <div class="flex items-center gap-2">
          <Icon name="lucide:shield-check" class="size-4 text-accent shrink-0" />
          <span>{{ t('upgrade.footerNote') }}</span>
        </div>
        <div v-if="platformAdmin" class="text-[11px] font-mono text-accent shrink-0">{{ t('upgrade.adminNote') }}</div>
      </div>
    </div>
  </div>
</template>
