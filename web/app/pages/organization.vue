<script setup lang="ts">
// Organizasyon ve proje yönetimi.
//
// Yetki: yeniden adlandırma ve silme owner/admin içindir. Buradaki gizleme
// yalnızca arayüzü sadeleştirir; asıl kapı sunucuda (projeler: Go API,
// organizasyon adı: Better Auth, organizasyon silme: Go API).
import type { Project } from '~/types/api'

const api = useApi()
const toast = useToast()
const { t } = useI18n()
const {
  user, platformAdmin, organizations, tenantSlug, method,
  switchOrganization, createOrganization, renameOrganization, check,
} = useAuth()
const { activeProject, setActiveProject, clearActiveProject } = useActiveProject()


const canManage = computed(() => {
  if (platformAdmin.value) return true
  const r = (user.value?.role || '').toLowerCase()
  return r === 'owner' || r === 'admin'
})
const isBetterAuth = computed(() => method.value === 'better-auth')

function errMsg(e: any, fallback: string): string {
  return e?.data?.error?.message || e?.data?.error || e?.message || fallback
}

// --- Organizasyon ---------------------------------------------------------
const currentOrg = computed(() => organizations.value.find(o => o.slug === tenantSlug.value))
const orgName = ref('')
watch(currentOrg, (o) => { orgName.value = o?.name || '' }, { immediate: true })
const savingOrg = ref(false)

async function saveOrgName() {
  const o = currentOrg.value
  const name = orgName.value.trim()
  if (!o || !name || name === o.name) return
  savingOrg.value = true
  try {
    await renameOrganization(o.id, name)
    toast.success(t('org.renamed'))
  } catch (e: any) {
    toast.error(errMsg(e, t('org.actionFailed')))
  } finally {
    savingOrg.value = false
  }
}

async function switchTo(id: string) {
  try {
    if (platformAdmin.value) await api.adminSwitchTenant(id)
    else await switchOrganization(id)
    // Proje slug'ı kiracıya özeldir; yeni organizasyona taşınmamalı.
    clearActiveProject()
    await check()
    window.location.reload()
  } catch (e: any) {
    toast.error(errMsg(e, t('shell.orgSwitchFailed')))
  }
}

const newOrgName = ref('')
const newOrgSlug = ref('')
const creatingOrg = ref(false)
async function createOrg() {
  if (!newOrgName.value.trim() || !newOrgSlug.value.trim()) return
  creatingOrg.value = true
  try {
    await createOrganization(newOrgName.value.trim(), newOrgSlug.value.trim().toLowerCase())
    clearActiveProject()
    window.location.reload()
  } catch (e: any) {
    toast.error(errMsg(e, t('shell.orgCreateFailed')))
  } finally {
    creatingOrg.value = false
  }
}

// Silme: kısa adın birebir yazılması gerekir (yanlış sekmede yanlış org'u silmeyi önler).
const confirmSlug = ref('')
const deletingOrg = ref(false)
const otherOrgs = computed(() => organizations.value.filter(o => o.slug !== tenantSlug.value))
const canDeleteOrg = computed(() =>
  canManage.value && isBetterAuth.value && tenantSlug.value !== 'default' && otherOrgs.value.length > 0)

async function deleteOrg() {
  if (confirmSlug.value.trim() !== tenantSlug.value) return
  if (!confirm(t('org.deleteFinalConfirm', { slug: tenantSlug.value }))) return
  deletingOrg.value = true
  try {
    await api.deleteOrganization(confirmSlug.value.trim())
    toast.success(t('org.deleted'))
    // Silinen organizasyonda kalınamaz: bir başkasına geç.
    const next = otherOrgs.value[0]
    clearActiveProject()
    if (next) await switchOrganization(next.id)
    window.location.href = '/'
  } catch (e: any) {
    toast.error(errMsg(e, t('org.actionFailed')))
  } finally {
    deletingOrg.value = false
  }
}

// --- Projeler -------------------------------------------------------------
const projects = ref<Project[]>([])
const loadingProjects = ref(true)

async function loadProjects() {
  loadingProjects.value = true
  try {
    projects.value = await api.listProjects()
  } catch (e: any) {
    toast.error(errMsg(e, t('org.actionFailed')))
  } finally {
    loadingProjects.value = false
  }
}

function isActive(p: Project): boolean {
  return activeProject.value ? activeProject.value === p.slug : p.slug === 'default'
}

function activate(p: Project) {
  if (isActive(p)) return
  setActiveProject(p.slug)
  window.location.reload()
}

const editingId = ref<string | null>(null)
const editName = ref('')
function startRename(p: Project) {
  editingId.value = p.id
  editName.value = p.name
}
async function saveRename(p: Project) {
  const name = editName.value.trim()
  if (!name || name === p.name) { editingId.value = null; return }
  try {
    await api.renameProject(p.id, name)
    editingId.value = null
    await loadProjects()
    toast.success(t('org.projectRenamed'))
  } catch (e: any) {
    toast.error(errMsg(e, t('org.actionFailed')))
  }
}

