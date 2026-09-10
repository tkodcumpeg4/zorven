<script setup lang="ts">
const {
  authed,
  method,
  logout,
  tenantSlug,
  userLogin,
  user,
  tenant,
  organizations,
  platformAdmin,
  switchOrganization,
  createOrganization,
  check,
} = useAuth()

const api = useApi()
const toast = useToast()
const {
  subscription,
  currentPlan,
  openUpgrade,
  loadBillingData,
} = useBilling()

const { isLight, toggleTheme, initTheme } = useTheme()
const { t, locale } = useI18n()

function switchLang() {
  const next = locale.value === 'tr' ? 'en' : 'tr'
  locale.value = next as any
  try { localStorage.setItem('zorven_lang', next) } catch { /* erişim yok */ }
}

onMounted(() => {
  initTheme()
  loadBillingData()
})

const nav = computed(() => {
  const items = [
    { to: '/',         label: t('nav.overview'), icon: 'lucide:layout-dashboard' },
    { to: '/tunnels',  label: t('nav.tunnels'),  icon: 'lucide:route' },
    { to: '/domains',  label: t('nav.domains'),  icon: 'lucide:globe' },
    { to: '/clients',  label: t('nav.clients'),  icon: 'lucide:monitor-smartphone' },
    { to: '/tokens',   label: t('nav.security'), icon: 'lucide:shield-check' },
    { to: '/team',     label: t('nav.team'),     icon: 'lucide:users' },
    { to: '/requests', label: t('nav.logs'),     icon: 'lucide:list' },
    { to: '/mail',     label: t('nav.mail'),     icon: 'lucide:mail' },
  ]
  // Profil her oturum acmis kullanicida gorunur (sayfa, e-posta/sifre disi
  // hesaplar icin uygun bir mesaj gosterir). 2FA yalnizca Better Auth'ta anlamli.
  if (authed.value) {
    items.push({ to: '/profile', label: t('nav.profile'), icon: 'lucide:user-round' })
  }
  if (method.value === 'better-auth') {
    items.push({ to: '/security', label: t('nav.accountSecurity'), icon: 'lucide:shield-check' })
  }
  if (platformAdmin.value) {
    items.push({ to: '/platform', label: t('nav.platform'), icon: 'lucide:server' })
  }
  return items
})

// Platform Admin (süper-admin) her zaman tam erişime sahiptir: plan kapısına takılmaz.
const hasTeamAccess = computed(() => {
  if (platformAdmin.value) return true
  const p = subscription.value?.plan
  return p === 'team' || p === 'enterprise'
})

const hasSecurityAccess = computed(() => {
  if (platformAdmin.value) return true
  const p = subscription.value?.plan
  return p === 'pro' || p === 'team' || p === 'enterprise'
})

function handleNavClick(e: MouseEvent, item: { to: string }) {
  if (item.to === '/team' && !hasTeamAccess.value) {
    e.preventDefault()
    openUpgrade(t('team.upgradeMsg'), 'members')
    return
  }
  if (item.to === '/tokens' && !hasSecurityAccess.value) {
    e.preventDefault()
    openUpgrade(t('tokens.upgradeMsg'), 'api_access')
    return
  }
}

const showOrgMenu = ref(false)
const showNewOrgModal = ref(false)
const newOrgName = ref('')
const newOrgSlug = ref('')
const orgError = ref('')
const creatingOrg = ref(false)

async function handleSwitchOrg(id: string) {
  showOrgMenu.value = false
  try {
    if (platformAdmin.value) {
      await api.adminSwitchTenant(id)
    } else {
      await switchOrganization(id)
    }
    toast.success(t('shell.orgSwitched'))
    await check()
    window.location.reload()
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('shell.orgSwitchFailed'))
  }
}

