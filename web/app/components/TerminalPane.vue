<script setup lang="ts">
/**
 * Tek bir terminal oturumu (sekme): kendi xterm ornegi + WSS baglantisi.
 * Kabuk sunucuda yasar (sayfa degisince kapanmaz): baglanti kopunca ayni
 * oturuma (sessionId) ustel geri cekilmeyle yeniden baglanir, sunucu son
 * ciktiyi tekrar gonderir. Kabuk bittiyse/oturum yoksa "Yeni oturum ac" sunar.
 * Sekme kapatilinca terminate() kabugu gercekten sonlandirir.
 */
import type { Terminal, ITheme } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import type { SearchAddon } from '@xterm/addon-search'

export type PaneState = 'connecting' | 'open' | 'reconnecting' | 'ended' | 'error'

const props = defineProps<{
  clientId: string
  shell?: string
  active: boolean
  dark: boolean
  fontSize: number
  copyOnSelect: boolean
  /** Yeniden baglanilacak sunucu oturumu (sessionStorage'dan). */
  sessionId?: string
}>()

const emit = defineEmits<{
  (e: 'status', s: PaneState): void
  (e: 'session', id: string | undefined): void
}>()

const { t } = useI18n()
const { connect } = useTerminal()

const host = ref<HTMLElement | null>(null)
const state = ref<PaneState>('connecting')
const errorMsg = ref('')
const attempt = ref(0)
const nextRetryIn = ref(0)

const MAX_ATTEMPTS = 8

let term: Terminal | null = null
let fit: FitAddon | null = null
let search: SearchAddon | null = null
let ws: WebSocket | null = null
let token = 0
let retryTimer: ReturnType<typeof setTimeout> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null
let ro: ResizeObserver | null = null
let hadOpen = false
let disposed = false
let selDisposable: { dispose: () => void } | null = null
let sid: string | undefined = props.sessionId

function setSid(id: string | undefined) {
  sid = id
  emit('session', id)
}

// Sunucu oturumu artik yok (kabuk bitti, 30 dk bagsiz kaldi, istemci koptu).
function sessionGone() {
  token++
  clearTimers()
  closeWs('oturum yok')
  setSid(undefined)
  hadOpen = true
  setState('ended')
}

// --- Tema ------------------------------------------------------------------
const DARK: ITheme = {
  background: '#0A0A0A', foreground: '#F5F5F5', cursor: '#22C55E', cursorAccent: '#0A0A0A',
  selectionBackground: 'rgba(34,197,94,0.30)',
  black: '#1F1F1F', red: '#F87171', green: '#4ADE80', yellow: '#FACC15',
  blue: '#60A5FA', magenta: '#C084FC', cyan: '#22D3EE', white: '#E5E5E5',
  brightBlack: '#6B7280', brightRed: '#FCA5A5', brightGreen: '#86EFAC', brightYellow: '#FDE047',
  brightBlue: '#93C5FD', brightMagenta: '#D8B4FE', brightCyan: '#67E8F9', brightWhite: '#FFFFFF',
}
const LIGHT: ITheme = {
  background: '#FAFAFA', foreground: '#171717', cursor: '#15803D', cursorAccent: '#FAFAFA',
  selectionBackground: 'rgba(21,128,61,0.25)',
  black: '#262626', red: '#B91C1C', green: '#15803D', yellow: '#A16207',
  blue: '#1D4ED8', magenta: '#7E22CE', cyan: '#0E7490', white: '#D4D4D4',
  brightBlack: '#737373', brightRed: '#DC2626', brightGreen: '#16A34A', brightYellow: '#CA8A04',
  brightBlue: '#2563EB', brightMagenta: '#9333EA', brightCyan: '#0891B2', brightWhite: '#171717',
}

function setState(s: PaneState) {
  state.value = s
  emit('status', s)
}

function clearTimers() {
  if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
  if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null }
}

function closeWs(reason: string) {
  if (ws) {
    try { ws.close(1000, reason) } catch { /* ignore */ }
    ws = null
  }
}

function note(msg: string) {
  term?.write('\r\n\x1b[90m' + msg + '\x1b[0m\r\n')
}

