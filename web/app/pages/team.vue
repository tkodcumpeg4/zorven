<script setup lang="ts">
import type { TeamMember, TeamOverview, Client } from '~/types/api'

const api = useApi()
const toast = useToast()
const { relativeTime } = useFormat()
const { subscription, currentPlan, openUpgrade, loadBillingData, loading: billingLoading } = useBilling()
const { user: currentUser, platformAdmin } = useAuth()
const { t } = useI18n()

const overview = ref<TeamOverview | null>(null)
const members = ref<TeamMember[]>([])
const pending = ref(true)
const searchQuery = ref('')

// Davet Modalı State
const showInviteModal = ref(false)
const inviteEmail = ref('')
const inviteName = ref('')
const inviteRole = ref<'admin' | 'member'>('member')
const inviting = ref(false)

// Token Yönetimi Modalı State
const activeMember = ref<TeamMember | null>(null)
const showTokenModal = ref(false)
const memberTokens = ref<Client[]>([])
const loadingTokens = ref(false)
const newTokenName = ref('')
const creatingToken = ref(false)
const issuedToken = ref<{ name: string, token: string } | null>(null)
const copiedToken = ref(false)

// Rol Değiştirme
const updatingRole = ref<string | null>(null)

// Üye Silme
const removingMemberId = ref<string | null>(null)

const router = useRouter()
const hasTeamAccess = computed(() => {
  if (platformAdmin.value) return true
  const p = subscription.value?.plan
  return p === 'team' || p === 'enterprise'
})

const effectiveMaxMembers = computed(() => {
  if (overview.value?.max_members !== undefined && overview.value?.max_members !== null) {
    return overview.value.max_members
  }
  if (currentPlan.value?.max_members !== undefined && currentPlan.value?.max_members !== null) {
    return currentPlan.value.max_members
  }
  if (subscription.value?.plan === 'team') {
    return 5
  }
  if (subscription.value?.plan === 'pro') {
    return 3
  }
  return null
})

const maxMembersDisplay = computed(() => {
  if (subscription.value?.plan === 'enterprise' || effectiveMaxMembers.value === null) {
    return t('team.unlimited')
  }
  return t('team.limitSuffix', { n: effectiveMaxMembers.value })
})

watch(() => subscription.value?.plan, async (newPlan, oldPlan) => {
  if (newPlan && newPlan !== oldPlan) {
    if (hasTeamAccess.value) {
      await fetchTeam()
    } else {
      openUpgrade(t('team.upgradeMsg'), 'members')
      router.replace('/')
    }
  }
})

onMounted(async () => {
  await loadBillingData()
  if (!hasTeamAccess.value) {
    // Teame erisemeyen model: hic sayfa acilmasin, direkt pop up ile uyarsin ve ana sayfaya yonlendirsin
    openUpgrade(t('team.upgradeMsg'), 'members')
    router.replace('/')
    return
  }
  await fetchTeam()
})

async function fetchTeam() {
  pending.value = true
  try {
    const data = await api.listTeamMembers()
    overview.value = data
    members.value = data.members || []
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('team.teamLoadFailed'))
  } finally {
    pending.value = false
  }
}

const filteredMembers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return members.value
  return members.value.filter(m =>
    m.name.toLowerCase().includes(q) ||
    m.email.toLowerCase().includes(q) ||
    m.role.toLowerCase().includes(q)
  )
})

const totalTokensCount = computed(() => {
  return members.value.reduce((acc, m) => acc + (m.tokens_count || 0), 0)
})

// --- Davet Islemleri ---

async function handleInvite() {
  if (!inviteEmail.value.trim() || !inviteEmail.value.includes('@')) {
    toast.warn(t('team.warnValidEmail'))
    return
  }

  inviting.value = true
  try {
    const newMem = await api.inviteTeamMember(
      inviteEmail.value.trim(),
      inviteName.value.trim() || undefined,
      inviteRole.value
    )
    members.value.push(newMem)
    if (overview.value) {
      overview.value.count++
    }
    toast.success(t('team.memberAdded', { name: newMem.name || newMem.email }))
    showInviteModal.value = false
    inviteEmail.value = ''
    inviteName.value = ''
    inviteRole.value = 'member'
    await loadBillingData()
  } catch (err: any) {
    const code = err?.data?.error?.code
    const msg = err?.data?.error?.message || err?.message || t('team.inviteFailed')
    if (code === 'member_limit_reached') {
      openUpgrade(msg, 'members')
    } else {
      toast.error(msg)
    }
  } finally {
    inviting.value = false
  }
}