async function removeProject(p: Project) {
  if (!confirm(t('org.projectDeleteConfirm', { name: p.name }))) return
  try {
    await api.deleteProject(p.id)
    if (isActive(p)) setActiveProject('default')
    await loadProjects()
    toast.success(t('org.projectDeleted'))
  } catch (e: any) {
    // Sunucu, içinde secret/policy/log hedefi olan projeyi reddeder ve sayıları söyler.
    toast.error(errMsg(e, t('org.actionFailed')))
  }
}

const newProjectName = ref('')
const newProjectSlug = ref('')
const creatingProject = ref(false)
async function createProject() {
  if (!newProjectName.value.trim() || !newProjectSlug.value.trim()) return
  creatingProject.value = true
  try {
    const created = await api.createProject({
      name: newProjectName.value.trim(),
      slug: newProjectSlug.value.trim().toLowerCase(),
    })
    newProjectName.value = ''
    newProjectSlug.value = ''
    await loadProjects()
    toast.success(t('projects.created'))
    setActiveProject(created.slug)
  } catch (e: any) {
    toast.error(errMsg(e, t('projects.createFailed')))
  } finally {
    creatingProject.value = false
  }
}

function slugify(v: string): string {
  return v.toLowerCase().replace(/[^a-z0-9_-]/g, '-')
}

onMounted(loadProjects)
</script>