// --- Baglanti --------------------------------------------------------------
async function startSession(fresh = false) {
  if (!term || disposed) return
  clearTimers()
  const my = ++token
  closeWs('yeniden baslatiliyor')
  if (fresh) {
    attempt.value = 0
    setSid(undefined)
    note(t('terminal.newSessionNote'))
  }
  setState(hadOpen ? 'reconnecting' : 'connecting')
  errorMsg.value = ''

  try {
    const sock = await connect(props.clientId, term, {
      shell: props.shell,
      attach: sid,
      onSession: (id) => {
        if (my !== token) return
        setSid(id)
      },
      onOpen: () => {
        if (my !== token) { try { sock.close(1000, 'eski') } catch { /* */ } return }
        const wasReconnect = hadOpen
        hadOpen = true
        attempt.value = 0
        setState('open')
        fit?.fit()
        // Yeniden baglanmada sunucu 80x24 ile basladigi icin gercek boyutu hemen gonder.
        if (sock.readyState === WebSocket.OPEN && term) {
          sock.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
        }
        if (wasReconnect) note(t('terminal.reconnectedNote'))
        if (props.active) term?.focus()
      },
      onExit: () => {
        if (my !== token) return
        // Kabuk bitti: otomatik yeniden baglanma yok.
        clearTimers()
        setSid(undefined)
        setState('ended')
      },
      onClose: (code) => {
        if (my !== token) return
        if (state.value === 'ended') return
        if (code === 4410) { sessionGone(); return }
        if (code === 4409) {
          // Ayni oturum baska bir tarayici sekmesinde acildi; kavga etme.
          clearTimers()
          note(t('terminal.takenOver'))
          setState('ended')
          return
        }
        if (code === 1000 && hadOpen) { setState('ended'); return }
        scheduleRetry()
      },
      onError: () => { /* close olayi ardindan gelir; orada ele aliniyor */ },
    })
    if (my !== token) { try { sock.close(1000, 'iptal') } catch { /* */ } return }
    ws = sock
  } catch (e) {
    if (my !== token) return
    if ((e as any)?.statusCode === 410 || (e as any)?.status === 410) { sessionGone(); return }
    errorMsg.value = (e as any)?.statusCode === 403 || (e as any)?.status === 403
      ? t('common.remoteAccessDenied')
      : e instanceof Error ? e.message : t('terminal.errOpen')
    scheduleRetry()
  }
}

function scheduleRetry() {
  if (disposed) return
  attempt.value++
  if (attempt.value > MAX_ATTEMPTS) {
    setState(hadOpen ? 'ended' : 'error')
    if (!errorMsg.value) errorMsg.value = t('terminal.errGaveUp')
    return
  }
  setState(hadOpen ? 'reconnecting' : 'connecting')
  const delay = Math.min(1000 * 2 ** (attempt.value - 1), 15000)
  nextRetryIn.value = Math.ceil(delay / 1000)
  countdownTimer = setInterval(() => { if (nextRetryIn.value > 0) nextRetryIn.value-- }, 1000)
  retryTimer = setTimeout(() => startSession(), delay)
}

function newSession() { void startSession(true) }

// terminate, kabugu sunucuda/istemcide gercekten sonlandirir (sekme kapatma).
function terminate() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    try { ws.send(JSON.stringify({ type: 'close' })) } catch { /* */ }
  }
  setSid(undefined)
}

function disconnect() {
  terminate()
  token++
  clearTimers()
  closeWs('kullanici kapatti')
  if (state.value !== 'ended') {
    note(t('terminal.disconnectedNote'))
    setState('ended')
  }
}

// --- Kopyala / yapistir ----------------------------------------------------
async function copySelection() {
  const sel = term?.getSelection()
  if (!sel) return
  try { await navigator.clipboard.writeText(sel) } catch { /* izin yok */ }
}

async function pasteClipboard() {
  try {
    const text = await navigator.clipboard.readText()
    if (text) term?.paste(text)
  } catch { /* izin yok */ }
}

// --- Arama -----------------------------------------------------------------
const searchOpen = ref(false)
const searchText = ref('')
const caseSensitive = ref(false)
const resultInfo = ref<{ index: number, count: number } | null>(null)
const searchInput = ref<HTMLInputElement | null>(null)

function searchOpts() {
  return {
    caseSensitive: caseSensitive.value,
    decorations: props.dark
      ? { matchBackground: '#3F3F00', matchBorder: '#A16207', matchOverviewRuler: '#FACC15', activeMatchBackground: '#166534', activeMatchBorder: '#4ADE80', activeMatchColorOverviewRuler: '#4ADE80' }
      : { matchBackground: '#FEF08A', matchBorder: '#CA8A04', matchOverviewRuler: '#CA8A04', activeMatchBackground: '#BBF7D0', activeMatchBorder: '#15803D', activeMatchColorOverviewRuler: '#15803D' },
  }
}
function findNext() { if (searchText.value) search?.findNext(searchText.value, searchOpts()) }
function findPrev() { if (searchText.value) search?.findPrevious(searchText.value, searchOpts()) }
function openSearch() {
  searchOpen.value = true
  const sel = term?.getSelection()
  if (sel && !sel.includes('\n')) searchText.value = sel
  nextTick(() => { searchInput.value?.focus(); searchInput.value?.select() })
}
function closeSearch() {
  searchOpen.value = false
  search?.clearDecorations()
  resultInfo.value = null
  term?.focus()
}
watch([searchText, caseSensitive], () => {
  if (!searchText.value) { search?.clearDecorations(); resultInfo.value = null; return }
  findNext()
})