// --- Rol Degistirme ---

async function handleRoleChange(member: TeamMember, newRole: string) {
  if (member.role === newRole) return
  updatingRole.value = member.id
  try {
    await api.updateMemberRole(member.id, newRole)
    member.role = newRole
    toast.success(t('team.roleUpdated', { name: member.name || member.email }))
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('team.roleUpdateFailed'))
  } finally {
    updatingRole.value = null
  }
}

// --- Uye Cikarma ---

async function handleRemoveMember(member: TeamMember) {
  if (member.role === 'owner') {
    toast.warn(t('team.ownerCantRemove'))
    return
  }
  if (!confirm(t('team.removeConfirm', { name: member.name || member.email }))) {
    return
  }

  removingMemberId.value = member.id
  try {
    await api.removeTeamMember(member.id)
    members.value = members.value.filter(m => m.id !== member.id)
    if (overview.value) {
      overview.value.count--
    }
    toast.success(t('team.memberRemoved'))
    await loadBillingData()
  } catch (err: any) {
    toast.error(err?.data?.error?.message || err?.message || t('team.removeFailed'))
  } finally {
    removingMemberId.value = null
  }
}

// --- Uye Token Yonetimi ---

async function openMemberTokens(member: TeamMember) {
  activeMember.value = member
  showTokenModal.value = true
  issuedToken.value = null
  newTokenName.value = ''
  copiedToken.value = false
  await fetchMemberTokens(member.id)
}

async function fetchMemberTokens(memberId: string) {
  loadingTokens.value = true
  try {
    memberTokens.value = await api.listMemberTokens(memberId)
  } catch (err: any) {
    toast.error(t('team.tokensLoadFailed'))
  } finally {
    loadingTokens.value = false
  }
}

async function handleCreateMemberToken() {
  if (!activeMember.value) return
  const name = newTokenName.value.trim() || `${activeMember.value.name} ${t('team.memberClientSuffix')}`
  creatingToken.value = true
  try {
    const res = await api.createMemberToken(activeMember.value.id, name)
    memberTokens.value.unshift(res.client)
    issuedToken.value = { name: res.client.name, token: res.token }
    newTokenName.value = ''
    copiedToken.value = false
    activeMember.value.tokens_count = (activeMember.value.tokens_count || 0) + 1
    toast.success(t('team.memberTokenIssued'))
    await loadBillingData()
  } catch (err: any) {
    const code = err?.data?.error?.code
    const msg = err?.data?.error?.message || err?.message || t('team.tokenCreateFailed')
    if (code === 'plan_limit_reached') {
      openUpgrade(msg, 'clients')
    } else {
      toast.error(msg)
    }
  } finally {
    creatingToken.value = false
  }
}

async function handleRevokeToken(client: Client) {
  if (!activeMember.value) return
  if (!confirm(t('team.revokeTokenConfirm', { name: client.name }))) {
    return
  }

  try {
    await api.revokeMemberToken(activeMember.value.id, client.id)
    memberTokens.value = memberTokens.value.filter(t => t.id !== client.id)
    if (activeMember.value.tokens_count > 0) {
      activeMember.value.tokens_count--
    }
    toast.success(t('team.tokenRevoked'))
  } catch (err: any) {
    toast.error(t('team.tokenRevokeFailed'))
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  copiedToken.value = true
  toast.success(t('team.copied'))
  setTimeout(() => { copiedToken.value = false }, 2000)
}

function getInitials(name: string, email: string) {
  const src = name || email || '?'
  const parts = src.split(' ').filter(Boolean)
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase()
  }
  return src.slice(0, 2).toUpperCase()
}
</script>

