<script setup lang="ts">
import type { Client } from '~/types/api'
import type { PaneState } from '~/components/TerminalPane.vue'

interface Tab {
  id: number
  name: string
  shell?: string
  mounted: boolean
  status: PaneState
  /** Sunucudaki kabuk oturumu; sayfa degisince/yenilenince buna geri baglanilir. */
  sid?: string
}

const MAX_TABS = 8
const FONT_MIN = 9
const FONT_MAX = 28

const route = useRoute()
const clientID = route.params.id as string

const api = useApi()
const { t } = useI18n()
const { isLight } = useTheme()

const client = ref<Client | null>(null)
const loadError = ref('')
const loading = ref(true)

const tabs = ref<Tab[]>([])
const activeId = ref(0)
let seq = 0
const paneRefs = new Map<number, any>()

const fontSize = ref(13)
const copyOnSelect = ref(false)
const newShell = ref('')
const menuOpen = ref(false)
const renamingId = ref<number | null>(null)
const renameText = ref('')

const shells = computed(() => client.value?.shells ?? [])
const hasShells = computed(() => shells.value.length > 0)
const activeTab = computed(() => tabs.value.find(x => x.id === activeId.value) ?? null)
const canAdd = computed(() => tabs.value.length < MAX_TABS && !loadError.value)

// --- Ayarlar (localStorage; erisim yoksa sessizce varsayilan) ---------------
function loadPrefs() {
  try {
    const fs = Number(localStorage.getItem('zorven_term_font'))
    if (fs >= FONT_MIN && fs <= FONT_MAX) fontSize.value = fs
    copyOnSelect.value = localStorage.getItem('zorven_term_copysel') === '1'
  } catch { /* depolama engelli */ }
}
watch(fontSize, (v) => { try { localStorage.setItem('zorven_term_font', String(v)) } catch { /* */ } })
watch(copyOnSelect, (v) => { try { localStorage.setItem('zorven_term_copysel', v ? '1' : '0') } catch { /* */ } })

// --- Acik sekmeler (sessionStorage): sayfadan cikip donunce/yenileyince korunur.
// Kabuk sunucuda yasamaya devam eder; sekme kendi oturumuna geri baglanir.
const tabsKey = `zorven_term_tabs:${clientID}`
function saveTabs() {
  try {
    const list = tabs.value.map(x => ({ id: x.id, name: x.name, shell: x.shell, sid: x.sid }))
    if (!list.length) sessionStorage.removeItem(tabsKey)
    else sessionStorage.setItem(tabsKey, JSON.stringify({ active: activeId.value, tabs: list }))
  } catch { /* depolama engelli */ }
}
function restoreTabs(): boolean {
  try {
    const raw = sessionStorage.getItem(tabsKey)
    if (!raw) return false
    const st = JSON.parse(raw) as { active?: number, tabs?: Array<{ id: number, name: string, shell?: string, sid?: string }> }
    const list = (st.tabs ?? []).filter(x => typeof x.id === 'number' && typeof x.name === 'string').slice(0, MAX_TABS)
    if (!list.length) return false
    tabs.value = list.map(x => ({ id: x.id, name: x.name.slice(0, 40), shell: x.shell, sid: x.sid, mounted: true, status: 'connecting' as PaneState }))
    seq = Math.max(...list.map(x => x.id))
    activeId.value = list.some(x => x.id === st.active) ? st.active! : list[0]!.id
    return true
  } catch { return false }
}
watch([tabs, activeId], saveTabs, { deep: true })

function setSid(id: number, sid: string | undefined) {
  const tab = tabs.value.find(x => x.id === id)
  if (tab) tab.sid = sid
}

function bumpFont(d: number) {
  fontSize.value = Math.min(FONT_MAX, Math.max(FONT_MIN, fontSize.value + d))
}
function resetFont() { fontSize.value = 13 }

// --- Sekmeler ----------------------------------------------------------------
function shellName(id?: string) {
  return shells.value.find(s => s.id === id)?.name
}

function addTab(shell?: string) {
  if (!canAdd.value) return
  const id = ++seq
  const sh = hasShells.value ? (shell || newShell.value || client.value?.default_shell || shells.value[0]?.id) : undefined
  const base = shellName(sh) || t('terminal.tabDefault')
  const n = tabs.value.filter(x => x.name.startsWith(base)).length
  tabs.value.push({
    id,
    name: n ? `${base} ${n + 1}` : base,
    shell: sh,
    mounted: true,
    status: 'connecting',
  })
  activeId.value = id
}