// --- Kurulum ---------------------------------------------------------------
function refit() {
  if (!props.active || !host.value || host.value.clientWidth === 0) return
  try { fit?.fit() } catch { /* henuz olculemiyor */ }
}

onMounted(async () => {
  const [
    { Terminal }, { FitAddon }, { SearchAddon }, { WebLinksAddon },
    { Unicode11Addon }, { ClipboardAddon },
  ] = await Promise.all([
    import('@xterm/xterm'),
    import('@xterm/addon-fit'),
    import('@xterm/addon-search'),
    import('@xterm/addon-web-links'),
    import('@xterm/addon-unicode11'),
    import('@xterm/addon-clipboard'),
  ])
  if (disposed) return

  // Yazi tipi yuklenmeden olcum yapilirsa hucre boyutu yanlis cikar.
  try { await document.fonts?.load(`${props.fontSize}px "JetBrains Mono"`) } catch { /* yedek yazi tipi */ }
  if (disposed || !host.value) return

  term = new Terminal({
    fontFamily: '"JetBrains Mono", ui-monospace, "Cascadia Code", monospace',
    fontSize: props.fontSize,
    cursorBlink: true,
    scrollback: 10000,
    allowProposedApi: true,
    theme: props.dark ? DARK : LIGHT,
  })
  fit = new FitAddon()
  search = new SearchAddon()
  term.loadAddon(fit)
  term.loadAddon(search)
  term.loadAddon(new WebLinksAddon((_ev, uri) => {
    if (!/^https?:\/\//i.test(uri)) return
    const w = window.open(uri, '_blank', 'noopener,noreferrer')
    if (w) w.opener = null
  }))
  term.loadAddon(new Unicode11Addon())
  term.unicode.activeVersion = '11'
  // OSC 52: yalnizca yazma. Uzak taraf panonuzu OKUYAMASIN diye okuma bos doner.
  term.loadAddon(new ClipboardAddon(undefined, {
    readText: () => '',
    writeText: async (_sel, text) => { try { await navigator.clipboard.writeText(text) } catch { /* izin yok */ } },
  }))

  term.open(host.value)

  // WebGL: desteklenmiyorsa/baglam kaybolursa DOM olusturucuya dus.
  try {
    const { WebglAddon } = await import('@xterm/addon-webgl')
    if (!disposed && term) {
      const gl = new WebglAddon()
      gl.onContextLoss(() => { try { gl.dispose() } catch { /* */ } })
      term.loadAddon(gl)
    }
  } catch { /* WebGL yok: DOM olusturucu */ }

  search.onDidChangeResults((r) => {
    resultInfo.value = { index: r.resultIndex, count: r.resultCount }
  })

  selDisposable = term.onSelectionChange(() => {
    if (props.copyOnSelect && term?.hasSelection()) void copySelection()
  })

  term.attachCustomKeyEventHandler((ev) => {
    if (ev.type !== 'keydown') return true
    const mod = ev.ctrlKey || ev.metaKey
    if (!mod) return true
    const k = ev.key.toLowerCase()
    if (k === 'f' && !ev.shiftKey && !ev.altKey) { ev.preventDefault(); openSearch(); return false }
    if (ev.shiftKey && k === 'c') { ev.preventDefault(); void copySelection(); return false }
    if (ev.shiftKey && k === 'v') { ev.preventDefault(); void pasteClipboard(); return false }
    // Sekme ve yazi boyutu kisayollari sayfa seviyesinde ele alinir; kabuga gitmesin.
    if (ev.shiftKey && (k === 't' || k === 'w')) return false
    if (!ev.shiftKey && (k === '=' || k === '+' || k === '-' || k === '0')) return false
    return true
  })

  host.value.addEventListener('contextmenu', onContextMenu)

  ro = new ResizeObserver(() => refit())
  ro.observe(host.value)
  refit()

  await startSession()
})

function onContextMenu(ev: MouseEvent) {
  ev.preventDefault()
  void pasteClipboard()
}

// Aktiflesince yeniden olc (gizliyken 0 boyutluydu) ve odaklan.
watch(() => props.active, (a) => {
  if (!a) return
  nextTick(() => {
    refit()
    term?.focus()
    term?.refresh(0, Math.max(0, (term?.rows ?? 1) - 1))
  })
})

watch(() => props.dark, (d) => { if (term) term.options.theme = d ? DARK : LIGHT })
watch(() => props.fontSize, (s) => {
  if (!term) return
  term.options.fontSize = s
  nextTick(refit)
})

onBeforeUnmount(() => {
  disposed = true
  token++
  clearTimers()
  closeWs('sekme kapandi')
  ro?.disconnect()
  host.value?.removeEventListener('contextmenu', onContextMenu)
  selDisposable?.dispose()
  try { term?.dispose() } catch { /* */ }
  term = null
})

defineExpose({ focus: () => term?.focus(), newSession, disconnect, terminate, openSearch, state })
</script>

<template>
  <div class="relative h-full w-full">
    <div
      ref="host"
      class="h-full w-full overflow-hidden rounded-lg border border-line p-2"
      :style="{ backgroundColor: dark ? DARK.background : LIGHT.background }"
    />

    <!-- Arama cubugu -->
    <div
      v-if="searchOpen"
      class="absolute right-4 top-3 z-10 flex items-center gap-1 rounded-md border border-line bg-surface px-2 py-1 shadow-lg"
      role="search"
    >
      <Icon name="lucide:search" class="size-3.5 text-fg-muted" />
      <input
        ref="searchInput"
        v-model="searchText"
        type="text"
        class="w-40 bg-transparent px-1 font-mono text-xs text-fg outline-none placeholder:text-fg-subtle"
        :placeholder="t('terminal.searchPlaceholder')"
        :aria-label="t('terminal.search')"
        @keydown.enter.prevent="$event.shiftKey ? findPrev() : findNext()"
        @keydown.esc.prevent="closeSearch"
      >
      <span v-if="resultInfo && searchText" class="min-w-10 text-center font-mono text-[10px] text-fg-muted">
        {{ resultInfo.count ? `${resultInfo.index + 1}/${resultInfo.count}` : t('terminal.noMatch') }}
      </span>
      <button
        type="button"
        class="cursor-pointer rounded px-1.5 py-0.5 font-mono text-[11px] transition-colors hover:bg-bg"
        :class="caseSensitive ? 'bg-accent/15 text-accent' : 'text-fg-muted'"
        :title="t('terminal.matchCase')"
        :aria-label="t('terminal.matchCase')"
        :aria-pressed="caseSensitive"
        @click="caseSensitive = !caseSensitive"
      >
        Aa
      </button>
      <button type="button" class="cursor-pointer rounded p-1 text-fg-muted hover:bg-bg" :title="t('terminal.findPrev')" :aria-label="t('terminal.findPrev')" @click="findPrev">
        <Icon name="lucide:chevron-up" class="size-3.5" />
      </button>
      <button type="button" class="cursor-pointer rounded p-1 text-fg-muted hover:bg-bg" :title="t('terminal.findNext')" :aria-label="t('terminal.findNext')" @click="findNext">
        <Icon name="lucide:chevron-down" class="size-3.5" />
      </button>
      <button type="button" class="cursor-pointer rounded p-1 text-fg-muted hover:bg-bg" :title="t('terminal.closeSearch')" :aria-label="t('terminal.closeSearch')" @click="closeSearch">
        <Icon name="lucide:x" class="size-3.5" />
      </button>
    </div>

    <!-- Baglanti durumu -->
    <div
      v-if="state === 'reconnecting' || (state === 'connecting' && attempt > 0)"
      class="absolute inset-x-4 top-3 z-10 mx-auto flex w-fit max-w-full items-center gap-2 rounded-md border border-warn/30 bg-warn/10 px-3 py-1.5 text-xs text-warn shadow"
      role="status"
    >
      <Icon name="lucide:loader-circle" class="size-3.5 animate-spin" />
      <span>{{ t('terminal.reconnectingBanner') }}</span>
      <span class="font-mono text-[10px] opacity-80">{{ t('terminal.retryIn', { s: nextRetryIn, n: attempt, max: MAX_ATTEMPTS }) }}</span>
    </div>

    <div
      v-if="state === 'ended' || state === 'error'"
      class="absolute inset-x-4 bottom-4 z-10 mx-auto flex w-fit max-w-full flex-wrap items-center gap-3 rounded-md border border-line bg-surface px-3 py-2 text-xs shadow-lg"
      role="status"
    >
      <span :class="state === 'error' ? 'text-danger' : 'text-fg-muted'">
        {{ state === 'error' ? (errorMsg || t('terminal.errOpen')) : t('terminal.sessionGone') }}
      </span>
      <button
        type="button"
        class="cursor-pointer rounded border border-accent/40 bg-accent/10 px-2.5 py-1 font-mono text-[11px] text-accent transition-colors hover:bg-accent/20"
        @click="newSession"
      >
        <Icon name="lucide:plus" class="mr-1 inline size-3" />{{ t('terminal.newSession') }}
      </button>
    </div>
  </div>
</template>
