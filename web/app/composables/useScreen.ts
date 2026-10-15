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
  /** Aktif mod; sunucunun ilk "mode" mesajindan sonra dolar. */
  mode: () => string
  /** screen2: sunucudan tam kare iste (en fazla saniyede bir). */
  requestKeyframe: () => void
  /** screen2: canli ayar (sifir/eksik alanlar degismez). */
  sendSettings: (s: { fps?: number, quality?: number, max_width?: number, display?: number, layer?: 'auto' | 'high' | 'low' }) => void
  /** screen2: kendi salt-izleme durumunu sunucuda ayarla (sunucu girdiyi dusurur). */
  sendViewOnly: (value: boolean) => void
  /** screen2: metni uzak makinenin panosuna gonder (yalniz kontrolcu; sunucu zorlar). */
  sendClipboard: (text: string) => void
}

/** Uzak imlec konumu: x/y tuval (olceklenmis kare) pikselinde, w/h o andaki tuval boyutu. */
export interface ScreenCursorPos { x: number, y: number, visible: boolean, w: number, h: number }

/** Uzak imlec bicimi: RGBA, hotspot bicim piksel uzayinda. */
export interface ScreenCursorShape { w: number, h: number, hotX: number, hotY: number, rgba: Uint8Array<ArrayBuffer> }

/**
 * Katmanli yayinda bu izleyicinin KENDI etkin durumu (yalniz kendisine gider).
 * layer: high | low; auto: katmani sunucu seciyor; fps: kendi kare hizi tavani.
 */
export interface ScreenViewerState { layered: boolean, layer: 'high' | 'low', auto: boolean, fps: number }

/** Sunucunun bildirdigi yayin yetenekleri (istemci pazarligina gore). */
export interface ScreenCaps { cursor: boolean, clipboard: boolean, files: boolean }

/** Dosya aktarimi durumu (izleyiciye ozel): token yalniz kontrolcuye verilir. */
export interface ScreenFilesState { enabled: boolean, controller: boolean, token: string }

type FrameMsg = {
  type: string
  codec?: string
  data?: string
  width?: number
  height?: number
  /** Uzak ekranin GERCEK boyutu (olceklenmemis). Eski istemcilerde yok. */
  screen_w?: number
  screen_h?: number
  seq?: number
  capture_ms?: number
  encode_ms?: number
  message?: string
  /** mode mesaji: screen2 | mjpeg | h264. */
  mode?: string
  /** input_status mesajlari: girdi uygulama durumu. */
  state?: string
  code?: number
  detail?: string
  /** viewers / view_only / settings_changed mesajlari. */
  count?: number
  controllers?: number
  value?: boolean
  locked?: boolean
  fps?: number
  quality?: number
  max_width?: number
  display?: number
  /** caps / cursor / cursor_shape / clipboard / clipboard_status mesajlari. */
  cursor?: boolean
  clipboard?: boolean
  files?: boolean
  /** files mesaji: dosya aktarimi etkin mi / kontrolcu mu / HTTP belirteci. */
  enabled?: boolean
  controller?: boolean
  token?: string
  x?: number
  y?: number
  visible?: boolean
  w?: number
  h?: number
  layered?: boolean
  layer?: string
  auto?: boolean
  hot_x?: number
  hot_y?: number
  text?: string
  ok?: boolean
  reason?: string
}

/** Paylasimli yayinin izleyici sayilari. */
export interface ScreenViewers { count: number, controllers: number }

/** Sunucunun bildirdigi yayin ayari (son yazan kazanir; tum izleyicilere gider). */
export interface ScreenSettingsChanged { fps: number, quality: number, max_width: number, display: number }

/**
 * Uzak girdinin gercekten uygulanip uygulanmadigi (istemciden gelir).
 * state: ok | blocked | unsupported | no_session | error | unknown
 */
export interface ScreenInputStatus {
  state: string
  code?: number
  detail?: string
}