function closeTab(id: number) {
  const i = tabs.value.findIndex(x => x.id === id)
  if (i < 0) return
  // Sekmeyi kapatmak kabugu da sonlandirir (sayfadan cikmak sonlandirmaz).
  paneRefs.get(id)?.terminate()
  tabs.value.splice(i, 1)
  paneRefs.delete(id)
  if (activeId.value === id) {
    activeId.value = (tabs.value[i] ?? tabs.value[i - 1])?.id ?? 0
  }
}

function selectTab(id: number) {
  activeId.value = id
  // Lazy mount: ilk kez secilince olusur, sonra tamponu korunur (v-show).
  const tab = tabs.value.find(x => x.id === id)
  if (tab) tab.mounted = true
}

function startRename(tab: Tab) {
  renamingId.value = tab.id
  renameText.value = tab.name
  nextTick(() => (document.getElementById('term-rename') as HTMLInputElement | null)?.select())
}
function commitRename() {
  const tab = tabs.value.find(x => x.id === renamingId.value)
  const v = renameText.value.trim()
  if (tab && v) tab.name = v.slice(0, 40)
  renamingId.value = null
}

function setStatus(id: number, s: PaneState) {
  const tab = tabs.value.find(x => x.id === id)
  if (tab) tab.status = s
}

const statusText = computed(() => {
  const s = activeTab.value?.status
  if (!s) return ''
  return ({
    connecting: t('terminal.stateConnecting'),
    open: t('terminal.stateOpen'),
    reconnecting: t('terminal.stateReconnecting'),
    ended: t('terminal.stateClosed'),
    error: t('terminal.stateError'),
  } as Record<PaneState, string>)[s]
})

function dotClass(s: PaneState) {
  if (s === 'open') return 'bg-accent'
  if (s === 'error') return 'bg-danger'
  if (s === 'reconnecting' || s === 'connecting') return 'animate-pulse bg-warn'
  return 'bg-fg-subtle'
}

function activePane() { return paneRefs.get(activeId.value) }

// --- Klavye kisayollari --------------------------------------------------------
function onKey(ev: KeyboardEvent) {
  const mod = ev.ctrlKey || ev.metaKey
  if (!mod || ev.altKey) return
  const k = ev.key.toLowerCase()
  if (ev.shiftKey && k === 't') { ev.preventDefault(); addTab(); return }
  if (ev.shiftKey && k === 'w') { ev.preventDefault(); if (activeId.value) closeTab(activeId.value); return }
  if (!ev.shiftKey || k === '+') {
    if (k === '=' || k === '+') { ev.preventDefault(); bumpFont(1) }
    else if (k === '-') { ev.preventDefault(); bumpFont(-1) }
    else if (k === '0') { ev.preventDefault(); resetFont() }
  }
}