async function handleCreateOrg() {
  if (!newOrgName.value.trim() || !newOrgSlug.value.trim()) {
    orgError.value = t('shell.errNameSlug')
    toast.warn(orgError.value)
    return
  }
  creatingOrg.value = true
  orgError.value = ''
  try {
    await createOrganization(newOrgName.value.trim(), newOrgSlug.value.trim().toLowerCase())
    showNewOrgModal.value = false
    newOrgName.value = ''
    newOrgSlug.value = ''
    toast.success(t('shell.orgCreated'))
    window.location.reload()
  } catch (err: any) {
    orgError.value = err?.data?.error?.message || err?.message || t('shell.orgCreateFailed')
    toast.error(orgError.value)
  } finally {
    creatingOrg.value = false
  }
}
</script>

<template>
  <div class="relative min-h-screen">
    <StarField />

    <!-- Üst bar -->
    <header class="sticky top-0 z-20 border-b border-line bg-bg/80 backdrop-blur">
      <div class="mx-auto flex max-w-6xl items-center justify-between gap-4 px-5 py-3">
        <div class="flex items-center gap-4">
          <NuxtLink to="/" class="flex cursor-pointer items-center gap-2.5">
            <Logo class="size-7 shrink-0" />
            <span class="font-mono text-base font-bold tracking-tight text-fg">zorven</span>
          </NuxtLink>

          <!-- Better Auth Organizasyon / Kiraci Secici -->
          <div v-if="authed && method === 'better-auth'" class="relative">
            <button
              type="button"
              class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-surface/60 px-2.5 py-1 text-xs text-fg transition-colors hover:border-accent/40 hover:bg-surface"
              @click="showOrgMenu = !showOrgMenu"
            >
              <Icon name="lucide:building-2" class="size-3.5 text-accent" />
              <span class="font-mono font-medium">{{ tenantSlug || t('shell.orgDefault') }}</span>
              <Icon name="lucide:chevron-down" class="size-3 text-fg-subtle transition-transform" :class="{ 'rotate-180': showOrgMenu }" />
            </button>

            <!-- Dropdown Menu -->
            <div
              v-if="showOrgMenu"
              class="absolute left-0 top-full mt-1.5 w-60 rounded-lg border border-line bg-surface p-1.5 shadow-xl backdrop-blur-md z-30"
            >
              <div class="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-fg-subtle">
                {{ t('shell.orgsHeader') }}
              </div>
              <div class="max-h-48 overflow-y-auto space-y-0.5">
                <button
                  v-for="org in organizations"
                  :key="org.id"
                  type="button"
                  class="flex w-full cursor-pointer items-center justify-between rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-bg"
                  :class="org.slug === tenantSlug ? 'bg-bg text-accent font-medium' : 'text-fg'"
                  @click="handleSwitchOrg(org.id)"
                >
                  <div class="flex items-center gap-2 truncate">
                    <Icon name="lucide:layers" class="size-3.5 shrink-0 opacity-70" />
                    <span class="truncate">{{ org.name }}</span>
                  </div>
                  <Icon v-if="org.slug === tenantSlug" name="lucide:check" class="size-3.5 text-accent shrink-0" />
                </button>
              </div>

              <div class="mt-1 border-t border-line pt-1">
                <button
                  type="button"
                  class="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs text-fg-muted transition-colors hover:bg-bg hover:text-fg"
                  @click="showOrgMenu = false; showNewOrgModal = true"
                >
                  <Icon name="lucide:plus" class="size-3.5" />
                  <span>{{ t('shell.newOrg') }}</span>
                </button>
              </div>
            </div>
          </div>

          <!-- Admin Key veya Legacy badge -->
          <div v-else-if="authed && tenantSlug" class="hidden sm:inline-flex items-center gap-1 rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-muted">
            <Icon :name="method === 'key' ? 'lucide:shield' : 'lucide:github'" class="size-3" />
            <span>{{ method === 'key' ? t('shell.adminBadge') : tenantSlug }}</span>
          </div>
        </div>

        <!-- Sag Kisim: Kullanici Profili & Cikis -->
        <div class="flex items-center gap-2">
          <!-- Plan Rozeti (Tiklanabilir -> UpgradeModal) -->
          <button
            v-if="authed"
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs transition-colors font-medium"
            :class="[
              subscription?.plan === 'pro'
                ? 'border-purple-300 bg-purple-100 text-purple-800 hover:bg-purple-200 dark:border-purple-500/40 dark:bg-purple-950/30 dark:text-purple-300 dark:hover:border-purple-400'
                : subscription?.plan === 'team' || subscription?.plan === 'enterprise'
                  ? 'border-accent/40 bg-accent/10 text-accent hover:border-accent font-semibold'
                  : 'border-line bg-surface text-fg-muted hover:border-line-hover hover:text-fg'
            ]"
            :title="t('shell.planBadgeTitle')"
            @click="openUpgrade()"
          >
            <Icon name="lucide:sparkles" class="size-3.5" :class="subscription?.plan === 'pro' ? 'text-purple-700 dark:text-purple-400' : 'text-accent'" />
            <span class="capitalize">{{ currentPlan?.name || subscription?.plan || 'Free' }}</span>
          </button>

          <!-- Platform Admin Rozeti -->
          <NuxtLink
            v-if="platformAdmin"
            to="/platform"
            class="flex items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 py-1 text-xs font-semibold text-fg transition-all duration-150 hover:border-accent/40 hover:bg-surface-2 active:scale-95 shadow-xs"
            :title="t('shell.platformPanel')"
          >
            <Icon name="lucide:shield-check" class="size-3.5 text-accent" />
            <span>{{ t('header.platformAdmin') }}</span>
          </NuxtLink>

          <div v-if="authed && user" class="hidden sm:flex items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 py-1 text-xs text-fg-muted">
            <Icon name="lucide:user" class="size-3.5 text-fg-muted" />
            <span class="max-w-[120px] truncate text-fg font-medium">{{ user.name || user.email }}</span>
            <!-- Rol rozeti: 'owner' herkeste (kisisel org sahibi) cikacagi icin
                 gosterilmez; yalnizca anlamli roller (admin/member) gosterilir. -->
            <span v-if="user.role && user.role !== 'owner'" class="rounded bg-surface-2 border border-line px-1.5 py-0.2 text-[9px] uppercase tracking-wider text-fg-muted font-mono font-semibold">
              {{ user.role }}
            </span>
          </div>

          <button
            v-if="authed"
            class="cursor-pointer inline-flex items-center gap-1.5 rounded-lg border border-line px-2.5 py-1.5
                   font-mono text-[11px] text-fg-muted transition-colors duration-150
                   hover:border-danger/40 hover:text-danger"
            :title="t('header.logoutTitle')"
            @click="logout"
          >
            <Icon name="lucide:log-out" class="size-3.5" aria-hidden="true" />
            <span class="hidden sm:inline">{{ t('header.logout') }}</span>
          </button>

          <!-- Dil Değiştirici (TR / EN) -->
          <button
            type="button"
            class="grid h-8 min-w-8 cursor-pointer place-items-center rounded-lg border border-line px-2
                   font-mono text-[11px] font-semibold text-fg-muted transition-all duration-200 hover:bg-surface hover:text-fg hover:border-accent/40 active:scale-95"
            :title="locale === 'tr' ? 'English' : 'Türkçe'"
            :aria-label="locale === 'tr' ? 'English' : 'Türkçe'"
            @click="switchLang"
          >
            {{ locale === 'tr' ? 'EN' : 'TR' }}
          </button>

          <!-- Tema Degistirici (Aydinlik / Koyu) -->
          <button
            type="button"
            class="grid size-8 cursor-pointer place-items-center rounded-lg border border-line
                   text-fg-muted transition-all duration-200 hover:bg-surface hover:text-fg hover:border-accent/40 active:scale-95"
            :title="isLight ? t('header.toDarkMode') : t('header.toLightMode')"
            :aria-label="isLight ? t('header.toDarkMode') : t('header.toLightMode')"
            @click="toggleTheme"
          >
            <Icon
              :name="isLight ? 'lucide:sun' : 'lucide:moon'"
              class="size-4 text-accent transition-transform duration-300"
              :class="{ 'rotate-90': isLight }"
            />
          </button>

          <a
            href="https://github.com/tkodcumpeg4/zorven"
            target="_blank" rel="noopener noreferrer"
            class="grid size-8 cursor-pointer place-items-center rounded-lg border border-line
                   text-fg-muted transition-colors duration-150 hover:bg-surface hover:text-fg"
            :aria-label="t('shell.githubRepo')"
          >
            <Icon name="lucide:github" class="size-4" />
          </a>
        </div>
      </div>

      <!-- Chip navigasyon -->
      <nav class="mx-auto flex max-w-6xl items-center gap-1.5 overflow-x-auto px-5 pb-3 no-scrollbar">
        <NuxtLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="chip flex shrink-0 cursor-pointer items-center gap-1.5 border border-line
                 px-2.5 py-1 text-xs sm:text-[13px] font-medium text-fg-muted hover:bg-surface hover:text-fg transition-all"
          active-class="chip-active !border-accent/40"
          @click="handleNavClick($event, item)"
        >
          <Icon :name="item.icon" class="size-3.5 shrink-0" aria-hidden="true" />
          <span>{{ item.label }}</span>
          <span
            v-if="item.to === '/team' && !hasTeamAccess"
            class="rounded bg-line/80 px-1.5 py-0.2 text-[9px] font-mono text-fg-subtle flex items-center gap-0.5"
            :title="t('shell.teamLock')"
          >
            <Icon name="lucide:lock" class="size-2.5 text-warn" />
            Team
          </span>
          <span
            v-if="item.to === '/tokens' && !hasSecurityAccess"
            class="rounded bg-line/80 px-1.5 py-0.2 text-[9px] font-mono text-fg-subtle flex items-center gap-0.5"
            :title="t('shell.proLock')"
          >
            <Icon name="lucide:lock" class="size-2.5 text-warn" />
            Pro
          </span>
        </NuxtLink>
      </nav>
    </header>

    <!-- Modal: Yeni Organizasyon Olustur -->
    <div
      v-if="showNewOrgModal"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showNewOrgModal = false"
    >
      <div class="w-full max-w-sm rounded-xl border border-line bg-surface p-5 shadow-2xl">
        <div class="flex items-center justify-between pb-3 border-b border-line">
          <h3 class="text-sm font-semibold text-fg">{{ t('shell.modalNewOrg') }}</h3>
          <button type="button" class="text-fg-muted hover:text-fg" @click="showNewOrgModal = false">
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <div v-if="orgError" class="mt-3 rounded border border-danger/30 bg-danger/10 p-2 text-xs text-danger">
          {{ orgError }}
        </div>

        <form class="mt-3 space-y-3" @submit.prevent="handleCreateOrg">
          <div>
            <label class="block text-xs font-medium text-fg-muted mb-1">{{ t('shell.orgName') }}</label>
            <input
              v-model="newOrgName"
              type="text"
              required
              placeholder="ACME Corp"
              class="w-full rounded border border-line bg-bg px-3 py-1.5 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
              @input="newOrgSlug = newOrgName.toLowerCase().replace(/[^a-z0-9_-]/g, '-')"
            />
          </div>

          <div>
            <label class="block text-xs font-medium text-fg-muted mb-1">{{ t('shell.slugLabel') }}</label>
            <input
              v-model="newOrgSlug"
              type="text"
              required
              placeholder="acme-corp"
              class="w-full rounded border border-line bg-bg px-3 py-1.5 font-mono text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
            />
          </div>

          <div class="flex justify-end gap-2 pt-2">
            <button
              type="button"
              class="rounded px-3 py-1.5 text-xs text-fg-muted hover:bg-bg"
              @click="showNewOrgModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="creatingOrg"
              class="rounded bg-accent px-3 py-1.5 text-xs font-medium text-on-accent hover:opacity-90 disabled:opacity-50"
            >
              {{ creatingOrg ? t('shell.creating') : t('common.create') }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- Hız Sınırlandırması Şeridi (Bant Genişliği Aşımı) -->
    <ThrottledBanner />

    <main class="relative z-10 mx-auto max-w-6xl px-5 py-8">
      <AdminKeyGate>
        <slot />
      </AdminKeyGate>
    </main>

    <!-- Global Plan Yükseltme Modalı -->
    <UpgradeModal />
  </div>
</template>