/**
 * Akis metrikleri. Saniyede bir yayinlanir.
 *
 * NEDEN GECIKME (latency) YOK: tek yonlu gecikmeyi olcmek istemci ile tarayici
 * saatlerinin senkron olmasini gerektirir; degiller. Onun yerine GERCEKTEN
 * olcebildiklerimizi veriyoruz — varis hizi, kare araligi ve kayip — cunku
 * uydurma bir gecikme sayisi hic sayi olmamasindan kotudur.
 */
export interface ScreenStats {
  /** Son saniyede gelen kare sayisi. */
  fps: number
  /** Son saniyedeki veri hizi (kilobit/sn). */
  kbps: number
  /** Kumulatif: sunucunun dusurdugu kare (seq atlamalarindan sayilir). */
  dropped: number
  /** Kumulatif: alinan kare. */
  frames: number
  /** Uzak makinede son karenin yakalanma suresi (ms). 0 => bildirilmedi. */
  captureMs: number
  /** Uzak makinede son karenin kodlanma suresi (ms). */
  encodeMs: number
  /** Son penceredeki ortalama kare araligi (ms). */
  intervalMs: number
  /** Son penceredeki EN KOTU kare araligi (ms) — takilmayi bu gosterir. */
  maxIntervalMs: number
  /** Son tamamlanan karenin toplam bayti (screen2: tum dikdortgenler). */
  lastFrameBytes: number
}

