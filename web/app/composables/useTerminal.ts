/**
 * Uzak terminal baglantisi. Akis:
 *   1. POST /terminal-ticket (admin bearer) -> tek kullanimlik bilet
 *   2. WSS .../clients/{id}/terminal?ticket=... ile baglan
 *   3. xterm <-> WSS: input/resize gonder, output/exit al
 *
 * Bilet neden gerekli: tarayici WebSocket'te Authorization basligi gonderemez.
 */
import type { Terminal } from '@xterm/xterm'

export interface TerminalOptions {
  onOpen?: () => void
  onClose?: (code?: number, reason?: string) => void
  onError?: (err?: unknown) => void
}

export function useTerminal() {
  const { key } = useAdminKey()

  async function connect(clientID: string, term: Terminal, opts?: TerminalOptions): Promise<WebSocket> {
    // 1. Bilet al (admin anahtari veya Better Auth cereziyle korunur).
    const headers: Record<string, string> = {}
    if (key.value) {
      headers.Authorization = `Bearer ${key.value}`
    }
    const { ticket } = await $fetch<{ ticket: string }>('/api/v1/terminal-ticket', {
      method: 'POST',
      headers,
      body: { client_id: clientID },
    })

    // 2. WSS ac.
    // wsBase ayarliysa ve gecerliyse dogrudan sunucuya baglan; degilse her zaman ayni origin (location.host).
    const wsBase = useRuntimeConfig().public.wsBase as string
    let base = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`
    if (wsBase && (import.meta.dev || wsBase.includes('localhost') || wsBase.includes(location.hostname))) {
      base = wsBase.replace(/\/$/, '')
    }
    const url = `${base}/api/v1/clients/${clientID}/terminal?ticket=${ticket}`
    const ws = new WebSocket(url)

    const enc = new TextEncoder()
    const dec = new TextDecoder()

    ws.onmessage = (ev) => {
      let msg: { type: string, data?: string, code?: number, message?: string }
      try { msg = JSON.parse(ev.data) } catch { return }

      if (msg.type === 'output' && msg.data != null) {
        // Go []byte JSON'da base64 string olur; coz ve yaz.
        term.write(base64ToBytes(msg.data))
      } else if (msg.type === 'exit') {
        const reason = msg.message ? ` (${msg.message})` : ''
        term.write(`\r\n\x1b[90m— oturum sonlandı, çıkış kodu ${msg.code ?? 0}${reason} —\x1b[0m\r\n`)
        try { ws.close(1000, 'shell exit') } catch {}
      }
    }

    // Kullanicinin tus vuruslari -> sunucu.
    term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'input', data: bytesToBase64(enc.encode(d)) }))
      }
    })

    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const handleOpen = () => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      }
      opts?.onOpen?.()
    }

    ws.addEventListener('open', handleOpen)
    ws.addEventListener('close', (ev) => {
      const cev = ev as CloseEvent
      opts?.onClose?.(cev.code, cev.reason)
    })
    ws.addEventListener('error', (ev) => opts?.onError?.(ev))

    if (ws.readyState === WebSocket.OPEN) {
      handleOpen()
    }

    void dec // (dec ileride gerekirse; output zaten byte olarak yaziliyor)
    return ws
  }

  return { connect }
}

// Go, []byte'i JSON'da standart base64 olarak kodlar.
function base64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64)
  const buf = new ArrayBuffer(bin.length)
  const out = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function bytesToBase64(bytes: Uint8Array): string {
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]!)
  return btoa(bin)
}