<template>
  <div class="mx-auto max-w-4xl space-y-6 px-4 py-6 sm:px-5">
    <div>
      <h1 class="flex items-center gap-2 text-xl font-semibold text-fg">
        <Icon name="lucide:building-2" class="size-5 text-accent" />
        {{ t('org.title') }}
      </h1>
      <p class="mt-1 text-sm text-fg-muted">{{ t('org.subtitle') }}</p>
    </div>

    <!-- Organizasyon -->
    <section class="rounded-xl border border-line bg-surface p-5">
      <h2 class="text-sm font-semibold text-fg">{{ t('org.orgSection') }}</h2>

      <div class="mt-4 grid gap-3 sm:grid-cols-[1fr_auto]">
        <div>
          <label class="mb-1 block text-xs font-medium text-fg-muted">{{ t('org.name') }}</label>
          <input
            v-model="orgName"
            type="text"
            :disabled="!canManage || !isBetterAuth || !currentOrg"
            class="w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent disabled:opacity-60"
          >
          <p class="mt-1 text-[11px] text-fg-subtle">
            {{ t('org.slugLabel') }}: <span class="font-mono">{{ tenantSlug || '—' }}</span> · {{ t('org.slugFixed') }}
          </p>
        </div>
        <button
          v-if="canManage && isBetterAuth && currentOrg"
          class="h-9 self-start rounded-lg bg-accent px-3 text-sm font-medium text-white disabled:opacity-50 sm:mt-5"
          :disabled="savingOrg || !orgName.trim() || orgName.trim() === currentOrg.name"
          @click="saveOrgName"
        >
          {{ t('common.save') }}
        </button>
      </div>

      <div v-if="organizations.length" class="mt-5">
        <div class="mb-2 text-xs font-medium text-fg-muted">{{ t('org.myOrgs') }}</div>
        <div class="space-y-1.5">
          <div
            v-for="o in organizations"
            :key="o.id"
            class="flex items-center justify-between rounded-lg border border-line px-3 py-2 text-sm"
          >
            <div class="min-w-0">
              <span class="text-fg">{{ o.name }}</span>
              <span class="ml-2 font-mono text-[11px] text-fg-subtle">{{ o.slug }}</span>
            </div>
            <span v-if="o.slug === tenantSlug" class="rounded bg-accent/15 px-1.5 py-0.5 text-[11px] font-medium text-accent">{{ t('org.current') }}</span>
            <button v-else class="rounded-md border border-line px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="switchTo(o.id)">
              {{ t('org.switch') }}
            </button>
          </div>
        </div>
      </div>

      <form v-if="isBetterAuth" class="mt-5 grid gap-2 sm:grid-cols-[1fr_1fr_auto]" @submit.prevent="createOrg">
        <input v-model="newOrgName" type="text" :placeholder="t('shell.orgName')" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent" @input="newOrgSlug = slugify(newOrgName)">
        <input v-model="newOrgSlug" type="text" :placeholder="t('shell.slugLabel')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
        <button type="submit" class="inline-flex items-center justify-center gap-1.5 rounded-lg border border-line px-3 py-2 text-sm text-fg hover:border-accent/40 disabled:opacity-50" :disabled="creatingOrg || !newOrgName.trim() || !newOrgSlug.trim()">
          <Icon name="lucide:plus" class="size-4" />{{ t('shell.newOrg') }}
        </button>
      </form>
    </section>

    <!-- Projeler -->
    <section class="rounded-xl border border-line bg-surface p-5">
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-sm font-semibold text-fg">{{ t('org.projectsSection') }}</h2>
        <span class="font-mono text-[11px] text-fg-subtle">
          {{ projects.length }}
        </span>
      </div>
      <p class="mt-1 text-xs text-fg-muted">{{ t('org.projectsHint') }}</p>

      <div v-if="loadingProjects" class="py-6 text-center text-fg-muted">
        <Icon name="lucide:loader-circle" class="mx-auto size-5 animate-spin" />
      </div>
      <div v-else class="mt-4 space-y-1.5">
        <div
          v-for="p in projects"
          :key="p.id"
          class="flex items-center justify-between gap-3 rounded-lg border border-line px-3 py-2 text-sm"
        >
          <div class="min-w-0 flex-1">
            <input
              v-if="editingId === p.id"
              v-model="editName"
              type="text"
              class="w-full rounded border border-accent bg-bg px-2 py-1 text-sm text-fg outline-none"
              @keydown.enter="saveRename(p)"
              @keydown.esc="editingId = null"
            >
            <template v-else>
              <span class="text-fg">{{ p.name }}</span>
              <span class="ml-2 font-mono text-[11px] text-fg-subtle">{{ p.slug }}</span>
            </template>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <span v-if="isActive(p)" class="rounded bg-accent/15 px-1.5 py-0.5 text-[11px] font-medium text-accent">{{ t('org.active') }}</span>
            <button v-else class="rounded-md border border-line px-2 py-1 text-xs text-fg-muted hover:text-fg" @click="activate(p)">{{ t('org.makeActive') }}</button>
            <template v-if="canManage">
              <button v-if="editingId === p.id" class="rounded-md px-2 py-1 text-xs text-accent" @click="saveRename(p)">{{ t('common.save') }}</button>
              <button v-else class="rounded-md px-2 py-1 text-fg-muted hover:text-fg" :title="t('org.rename')" @click="startRename(p)">
                <Icon name="lucide:pencil" class="size-3.5" />
              </button>
              <button
                v-if="p.slug !== 'default'"
                class="rounded-md px-2 py-1 text-fg-muted hover:text-danger"
                :title="t('org.deleteProject')"
                @click="removeProject(p)"
              >
                <Icon name="lucide:trash-2" class="size-3.5" />
              </button>
            </template>
          </div>
        </div>
      </div>

      <form class="mt-4 grid gap-2 sm:grid-cols-[1fr_1fr_auto]" @submit.prevent="createProject">
        <input v-model="newProjectName" type="text" :placeholder="t('projects.name')" class="rounded-lg border border-line bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent" @input="newProjectSlug = slugify(newProjectName)">
        <input v-model="newProjectSlug" type="text" :placeholder="t('projects.slug')" class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-accent">
        <button type="submit" class="inline-flex items-center justify-center gap-1.5 rounded-lg border border-line px-3 py-2 text-sm text-fg hover:border-accent/40 disabled:opacity-50" :disabled="creatingProject || !newProjectName.trim() || !newProjectSlug.trim()">
          <Icon name="lucide:plus" class="size-4" />{{ t('projects.new') }}
        </button>
      </form>
    </section>

    <!-- Tehlikeli bölge: organizasyon silme -->
    <section v-if="canManage && isBetterAuth" class="rounded-xl border border-danger/40 bg-danger/5 p-5">
      <h2 class="flex items-center gap-2 text-sm font-semibold text-danger">
        <Icon name="lucide:triangle-alert" class="size-4" />
        {{ t('org.dangerZone') }}
      </h2>
      <p class="mt-2 text-xs leading-relaxed text-fg-muted">{{ t('org.deleteExplain') }}</p>

      <p v-if="tenantSlug === 'default'" class="mt-3 text-xs text-fg-subtle">{{ t('org.cannotDeleteDefault') }}</p>
      <p v-else-if="!otherOrgs.length" class="mt-3 text-xs text-fg-subtle">{{ t('org.cannotDeleteLast') }}</p>
      <div v-else class="mt-3 grid gap-2 sm:grid-cols-[1fr_auto]">
        <input
          v-model="confirmSlug"
          type="text"
          :placeholder="t('org.typeSlug', { slug: tenantSlug })"
          class="rounded-lg border border-line bg-bg px-3 py-2 font-mono text-sm text-fg outline-none focus:border-danger"
        >
        <button
          class="rounded-lg bg-danger px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
          :disabled="!canDeleteOrg || deletingOrg || confirmSlug.trim() !== tenantSlug"
          @click="deleteOrg"
        >
          {{ t('org.deleteOrg') }}
        </button>
      </div>
    </section>
  </div>
</template>
