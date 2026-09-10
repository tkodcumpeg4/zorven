<script setup lang="ts">
import type { Client } from '~/types/api'

const route = useRoute()
const clientID = route.params.id as string

const api = useApi()
const { connect } = useTerminal()
const { t } = useI18n()

const client = ref<Client | null>(null)
const state = ref<'loading' | 'connecting' | 'open' | 'closed' | 'error'>('loading')
const errorMsg = ref('')

const termHost = ref<HTMLElement | null>(null)
let ws: WebSocket | null = null
let termInstance: any = null
let fitAddon: any = null
let onResize: (() => void) | null = null
let sessionToken = 0

function disconnectSession() {
  sessionToken++
  if (ws) {
    try {
      ws.close(1000, 'kullanıcı kapattı')
    } catch { /* ignore */ }
    ws = null
  }
  if (state.value === 'open' || state.value === 'connecting') {
    state.value = 'closed'
  }
}

async function startSession() {
  const currentToken = ++sessionToken
  if (ws) {
    try { ws.close(1000, 'yeniden başlatılıyor') } catch { /* ignore */ }
    ws = null
  }
  state.value = 'connecting'
  errorMsg.value = ''
  if (termInstance) {
    termInstance.write('\r\n\x1b[90m' + t('terminal.connecting') + '\x1b[0m\r\n')
  }

  try {
    const newWs = await connect(clientID, termInstance, {
      onOpen: () => {
        if (currentToken !== sessionToken) {
          try { newWs.close(1000, 'eski oturum') } catch {}
          return
        }
        state.value = 'open'
        fitAddon?.fit()
        termInstance?.focus()
      },
      onClose: (code, reason) => {
        if (currentToken !== sessionToken) return
        if (state.value === 'connecting' || (code && code !== 1000)) {
          state.value = 'error'
          errorMsg.value = t('terminal.errClosed', { code: code ?? 1006, reason: reason ? ' · ' + reason : '' })
        } else if (state.value !== 'error') {
          state.value = 'closed'
        }
      },
      onError: () => {
        if (currentToken !== sessionToken) return
        state.value = 'error'
        if (!errorMsg.value) {
          errorMsg.value = t('terminal.errWsError')
        }
      },
    })

    if (currentToken !== sessionToken) {
      try { newWs.close(1000, 'oturum iptal edildi') } catch {}
      return
    }
    ws = newWs
  } catch (e) {
    if (currentToken !== sessionToken) return
    state.value = 'error'
    errorMsg.value = e instanceof Error ? e.message : t('terminal.errOpen')
  }
}

function cleanup() {
  disconnectSession()
  if (termInstance) {
    try { termInstance.dispose() } catch {}
    termInstance = null
  }
  if (onResize) {
    window.removeEventListener('resize', onResize)
    onResize = null
  }
  window.removeEventListener('beforeunload', cleanup)
}

onMounted(async () => {
  window.addEventListener('beforeunload', cleanup)

  try {
    const list = await api.listClients()
    client.value = list.find(c => c.id === clientID) ?? null
  } catch { /* liste alinamazsa yine de baglanmayi dene */ }

  if (client.value && client.value.status !== 'online') {
    state.value = 'error'
    errorMsg.value = t('terminal.errOffline')
    return
  }

  const [{ Terminal }, { FitAddon }] = await Promise.all([
    import('@xterm/xterm'),
    import('@xterm/addon-fit'),
  ])

  termInstance = new Terminal({
    fontFamily: '"JetBrains Mono", ui-monospace, monospace',
    fontSize: 13,
    cursorBlink: true,
    theme: {
      background: '#0A0A0A',
      foreground: '#F5F5F5',
      cursor: '#22C55E',
      selectionBackground: 'rgba(34,197,94,0.3)',
    },
  })
  fitAddon = new FitAddon()
  termInstance.loadAddon(fitAddon)
  await nextTick()
  if (!termHost.value) return
  termInstance.open(termHost.value)
  fitAddon.fit()

  onResize = () => fitAddon?.fit()
  window.addEventListener('resize', onResize)

  await startSession()
})

onBeforeUnmount(cleanup)
onUnmounted(cleanup)

const statusText = computed(() => ({
  loading: t('terminal.stateLoading'),
  connecting: t('terminal.stateConnecting'),
  open: t('terminal.stateOpen'),
  closed: t('terminal.stateClosed'),
  error: t('terminal.stateError'),
}[state.value]))
</script>

<template>
  <div class="flex h-[calc(100vh-8rem)] flex-col gap-4">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('terminal.header') }}</h1>
        <p class="mt-0.5 font-mono text-xs text-fg-muted">
          {{ client?.name ?? clientID }}
          <span class="text-fg-subtle">· {{ clientID }}</span>
        </p>
      </div>
      <div class="flex items-center gap-3">
        <span
          class="inline-flex items-center gap-1.5 font-mono text-[11px]"
          :class="state === 'open' ? 'text-accent-bright' : state === 'error' ? 'text-danger' : 'text-fg-muted'"
        >
          <span
            class="size-1.5 rounded-full"
            :class="state === 'open' ? 'animate-pulse bg-accent'
                    : state === 'error' ? 'bg-danger' : 'bg-fg-subtle'"
          />
          {{ statusText }}
        </span>

        <button
          v-if="state === 'open'"
          type="button"
          class="cursor-pointer rounded border border-danger/40 bg-danger/10 px-2.5 py-1 font-mono text-[11px] text-danger transition-colors hover:bg-danger/20"
          @click="disconnectSession"
        >
          <Icon name="lucide:x" class="mr-1 inline size-3" />{{ t('terminal.disconnect') }}
        </button>

        <button
          v-if="state === 'error' || state === 'closed'"
          type="button"
          class="cursor-pointer rounded border border-line bg-surface px-2.5 py-1 font-mono text-[11px] text-accent transition-colors hover:border-accent/40"
          @click="startSession"
        >
          <Icon name="lucide:refresh-cw" class="mr-1 inline size-3" />{{ t('terminal.reconnect') }}
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
      v-if="state === 'error'"
      class="flex items-center justify-between gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-sm text-danger"
      role="alert"
    >
      <span>{{ errorMsg }}</span>
      <button
        type="button"
        class="cursor-pointer rounded border border-danger/40 bg-danger/20 px-2.5 py-1 text-xs font-semibold hover:bg-danger/30"
        @click="startSession"
      >
        {{ t('common.retry') }}
      </button>
    </div>

    <p class="rounded-lg border border-warn/25 bg-warn/5 px-3 py-2 text-xs text-warn">
      <Icon name="lucide:triangle-alert" class="mr-1 inline size-3.5" />{{ t('terminal.shellWarnPre') }} <strong>{{ t('terminal.shellStrong') }}</strong> {{ t('terminal.shellWarnPost') }}
    </p>

    <div
      ref="termHost"
      class="min-h-0 flex-1 overflow-hidden rounded-lg border border-line bg-bg p-2"
    />
  </div>
</template>