onMounted(async () => {
  loadPrefs()
  window.addEventListener('keydown', onKey)
  try {
    const list = await api.listClients()
    client.value = list.find(c => c.id === clientID) ?? null
  } catch { /* liste alinamazsa yine de baglanmayi dene */ }
  loading.value = false

  if (client.value && client.value.status !== 'online') {
    loadError.value = t('terminal.errOffline')
    return
  }
  newShell.value = client.value?.default_shell || client.value?.shells?.[0]?.id || ''
  // Acik sekme varsa geri yukle; yoksa bos ekran (kullanici "Yeni sekme" ile acar).
  restoreTabs()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div class="flex h-[calc(100vh-8rem)] flex-col gap-3">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div class="min-w-0">
        <h1 class="text-xl font-semibold tracking-tight">{{ t('terminal.header') }}</h1>
        <p class="mt-0.5 truncate font-mono text-xs text-fg-muted">
          {{ client?.name ?? clientID }}
          <span class="text-fg-subtle">· {{ clientID }}</span>
        </p>
      </div>

      <div class="flex items-center gap-2">
        <span
          v-if="activeTab"
          class="inline-flex items-center gap-1.5 font-mono text-[11px]"
          :class="activeTab.status === 'open' ? 'text-accent-bright' : activeTab.status === 'error' ? 'text-danger' : 'text-fg-muted'"
        >
          <span class="size-1.5 rounded-full" :class="dotClass(activeTab.status)" />
          {{ statusText }}
        </span>

        <!-- Masaustu araclari -->
        <div class="hidden items-center gap-1 sm:flex">
          <button type="button" class="tb-btn" :title="t('terminal.search') + ' (Ctrl+F)'" :aria-label="t('terminal.search')" @click="activePane()?.openSearch()">
            <Icon name="lucide:search" class="size-3.5" />
          </button>
          <button type="button" class="tb-btn" :title="t('terminal.fontSmaller') + ' (Ctrl+-)'" :aria-label="t('terminal.fontSmaller')" :disabled="fontSize <= FONT_MIN" @click="bumpFont(-1)">
            <Icon name="lucide:minus" class="size-3.5" />
          </button>
          <span class="w-7 text-center font-mono text-[11px] text-fg-muted" :title="t('terminal.fontSize')">{{ fontSize }}</span>
          <button type="button" class="tb-btn" :title="t('terminal.fontLarger') + ' (Ctrl+=)'" :aria-label="t('terminal.fontLarger')" :disabled="fontSize >= FONT_MAX" @click="bumpFont(1)">
            <Icon name="lucide:plus" class="size-3.5" />
          </button>
          <label class="ml-1 flex cursor-pointer items-center gap-1.5 font-mono text-[11px] text-fg-muted">
            <input v-model="copyOnSelect" type="checkbox" class="accent-accent">
            {{ t('terminal.copyOnSelect') }}
          </label>
        </div>

        <!-- Mobil menu -->
        <div class="relative sm:hidden">
          <button type="button" class="tb-btn" :aria-label="t('terminal.menu')" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
            <Icon name="lucide:ellipsis-vertical" class="size-4" />
          </button>
          <div v-if="menuOpen" class="absolute right-0 top-full z-20 mt-1 w-56 space-y-2 rounded-md border border-line bg-surface p-3 shadow-lg">
            <button type="button" class="flex w-full cursor-pointer items-center gap-2 font-mono text-xs text-fg" @click="activePane()?.openSearch(); menuOpen = false">
              <Icon name="lucide:search" class="size-3.5" />{{ t('terminal.search') }}
            </button>
            <div class="flex items-center justify-between font-mono text-xs text-fg-muted">
              {{ t('terminal.fontSize') }}
              <span class="flex items-center gap-1">
                <button type="button" class="tb-btn" :aria-label="t('terminal.fontSmaller')" @click="bumpFont(-1)"><Icon name="lucide:minus" class="size-3.5" /></button>
                <span class="w-6 text-center">{{ fontSize }}</span>
                <button type="button" class="tb-btn" :aria-label="t('terminal.fontLarger')" @click="bumpFont(1)"><Icon name="lucide:plus" class="size-3.5" /></button>
              </span>
            </div>
            <label class="flex cursor-pointer items-center gap-2 font-mono text-xs text-fg-muted">
              <input v-model="copyOnSelect" type="checkbox" class="accent-accent">{{ t('terminal.copyOnSelect') }}
            </label>
          </div>
        </div>

        <button
          v-if="activeTab && (activeTab.status === 'open' || activeTab.status === 'reconnecting')"
          type="button"
          class="cursor-pointer rounded border border-danger/40 bg-danger/10 px-2.5 py-1 font-mono text-[11px] text-danger transition-colors hover:bg-danger/20"
          @click="activePane()?.disconnect()"
        >
          <Icon name="lucide:x" class="mr-1 inline size-3" /><span class="hidden sm:inline">{{ t('terminal.disconnect') }}</span>
        </button>

        <NuxtLink
          to="/clients"
          class="cursor-pointer rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors duration-150 hover:bg-surface"
        >
          <Icon name="lucide:arrow-left" class="mr-1 inline size-3" />{{ t('terminal.backToClients') }}
        </NuxtLink>
      </div>
    </header>

    <div
      v-if="loadError"
      class="rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-sm text-danger"
      role="alert"
    >
      {{ loadError }}
    </div>

    <p class="rounded-lg border border-warn/25 bg-warn/5 px-3 py-2 text-xs text-warn">
      <Icon name="lucide:triangle-alert" class="mr-1 inline size-3.5" />{{ t('terminal.shellWarnPre') }} <strong>{{ t('terminal.shellStrong') }}</strong> {{ t('terminal.shellWarnPost') }}
    </p>

    <!-- Sekme cubugu -->
    <div v-if="!loadError && !loading" class="flex items-center gap-2">
      <div class="flex min-w-0 flex-1 items-end gap-1 overflow-x-auto" role="tablist" :aria-label="t('terminal.tabs')">
        <div
          v-for="tab in tabs"
          :key="tab.id"
          role="tab"
          :aria-selected="tab.id === activeId"
          class="group flex shrink-0 cursor-pointer items-center gap-1.5 rounded-t-md border border-b-0 px-3 py-1.5 font-mono text-[11px] transition-colors"
          :class="tab.id === activeId ? 'border-line bg-surface text-fg' : 'border-transparent text-fg-muted hover:bg-surface/60'"
          :title="t('terminal.renameHint')"
          @click="selectTab(tab.id)"
          @dblclick.stop="startRename(tab)"
        >
          <span class="size-1.5 shrink-0 rounded-full" :class="dotClass(tab.status)" />
          <input
            v-if="renamingId === tab.id"
            id="term-rename"
            v-model="renameText"
            type="text"
            class="w-28 rounded border border-line bg-bg px-1 text-fg outline-none"
            maxlength="40"
            @click.stop
            @keydown.enter.prevent="commitRename"
            @keydown.esc.prevent="renamingId = null"
            @blur="commitRename"
          >
          <span v-else class="max-w-32 truncate">{{ tab.name }}</span>
          <button
            type="button"
            class="cursor-pointer rounded p-0.5 text-fg-subtle hover:bg-bg hover:text-fg"
            :title="t('terminal.closeTab') + ' (Ctrl+Shift+W)'"
            :aria-label="t('terminal.closeTab')"
            @click.stop="closeTab(tab.id)"
          >
            <Icon name="lucide:x" class="size-3" />
          </button>
        </div>
      </div>

      <select
        v-if="hasShells"
        v-model="newShell"
        class="shrink-0 rounded border border-line bg-surface px-1.5 py-1 font-mono text-[11px] text-fg"
        :aria-label="t('terminal.shell')"
        :title="t('terminal.shell')"
      >
        <option v-for="s in shells" :key="s.id" :value="s.id">{{ s.name }}</option>
      </select>
      <button
        type="button"
        class="tb-btn shrink-0"
        :disabled="!canAdd"
        :title="canAdd ? t('terminal.newTab') + ' (Ctrl+Shift+T)' : t('terminal.maxTabs', { n: MAX_TABS })"
        :aria-label="t('terminal.newTab')"
        @click="addTab()"
      >
        <Icon name="lucide:plus" class="size-4" />
      </button>
    </div>

    <!-- Paneller: sadece secilmis olanlar olusur, gizliler tamponunu korur -->
    <div class="relative min-h-0 flex-1">
      <TerminalPane
        v-for="tab in tabs"
        v-show="tab.id === activeId"
        :key="tab.id"
        :ref="(el: any) => { if (el) paneRefs.set(tab.id, el) }"
        :client-id="clientID"
        :shell="tab.shell"
        :active="tab.id === activeId"
        :dark="!isLight"
        :font-size="fontSize"
        :copy-on-select="copyOnSelect"
        :session-id="tab.sid"
        @status="setStatus(tab.id, $event)"
        @session="setSid(tab.id, $event)"
      />
      <div
        v-if="!tabs.length && !loadError && !loading"
        class="flex h-full flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-line text-sm text-fg-muted"
      >
        {{ t('terminal.noTabs') }}
        <button type="button" class="cursor-pointer rounded border border-accent/40 bg-accent/10 px-3 py-1.5 font-mono text-xs text-accent hover:bg-accent/20" @click="addTab()">
          <Icon name="lucide:plus" class="mr-1 inline size-3" />{{ t('terminal.newTab') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tb-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  border-radius: 0.25rem;
  border: 1px solid var(--color-line, rgba(128, 128, 128, 0.3));
  padding: 0.25rem 0.375rem;
  color: var(--color-fg-muted, inherit);
  transition: background-color 0.15s;
}
.tb-btn:hover:not(:disabled) {
  background-color: var(--color-surface, rgba(128, 128, 128, 0.1));
}
.tb-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