<template>
  <div v-if="hasTeamAccess" class="space-y-6">
    <!-- Sayfa Basligi -->
    <div class="mb-6 flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
      <div>
        <h1 class="text-xl font-bold text-fg sm:text-2xl flex items-center gap-2.5">
          <Icon name="lucide:users" class="size-6 text-accent" />
          {{ t('team.title') }}
        </h1>
        <p class="mt-1 text-xs text-fg-muted sm:text-sm">{{ t('team.subtitle') }}</p>
      </div>

      <div class="flex items-center gap-2.5">
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-line bg-surface px-3 py-2 text-xs font-semibold text-fg-muted transition-all duration-150 hover:bg-surface-2 hover:text-fg active:scale-95 shadow-sm"
          :title="t('team.refreshTitle')"
          :disabled="pending"
          @click="fetchTeam"
        >
          <Icon name="lucide:refresh-cw" class="size-3.5" :class="{ 'animate-spin': pending }" />
          <span class="hidden sm:inline">{{ t('common.refresh') }}</span>
        </button>

        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-semibold text-on-accent transition-all duration-150 hover:opacity-90 active:scale-95 shadow-sm"
          @click="showInviteModal = true"
        >
          <Icon name="lucide:user-plus" class="size-4" />
          <span>{{ t('team.inviteBtn') }}</span>
        </button>
      </div>
    </div>

    <!-- Ozet Istatistik Kartlari -->
    <div class="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
      <div class="card-interactive rounded-xl border border-line bg-surface p-4">
        <div class="label-sys mb-1 flex items-center gap-1.5">
          <Icon name="lucide:users" class="size-3.5 text-accent" />
          <span>{{ t('team.totalMembers') }}</span>
        </div>
        <div class="flex items-baseline gap-2">
          <span class="font-mono text-2xl font-bold text-fg">{{ overview?.count ?? members.length }}</span>
          <span class="text-xs font-semibold text-fg-muted">
            / {{ maxMembersDisplay }}
          </span>
        </div>
      </div>

      <div class="card-interactive rounded-xl border border-line bg-surface p-4">
        <div class="label-sys mb-1 flex items-center gap-1.5">
          <Icon name="lucide:key" class="size-3.5 text-accent" />
          <span>{{ t('team.memberTokens') }}</span>
        </div>
        <div class="font-mono text-2xl font-bold text-fg">{{ totalTokensCount }}</div>
      </div>

      <div class="card-interactive rounded-xl border border-line bg-surface p-4">
        <div class="label-sys mb-1 flex items-center gap-1.5">
          <Icon name="lucide:shield-check" class="size-3.5 text-accent" />
          <span>{{ t('team.currentPlan') }}</span>
        </div>
        <div class="flex items-center gap-2 mt-1">
          <span class="rounded bg-accent/15 border border-accent/30 px-2 py-0.5 font-mono text-xs font-bold capitalize text-accent">
            {{ currentPlan?.name || subscription?.plan || 'Free' }}
          </span>
        </div>
      </div>

      <div class="card-interactive rounded-xl border border-line bg-surface p-4">
        <div class="label-sys mb-1 flex items-center gap-1.5">
          <Icon name="lucide:sparkles" class="size-3.5 text-accent" />
          <span>{{ t('team.quotaStatus') }}</span>
        </div>
        <div class="mt-1 flex items-center gap-1.5">
          <span class="size-2 rounded-full" :class="overview?.can_add ? 'bg-accent pulse-glow' : 'bg-warn'" />
          <span class="text-xs font-semibold" :class="overview?.can_add ? 'text-accent' : 'text-warn'">
            {{ overview?.can_add ? t('team.quotaAvailable') : t('team.quotaFull') }}
          </span>
        </div>
      </div>
    </div>

    <!-- Filtre & Arama -->
    <div class="mb-4 flex items-center justify-between gap-4">
      <div class="relative w-full max-w-xs">
        <Icon name="lucide:search" class="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-fg-muted" />
        <input
          v-model="searchQuery"
          type="text"
          :placeholder="t('team.searchPlaceholder')"
          class="w-full rounded-lg border border-line bg-surface py-1.5 pl-9 pr-3 text-xs text-fg placeholder:text-fg-muted/70 focus:border-accent/50 focus:outline-none transition-colors"
        />
      </div>

      <div class="text-xs font-medium text-fg-muted">{{ t('team.membersListed', { n: filteredMembers.length }) }}</div>
    </div>

    <!-- Uyeler Tablosu / Kartlari -->
    <div class="rounded-xl border border-line bg-surface overflow-hidden shadow-sm">
      <div v-if="pending" class="p-8 text-center text-xs text-fg-muted">
        <Icon name="lucide:loader-2" class="size-5 animate-spin mx-auto mb-2 text-accent" />{{ t('team.loadingMembers') }}</div>

      <div v-else-if="filteredMembers.length === 0" class="p-8 text-center text-xs text-fg-muted">{{ t('team.noMatch') }}</div>

      <div v-else class="overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead class="border-b border-line bg-surface-2/90 text-fg-muted uppercase tracking-wider font-mono text-[11px] font-bold">
            <tr>
              <th class="px-4 py-3">{{ t('team.colMember') }}</th>
              <th class="px-4 py-3">{{ t('team.colRole') }}</th>
              <th class="px-4 py-3">{{ t('team.colTokens') }}</th>
              <th class="px-4 py-3">{{ t('team.colJoined') }}</th>
              <th class="px-4 py-3 text-right">{{ t('team.colActions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr
              v-for="member in filteredMembers"
              :key="member.id"
              class="transition-colors hover:bg-surface-2/60"
            >
              <!-- Uye Bilgisi (Avatar, Isim, Email) -->
              <td class="px-4 py-3">
                <div class="flex items-center gap-3">
                  <div class="grid size-9 shrink-0 place-items-center rounded-full bg-accent/15 border border-accent/40 font-mono text-xs font-bold text-accent">
                    {{ getInitials(member.name, member.email) }}
                  </div>
                  <div>
                    <div class="font-semibold text-fg flex items-center gap-1.5">
                      <span>{{ member.name || member.email }}</span>
                      <span v-if="member.status === 'pending'" class="inline-flex items-center gap-0.5 rounded bg-warn/15 border border-warn/30 px-1.5 py-0.2 text-[10px] font-mono font-bold text-warn">
                        <Icon name="lucide:clock" class="size-2.5" />
                        {{ t('team.pendingBadge') }}
                      </span>
                      <span v-else-if="currentUser?.id === member.user_id" class="rounded bg-accent/15 border border-accent/30 px-1.5 py-0.2 text-[10px] font-mono font-bold text-accent">
                        {{ t('team.you') }}
                      </span>
                    </div>
                    <div class="text-[11px] text-fg-muted font-mono font-medium">{{ member.email }}</div>
                  </div>
                </div>
              </td>

              <!-- Rol -->
              <td class="px-4 py-3">
                <div
                  v-if="member.role === 'owner'"
                  class="inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-semibold
                         border-amber-300 bg-amber-50 text-amber-800
                         dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-400"
                >
                  <Icon name="lucide:crown" class="size-3 text-amber-600 dark:text-amber-400" />
                  <span>{{ t('team.ownerRole') }}</span>
                </div>
                <!-- Bekleyen davet: rol henuz degistirilemez (uyelik olusmadi) -->
                <span
                  v-else-if="member.status === 'pending'"
                  class="inline-flex items-center rounded-md border border-line bg-surface-2 px-2.5 py-1 font-mono text-[11px] font-semibold text-fg-muted"
                >
                  {{ member.role === 'admin' ? t('team.roleAdmin') : t('team.roleMember') }}
                </span>
                <div v-else class="relative inline-block">
                  <select
                    :value="member.role"
                    class="cursor-pointer appearance-none rounded-md border border-line bg-surface px-2.5 py-1 pr-6 font-mono text-[11px] font-semibold text-fg hover:border-accent/40 focus:outline-none transition-colors"
                    :disabled="updatingRole === member.id"
                    @change="handleRoleChange(member, ($event.target as HTMLSelectElement).value)"
                  >
                    <option value="member">{{ t('team.roleMember') }}</option>
                    <option value="admin">{{ t('team.roleAdmin') }}</option>
                  </select>
                  <Icon name="lucide:chevron-down" class="pointer-events-none absolute right-1.5 top-1/2 size-3 -translate-y-1/2 text-fg-muted" />
                </div>
              </td>

              <!-- Ozel Jetonlar (bekleyen davette jeton yonetimi yok) -->
              <td class="px-4 py-3">
                <span v-if="member.status === 'pending'" class="font-mono text-[11px] text-fg-subtle">—</span>
                <button
                  v-else
                  type="button"
                  class="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-line bg-surface px-2.5 py-1 text-[11px] font-semibold text-fg hover:border-accent/40 hover:bg-surface-2 transition-all active:scale-95 shadow-sm"
                  @click="openMemberTokens(member)"
                >
                  <Icon name="lucide:key" class="size-3 text-accent" />
                  <span class="font-mono">{{ t('team.tokensBadge', { n: member.tokens_count || 0 }) }}</span>
                </button>
              </td>

              <!-- Katilma Tarihi -->
              <td class="px-4 py-3 font-mono text-[11px] text-fg-muted font-medium">
                {{ relativeTime(member.created_at) }}
              </td>

              <!-- Islemler -->
              <td class="px-4 py-3 text-right">
                <div class="flex items-center justify-end gap-1.5">
                  <button
                    v-if="member.status !== 'pending'"
                    type="button"
                    class="cursor-pointer rounded-lg border border-line p-1.5 text-fg-muted transition-colors hover:border-accent/40 hover:bg-surface-2 hover:text-accent"
                    :title="t('team.manageTokensTitle')"
                    @click="openMemberTokens(member)"
                  >
                    <Icon name="lucide:key" class="size-3.5" />
                  </button>

                  <button
                    v-if="member.role !== 'owner' && currentUser?.id !== member.user_id"
                    type="button"
                    class="cursor-pointer rounded-lg border border-line p-1.5 text-fg-muted transition-colors hover:border-danger/40 hover:bg-surface-2 hover:text-danger"
                    :disabled="removingMemberId === member.id"
                    :title="member.status === 'pending' ? t('team.cancelInvite') : t('team.removeMemberTitle')"
                    @click="handleRemoveMember(member)"
                  >
                    <Icon v-if="removingMemberId === member.id" name="lucide:loader-2" class="size-3.5 animate-spin" />
                    <Icon v-else name="lucide:user-minus" class="size-3.5" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Modal: Yeni Uye Davet Et -->
    <div
      v-if="showInviteModal"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showInviteModal = false"
    >
      <div class="animate-modal w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-2xl">
        <div class="flex items-center justify-between pb-3 border-b border-line">
          <h3 class="text-sm font-semibold text-fg flex items-center gap-2">
            <Icon name="lucide:user-plus" class="size-4 text-accent" />
            {{ t('team.modalInviteTitle') }}
          </h3>
          <button
            type="button"
            class="text-fg-subtle hover:text-fg cursor-pointer"
            @click="showInviteModal = false"
          >
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <form class="mt-4 space-y-4" @submit.prevent="handleInvite">
          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('team.emailLabel') }}</label>
            <input
              v-model="inviteEmail"
              type="email"
              required
              :placeholder="t('team.emailPlaceholder')"
              class="w-full rounded-lg border border-line bg-surface-2 px-3 py-2 text-xs text-fg focus:border-accent/50 focus:outline-none transition-colors"
            />
          </div>

          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('team.nameLabel') }}</label>
            <input
              v-model="inviteName"
              type="text"
              :placeholder="t('team.namePlaceholder')"
              class="w-full rounded-lg border border-line bg-surface-2 px-3 py-2 text-xs text-fg focus:border-accent/50 focus:outline-none transition-colors"
            />
          </div>

          <div>
            <label class="block text-xs font-medium text-fg mb-1">{{ t('team.roleLabel') }}</label>
            <div class="grid grid-cols-2 gap-2">
              <label
                class="flex cursor-pointer items-center gap-2 rounded-lg border p-2.5 text-xs transition-colors"
                :class="inviteRole === 'member' ? 'border-accent/50 bg-accent/10 text-accent font-medium' : 'border-line bg-surface-2 text-fg-muted'"
              >
                <input v-model="inviteRole" type="radio" value="member" class="sr-only" />
                <Icon name="lucide:user" class="size-3.5" />
                <span>{{ t('team.roleMemberOption') }}</span>
              </label>

              <label
                class="flex cursor-pointer items-center gap-2 rounded-lg border p-2.5 text-xs transition-colors"
                :class="inviteRole === 'admin' ? 'border-accent/50 bg-accent/10 text-accent font-medium' : 'border-line bg-surface-2 text-fg-muted'"
              >
                <input v-model="inviteRole" type="radio" value="admin" class="sr-only" />
                <Icon name="lucide:shield" class="size-3.5" />
                <span>{{ t('team.roleAdminOption') }}</span>
              </label>
            </div>
          </div>

          <div class="flex justify-end gap-2 pt-2 border-t border-line">
            <button
              type="button"
              class="cursor-pointer rounded-lg border border-line px-3.5 py-1.5 text-xs text-fg-muted hover:bg-surface-2"
              @click="showInviteModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-4 py-1.5 text-xs font-semibold text-on-accent hover:opacity-90 active:scale-95 disabled:opacity-50"
              :disabled="inviting"
            >
              <Icon v-if="inviting" name="lucide:loader-2" class="size-3.5 animate-spin" />
              <span>{{ inviting ? t('team.inviting') : t('team.invite') }}</span>
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- Modal / Drawer: Uye Jetonlari (Per-Member Tokens) -->
    <div
      v-if="showTokenModal && activeMember"
      class="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4 backdrop-blur-sm"
      @click.self="showTokenModal = false"
    >
      <div class="animate-modal w-full max-w-xl rounded-xl border border-line bg-surface p-5 shadow-2xl max-h-[90vh] flex flex-col">
        <!-- Baslik -->
        <div class="flex items-center justify-between pb-3 border-b border-line shrink-0">
          <div>
            <h3 class="text-sm font-semibold text-fg flex items-center gap-2">
              <Icon name="lucide:key" class="size-4 text-accent" />
              {{ activeMember.name || activeMember.email }} — {{ t('team.clientTokensTitle') }}
            </h3>
            <p class="text-[11px] text-fg-subtle mt-0.5 font-mono">{{ t('team.memberKeysSubtitle') }}</p>
          </div>
          <button
            type="button"
            class="text-fg-subtle hover:text-fg cursor-pointer"
            @click="showTokenModal = false"
          >
            <Icon name="lucide:x" class="size-4" />
          </button>
        </div>

        <div class="overflow-y-auto space-y-4 py-4 flex-1">
          <!-- Yeni Uretilen Token Uyarisi -->
          <div v-if="issuedToken" class="rounded-xl border border-accent/40 bg-accent/10 p-4">
            <div class="flex items-center justify-between mb-2">
              <span class="text-xs font-semibold text-accent flex items-center gap-1.5">
                <Icon name="lucide:check-circle-2" class="size-4 text-accent" />{{ t('team.tokenIssued', { name: issuedToken.name }) }}</span>
              <span class="text-[10px] text-fg-subtle">{{ t('team.oneTimeDisplay') }}</span>
            </div>

            <div class="relative flex items-center rounded-lg border border-accent/30 bg-bg p-2.5 font-mono text-xs text-accent">
              <span class="truncate select-all pr-12">{{ issuedToken.token }}</span>
              <button
                type="button"
                class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded bg-accent px-2.5 py-1 text-[11px] font-semibold text-on-accent transition active:scale-95 cursor-pointer"
                @click="copyToClipboard(issuedToken.token)"
              >
                {{ copiedToken ? t('common.copied') : t('common.copy') }}
              </button>
            </div>

            <div class="mt-2.5 rounded border border-line bg-surface/80 p-2 text-[11px] text-fg-muted font-mono">
              <div class="text-[10px] text-fg-subtle uppercase tracking-wider mb-1">{{ t('team.cliLoginCmd') }}</div>
              <code>zorven login --token {{ issuedToken.token }}</code>
            </div>
          </div>

          <!-- Yeni Jeton Uret Formu -->
          <div class="rounded-lg border border-line bg-surface-2/40 p-3.5">
            <div class="text-xs font-semibold text-fg mb-2 flex items-center gap-1.5">
              <Icon name="lucide:plus-circle" class="size-3.5 text-accent" />
              <span>{{ t('team.createTokenForMember') }}</span>
            </div>
            <form class="flex gap-2" @submit.prevent="handleCreateMemberToken">
              <input
                v-model="newTokenName"
                type="text"
                :placeholder="t('team.clientNamePlaceholder')"
                class="flex-1 rounded-lg border border-line bg-surface px-3 py-1.5 text-xs text-fg focus:border-accent/50 focus:outline-none transition-colors"
              />
              <button
                type="submit"
                class="flex cursor-pointer items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-on-accent hover:opacity-90 active:scale-95 disabled:opacity-50"
                :disabled="creatingToken"
              >
                <Icon v-if="creatingToken" name="lucide:loader-2" class="size-3.5 animate-spin" />
                <span>{{ creatingToken ? t('team.generating') : t('team.generateToken') }}</span>
              </button>
            </form>
          </div>

          <!-- Mevcut Jetonlar Listesi -->
          <div>
            <div class="label-sys mb-2 flex items-center justify-between">
              <span>{{ t('team.definedClients', { n: memberTokens.length }) }}</span>
            </div>

            <div v-if="loadingTokens" class="p-4 text-center text-xs text-fg-subtle">
              <Icon name="lucide:loader-2" class="size-4 animate-spin mx-auto mb-1 text-accent" />{{ t('team.loadingTokens') }}</div>

            <div v-else-if="memberTokens.length === 0" class="rounded-lg border border-dashed border-line p-6 text-center text-xs text-fg-subtle">{{ t('team.noMemberTokens') }}</div>

            <div v-else class="space-y-2">
              <div
                v-for="tokenItem in memberTokens"
                :key="tokenItem.id"
                class="flex items-center justify-between rounded-lg border border-line bg-surface p-3 transition-colors hover:border-line-hover"
              >
                <div class="flex items-center gap-3">
                  <div class="grid size-8 place-items-center rounded-lg bg-surface-2 border border-line text-accent">
                    <Icon name="lucide:terminal" class="size-4" />
                  </div>
                  <div>
                    <div class="text-xs font-semibold text-fg flex items-center gap-2">
                      <span>{{ tokenItem.name }}</span>
                      <StatusPill :status="tokenItem.status" />
                    </div>
                    <div class="text-[10px] text-fg-subtle font-mono mt-0.5">
                      ID: {{ tokenItem.id }} • {{ relativeTime(tokenItem.created_at) }}
                    </div>
                  </div>
                </div>

                <div class="flex items-center gap-2">
                  <button
                    type="button"
                    class="cursor-pointer rounded-lg border border-line px-2.5 py-1 text-[11px] text-fg-muted hover:border-danger/40 hover:text-danger hover:bg-surface-2 transition-colors"
                    :title="t('team.revokeTokenTitle')"
                    @click="handleRevokeToken(tokenItem)"
                  >
                    <span>{{ t('team.revoke') }}</span>
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="pt-3 border-t border-line flex justify-end shrink-0">
          <button
            type="button"
            class="cursor-pointer rounded-lg border border-line px-4 py-1.5 text-xs text-fg-muted hover:bg-surface-2"
            @click="showTokenModal = false"
          >
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>
  </div>
  <div v-else-if="billingLoading" class="flex min-h-[50vh] flex-col items-center justify-center gap-3">
    <Icon name="lucide:loader-2" class="size-6 animate-spin text-accent" />
    <span class="text-xs text-fg-subtle font-mono">{{ t('team.checkingAccess') }}</span>
  </div>
  <div v-else class="flex min-h-[50vh] flex-col items-center justify-center gap-3">
    <!-- Non-team: UpgradeModal will pop up and router redirects to / -->
  </div>
</template>
