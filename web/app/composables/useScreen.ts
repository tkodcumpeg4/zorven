/**
 * Uzak ekran baglantisi. Terminal ile ayni ticket+WS deseni.
 *
 * İki render yolu:
 *   - codec "mjpeg": her kare bir JPEG -> <img>'e cizilir.
 *   - codec "h264" : fragmented-MP4 akisi -> MediaSource SourceBuffer'a eklenir
 *     (donanim encode; cok daha akici). <video> elemani oynatir.
 *
 * Girdi: fare/klavye olaylari 0..1 normalize koordinatla sunucuya, oradan
 * istemciye gider (istemci gercek ekranina olcekler).
 */
export interface ScreenHandle {
  ws: WebSocket
  close: () => void
}

type FrameMsg = {
  type: string
  codec?: string
  data?: string
  width?: number
  height?: number
  /** Uzak ekranin GERCEK boyutu (olceklenmemis). Eski istemcilerde yok. */
  screen_w?: number
  screen_h?: number
  message?: string
}

export function useScreen() {
  const { key } = useAdminKey()

  async function connect(
    clientID: string,
    opts: {
      img: HTMLImageElement
      video: HTMLVideoElement
      onCodec: (codec: string) => void
      onError: (msg: string) => void
      onOpen: () => void
      onClose: (code?: number, reason?: string) => void
      mode?: string
      /** Kareyi bu genislige olcekle (0/undefined => tam cozunurluk). */
      maxWidth?: number
      /** Gelen karenin (olceklenmis) boyutu. */
      onFrameSize?: (w: number, h: number) => void
      /**
       * Uzak ekranin GERCEK boyutu. Cozunurluk secenekleri buna gore uretilir;
       * kare boyutu kullanilamaz cunku istemci max_width verilmese bile
       * varsayilan bir sinir uygular (4K ekran 1600x900 gorunurdu).
       */
      onScreenSize?: (w: number, h: number) => void
    },
  ): Promise<ScreenHandle> {
    const headers: Record<string, string> = {}
    if (key.value) {
      headers.Authorization = `Bearer ${key.value}`
    }
    const { ticket } = await $fetch<{ ticket: string }>('/api/v1/screen-ticket', {
      method: 'POST',
      headers,
      body: { client_id: clientID },
    })

    const wsBase = useRuntimeConfig().public.wsBase as string
    let base = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`
    if (wsBase && (import.meta.dev || wsBase.includes('localhost') || wsBase.includes(location.hostname))) {
      base = wsBase.replace(/\/$/, '')
    }
    // Sunucu bu parametreleri dogrudan ScreenOpen'a gecirir (api/screen.go).
    const params = new URLSearchParams({ ticket })
    if (opts.mode) params.set('mode', opts.mode)
    if (opts.maxWidth && opts.maxWidth > 0) params.set('max_width', String(opts.maxWidth))
    const ws = new WebSocket(`${base}/api/v1/clients/${clientID}/screen?${params}`)

    // --- H.264 (MSE) durumu ---
    let mediaSource: MediaSource | null = null
    let sourceBuffer: SourceBuffer | null = null
    const pending: Uint8Array<ArrayBuffer>[] = []
    let codec = ''

    function pumpMSE() {
      if (!sourceBuffer || sourceBuffer.updating || pending.length === 0) return
      const chunk = pending.shift()!
      try {
        sourceBuffer.appendBuffer(chunk as unknown as BufferSource)
      } catch {
        // Quota vb.; en eski veriyi at ve devam et.
        try { sourceBuffer.remove(0, Math.max(0, opts.video.currentTime - 5)) } catch { /* yok say */ }
      }
    }

    function startH264() {
      // MSE codec dizesi: baseline profil (client ffmpeg -profile:v baseline).
      const mime = 'video/mp4; codecs="avc1.42E01F"'
      if (!('MediaSource' in window) || !MediaSource.isTypeSupported(mime)) {
        opts.onError('Bu tarayıcı H.264/MSE oynatmayı desteklemiyor.')
        return
      }
      mediaSource = new MediaSource()
      opts.video.src = URL.createObjectURL(mediaSource)
      opts.video.hidden = false
      opts.img.hidden = true

      mediaSource.addEventListener('sourceopen', () => {
        sourceBuffer = mediaSource!.addSourceBuffer(mime)
        sourceBuffer.mode = 'sequence'
        sourceBuffer.addEventListener('updateend', pumpMSE)
        pumpMSE()
      }, { once: true })

      // Otomatik oynat; tarayici bazen kullanici etkilesimi ister.
      opts.video.muted = true
      opts.video.play().catch(() => { /* play() engellenirse kullanici tiklar */ })
    }

    function startMJPEG() {
      opts.img.hidden = false
      opts.video.hidden = true
    }

    ws.addEventListener('open', () => opts.onOpen())
    ws.addEventListener('close', (ev) => {
      const cev = ev as CloseEvent
      opts.onClose(cev.code, cev.reason)
    })
    ws.addEventListener('error', () => opts.onError('Bağlantı hatası'))

    ws.onmessage = (ev) => {
      let msg: FrameMsg
      try { msg = JSON.parse(ev.data) } catch { return }

      if (msg.type === 'error') {
        opts.onError(msg.message || 'Ekran hatası')
        return
      }
      if (msg.type !== 'frame' || msg.data == null) return

      // Olceklenmis kare boyutu (rozette gosterilir).
      if (msg.width && msg.height) opts.onFrameSize?.(msg.width, msg.height)
      // Uzak ekranin gercek boyutu (cozunurluk menusunu besler).
      if (msg.screen_w && msg.screen_h) opts.onScreenSize?.(msg.screen_w, msg.screen_h)

      // Ilk karede codec'e gore render yolunu kur.
      if (!codec) {
        codec = msg.codec || 'mjpeg'
        opts.onCodec(codec)
        if (codec === 'h264') startH264()
        else startMJPEG()
      }

      const bytes = base64ToBytes(msg.data)

      if (codec === 'h264') {
        pending.push(bytes)
        pumpMSE()
      } else {
        // MJPEG: blob URL olustur, img'e ver. Onceki URL'i serbest birak.
        const blob = new Blob([bytes as unknown as BlobPart], { type: 'image/jpeg' })
        const url = URL.createObjectURL(blob)
        const prev = opts.img.dataset.blobUrl
        opts.img.onload = () => { if (prev) URL.revokeObjectURL(prev) }
        opts.img.src = url
        opts.img.dataset.blobUrl = url
      }
    }

    if (ws.readyState === WebSocket.OPEN) {
      opts.onOpen()
    }

    function close() {
      try { ws.close() } catch { /* yok say */ }
      if (mediaSource && mediaSource.readyState === 'open') {
        try { mediaSource.endOfStream() } catch { /* yok say */ }
      }
      const prev = opts.img.dataset.blobUrl
      if (prev) URL.revokeObjectURL(prev)
    }

    return { ws, close }
  }

  /** sendInput, normalize (0..1) koordinatli fare/klavye olayini gonderir. */
  function sendInput(ws: WebSocket, ev: Record<string, unknown>) {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(ev))
    }
  }

  return { connect, sendInput }
}

// base64ToBytes, Go []byte'in JSON base64'unu Uint8Array'e cevirir.
function base64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64)
  const buf = new ArrayBuffer(bin.length)
  const out = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}