export function useScreen() {
  const { key } = useAdminKey()

  async function connect(
    clientID: string,
    opts: {
      img: HTMLImageElement
      video: HTMLVideoElement
      /** screen2 tuvali. */
      canvas?: HTMLCanvasElement
      /** Sunucunun sectigi mod: screen2 | mjpeg | h264. */
      onMode?: (mode: string) => void
      onCodec: (codec: string) => void
      onError: (msg: string) => void
      onOpen: () => void
      onClose: (code?: number, reason?: string) => void
      mode?: string
      /** JPEG kalite tavani (screen2; 0/undefined => otomatik). */
      quality?: number
      /** Saniyedeki kare hedefi (0/undefined => sunucu varsayilani). */
      fps?: number
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
      /** Akis metrikleri; saniyede bir cagrilir. */
      onStats?: (s: ScreenStats) => void
      /** Girdi durumu (ilk girdide ve degisince gelir; eski istemcide "unknown"). */
      onInputStatus?: (s: ScreenInputStatus) => void
      /** Izleyici sayisi (screen2 paylasimli yayin). */
      onViewers?: (v: ScreenViewers) => void
      /** Sunucu salt-izleme durumunu bildirdi; locked=true => kullanici acamaz. */
      onViewOnly?: (value: boolean, locked: boolean) => void
      /** Baska bir izleyici (ya da biz) yayin ayarini degistirdi. */
      onSettingsChanged?: (s: ScreenSettingsChanged) => void
      /** Bilet yanitindaki control_allowed (rol: yalniz owner/admin denetler); WS acilmadan once cagrilir. */
      onControlAllowed?: (allowed: boolean) => void
      /** Baglantida salt-izleme iste. */
      viewOnly?: boolean
      /** Katmanli yayinda kendi etkin katmanin / fps tavanin (degisince gelir). */
      onViewerState?: (s: ScreenViewerState) => void
      /** Yayin yetenekleri (imlec katmani / pano). */
      onCaps?: (c: ScreenCaps) => void
      /** Dosya aktarimi durumu (katilimda ve salt-izleme degisince gelir). */
      onFiles?: (f: ScreenFilesState) => void
      /** Uzak imlec konumu (en fazla ~30/sn). */
      onCursor?: (c: ScreenCursorPos) => void
      /** Uzak imlec bicimi (yalniz bicim degisince). */
      onCursorShape?: (s: ScreenCursorShape) => void
      /** Uzak makinenin panosu degisti (yalniz kontrolcuye gelir). */
      onClipboard?: (text: string) => void
      /** Pano gonderiminin sonucu: ok=false iken reason: view_only|unsupported|too_large|invalid. */
      onClipboardStatus?: (ok: boolean, reason: string) => void
    },
  ): Promise<ScreenHandle> {
    const headers: Record<string, string> = {}
    if (key.value) {
      headers.Authorization = `Bearer ${key.value}`
    }
    const { ticket, control_allowed: controlAllowed } = await $fetch<{ ticket: string, control_allowed?: boolean }>('/api/v1/screen-ticket', {
      method: 'POST',
      headers,
      body: { client_id: clientID },
    })
    opts.onControlAllowed?.(controlAllowed !== false)
    // Yetkisiz (uye) rol: sunucu zaten kilitli acar; istemci de salt-izleme ister.
    if (controlAllowed === false) opts.viewOnly = true

    const wsBase = useRuntimeConfig().public.wsBase as string
    let base = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`
    if (wsBase && (import.meta.dev || wsBase.includes('localhost') || wsBase.includes(location.hostname))) {
      base = wsBase.replace(/\/$/, '')
    }
    // Sunucu bu parametreleri dogrudan ScreenOpen'a gecirir (api/screen.go).
    const params = new URLSearchParams({ ticket })
    if (opts.mode) params.set('mode', opts.mode)
    if (opts.viewOnly) params.set('view_only', '1')
    if (opts.quality && opts.quality > 0) params.set('quality', String(opts.quality))
    if (opts.fps && opts.fps > 0) params.set('fps', String(opts.fps))
    if (opts.maxWidth && opts.maxWidth > 0) params.set('max_width', String(opts.maxWidth))
    const ws = new WebSocket(`${base}/api/v1/clients/${clientID}/screen?${params}`)
    ws.binaryType = 'arraybuffer'

    // --- Metrikler ---
    //
    // Sunucu tikaninca kare dusurur (bkz. tunnel.Session). Kareler istemcide
    // 1'den baslayarak ARTAN seq tasidigi icin atlamalari sayarak kaybi
    // tarayicida tam olarak olcebiliyoruz — tahmin yok.
    let frames = 0
    let dropped = 0
    let lastSeq = 0
    let winFrames = 0
    let winBytes = 0
    let lastFrameAt = 0
    let winIntervalSum = 0
    let winIntervalMax = 0
    let lastCaptureMs = 0
    let lastEncodeMs = 0
    let lastFrameBytes = 0

    const statsTimer = opts.onStats
      ? setInterval(() => {
          opts.onStats!({
            fps: winFrames,
            kbps: Math.round((winBytes * 8) / 1000),
            dropped,
            frames,
            captureMs: lastCaptureMs,
            encodeMs: lastEncodeMs,
            intervalMs: winFrames > 1 ? Math.round(winIntervalSum / (winFrames - 1)) : 0,
            maxIntervalMs: Math.round(winIntervalMax),
            lastFrameBytes,
          })
          winFrames = 0
          winBytes = 0
          winIntervalSum = 0
          winIntervalMax = 0
        }, 1000)
      : null

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

    // --- screen2 (canvas, karo tabanli) ---
    let activeMode = ''
    let ctx2d: CanvasRenderingContext2D | null = null
    let cw = 0
    let ch = 0
    let haveKey = false // tuval bir anahtar kareyle kuruldu mu
    let lastS2Seq = -1
    let lastKeyReq = 0
    let curFrameBytes = 0
    let closed = false
    // Katmanli yayinda etkin katman: dusuk katman tuvali gercek ekran boyutu degildir.
    let curLayer: 'high' | 'low' = 'high'
    // Cizim sirasi: kod cozme paralel baslar, cizim bu zincirde SIRAYLA olur;
    // yavas bir onceki kare sonrakinin uzerine yazamaz.
    let chain: Promise<void> = Promise.resolve()

    function requestKeyframe() {
      const now = performance.now()
      if (now - lastKeyReq < 1000 || ws.readyState !== WebSocket.OPEN) return
      lastKeyReq = now
      ws.send(JSON.stringify({ type: 'keyframe' }))
    }

    function sendSettings(s: { fps?: number, quality?: number, max_width?: number, display?: number, layer?: 'auto' | 'high' | 'low' }) {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'settings', ...s }))
    }

    function sendViewOnly(value: boolean) {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'view_only', value }))
    }

    function sendClipboard(text: string) {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'clipboard', text }))
    }

    function startScreen2() {
      opts.img.hidden = true
      opts.video.hidden = true
      if (opts.canvas) opts.canvas.hidden = false
    }

    function onBinary(buf: ArrayBuffer) {
      if (activeMode !== 'screen2' || !opts.canvas) return
      const m = parseScreen2(buf)
      if (!m) { requestKeyframe(); return }

      // Boyut degisimi her zaman anahtar karedir.
      const sizeChanged = m.screenW !== cw || m.screenH !== ch
      if (sizeChanged && !m.keyframe) { requestKeyframe(); return }
      if (!haveKey && !(m.keyframe && m.first)) { requestKeyframe(); return }

      // seq boslugu: ayni karenin parcalari ayni seq'i tasir.
      const isKeyStart = m.keyframe && m.first
      if (lastS2Seq >= 0 && m.seq !== lastS2Seq && m.seq !== lastS2Seq + 1) {
        dropped += Math.max(0, m.seq - lastS2Seq - 1)
        if (!isKeyStart) { requestKeyframe(); return }
      }
      lastS2Seq = m.seq
      if (isKeyStart) {
        cw = m.screenW
        ch = m.screenH
        haveKey = true
      }

      // Metrikler
      winBytes += buf.byteLength
      curFrameBytes += buf.byteLength
      if (m.eof) {
        frames++
        winFrames++
        lastFrameBytes = curFrameBytes
        curFrameBytes = 0
        const now = performance.now()
        if (lastFrameAt) {
          const gap = now - lastFrameAt
          winIntervalSum += gap
          if (gap > winIntervalMax) winIntervalMax = gap
        }
        lastFrameAt = now
      }

      const canvas = opts.canvas
      // Kod cozmeyi hemen baslat (paralel), cizimi zincire sirala.
      const decoded = m.rects.map((r) => {
        if (r.enc === ENC_SOLID) return Promise.resolve(null)
        const type = r.enc === ENC_PNG ? 'image/png' : 'image/jpeg'
        return createImageBitmap(new Blob([r.payload as unknown as BlobPart], { type })).catch(() => undefined)
      })
      chain = chain.then(async () => {
        const bitmaps = await Promise.all(decoded)
        if (closed) { for (const b of bitmaps) if (b) b.close(); return }
        if (isKeyStart) {
          if (canvas.width !== m.screenW || canvas.height !== m.screenH) {
            canvas.width = m.screenW
            canvas.height = m.screenH
            ctx2d = null
          }
          opts.onFrameSize?.(m.screenW, m.screenH)
          if (curLayer === 'high') opts.onScreenSize?.(m.screenW, m.screenH)
        }
        if (!ctx2d) ctx2d = canvas.getContext('2d')
        if (!ctx2d) return
        let failed = false
        m.rects.forEach((r, i) => {
          if (r.enc === ENC_SOLID) {
            const p = r.payload
            ctx2d!.clearRect(r.x, r.y, r.w, r.h)
            ctx2d!.fillStyle = `rgba(${p[0]},${p[1]},${p[2]},${(p[3] ?? 255) / 255})`
            ctx2d!.fillRect(r.x, r.y, r.w, r.h)
            return
          }
          const bmp = bitmaps[i]
          if (!bmp) { failed = true; return }
          ctx2d!.drawImage(bmp, r.x, r.y, r.w, r.h)
          bmp.close()
        })
        if (failed) requestKeyframe()
      })
    }

    ws.onmessage = (ev) => {
      if (typeof ev.data !== 'string') {
        onBinary(ev.data as ArrayBuffer)
        return
      }
      let msg: FrameMsg
      try { msg = JSON.parse(ev.data) } catch { return }

      if (msg.type === 'mode') {
        activeMode = msg.mode || ''
        opts.onMode?.(activeMode)
        if (activeMode === 'screen2') {
          opts.onCodec('screen2')
          startScreen2()
        }
        return
      }

      if (msg.type === 'error') {
        opts.onError(msg.message || 'Ekran hatası')
        return
      }
      if (msg.type === 'viewers') {
        opts.onViewers?.({ count: msg.count || 1, controllers: msg.controllers || 0 })
        return
      }
      if (msg.type === 'view_only') {
        opts.onViewOnly?.(!!msg.value, !!msg.locked)
        return
      }
      if (msg.type === 'viewer_state') {
        curLayer = msg.layer === 'low' ? 'low' : 'high'
        opts.onViewerState?.({ layered: !!msg.layered, layer: curLayer, auto: !!msg.auto, fps: msg.fps || 0 })
        return
      }
      if (msg.type === 'settings_changed') {
        opts.onSettingsChanged?.({
          fps: msg.fps || 0, quality: msg.quality || 0, max_width: msg.max_width || 0, display: msg.display || 0,
        })
        return
      }
      if (msg.type === 'caps') {
        opts.onCaps?.({ cursor: !!msg.cursor, clipboard: !!msg.clipboard, files: !!msg.files })
        return
      }
      if (msg.type === 'files') {
        opts.onFiles?.({ enabled: !!msg.enabled, controller: !!msg.controller, token: msg.token || '' })
        return
      }
      if (msg.type === 'cursor') {
        opts.onCursor?.({ x: msg.x || 0, y: msg.y || 0, visible: !!msg.visible, w: msg.w || 0, h: msg.h || 0 })
        return
      }
      if (msg.type === 'cursor_shape') {
        if (msg.data && msg.w && msg.h) {
          const rgba = base64ToBytes(msg.data)
          if (rgba.length === msg.w * msg.h * 4) {
            opts.onCursorShape?.({ w: msg.w, h: msg.h, hotX: msg.hot_x || 0, hotY: msg.hot_y || 0, rgba })
          }
        }
        return
      }
      if (msg.type === 'clipboard') {
        opts.onClipboard?.(msg.text || '')
        return
      }
      if (msg.type === 'clipboard_status') {
        opts.onClipboardStatus?.(!!msg.ok, msg.reason || '')
        return
      }
      if (msg.type === 'input_status') {
        opts.onInputStatus?.({ state: msg.state || 'unknown', code: msg.code, detail: msg.detail })
        return
      }
      if (msg.type !== 'frame' || msg.data == null) return
      if (activeMode === 'screen2') return

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

      // --- Metrik toplama ---
      frames++
      winFrames++
      winBytes += bytes.length
      if (msg.seq) {
        // seq 1'den baslar ve her karede bir artar; aradaki bosluk sunucunun
        // dusurdugu kare sayisidir.
        if (lastSeq && msg.seq > lastSeq + 1) dropped += msg.seq - lastSeq - 1
        lastSeq = msg.seq
      }
      const now = performance.now()
      if (lastFrameAt) {
        const gap = now - lastFrameAt
        winIntervalSum += gap
        if (gap > winIntervalMax) winIntervalMax = gap
      }
      lastFrameAt = now
      if (msg.capture_ms != null) lastCaptureMs = msg.capture_ms
      if (msg.encode_ms != null) lastEncodeMs = msg.encode_ms

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
      closed = true
      if (statsTimer) clearInterval(statsTimer)
      try { ws.close() } catch { /* yok say */ }
      if (mediaSource && mediaSource.readyState === 'open') {
        try { mediaSource.endOfStream() } catch { /* yok say */ }
      }
      const prev = opts.img.dataset.blobUrl
      if (prev) URL.revokeObjectURL(prev)
    }

    return { ws, close, mode: () => activeMode, requestKeyframe, sendSettings, sendViewOnly, sendClipboard }
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
