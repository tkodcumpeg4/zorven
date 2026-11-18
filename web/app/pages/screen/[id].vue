<script setup lang="ts">
import type { ScreenCursorPos, ScreenFilesState, ScreenHandle, ScreenInputStatus, ScreenStats, ScreenViewerState } from '~/composables/useScreen'
import type { AgentStats, BrowserPerf } from '~/lib/screenPerf'
import { STAGE_KEYS, effectiveQuality, findBottleneck, fmtMs, layerTargetFps } from '~/lib/screenPerf'
import type { VideoInfo, VideoPref } from '~/lib/screenVideo'
import { loadVideoPref, saveVideoPref } from '~/lib/screenVideo'
import { clampFps, effectiveFpsLimit, fpsOptions } from '~/lib/screenFps'
import { FileTransferError, MAX_FILE_BYTES } from '~/composables/useScreenFiles'
import type { RemoteFile } from '~/composables/useScreenFiles'
import { DeferredPaste, RECONNECT_MAX_ATTEMPTS, clipboardFits, collectPaste, isPasteCombo, pastedFileName, reconnectDelayMs, shouldAutoRetry } from '~/lib/screenClipboard'
import { isEchoImage, pickPasteImage, pngWithinLimit, remoteImageFileName, renderPngBrowser } from '~/lib/screenImagePaste'
import { RttWindow, formatRtt, isRttStale, rttTone } from '~/lib/screenLatency'
import type { RttAverage } from '~/lib/screenLatency'
import { TouchGestures, ZOOM_IDENTITY, applyPinch, codeForChar, consumeSticky, zoomTransform } from '~/lib/screenTouch'
import type { TouchAction, ZoomState } from '~/lib/screenTouch'

const route = useRoute()
const clientID = route.params.id as string

const { connect, sendInput } = useScreen()
const { openUpgrade, formatBytes } = useBilling()
const { t } = useI18n()

// Istemci Windows servisi mi? Servisler (oturum 0) ekrani yakalayamaz.
const isWinService = ref(false)
const isWinClient = ref(false)
async function loadClientInfo() {
  try {
    const list = await useApi().listClients()
    const c = list.find(x => x.id === clientID)
    isWinClient.value = (c?.os || '').toLowerCase() === 'windows'
    isWinService.value = isWinClient.value && !!c?.is_service
  } catch { /* bilgi alinamadi: bildirim gosterilmez */ }
}
const showServiceNotice = computed(() => isWinService.value || (isWinClient.value && state.value === 'error'))

const img = ref<HTMLImageElement>()
const video = ref<HTMLVideoElement>()
const canvas = ref<HTMLCanvasElement>()
const stage = ref<HTMLDivElement>()

const state = ref<'connecting' | 'open' | 'error' | 'closed'>('connecting')
const errorMsg = ref('')
const codec = ref('')
const control = ref(true) // fare/klavye kontrolu acik mi
const viewOnlyLocked = ref(false) // sunucu zorlamali salt-izleme: kullanici acamaz
const viewers = ref<{ count: number, controllers: number } | null>(null)

// Tam ekran: sahne (uzak ekran) tum ekrani kaplar. Destekleyen tarayicilarda klavye kilidiyle Esc ve
// sistem kisayollari da uzak makineye gider; cikmak icin Esc basili tutulur ya da ustteki dugme kullanilir.
const isFullscreen = ref(false)
const fsBar = ref(false)
let fsBarTimer: ReturnType<typeof setTimeout> | null = null
const fullscreenSupported = ref(false)
function showFsBar(ms = 2500) {
  fsBar.value = true
  if (fsBarTimer) clearTimeout(fsBarTimer)
  // Kisayol menusu aciksa cubuk kapanmaz (menu cubugun icinde).
  fsBarTimer = setTimeout(() => { if (showShortcuts.value) showFsBar(ms); else fsBar.value = false }, ms)
}
// Klavye kilidi durumu (tam ekran): 'locked' => Esc/Alt+Tab/Win uzak makineye gider (Esc basili tutulunca cikilir);
// 'unsupported' => tarayici kilidi desteklemiyor ya da reddetti (Firefox/Safari): Kisayollar menusu kullanilmali.
const kbLock = ref<'pending' | 'locked' | 'unsupported'>('pending')
async function toggleFullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen()
    else await stage.value?.requestFullscreen({ navigationUI: 'hide' })
  } catch { /* tarayici izin vermedi */ }
}
function onFullscreenChange() {
  isFullscreen.value = document.fullscreenElement === stage.value
  const kb = (navigator as any).keyboard
  if (isFullscreen.value) {
    showFsBar(3500)
    // Klavye kilidi (Chromium): Esc, Alt+Tab, Win tusu vb. uzak makineye gider.
    kbLock.value = 'pending'
    try {
      if (typeof kb?.lock !== 'function') kbLock.value = 'unsupported'
      else Promise.resolve(kb.lock()).then(() => { if (isFullscreen.value) kbLock.value = 'locked' }).catch(() => { kbLock.value = 'unsupported' })
    } catch { kbLock.value = 'unsupported' }
    stage.value?.focus()
  } else {
    fsBar.value = false
    try { kb?.unlock?.() } catch { /* desteklenmiyor */ }
  }
  schedulePlaceCursor()
}
function onStagePointerMoveFs(e: PointerEvent) {
  if (isFullscreen.value && e.clientY <= 6) showFsBar()
}

function toggleControl() {
  if (viewOnlyLocked.value) return
  control.value = !control.value
  handle?.sendViewOnly(!control.value)
}

// --- Imlec katmani, pano, kisayollar -----------------------------------------
const caps = ref<{ cursor: boolean, clipboard: boolean, files: boolean, clipboardImage: boolean, displays: number, display: number }>({ cursor: false, clipboard: false, files: false, clipboardImage: false, displays: 1, display: 0 })
// Monitor secimi: son kullanici secimi (-1 = secilmedi). Yeniden baglanmada ayni monitor yeniden istenir.
const wantedDisplay = ref(-1)
let displayRestoreTried = false
function selectDisplay(i: number) {
  if (!handle || i === caps.value.display || i < 0 || i >= caps.value.displays) return
  wantedDisplay.value = i
  handle.sendSettings({ display: i })
}
const showRemoteCursor = ref(true)
const cursorPos = ref<ScreenCursorPos | null>(null)
const cursorShape = ref<{ url: string, w: number, h: number, hotX: number, hotY: number } | null>(null)
// Imlec katmaninin ekrandaki konumu/boyutu (px, sahneye gore); null => gizli.
const cursorStyle = ref<Record<string, string> | null>(null)
let cursorRAF = 0

/** RGBA imlec bicimini PNG data URL'ine cevirir (img olarak ustte cizilir). */
function shapeToUrl(w: number, h: number, rgba: Uint8Array<ArrayBuffer>): string {
  const cv = document.createElement('canvas')
  cv.width = w
  cv.height = h
  const c2 = cv.getContext('2d')
  if (!c2) return ''
  c2.putImageData(new ImageData(new Uint8ClampedArray(rgba), w, h), 0, 0)
  return cv.toDataURL('image/png')
}

/**
 * Uzak imlec konumunu tuvalin EKRANDAKI yerlesimine cevirir. Tuval kutuya
 * "contain" ile sigar (normPoint ile ayni hesap); konum tuval piksel uzayinda
 * gelir, bicim de ayni olcekle cizilir.
 */
function placeCursor() {
  cursorRAF = 0
  const c = canvas.value
  const st = stage.value
  const p = cursorPos.value
  if (!c || !st || !p || !p.visible || !showRemoteCursor.value || !caps.value.cursor || !isScreen2.value) {
    cursorStyle.value = null
    return
  }
  const cr = c.getBoundingClientRect()
  const sr = st.getBoundingClientRect()
  const natW = c.width || cr.width
  const natH = c.height || cr.height
  if (!natW || !natH) { cursorStyle.value = null; return }
  const scale = Math.min(cr.width / natW, cr.height / natH)
  const offX = cr.left - sr.left + (cr.width - natW * scale) / 2
  const offY = cr.top - sr.top + (cr.height - natH * scale) / 2
  const x = offX + (p.x / (p.w || natW)) * natW * scale
  const y = offY + (p.y / (p.h || natH)) * natH * scale
  const sh = cursorShape.value
  const w = sh ? sh.w * scale : 14
  const h = sh ? sh.h * scale : 14
  const hx = sh ? sh.hotX * scale : 0
  const hy = sh ? sh.hotY * scale : 0
  cursorStyle.value = {
    transform: `translate(${(x - hx).toFixed(1)}px, ${(y - hy).toFixed(1)}px)`,
    width: `${w.toFixed(1)}px`,
    height: `${h.toFixed(1)}px`,
  }
}

function schedulePlaceCursor() {
  if (!cursorRAF) cursorRAF = requestAnimationFrame(placeCursor)
}
watch(showRemoteCursor, schedulePlaceCursor)

// Pano
const showClipboard = ref(false)
const clipText = ref('')
const clipAutoSend = ref(true)
const clipStatus = ref<{ ok: boolean, text: string } | null>(null)
const remoteClip = ref('')
const clipUnread = ref(false)
const clipCopied = ref(false)
// Otomatik pano: Ctrl+V'de bu bilgisayarin panosu once uzak makineye gonderilir; uzakta kopyalanan
// metin de bu bilgisayarin panosuna yazilir. Tercih tarayicida saklanir (varsayilan acik).
const AUTOCLIP_KEY = 'zv_screen_autoclip'
const autoClip = ref(true)
function loadAutoClip() { try { autoClip.value = localStorage.getItem(AUTOCLIP_KEY) !== '0' } catch { /* depolama yok */ } }
watch(autoClip, (v) => { try { localStorage.setItem(AUTOCLIP_KEY, v ? '1' : '0') } catch { /* depolama yok */ } })
let lastSentClip = ''
let lastRemoteClip = ''
let clipAck: (() => void) | null = null
const deferredPaste = new DeferredPaste<Record<string, unknown>>()
const clipReadBlocked = ref(false) // tarayici pano okumaya izin vermedi: bir kez bilgi goster

/** Bu bilgisayarin panosunu okur; izin yoksa/desteklenmiyorsa null. */
async function readLocalClipboard(): Promise<string | null> {
  try {
    if (!navigator.clipboard?.readText) return null
    return await navigator.clipboard.readText()
  } catch {
    clipReadBlocked.value = true
    return null
  }
}

/** Metni uzak panoya gonderir ve onayi (clipboard_status) ya da kisa bir sureyi bekler. */
function pushClipboardAndWait(text: string, timeoutMs = 600): Promise<void> {
  return new Promise((resolve) => {
    if (!handle) { resolve(); return }
    const done = () => { if (clipAck === done) clipAck = null; clearTimeout(timer); resolve() }
    const timer = setTimeout(done, timeoutMs)
    clipAck = done
    lastSentClip = text
    handle.sendClipboard(text)
  })
}

// Gorsel pano: yerel -> uzak (Ctrl+V'de panodaki gorsel PNG olarak uzak panoya) ve uzak -> yerel.
const PASTEIMG_KEY = 'zv_screen_pasteimg'
const pasteImages = ref(true)
function loadPasteImages() { try { pasteImages.value = localStorage.getItem(PASTEIMG_KEY) !== '0' } catch { /* depolama yok */ } }
watch(pasteImages, (v) => { try { localStorage.setItem(PASTEIMG_KEY, v ? '1' : '0') } catch { /* depolama yok */ } })
// Uzaktan gelen son gorsel (onizleme + kopyala + indir). Nesne URL'leri degisince/ayrilinca serbest birakilir.
const remoteImage = ref<{ url: string, blob: Blob, name: string } | null>(null)
const imgCopied = ref(false)
let lastRemoteImage: { size: number, at: number } | null = null
let clipImageAck: ((r: { ok: boolean, reason: string }) => void) | null = null
function setRemoteImage(png: Blob) {
  if (remoteImage.value) URL.revokeObjectURL(remoteImage.value.url)
  remoteImage.value = { url: URL.createObjectURL(png), blob: png, name: remoteImageFileName(new Date()) }
  imgCopied.value = false
  lastRemoteImage = { size: png.size, at: Date.now() }
}

/** Gorseli uzak panoya gonderir; sunucunun sonucunu (clipboard_status image=true) ya da zaman asimini bekler. */
function pushImageAndWait(png: Blob, timeoutMs = 2500): Promise<{ ok: boolean, reason: string }> {
  return new Promise((resolve) => {
    if (!handle) { resolve({ ok: false, reason: 'closed' }); return }
    const done = (r: { ok: boolean, reason: string }) => { if (clipImageAck === done) clipImageAck = null; clearTimeout(timer); resolve(r) }
    // Onay gelmezse (eski sunucu) gonderim basarili sayilir: tus yine iletilir.
    const timer = setTimeout(() => done({ ok: true, reason: 'timeout' }), timeoutMs)
    clipImageAck = done
    handle.sendClipboardImage(png)
  })
}

const IMG_REASON_KEYS: Record<string, string> = {
  view_only: 'clipImgViewOnly',
  unsupported: 'clipImgUnsupported',
  too_large: 'clipImgTooLarge',
  invalid: 'clipImgInvalid',
  rate_limited: 'clipImgRateLimited',
}
function imgReasonText(reason: string): string {
  return t('screen.' + (IMG_REASON_KEYS[reason] ?? 'clipImgInvalid'))
}

/** Yapistirilan tek gorseli dosya olarak Zorven klasorune yukler (gorsel panosu yoksa/basarisizsa geri donus). */
function uploadPastedImageAsFile(file: File) {
  if (!canSendFiles.value) {
    showStageNote(t('screen.pasteFilesUnavailable'), 'error', 6000)
    return
  }
  const name = pastedFileName(file.name, file.type, new Date())
  enqueueFiles([name === file.name ? file : new File([file], name, { type: file.type })], false)
  showStageNote(t('screen.pasteFilesSending', { n: 1 }), 'info', 6000)
}

/**
 * Panodaki gorseli PNG'ye cevirip uzak panoya yazar, SONRA bekletilen Ctrl+V'yi iletir (uzak uygulamaya yapissin).
 * Cevrilemez/cok buyukse ya da istemci gorseli kabul etmezse dosya olarak yuklenir.
 */
async function pasteImageToRemote(file: File) {
  if (lastRemoteImage && isEchoImage(lastRemoteImage, file.size, Date.now())) {
    // Uzaktan gelip yerel panoya yazdigimiz gorsel: uzak pano zaten ayni gorseli tasiyor.
    flushDeferredPaste(true)
    return
  }
  const png = await pngWithinLimit(file, renderPngBrowser)
  if (!png) {
    flushDeferredPaste(false)
    uploadPastedImageAsFile(file)
    return
  }
  showStageNote(t('screen.clipImgSending'), 'info', 3000)
  const r = await pushImageAndWait(png)
  if (r.ok) {
    clipStatus.value = { ok: true, text: t('screen.clipImgSent') }
    if (r.reason !== 'timeout') showStageNote(t('screen.clipImgSent'), 'info', 2500)
    flushDeferredPaste(true)
    return
  }
  flushDeferredPaste(false)
  if ((r.reason === 'unsupported' || r.reason === 'too_large') && canSendFiles.value) {
    uploadPastedImageAsFile(file)
    return
  }
  const text = imgReasonText(r.reason)
  clipStatus.value = { ok: false, text }
  showStageNote(text, 'error', 6000)
}

/** Uzak makineden gelen gorsel: onizleme + (otomatik pano aciksa) bu bilgisayarin panosuna yaz. */
function onRemoteClipImage(png: Blob) {
  setRemoteImage(png)
  if (!showClipboard.value) clipUnread.value = true
  if (autoClip.value && typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    navigator.clipboard.write([new ClipboardItem({ 'image/png': png })])
      .then(() => showStageNote(t('screen.clipImgReceivedCopied'), 'info', 3000))
      .catch(() => { showStageNote(t('screen.clipImgReceived'), 'info', 3000) /* odak/izin yok: panelden kopyalanir */ })
  } else {
    showStageNote(t('screen.clipImgReceived'), 'info', 3000)
  }
}

async function copyRemoteImage() {
  if (!remoteImage.value) return
  try {
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': remoteImage.value.blob })])
    imgCopied.value = true
    setTimeout(() => { imgCopied.value = false }, 1500)
  } catch { showStageNote(t('screen.clipImgCopyFailed'), 'error', 4000) }
}

function downloadRemoteImage() {
  const im = remoteImage.value
  if (!im) return
  const a = document.createElement('a')
  a.href = im.url
  a.download = im.name
  document.body.appendChild(a)
  a.click()
  a.remove()
}

// Sahne uzerinde kisa bilgi (tam ekranda da gorunur): pano/dosya aktarimi ve yeniden baglanma.
const stageNote = ref<{ text: string, tone: 'info' | 'error' } | null>(null)
let stageNoteTimer: ReturnType<typeof setTimeout> | null = null
function showStageNote(text: string, tone: 'info' | 'error' = 'info', ms = 4000) {
  stageNote.value = { text, tone }
  if (stageNoteTimer) clearTimeout(stageNoteTimer)
  stageNoteTimer = setTimeout(() => { stageNote.value = null }, ms)
}

let pasteFallbackTimer: ReturnType<typeof setTimeout> | null = null
// Dosya yapistirildiginda Ctrl+V uzak makineye ILETILMEZ (uzak panoda eski icerik yapisirdi);
// bu durumda V tusunun birakilmasi da yutulur.
let swallowPasteKeyup = false

/** Bekletilen yapistir tuslarini uzak makineye iletir (ya da atar). */
function flushDeferredPaste(forward: boolean) {
  if (pasteFallbackTimer) { clearTimeout(pasteFallbackTimer); pasteFallbackTimer = null }
  const evs = deferredPaste.finish()
  if (!forward) {
    // keydown gitmedi: keyup da gitmesin (henuz gelmediyse geldiginde yutulur).
    swallowPasteKeyup = evs.length < 2
    return
  }
  if (!handle) return
  for (const ev of evs) sendInput(handle.ws, ev as any)
}

/** Yapistir olayi gelmediyse (tarayici vermedi) panoyu dogrudan okuyarak devam et. */
async function pasteWithLocalClipboard() {
  const text = await readLocalClipboard()
  if (text !== null && text !== lastSentClip && text !== lastRemoteClip && clipboardFits(text)) {
    await pushClipboardAndWait(text)
  }
  flushDeferredPaste(true)
}

/**
 * Tarayicinin yapistir olayi: bu bilgisayarin panosundaki metin, dosya ve gorselleri verir (izin
 * sorusu olmadan). Metin uzak panoya yazilip Ctrl+V iletilir; dosya ve gorseller dosya aktarimiyla
 * uzak makinedeki Zorven klasorune gonderilir.
 */
function onWindowPaste(e: ClipboardEvent) {
  if (!control.value || !handle || !autoClip.value || !isScreen2.value) return
  const el = e.target as HTMLElement | null
  if (el && /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName) && !el.hasAttribute('data-remote-ime')) return
  const dt = e.clipboardData
  if (!dt) return
  e.preventDefault()
  // Yapistir olayi geldi: "olay gelmedi" yedegi (pasteWithLocalClipboard) devreye girmesin; yanit gelince tus iletilir.
  if (pasteFallbackTimer) { clearTimeout(pasteFallbackTimer); pasteFallbackTimer = null }
  // Tek gorsel + istemci gorsel panosunu destekliyor + ayar acik: PNG olarak uzak panoya, sonra Ctrl+V.
  if (caps.value.clipboardImage && pasteImages.value) {
    const img = pickPasteImage(
      Array.from(dt.files ?? []),
      Array.from(dt.items ?? []).map(i => ({ kind: i.kind, type: i.type, getAsFile: () => i.getAsFile() })),
    )
    if (img) {
      void pasteImageToRemote(img)
      return
    }
  }
  const data = collectPaste(dt.getData('text/plain') || '', Array.from(dt.files ?? []))
  if (data.files.length || data.tooLarge.length || data.dropped) {
    flushDeferredPaste(false)
    if (!canSendFiles.value) {
      showStageNote(t('screen.pasteFilesUnavailable'), 'error', 6000)
      return
    }
    const now = new Date()
    const files = data.files.map((f, i) => {
      const name = pastedFileName(f.name, f.type, new Date(now.getTime() + i * 1000))
      return name === f.name ? f : new File([f], name, { type: f.type })
    })
    if (files.length) enqueueFiles(files, false)
    const parts: string[] = []
    if (files.length) parts.push(t('screen.pasteFilesSending', { n: files.length }))
    if (data.tooLarge.length) parts.push(t('screen.pasteFilesTooLarge', { n: data.tooLarge.length }))
    if (data.dropped) parts.push(t('screen.pasteFilesDropped', { n: data.dropped }))
    showStageNote(parts.join(' '), files.length ? 'info' : 'error', 6000)
    return
  }
  const text = data.text
  if (!caps.value.clipboard || !text || text === lastSentClip || text === lastRemoteClip || !clipboardFits(text)) {
    if (text && !clipboardFits(text)) showStageNote(t('screen.clipboardErrTooLarge'), 'error')
    flushDeferredPaste(true)
    return
  }
  void pushClipboardAndWait(text).then(() => flushDeferredPaste(true))
}

function sendClipboard() {
  if (!handle || !clipText.value) return
  handle.sendClipboard(clipText.value)
}

function onClipPaste() {
  if (!clipAutoSend.value) return
  // paste olayi metin kutusu guncellenmeden once gelir.
  void nextTick(() => setTimeout(sendClipboard, 0))
}

function onClipboardStatus(ok: boolean, reason: string, image = false) {
  if (image) {
    // Gorsel gonderiminin sonucu: bekleyen yapistirma akisi (pushImageAndWait) varsa ona, yoksa dogrudan ekrana.
    if (clipImageAck) { clipImageAck({ ok, reason }); return }
    const text = ok ? t('screen.clipImgSent') : imgReasonText(reason)
    clipStatus.value = { ok, text }
    if (!ok) showStageNote(text, 'error', 5000)
    return
  }
  clipAck?.()
  if (ok) {
    clipStatus.value = { ok: true, text: t('screen.clipboardSent') }
    return
  }
  const keys: Record<string, string> = {
    view_only: 'clipboardErrViewOnly',
    unsupported: 'clipboardErrUnsupported',
    too_large: 'clipboardErrTooLarge',
    invalid: 'clipboardErrInvalid',
  }
  clipStatus.value = { ok: false, text: t('screen.' + (keys[reason] ?? 'clipboardErrInvalid')) }
}

async function copyRemoteClip() {
  try {
    await navigator.clipboard.writeText(remoteClip.value)
    clipCopied.value = true
    setTimeout(() => { clipCopied.value = false }, 1500)
  } catch { /* izin yok / guvensiz baglam: yok say */ }
}

function toggleClipboard() {
  showClipboard.value = !showClipboard.value
  if (showClipboard.value) clipUnread.value = false
}

// Dosya aktarimi (panel <-> uzak makine; izinli klasor: <Indirilenler>/Zorven)
const { upload: uploadFile, list: listFiles, download: downloadFile } = useScreenFiles(clientID)
const MAX_PARALLEL_UPLOADS = 3
const filesState = ref<ScreenFilesState>({ enabled: false, controller: false, token: '' })
const showFiles = ref(false)
const dragOver = ref(false)
const fileInput = ref<HTMLInputElement>()

interface Xfer {
  id: number
  file: File
  loaded: number
  status: 'queued' | 'uploading' | 'done' | 'error' | 'canceled'
  error: string
  savedAs: string
  abort: (() => void) | null
}
const xfers = ref<Xfer[]>([])
let xferSeq = 0

const canSendFiles = computed(() => caps.value.files && filesState.value.controller && !!filesState.value.token)

function fileErrText(code: string): string {
  const known = ['view_only', 'unsupported', 'disabled', 'busy', 'bad_name', 'too_large', 'not_found', 'checksum', 'network', 'invalid_file_token']
  return t('screen.filesErr.' + (known.includes(code) ? code : 'io'))
}

function pumpUploads() {
  const active = xfers.value.filter(x => x.status === 'uploading').length
  let slots = MAX_PARALLEL_UPLOADS - active
  for (const x of xfers.value) {
    if (slots <= 0) break
    if (x.status !== 'queued') continue
    slots--
    x.status = 'uploading'
    const job = uploadFile(x.file, filesState.value.token, (loaded) => { x.loaded = loaded })
    x.abort = job.abort
    job.promise.then((r) => {
      x.status = 'done'
      x.loaded = x.file.size
      x.savedAs = r.name
      if (!showFiles.value) showStageNote(t('screen.pasteFileDone', { name: r.name }))
    }).catch((e: unknown) => {
      const code = e instanceof FileTransferError ? e.code : 'io'
      if (code === 'canceled') {
        x.status = 'canceled'
      } else {
        x.status = 'error'
        x.error = fileErrText(code)
        if (!showFiles.value) showStageNote(`${x.file.name}: ${x.error}`, 'error', 6000)
      }
    }).finally(() => {
      x.abort = null
      pumpUploads()
      if (showFiles.value && !xfers.value.some(i => i.status === 'uploading' || i.status === 'queued')) void refreshRemoteFiles()
    })
  }
}

function enqueueFiles(list: File[], openPanel = true) {
  if (!canSendFiles.value) return
  for (const file of list) {
    const x: Xfer = { id: ++xferSeq, file, loaded: 0, status: 'queued', error: '', savedAs: '', abort: null }
    if (file.size > MAX_FILE_BYTES) {
      x.status = 'error'
      x.error = fileErrText('too_large')
    }
    xfers.value.push(x)
  }
  if (openPanel) showFiles.value = true
  pumpUploads()
}

function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  enqueueFiles(Array.from(input.files ?? []))
  input.value = ''
}

function cancelXfer(x: Xfer) {
  if (x.status === 'queued') x.status = 'canceled'
  else x.abort?.()
}

function clearFinishedXfers() {
  xfers.value = xfers.value.filter(x => x.status === 'queued' || x.status === 'uploading')
}

function hasFiles(e: DragEvent): boolean {
  return !!e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')
}
function onDragOver(e: DragEvent) {
  if (!canSendFiles.value || !hasFiles(e)) return
  e.preventDefault()
  dragOver.value = true
}
function onDragLeave(e: DragEvent) {
  // Sahne icindeki alt ogelere gecis "leave" sayilmasin.
  if (e.relatedTarget instanceof Node && stage.value?.contains(e.relatedTarget)) return
  dragOver.value = false
}
function onDrop(e: DragEvent) {
  dragOver.value = false
  if (!canSendFiles.value || !hasFiles(e)) return
  e.preventDefault()
  const out: File[] = []
  const items = Array.from(e.dataTransfer?.items ?? [])
  const files = Array.from(e.dataTransfer?.files ?? [])
  files.forEach((f, i) => {
    // Klasorler desteklenmez: yalniz dosyalar gonderilir.
    const entry = (items[i] as DataTransferItem & { webkitGetAsEntry?: () => { isFile: boolean } | null } | undefined)?.webkitGetAsEntry?.()
    if (entry && !entry.isFile) return
    out.push(f)
  })
  enqueueFiles(out)
}

// Uzak makineden al: izinli klasordeki dosyalar
const remoteFiles = ref<RemoteFile[]>([])
const remoteDir = ref('')
const filesLoading = ref(false)
const filesListError = ref('')
const downloadError = ref('')

async function refreshRemoteFiles() {
  if (!canSendFiles.value) return
  filesLoading.value = true
  filesListError.value = ''
  try {
    const r = await listFiles(filesState.value.token)
    remoteFiles.value = r.files
    remoteDir.value = r.dir
  } catch (e: unknown) {
    filesListError.value = fileErrText(e instanceof FileTransferError ? e.code : 'io')
  } finally {
    filesLoading.value = false
  }
}

async function downloadRemote(f: RemoteFile) {
  downloadError.value = ''
  try {
    await downloadFile(f.name, filesState.value.token)
  } catch (e: unknown) {
    downloadError.value = fileErrText(e instanceof FileTransferError ? e.code : 'io')
  }
}

function toggleFiles() {
  showFiles.value = !showFiles.value
  if (showFiles.value) void refreshRemoteFiles()
}

watch(() => filesState.value.token, (tok) => {
  // Belirtec gitti (salt-izleme) ya da yenilendi: bekleyenler gonderilemez.
  if (!tok) for (const x of xfers.value) if (x.status === 'queued') x.status = 'canceled'
})

// Kisayollar (tarayicinin yakalayamadigi kombinasyonlar)
const showShortcuts = ref(false)
const sasNotice = ref<{ ok: boolean, text: string } | null>(null)
let sasPending = false
let sasTimer: ReturnType<typeof setTimeout> | undefined

type Combo = { key: string, code: string, ctrl?: boolean, alt?: boolean, shift?: boolean, meta?: boolean }
const SHORTCUTS: { id: string, labelKey: string, combo?: Combo }[] = [
  { id: 'sas', labelKey: 'shortcutCad' },
  { id: 'win', labelKey: 'shortcutWin', combo: { key: 'Meta', code: 'MetaLeft', meta: true } },
  { id: 'alttab', labelKey: 'shortcutAltTab', combo: { key: 'Tab', code: 'Tab', alt: true } },
  { id: 'ctrlesc', labelKey: 'shortcutCtrlEsc', combo: { key: 'Escape', code: 'Escape', ctrl: true } },
  { id: 'altf4', labelKey: 'shortcutAltF4', combo: { key: 'F4', code: 'F4', alt: true } },
  { id: 'wind', labelKey: 'shortcutWinD', combo: { key: 'd', code: 'KeyD', meta: true } },
  { id: 'ctrlshiftesc', labelKey: 'shortcutCtrlShiftEsc', combo: { key: 'Escape', code: 'Escape', ctrl: true, shift: true } },
  { id: 'printscreen', labelKey: 'shortcutPrintScreen', combo: { key: 'PrintScreen', code: 'PrintScreen' } },
]

function sendShortcut(id: string) {
  showShortcuts.value = false
  if (!handle || !control.value) return
  if (id === 'sas') {
    sendInput(handle.ws, { kind: 'sas' })
    sasPending = true
    sasNotice.value = null
    clearTimeout(sasTimer)
    // Basari sessizdir (durum degismezse mesaj gelmez); hata bu sure icinde gelir.
    sasTimer = setTimeout(() => { sasPending = false }, 3000)
    return
  }
  const sc = SHORTCUTS.find(s => s.id === id)
  if (!sc?.combo) return
  const m = { ctrl: !!sc.combo.ctrl, alt: !!sc.combo.alt, shift: !!sc.combo.shift, meta: !!sc.combo.meta }
  sendInput(handle.ws, { kind: 'keydown', key: sc.combo.key, code: sc.combo.code, repeat: false, ...m })
  sendInput(handle.ws, { kind: 'keyup', key: sc.combo.key, code: sc.combo.code, repeat: false, ...m })
}

// Uzak makinede goruntu/girdi kisitlamasi (guvenli masaustu, yakalama yeniden
// deneme, pano siniri). Girdi durumu rozetinden AYRI tutulur.
const degraded = ref('')
let degradedTimer: ReturnType<typeof setTimeout> | undefined
const DEGRADED_KEYS: Record<string, string> = {
  secure_desktop: 'screen.degradedSecureDesktop',
  capture_retry: 'screen.degradedCaptureRetry',
  clipboard_too_large: 'screen.degradedClipboardTooLarge',
}
const degradedText = computed(() => (degraded.value && DEGRADED_KEYS[degraded.value]) ? t(DEGRADED_KEYS[degraded.value]!) : '')
function onDegraded(detail: string) {
  clearTimeout(degradedTimer)
  degraded.value = detail
  // Pano uyarisi bir olaydir (kalici durum degil): kendiliginden kapanir.
  if (detail === 'clipboard_too_large') degradedTimer = setTimeout(() => { degraded.value = '' }, 8000)
}

function onInputStatusMsg(s: ScreenInputStatus) {
  if (s.state === 'degraded') {
    onDegraded(s.detail || '')
    return
  }
  inputStatus.value = s
  if (sasPending && s.state !== 'ok') {
    sasPending = false
    sasNotice.value = { ok: false, text: t('screen.sasFailed', { detail: s.detail || s.state }) }
  }
}

// --- Akis metrikleri --------------------------------------------------------
const stats = ref<ScreenStats | null>(null)
// Uzak girdinin uygulanma durumu; null => henuz girdi gonderilmedi / bilgi yok.
const inputStatus = ref<ScreenInputStatus | null>(null)
const inputBadge = computed(() => {
  const st = inputStatus.value?.state
  if (!st) return null
  const map: Record<string, { key: string, tone: string, icon: string }> = {
    ok: { key: 'inputOk', tone: 'text-accent', icon: 'lucide:check' },
    blocked: { key: 'inputBlocked', tone: 'text-danger', icon: 'lucide:shield-alert' },
    unsupported: { key: 'inputUnsupported', tone: 'text-warn', icon: 'lucide:eye' },
    no_session: { key: 'inputNoSession', tone: 'text-danger', icon: 'lucide:monitor-off' },
    error: { key: 'inputError', tone: 'text-danger', icon: 'lucide:triangle-alert' },
    unknown: { key: 'inputUnknown', tone: 'text-fg-subtle', icon: 'lucide:circle-help' },
  }
  return map[st] ?? map.unknown
})
const showStats = ref(false)

// --- Gecikme rozeti: son 5 olcumun ortalamasi (panel<->sunucu + sunucu<->istemci), 6 sn olcum gelmezse soluk ---
const rttWindow = new RttWindow()
const rtt = ref<RttAverage | null>(null)
const rttAt = ref<number | null>(null)
const rttNow = ref(Date.now())
let rttClock: ReturnType<typeof setInterval> | null = null
function onRttSample(ms: number, agentMs?: number) {
  rtt.value = rttWindow.push(ms, agentMs)
  rttAt.value = Date.now()
  rttNow.value = Date.now()
}
function resetRtt() { rttWindow.reset(); rtt.value = null; rttAt.value = null }
const rttStale = computed(() => isRttStale(rttNow.value, rttAt.value))
const rttView = computed(() => {
  const r = rtt.value
  if (!r) return null
  const tone = rttTone(r.total)
  return {
    text: formatRtt(r.total),
    cls: tone === 'good' ? 'text-accent' : tone === 'fair' ? 'text-warn' : 'text-danger',
    title: `${t('screen.rttTitle', { ms: Math.round(r.total) })} · ${t('screen.rttBrowser', { ms: Math.round(r.ms) })} · ${r.agentMs === undefined ? t('screen.rttAgentNone') : t('screen.rttAgent', { ms: Math.round(r.agentMs) })}${rttStale.value ? ' · ' + t('screen.rttStale') : ''}`,
  }
})

// --- Performans katmani (varsayilan kapali; tercih localStorage'da) ---------
// H.264 video modu (screen2_video): tercih + durum (bkz. lib/screenVideo.ts).
const videoMode = ref<VideoPref>('auto')
const videoInfo = ref<VideoInfo | null>(null)
watch(videoMode, (m) => { saveVideoPref(m); handle?.sendVideoMode(m) })

const PERF_KEY = 'zorven.screen.perf'
const showPerf = ref(false)
const agentStats = ref<AgentStats | null>(null)
function loadPerfPref() {
  try { showPerf.value = localStorage.getItem(PERF_KEY) === '1' } catch { /* yok say */ }
}
function togglePerf() {
  showPerf.value = !showPerf.value
  try { localStorage.setItem(PERF_KEY, showPerf.value ? '1' : '0') } catch { /* yok say */ }
}
const browserPerf = computed<BrowserPerf | null>(() => {
  const s = stats.value
  if (!s) return null
  return { recvFps: s.fps, drawFps: s.drawFps, decodeMs: s.decodeMs, queueDepth: s.queueDepth, recvKBps: Math.round(s.kbps / 8) }
})
const bottleneck = computed(() => findBottleneck(agentStats.value, browserPerf.value))
const perfInterval = computed(() => 1000 / Math.max((agentStats.value ? layerTargetFps(agentStats.value) : 0) || 30, 1))
const perfQuality = computed(() => (agentStats.value ? effectiveQuality(agentStats.value) : null))
const perfTargetFps = computed(() => (agentStats.value ? layerTargetFps(agentStats.value) : 0))
function stageBar(avg: number): string {
  return Math.min(100, Math.round((avg / perfInterval.value) * 100)) + '%'
}
// Katmanli yayin: bu izleyicinin kendi etkin durumu (null => eski yayin, ortak ayar).
const viewerState = ref<ScreenViewerState | null>(null)
const layerLabel = computed(() => {
  const s = viewerState.value
  if (!s) return ''
  const name = s.layer === 'low' ? t('screen.layerLow') : t('screen.layerHigh')
  return s.auto ? `${t('screen.layerAuto')}: ${name}` : name
})

// screen2 kalite on ayari: 0 => otomatik (istemci uyarlar), digerleri JPEG kalitesi TAVANI.
const qualityPreset = ref<'auto' | 'high' | 'low'>('auto')
const QUALITY_VALUES = { auto: 0, high: 90, low: 40 } as const
const isScreen2 = computed(() => codec.value === 'screen2')
const kbPerSec = computed(() => (stats.value ? Math.round(stats.value.kbps / 8) : 0))
const lastFrameKB = computed(() => (stats.value ? (stats.value.lastFrameBytes / 1024).toFixed(1) : '0'))

/**
 * Akis saglikli mi?
 *
 * "Saglikli" tanimini tek bir esige indirgemiyoruz: kare kaybi VEYA hedefin
 * cok altinda bir kare araligi, kullanicinin gordugu takilmanin iki ayri
 * sebebidir. Rozet rengi bunu ozetler, panel ayrintiyi verir.
 */
const statsTone = computed(() => {
  const s = stats.value
  if (!s || !s.fps) return 'text-fg-subtle'
  if (s.dropped > 0 || s.maxIntervalMs > 1000) return 'text-danger'
  if (s.maxIntervalMs > 500) return 'text-warn'
  return 'text-accent'
})

let handle: ScreenHandle | null = null

// --- Cozunurluk secimi ------------------------------------------------------
//
// Uzak ekranin GERCEK boyutunu ilk baglantidan ogreniyoruz (max_width vermeden
// baglaniyoruz, yani kareler tam cozunurlukte geliyor). Sonrasinda ayni EN-BOY
// ORANINI koruyan secenekler sunuluyor; secim yalnizca genislik olarak sunucuya
// gidiyor (ScreenOpen.max_width) ve istemci orani koruyarak olcekliyor.

const nativeW = ref(0) // uzak ekranin gercek genisligi
const nativeH = ref(0)
const frameW = ref(0) // su an gelen karenin boyutu
const frameH = ref(0)
const selectedWidth = ref(0) // 0 => tam cozunurluk
const selectedFps = ref(30) // hedef kare hizi (varsayilan 30; 60 yalniz acikca secilir, ajan agda otomatik dusurur)
// Kare hizi tavani: biletteki plan degeri (Team/Enterprise 60, digerleri 30) ile WS'teki
// caps.max_fps'in (plan + ajan yetenegi; eski ajanda 30) asgarisi. Sunucu zaten zorlar;
// 45/60 yalniz tavan izin veriyorsa listelenir.
const planFps = ref(30)
const capsFps = ref<number | undefined>(undefined)
const fpsLimit = computed(() => effectiveFpsLimit(planFps.value, capsFps.value))
const fpsList = computed(() => fpsOptions(fpsLimit.value))
watch(fpsLimit, (lim) => { selectedFps.value = clampFps(selectedFps.value, lim) })

// Yaygin en-boy oranlari icin standart yukseklik merdivenleri.
const RATIO_LADDERS = [
  { name: '16:9', ratio: 16 / 9, heights: [2160, 1440, 1080, 900, 720, 540, 480, 360] },
  { name: '16:10', ratio: 16 / 10, heights: [1600, 1200, 1050, 900, 800, 600, 480] },
  { name: '4:3', ratio: 4 / 3, heights: [1536, 1200, 1024, 768, 600, 480] },
  { name: '21:9', ratio: 21 / 9, heights: [1440, 1080, 900, 720, 540] },
  { name: '3:2', ratio: 3 / 2, heights: [1600, 1280, 1080, 900, 720, 540] },
]

const aspect = computed(() => (nativeH.value ? nativeW.value / nativeH.value : 0))

/** Native orana en yakin standart merdiven. */
const closestLadder = computed(() => {
  let best = RATIO_LADDERS[0]!
  for (const l of RATIO_LADDERS) {
    if (Math.abs(l.ratio - aspect.value) < Math.abs(best.ratio - aspect.value)) best = l
  }
  return best
})

/** Oran etiketi: standart bir orana yakinsa adi, degilse ham oran. */
const ratioLabel = computed(() => {
  if (!aspect.value) return ''
  const l = closestLadder.value
  return Math.abs(l.ratio - aspect.value) < 0.06 ? l.name : aspect.value.toFixed(2) + ':1'
})

/**
 * Native orani KORUYAN cozunurluk secenekleri (native dahil, azalan sirada).
 *
 * Native secenegin degeri 0 DEGIL, gercek genisliktir: istemci max_width=0
 * geldiginde kendi VARSAYILAN sinirini (1600) uygular, yani 0 gondermek 4K
 * ekranda tam cozunurluk vermezdi.
 */
const options = computed(() => {
  if (!nativeW.value || !nativeH.value) return []
  const list: { w: number, h: number, label: string }[] = []
  const push = (rawW: number, native = false) => {
    // Cift sayiya yuvarla: H.264 encoder'lari tek boyut sevmez.
    const w = Math.round(rawW / 2) * 2
    if (w <= 160 || w > nativeW.value) return
    if (list.some(o => o.w === w)) return
    const h = native ? nativeH.value : Math.round(w / aspect.value / 2) * 2
    list.push({ w, h, label: native ? `Tam · ${w} × ${h}` : `${w} × ${h}` })
  }

  push(nativeW.value, true)

  const l = closestLadder.value
  if (Math.abs(l.ratio - aspect.value) < 0.06) {
    // Standart oran: bilinen yukseklikleri GERCEK orana gore genislige cevir.
    for (const h of l.heights) push(h * aspect.value)
  } else {
    // Sira disi oran (or. birlestirilmis monitorler): native'in yuzdeleri.
    for (const p of [0.75, 0.6, 0.5, 0.4, 0.33, 0.25]) push(nativeW.value * p)
  }

  // Su an akan genislik listede yoksa ekle; aksi halde secim kutusu bos gorunur
  // (istemcinin varsayilan siniri merdivene denk gelmeyebilir).
  if (frameW.value) push(frameW.value)

  return list.sort((a, b) => b.w - a.w)
})

// Olayin ekran uzerindeki NORMALIZE (0..1) konumunu hesapla.
// Goruntu, kutuya "contain" ile sigdigi icin gercek resim alanini buluyoruz;
// kenar bosluklarina denk gelen tiklamalar yok sayilir.
function normPoint(e: { clientX: number, clientY: number }): { x: number, y: number } | null {
  const el = isScreen2.value ? canvas.value : (codec.value === 'h264' ? video.value : img.value)
  if (!el) return null
  const rect = el.getBoundingClientRect()

  // screen2: tuval boyutu (olceklenmis kare) kaynak boyuttur; girdi yine
  // normalize (0..1) gittigi icin olcekleme istemci tarafinda sorun yaratmaz.
  let natW: number
  let natH: number
  if (isScreen2.value) {
    natW = canvas.value?.width || rect.width
    natH = canvas.value?.height || rect.height
  } else if (codec.value === 'h264') {
    natW = video.value?.videoWidth || rect.width
    natH = video.value?.videoHeight || rect.height
  } else {
    natW = img.value?.naturalWidth || rect.width
    natH = img.value?.naturalHeight || rect.height
  }

  // contain: goruntunun kutudaki gercek yerlesimi
  const scale = Math.min(rect.width / natW, rect.height / natH)
  const dispW = natW * scale
  const dispH = natH * scale
  const offX = rect.left + (rect.width - dispW) / 2
  const offY = rect.top + (rect.height - dispH) / 2

  const x = (e.clientX - offX) / dispW
  const y = (e.clientY - offY) / dispH
  if (x < 0 || x > 1 || y < 0 || y > 1) return null
  return { x, y }
}

/** Olaydaki modifier durumu — her olayda gonderilir (bkz. protocol.ScreenInput). */
function mods(e: MouseEvent | KeyboardEvent | WheelEvent) {
  return { ctrl: e.ctrlKey, shift: e.shiftKey, alt: e.altKey, meta: e.metaKey }
}

/**
 * Bekleyen fare hareketi.
 *
 * NEDEN: tarayici saniyede 60-125 pointermove uretir ve her biri ayri bir JSON
 * mesaji olarak WS'e gidiyordu. Bu hem bagi doldurup kontrolu takiyordu hem de
 * uzak tarafta ayni sayida syscall demekti. Artik kareye BIR hareket gonderilir;
 * ara konumlar zaten goze gorunmez.
 */
let pendingMove: { x: number, y: number, button: number, ctrl: boolean, shift: boolean, alt: boolean, meta: boolean } | null = null
let moveRAF = 0

/**
 * Goruntu icindeki SON gecerli konum.
 *
 * Surukleyip sahne disinda birakildiginda pointerup'in normalize konumu
 * hesaplanamaz; mouseup'i dusurmek fareyi uzak makinede basili birakir, (0,0)
 * gondermek de imleci sol ust koseye firlatir. Ikisi de yanlis — son bilinen
 * konumu kullanmak dogrusu.
 */
let lastPoint = { x: 0, y: 0 }

function flushMove() {
  moveRAF = 0
  if (!pendingMove || !handle) return
  sendInput(handle.ws, { kind: 'mousemove', ...pendingMove })
  pendingMove = null
}

function onPointerMove(e: PointerEvent) {
  if (e.pointerType === 'touch') { onTouchPointer('move', e); return }
  if (!control.value || !handle) return
  const p = normPoint(e)
  if (!p) return
  e.preventDefault()
  lastPoint = p
  pendingMove = { x: p.x, y: p.y, button: 0, ...mods(e) }
  if (!moveRAF) moveRAF = requestAnimationFrame(flushMove)
}

function onPointerDown(e: PointerEvent) {
  if (e.pointerType === 'touch') { onTouchPointer('down', e); return }
  if (!control.value || !handle) return
  const p = normPoint(e)
  if (!p) return
  e.preventDefault()
  // Pointer capture: fare sahne DISINA ciksa da olaylar gelmeye devam eder.
  // Olmadan surukleme (pencere tasima, metin secme) yarida kopuyordu.
  try { (e.currentTarget as Element).setPointerCapture(e.pointerId) } catch { /* destegi yoksa yok say */ }
  focusIme()
  lastPoint = p
  // Bekleyen hareketi once bosalt: tiklama dogru konumda olmali.
  flushMove()
  sendInput(handle.ws, { kind: 'mousedown', x: p.x, y: p.y, button: e.button, ...mods(e) })
}

function onPointerUp(e: PointerEvent) {
  if (e.pointerType === 'touch') { onTouchPointer(e.type === 'pointercancel' ? 'cancel' : 'up', e); return }
  if (!control.value || !handle) return
  const p = normPoint(e)
  e.preventDefault()
  try { (e.currentTarget as Element).releasePointerCapture(e.pointerId) } catch { /* yok say */ }
  if (p) lastPoint = p
  flushMove()
  // Konum sahne disindaysa (surukleyip birakma) SON GECERLI konumu kullan:
  // mouseup'i dusurmek fareyi uzak makinede basili birakir.
  sendInput(handle.ws, {
    kind: 'mouseup',
    x: lastPoint.x,
    y: lastPoint.y,
    button: e.button,
    ...mods(e),
  })
}

function onWheel(e: WheelEvent) {
  if (!control.value || !handle) return
  e.preventDefault()
  // delta_mode olmadan istemci birimi bilemez: deltaMode=1'de deltaY=3 gelir
  // ve ham gonderildiginde neredeyse hic kaydirma olmuyordu.
  sendInput(handle.ws, {
    kind: 'wheel',
    delta_x: e.deltaX,
    delta_y: e.deltaY,
    delta_mode: e.deltaMode,
    ...mods(e),
  })
}

// --- Dokunmatik / mobil ------------------------------------------------------------------------
// Hareket cozumleme lib/screenTouch.ts'tedir (tek dokunus = sol tik, cift dokunus = cift tik, uzun basma = sag tik,
// tek parmak = imleci parmagin altina tasi, iki parmak = tekerlek, kistirma = YEREL yakinlastirma). Fare/kalem yolu degismez.
const isTouchDevice = ref(false)
const touchLock = ref(false) // surukleme kilidi: tek parmak surukleme sol dugme basili gider
const zoom = ref<ZoomState>({ ...ZOOM_IDENTITY })
const isZoomed = computed(() => zoom.value.scale > 1.001)
const zoomStyle = computed(() => {
  const tr = zoomTransform(zoom.value)
  return tr ? { transform: tr } : {}
})
const zoomPct = computed(() => Math.round(zoom.value.scale * 100))
const TOUCH_WHEEL_GAIN = 2 // parmak pikseli -> tekerlek birimi
const touchGestures = new TouchGestures({ dragLock: () => touchLock.value })
let touchTimer: ReturnType<typeof setTimeout> | null = null
let touchHintShown = false

function clearTouchTimer() { if (touchTimer) { clearTimeout(touchTimer); touchTimer = null } }
function armTouchTimer() {
  clearTouchTimer()
  const d = touchGestures.nextDeadline
  if (d === null) return
  touchTimer = setTimeout(() => {
    touchTimer = null
    runTouchActions(touchGestures.tick(performance.now()))
  }, Math.max(0, d - performance.now()))
}
function resetTouch() {
  clearTouchTimer()
  touchGestures.reset()
  zoom.value = { ...ZOOM_IDENTITY }
  sticky.value = { ctrl: false, alt: false }
}
function fitZoom() {
  zoom.value = { ...ZOOM_IDENTITY }
  schedulePlaceCursor()
}
const NO_MODS = { ctrl: false, shift: false, alt: false, meta: false }

function runTouchActions(acts: TouchAction[]) {
  if (!handle || !acts.length) return
  for (const a of acts) {
    if (a.type === 'wheel') {
      sendInput(handle.ws, { kind: 'wheel', delta_x: a.dx * TOUCH_WHEEL_GAIN, delta_y: a.dy * TOUCH_WHEEL_GAIN, delta_mode: 0, ...NO_MODS })
      continue
    }
    if (a.type === 'zoom') {
      const r = stage.value?.getBoundingClientRect()
      if (!r) continue
      zoom.value = applyPinch(zoom.value, { factor: a.factor, cx: a.cx - r.left, cy: a.cy - r.top, panX: a.panX, panY: a.panY }, r.width, r.height)
      schedulePlaceCursor()
      continue
    }
    const p = normPoint({ clientX: a.x, clientY: a.y })
    if (!p) continue
    lastPoint = p
    if (a.type === 'move') {
      pendingMove = { x: p.x, y: p.y, button: 0, ...NO_MODS }
      if (!moveRAF) moveRAF = requestAnimationFrame(flushMove)
      continue
    }
    flushMove() // tiklama dogru konumda olsun
    const button = a.type === 'click' ? (a.button === 'right' ? 2 : 0) : 0
    if (a.type === 'click' || a.type === 'down') sendInput(handle.ws, { kind: 'mousedown', x: p.x, y: p.y, button, ...NO_MODS })
    if (a.type === 'click' || a.type === 'up') sendInput(handle.ws, { kind: 'mouseup', x: p.x, y: p.y, button, ...NO_MODS })
  }
}

function onTouchPointer(kind: 'down' | 'move' | 'up' | 'cancel', e: PointerEvent) {
  if (!isTouchDevice.value) isTouchDevice.value = true
  if (!control.value || !handle) return
  e.preventDefault()
  const t0 = e.timeStamp
  if (kind === 'down') {
    // Sahne disina cikan parmak da izlensin (surukleme yarida kopmasin).
    try { (e.currentTarget as Element).setPointerCapture(e.pointerId) } catch { /* destek yok */ }
    stage.value?.focus({ preventScroll: true })
    if (isFullscreen.value && e.clientY <= 24) showFsBar() // dokunmatikte "ust kenara gel" yok: ust kenara dokununca cubuk acilir
    if (!touchHintShown) { touchHintShown = true; showStageNote(t('screen.touchHelp'), 'info', 6000) }
    runTouchActions(touchGestures.pointerDown(e.pointerId, e.clientX, e.clientY, t0))
    armTouchTimer()
  } else if (kind === 'move') {
    runTouchActions(touchGestures.pointerMove(e.pointerId, e.clientX, e.clientY, t0))
  } else {
    runTouchActions(touchGestures.pointerUp(e.pointerId, t0, kind === 'cancel'))
    armTouchTimer()
  }
}

// Sanal klavye: gizli IME kutusuna odaklanir (yazim "text" olarak gider); ek tus seridi Esc/Tab/oklar/F tuslari icindir.
// Ctrl/Alt "yapiskan": bir sonraki tusa (serit tusu ya da klavyeden yazilan harf) uygulanir.
const showVkb = ref(false)
const vkbFKeys = ref(false)
const sticky = ref({ ctrl: false, alt: false })
const VKB_KEYS: { label: string, key: string, code: string, icon?: string, meta?: boolean }[] = [
  { label: 'Esc', key: 'Escape', code: 'Escape' },
  { label: 'Tab', key: 'Tab', code: 'Tab' },
  { label: 'Win', key: 'Meta', code: 'MetaLeft', meta: true },
  { label: '', key: 'ArrowLeft', code: 'ArrowLeft', icon: 'lucide:arrow-left' },
  { label: '', key: 'ArrowDown', code: 'ArrowDown', icon: 'lucide:arrow-down' },
  { label: '', key: 'ArrowUp', code: 'ArrowUp', icon: 'lucide:arrow-up' },
  { label: '', key: 'ArrowRight', code: 'ArrowRight', icon: 'lucide:arrow-right' },
]
const F_KEYS = Array.from({ length: 12 }, (_, i) => ({ label: `F${i + 1}`, key: `F${i + 1}`, code: `F${i + 1}` }))
function toggleVkb() {
  showVkb.value = !showVkb.value
  if (showVkb.value) focusIme()
  else imeInput.value?.blur()
}
function toggleSticky(k: 'ctrl' | 'alt') {
  sticky.value = { ...sticky.value, [k]: !sticky.value[k] }
  focusIme()
}
/** Ek tus seridinden tek tus: yapiskan Ctrl/Alt uygulanip sifirlanir. */
function tapKey(key: string, code: string, meta = false) {
  if (!handle || !control.value) return
  const { mods, next } = consumeSticky(sticky.value)
  sticky.value = next
  const m = { ctrl: mods.ctrl, alt: mods.alt, shift: false, meta }
  sendInput(handle.ws, { kind: 'keydown', key, code, repeat: false, ...m })
  sendInput(handle.ws, { kind: 'keyup', key, code, repeat: false, ...m })
  focusIme()
}

// Basili tuslar (code -> key): odak kaybinda hepsi birakilir (yalniz modifier degil).
const heldKeys = new Map<string, string>()
// IME bilesimi sirasinda yutulan tuslar: ayni tusun keyup'i da yutulur.
const swallowedCodes = new Set<string>()
let composing = false

function onKey(kind: string, e: KeyboardEvent) {
  if (!control.value || !handle) return
  // Sayfanin kendi form elemanlarinda (cozunurluk secimi) yazarken tuslari
  // uzak makineye gondermeyelim. Istisna: uzak girdi icin gizli IME kutusu.
  const el = e.target as HTMLElement | null
  if (el && /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName) && !el.hasAttribute('data-remote-ime')) return

  // IME bilesimi (Cince/Japonca/Korece, emoji paleti): tuslar bilesime aittir,
  // uzak makineye tek tek GITMEZ; sonuc compositionend'de "text" olarak gider.
  if (e.isComposing || composing || e.key === 'Process' || e.keyCode === 229) {
    if (kind === 'keydown') swallowedCodes.add(e.code)
    return
  }
  if (kind === 'keyup' && swallowedCodes.delete(e.code)) return

  const ev = {
    kind,
    key: e.key,
    // code FIZIKSEL tustur: ozel tuslar ve kisayollar bununla eslenir.
    code: e.code,
    repeat: e.repeat,
    ...mods(e),
  }
  // Otomatik pano: yapistir kisayolunda tarayicinin kendi "paste" olayina izin verilir (varsayilan
  // ENGELLENMEZ); olay bu bilgisayarin panosundaki metni, dosyalari ve gorselleri verir. Tus, pano
  // uzak makineye ulastiktan sonra iletilir (keyup da siradan cikmasin diye bekletilir).
  if (autoClip.value && isScreen2.value && (caps.value.clipboard || caps.value.clipboardImage || canSendFiles.value)) {
    if (kind === 'keydown' && isPasteCombo(e)) {
      heldKeys.set(e.code, e.key)
      if (e.repeat || !deferredPaste.begin(ev)) { e.preventDefault(); return }
      swallowPasteKeyup = false
      // Yapistir olayi gelmezse (bazi tarayicilar duzenlenebilir olmayan alanda vermez) panoyu dogrudan oku.
      pasteFallbackTimer = setTimeout(() => { pasteFallbackTimer = null; if (deferredPaste.active) void pasteWithLocalClipboard() }, 150)
      return
    }
    if (kind === 'keyup' && e.code === 'KeyV') {
      heldKeys.delete(e.code)
      if (deferredPaste.keyup(ev)) { e.preventDefault(); return }
      if (swallowPasteKeyup) { swallowPasteKeyup = false; e.preventDefault(); return }
    }
  }
  // Tarayici kisayollarini (F5, Ctrl+W...) uzak makineye gecirmek icin engelle.
  e.preventDefault()
  if (kind === 'keydown') heldKeys.set(e.code, e.key)
  else heldKeys.delete(e.code)
  sendInput(handle.ws, ev)
}

function onKeyDown(e: KeyboardEvent) {
  // Yerel kisayol: Alt+Shift+P performans katmanini acar/kapatir (uzak makineye gitmez).
  if (e.altKey && e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === 'KeyP') {
    e.preventDefault()
    e.stopPropagation()
    if (!e.repeat) togglePerf()
    return
  }
  if (control.value) onKey('keydown', e)
}
function onKeyUp(e: KeyboardEvent) {
  if (e.altKey && e.shiftKey && e.code === 'KeyP') return
  if (control.value) onKey('keyup', e)
}

/**
 * Odak kaybinda basili modifier'lari birak.
 *
 * NEDEN: Alt+Tab ile baska pencereye gecince keyup olayi BU sayfaya hic
 * gelmez; uzak makinede Alt sonsuza dek basili kalir ve sonraki her tus
 * Alt+<tus> olur. Sekmeden ciktigimizda hepsini acikca birakiyoruz.
 */
function releaseModifiers() {
  if (!handle) return
  // Basili HER tusu birak (yalniz modifier degil): odak degisince keyup gelmez.
  for (const [code, key] of heldKeys) {
    sendInput(handle.ws, { kind: 'keyup', key, code, ctrl: false, shift: false, alt: false, meta: false })
  }
  heldKeys.clear()
  swallowedCodes.clear()
  for (const [key, code] of [['Control', 'ControlLeft'], ['Shift', 'ShiftLeft'], ['Alt', 'AltLeft'], ['Meta', 'MetaLeft']] as const) {
    sendInput(handle.ws, { kind: 'keyup', key, code, ctrl: false, shift: false, alt: false, meta: false })
  }
}

/**
 * Gizli IME kutusu: bilesim (compositionend) ve paletten gelen metin
 * (emoji, giris yontemi) burada yakalanip "text" olarak uzak makineye gonderilir.
 * Normal yazim tus olaylariyla (keydown/keyup) gider; bu kutu yalniz IME icin.
 */
const imeInput = ref<HTMLTextAreaElement | null>(null)
function sendText(text: string) {
  if (!text || !handle || !control.value) return
  // Yapiskan Ctrl/Alt + tek harf/rakam (sanal klavye): metin degil, kisayol olarak gider (Ctrl+C gibi).
  if ((sticky.value.ctrl || sticky.value.alt) && Array.from(text).length === 1 && codeForChar(text)) {
    tapKey(text, codeForChar(text))
    return
  }
  // Istemci en fazla 4096 bayt kabul eder; uzun metni parcala.
  const chunk = 1024
  const units = Array.from(text)
  for (let i = 0; i < units.length; i += chunk) {
    sendInput(handle.ws, { kind: 'text', key: units.slice(i, i + chunk).join('') })
  }
}
function onCompositionStart() { composing = true }
function onCompositionEnd(e: CompositionEvent) {
  composing = false
  sendText(e.data)
  if (imeInput.value) imeInput.value.value = ''
}
function onImeInput(e: Event) {
  const ev = e as InputEvent
  // Bilesim sirasindaki input'lar compositionend'de tek seferde gonderilir.
  if (ev.isComposing || composing) return
  // Bilesim disi ekleme (emoji paleti, sesli yazim): metni gonder.
  if (ev.data) sendText(ev.data)
  if (imeInput.value) imeInput.value.value = ''
}
function focusIme() {
  if (control.value) imeInput.value?.focus({ preventScroll: true })
}

/**
 * Akisi (yeniden) acar. Cozunurluk degistiginde ScreenOpen yeniden gonderilmeli,
 * cunku olcekleme istemci tarafinda yapiliyor — bu yuzden WS'i kapatip aciyoruz.
 */
// Otomatik yeniden baglanma: baglanti beklenmedik sekilde koparsa artan araliklarla yeniden denenir.
const reconnect = ref<{ attempt: number, inSec: number } | null>(null)
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let reconnectTick: ReturnType<typeof setInterval> | null = null
let reconnectAttempts = 0
let stableTimer: ReturnType<typeof setTimeout> | null = null
let unmounted = false

function cancelReconnect() {
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null }
  if (reconnectTick) { clearInterval(reconnectTick); reconnectTick = null }
  reconnect.value = null
}
/** Kullanici elle yeniden denedi ya da vazgecti: sayaci sifirla. */
function manualRetry() {
  cancelReconnect()
  reconnectAttempts = 0
  void open(selectedWidth.value || 0)
}
function stopRetrying() {
  cancelReconnect()
  reconnectAttempts = RECONNECT_MAX_ATTEMPTS
}
function scheduleReconnect() {
  if (unmounted || reconnectTimer) return
  if (reconnectAttempts >= RECONNECT_MAX_ATTEMPTS) return
  reconnectAttempts++
  const delay = reconnectDelayMs(reconnectAttempts)
  reconnect.value = { attempt: reconnectAttempts, inSec: Math.ceil(delay / 1000) }
  reconnectTick = setInterval(() => {
    if (reconnect.value && reconnect.value.inSec > 1) reconnect.value = { ...reconnect.value, inSec: reconnect.value.inSec - 1 }
  }, 1000)
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    if (reconnectTick) { clearInterval(reconnectTick); reconnectTick = null }
    if (!unmounted) void open(selectedWidth.value || 0, true)
  }, delay)
}
/** Ag geri geldi: beklemeden hemen dene. */
function onOnline() {
  if (state.value === 'error' && reconnectAttempts < RECONNECT_MAX_ATTEMPTS) {
    cancelReconnect()
    void open(selectedWidth.value || 0, true)
  }
}

async function open(maxWidth: number, auto = false) {
  if (!auto) { cancelReconnect(); reconnectAttempts = 0 }
  else reconnect.value = null
  if (stableTimer) { clearTimeout(stableTimer); stableTimer = null }
  handle?.close()
  handle = null
  state.value = 'connecting'
  codec.value = ''
  errorMsg.value = ''
  stats.value = null // yeni oturum: eski sayaclar yaniltmasin
  agentStats.value = null
  videoInfo.value = null
  inputStatus.value = null
  viewerState.value = null
  viewers.value = null
  caps.value = { cursor: false, clipboard: false, files: false, clipboardImage: false, displays: 1, display: 0 }
  displayRestoreTried = false
  resetRtt()
  resetTouch()
  capsFps.value = undefined // yeni oturum: ajan tavani WS'ten yeniden gelir
  filesState.value = { enabled: false, controller: false, token: '' }
  cursorPos.value = null
  cursorShape.value = null
  cursorStyle.value = null
  clipStatus.value = null
  remoteClip.value = ''
  sasNotice.value = null
  await nextTick()

  if (!img.value || !video.value) {
    state.value = 'error'
    errorMsg.value = t('screen.errNotReady')
    return
  }

  try {
    handle = await connect(clientID, {
      img: img.value,
      video: video.value,
      canvas: canvas.value,
      mode: 'auto',
      quality: QUALITY_VALUES[qualityPreset.value] || undefined,
      fps: selectedFps.value || undefined,
      maxWidth: maxWidth || undefined,
      onCodec: (c) => { codec.value = c },
      onFrameSize: (w, h) => {
        frameW.value = w
        frameH.value = h
        // Kullanici henuz secim yapmadiysa, menu GERCEKTE akan cozunurlugu
        // gostersin (ilk baglantida bunu istemcinin varsayilani belirler).
        if (!selectedWidth.value) selectedWidth.value = w
        // Istemci screen_w/h bildirmiyorsa (eski surum) kare boyutuna dus —
        // eksik olur ama menu yine de bir seyler sunar.
        if (!nativeW.value) {
          nativeW.value = w
          nativeH.value = h
        }
      },
      onScreenSize: (w, h) => {
        // Gercek ekran boyutu: kare boyutundan gelen tahmini EZER.
        nativeW.value = w
        nativeH.value = h
      },
      onStats: (s) => { stats.value = s },
      onAgentStats: (a) => { agentStats.value = a },
      videoMode: () => videoMode.value,
      onVideoInfo: (i) => { videoInfo.value = i },
      onInputStatus: onInputStatusMsg,
      onCaps: (c) => {
        const displays = c.displays ?? 1
        caps.value = { cursor: c.cursor, clipboard: c.clipboard, files: c.files, clipboardImage: !!c.clipboardImage, displays, display: c.display ?? 0 }
        capsFps.value = c.maxFps
        // Yeniden baglanma: onceki monitor secimi korunur (her oturumda bir kez yeniden istenir).
        if (!displayRestoreTried) {
          displayRestoreTried = true
          if (wantedDisplay.value >= 0 && wantedDisplay.value < displays && wantedDisplay.value !== (c.display ?? 0)) {
            handle?.sendSettings({ display: wantedDisplay.value })
          } else {
            wantedDisplay.value = displays > 1 ? (c.display ?? 0) : -1
          }
        }
      },
      onRtt: onRttSample,
      onClipboardImage: onRemoteClipImage,
      onPlanLimits: (l) => { planFps.value = l.maxFps },
      onFiles: (f) => { filesState.value = f },
      onCursor: (c) => { cursorPos.value = c; schedulePlaceCursor() },
      onCursorShape: (s) => {
        const url = shapeToUrl(s.w, s.h, s.rgba)
        cursorShape.value = url ? { url, w: s.w, h: s.h, hotX: s.hotX, hotY: s.hotY } : null
        schedulePlaceCursor()
      },
      onClipboard: (text) => {
        remoteClip.value = text
        lastRemoteClip = text
        if (!showClipboard.value) clipUnread.value = true
        // Otomatik pano: uzakta kopyalanan metin bu bilgisayarin panosuna da yazilir (izin yoksa sessizce gecilir;
        // panelden elle kopyalanabilir).
        if (autoClip.value && text && navigator.clipboard?.writeText) {
          navigator.clipboard.writeText(text).then(() => { lastSentClip = text }).catch(() => { /* odak/izin yok */ })
        }
      },
      onClipboardStatus,
      viewOnly: !control.value,
      onControlAllowed: (allowed) => {
        if (!allowed) { control.value = false; viewOnlyLocked.value = true }
      },
      onViewers: (v) => { viewers.value = v },
      onViewOnly: (value, locked) => { control.value = !value; viewOnlyLocked.value = locked },
      onViewerState: (s) => { viewerState.value = s.layered ? s : null },
      onSettingsChanged: (s) => {
        // Katmanli yayinda ayarlar izleyiciye ozeldir; baskasinin degisikligi menuyu degistirmez.
        if (viewerState.value) return
        // Eski yayin: baska izleyicinin degisikligi, menuler yayinin gercek ayarini gostersin.
        if (s.fps) selectedFps.value = s.fps
        if (s.max_width) selectedWidth.value = s.max_width
        if (s.quality) qualityPreset.value = s.quality >= 70 ? 'high' : 'low'
      },
      onOpen: () => {
        state.value = 'open'
        // Baglanti bir sure ayakta kalirsa deneme sayaci sifirlanir (sonraki kopusta bastan baslar).
        stableTimer = setTimeout(() => { reconnectAttempts = 0 }, 10000)
      },
      onError: (m) => { state.value = 'error'; errorMsg.value = m; scheduleReconnect() },
      onClose: (code, reason) => {
        if (stableTimer) { clearTimeout(stableTimer); stableTimer = null }
        if (state.value === 'connecting' || (code && code !== 1000)) {
          state.value = 'error'
          errorMsg.value = t('screen.errClosed', { code: code ?? 1006, reason: reason ? ' · ' + reason : '' })
          scheduleReconnect()
        } else if (state.value !== 'error') {
          state.value = 'closed'
        }
      },
    })
  } catch (e: any) {
    state.value = 'error'
    const code = e?.data?.error?.code
    const msg = e?.statusCode === 403 || e?.status === 403
      ? t('common.remoteAccessDenied')
      : e?.data?.error?.message || (e instanceof Error ? e.message : t('screen.errConnect'))
    errorMsg.value = msg
    if (code === 'screen_stream_limit_reached') {
      openUpgrade(msg, 'screen')
    }
    // Kalici hatalarda (yetki, plan, bulunamadi) denenmez; ag/sunucu hatasi ve cevrimdisi istemcide denenir.
    if (shouldAutoRetry(e?.statusCode ?? e?.status, code)) scheduleReconnect()
    else cancelReconnect()
  }
}

function onResolutionChange() {
  // screen2: yeniden baglanmadan canli ayar (istemci anahtar kare yollar).
  if (isScreen2.value && handle) {
    handle.sendSettings({ fps: selectedFps.value || 0, max_width: selectedWidth.value || 0 })
    return
  }
  void open(selectedWidth.value)
}

function onQualityChange() {
  // Katmanli yayinda kalite secimi KATMAN secimidir (otomatik/yuksek/dusuk) ve
  // yalniz bu izleyiciyi etkiler; eski yayinda kalite tavani ortak ayardir.
  handle?.sendSettings({ quality: QUALITY_VALUES[qualityPreset.value], layer: qualityPreset.value === 'auto' ? 'auto' : qualityPreset.value })
}

let resizeObs: ResizeObserver | null = null

onMounted(async () => {
  loadPerfPref()
  videoMode.value = loadVideoPref()
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  // Sekme/pencere odagi kaybolunca basili modifier'lari birak (Alt+Tab).
  window.addEventListener('blur', releaseModifiers)
  document.addEventListener('fullscreenchange', onFullscreenChange)
  window.addEventListener('paste', onWindowPaste, true)
  window.addEventListener('online', onOnline)
  fullscreenSupported.value = !!document.fullscreenEnabled
  try { isTouchDevice.value = window.matchMedia('(pointer: coarse)').matches } catch { /* yok say */ }
  // Gecikme rozetinin "eski" durumu icin saat (olcum 6 sn gelmezse soluklasir).
  rttClock = setInterval(() => { rttNow.value = Date.now() }, 1000)
  loadAutoClip()
  loadPasteImages()
  void loadClientInfo()
  await nextTick()
  if (typeof ResizeObserver !== 'undefined' && stage.value) {
    resizeObs = new ResizeObserver(schedulePlaceCursor)
    resizeObs.observe(stage.value)
    if (canvas.value) resizeObs.observe(canvas.value)
  }
  // Ilk baglanti TAM cozunurlukte: native boyutu ancak boyle ogrenebiliriz.
  await open(0)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('keyup', onKeyUp, true)
  window.removeEventListener('blur', releaseModifiers)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  window.removeEventListener('paste', onWindowPaste, true)
  window.removeEventListener('online', onOnline)
  unmounted = true
  cancelReconnect()
  if (stableTimer) clearTimeout(stableTimer)
  if (stageNoteTimer) clearTimeout(stageNoteTimer)
  if (pasteFallbackTimer) clearTimeout(pasteFallbackTimer)
  if (fsBarTimer) clearTimeout(fsBarTimer)
  if (rttClock) clearInterval(rttClock)
  clearTouchTimer()
  if (remoteImage.value) URL.revokeObjectURL(remoteImage.value.url)
  try { (navigator as any).keyboard?.unlock?.() } catch { /* desteklenmiyor */ }
  if (moveRAF) cancelAnimationFrame(moveRAF)
  if (cursorRAF) cancelAnimationFrame(cursorRAF)
  clearTimeout(sasTimer)
  resizeObs?.disconnect()
  for (const x of xfers.value) x.abort?.()
  handle?.close()
})
</script>

<template>
  <div class="space-y-4">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-semibold tracking-tight">{{ t('screen.header') }}</h1>
        <p class="mt-0.5 font-mono text-xs text-fg-subtle">{{ clientID }}</p>
      </div>

      <!-- Arac cubugu dar ekranda satir kirar; ogeler kendi icinde kirilmaz -->
      <div class="flex flex-wrap items-center justify-end gap-2 [&>*]:shrink-0 [&>*]:whitespace-nowrap">
        <span
          class="inline-flex items-center gap-1.5 font-mono text-[11px]"
          :class="{
            'text-accent': state === 'open',
            'text-warn': state === 'connecting',
            'text-danger': state === 'error',
            'text-fg-subtle': state === 'closed',
          }"
        >
          <span
            class="size-1.5 rounded-full"
            :class="{
              'bg-accent animate-pulse': state === 'open',
              'bg-warn animate-pulse': state === 'connecting',
              'bg-danger': state === 'error',
              'bg-fg-subtle': state === 'closed',
            }"
          />
          {{ { connecting: t('screen.stateConnecting'), open: t('screen.stateOpen'), error: t('screen.stateError'), closed: t('screen.stateClosed') }[state] }}
        </span>

        <span v-if="codec" class="rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-muted">
          {{ codec === 'screen2' ? 'Screen2' : codec === 'h264' ? t('screen.codecHw') : 'MJPEG' }}
        </span>

        <!-- Gecikme rozeti: son 5 olcumun ortalamasi; 6 sn olcum gelmezse soluk -->
        <span
          v-if="rttView"
          class="inline-flex items-center gap-1 rounded border border-line px-2 py-0.5 font-mono text-[10px] transition-opacity duration-300"
          :class="[rttView.cls, rttStale ? 'opacity-40' : '']"
          role="status"
          :aria-label="t('screen.rttLabel')"
          :title="rttView.title"
          data-testid="screen-rtt"
        >
          <Icon name="lucide:signal" class="size-3" aria-hidden="true" />{{ rttView.text }}
        </span>

        <!-- Monitor secici (yalniz birden cok monitor varsa) -->
        <div
          v-if="caps.displays > 1"
          class="flex items-center gap-1"
          role="group"
          :aria-label="t('screen.displayLabel')"
          :title="t('screen.displayHint')"
          data-testid="screen-displays"
        >
          <Icon name="lucide:monitor-smartphone" class="size-3.5 text-fg-subtle" aria-hidden="true" />
          <button
            v-for="i in caps.displays"
            :key="i"
            type="button"
            class="cursor-pointer rounded border px-2 py-1 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
            :class="caps.display === i - 1 ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="caps.display === i - 1"
            :aria-label="t('screen.displayN', { n: i })"
            :data-testid="'screen-display-' + (i - 1)"
            @click="selectDisplay(i - 1)"
          >
            {{ i }}
          </button>
        </div>

        <!-- Kalite (yalniz screen2): otomatik istemcinin uyarlamasina birakir -->
        <label v-if="isScreen2" class="flex items-center gap-1.5">
          <span class="sr-only">{{ t('screen.quality') }}</span>
          <Icon name="lucide:sliders-horizontal" class="size-3.5 text-fg-subtle" aria-hidden="true" />
          <select
            v-model="qualityPreset"
            class="cursor-pointer rounded border border-line bg-bg px-2 py-1.5 font-mono text-[11px] text-fg-muted focus:border-accent"
            :title="viewerState ? t('screen.layerHint') : t('screen.qualityHint')"
            @change="onQualityChange"
          >
            <option value="auto">{{ t('screen.qualityAuto') }}</option>
            <option value="high">{{ t('screen.qualityHigh') }}</option>
            <option value="low">{{ t('screen.qualityLow') }}</option>
          </select>
        </label>

        <!-- Karo / video (H.264) modu -->
        <ScreenVideoControl v-if="isScreen2" v-model="videoMode" :info="videoInfo" />

        <!-- Cozunurluk: uzak ekranin en-boy oranina gore uretilen secenekler -->
        <label v-if="nativeW" class="flex items-center gap-1.5">
          <span class="sr-only">{{ t('screen.resolution') }}</span>
          <Icon name="lucide:monitor" class="size-3.5 text-fg-subtle" aria-hidden="true" />
          <select
            v-model.number="selectedWidth"
            class="cursor-pointer rounded border border-line bg-bg px-2 py-1.5 font-mono text-[11px] text-fg-muted focus:border-accent"
            :title="`Uzak ekran ${nativeW}×${nativeH} (${ratioLabel}) — oran korunur`"
            @change="onResolutionChange"
          >
            <option v-for="o in options" :key="o.w" :value="o.w">
              {{ o.label }}
            </option>
          </select>
        </label>

        <!-- Kare hızı (FPS): akıcılık ↔ bant genişliği dengesi -->
        <label class="flex items-center gap-1.5">
          <span class="sr-only">{{ t('screen.fps') }}</span>
          <Icon name="lucide:gauge" class="size-3.5 text-fg-subtle" aria-hidden="true" />
          <select
            v-model.number="selectedFps"
            class="cursor-pointer rounded border border-line bg-bg px-2 py-1.5 font-mono text-[11px] text-fg-muted focus:border-accent"
            :title="fpsLimit > 30 ? t('screen.fpsHintHigh') : t('screen.fpsHint')"
            @change="onResolutionChange"
          >
            <option v-for="f in fpsList" :key="f" :value="f">{{ f }} FPS</option>
          </select>
        </label>

        <span
          v-if="frameW"
          class="rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-subtle"
          :title="`Oran ${ratioLabel}`"
        >
          {{ frameW }}×{{ frameH }} · {{ ratioLabel }}
        </span>

        <!-- Performans katmani: ajan + tarayici olcumleri -->
        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="showPerf ? 'border-accent text-accent' : 'border-line text-fg-subtle'"
          :aria-pressed="showPerf"
          :title="t('screen.perf.toggleHint')"
          @click="togglePerf"
        >
          <Icon name="lucide:gauge" class="size-3.5" aria-hidden="true" />{{ t('screen.perf.toggle') }}
        </button>

        <!-- Akis metrikleri: rozet ozet, tiklayinca ayrinti paneli -->
        <button
          v-if="stats"
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="statsTone"
          :aria-expanded="showStats"
          :title="t('screen.statsToggle')"
          @click="showStats = !showStats"
        >
          <Icon name="lucide:activity" class="size-3.5" aria-hidden="true" />
          <template v-if="isScreen2">{{ stats.fps }} fps<template v-if="viewerState">/{{ viewerState.fps }}</template> · {{ kbPerSec }} KB/s · {{ lastFrameKB }} KB<template v-if="viewerState"> · {{ layerLabel }}</template></template>
          <template v-else>{{ stats.fps }} fps · {{ stats.kbps }} kbps</template>
          <span v-if="stats.dropped" class="text-danger">· {{ stats.dropped }} ↓</span>
        </button>

        <span
          v-if="control && inputBadge"
          class="flex items-center gap-1.5 rounded border border-line px-2 py-0.5 font-mono text-[10px]"
          :class="inputBadge.tone"
          role="status"
          :title="inputStatus?.detail || ''"
        >
          <Icon :name="inputBadge.icon" class="size-3" />
          {{ t('screen.' + inputBadge.key) }}
        </span>

        <span
          v-if="viewers && viewers.count > 1"
          class="flex items-center gap-1.5 rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-muted"
          role="status"
          :title="t('screen.viewersTitle', { c: viewers.controllers })"
        >
          <Icon name="lucide:users" class="size-3" aria-hidden="true" />
          {{ t('screen.viewers', { n: viewers.count }) }}
        </span>

        <span
          v-if="viewOnlyLocked"
          class="flex items-center gap-1.5 rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-muted"
          role="status"
          :title="t('screen.viewOnlyLockedHint')"
        >
          <Icon name="lucide:lock" class="size-3" aria-hidden="true" />
          {{ t('screen.viewOnlyLockedBadge') }}
        </span>

        <!-- Uzak imlec katmani -->
        <button
          v-if="caps.cursor"
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="showRemoteCursor ? 'text-accent' : 'text-fg-muted'"
          :aria-pressed="showRemoteCursor"
          :title="t('screen.remoteCursorHint')"
          @click="showRemoteCursor = !showRemoteCursor"
        >
          <Icon name="lucide:mouse-pointer-2" class="size-3.5" aria-hidden="true" />
          {{ t('screen.remoteCursor') }}
        </button>

        <!-- Pano (metin, iki yon) -->
        <button
          v-if="isScreen2 && control"
          type="button"
          class="relative flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="showClipboard ? 'text-accent' : 'text-fg-muted'"
          :aria-expanded="showClipboard"
          :title="t('screen.clipboardHint')"
          data-testid="screen-clipboard-toggle"
          @click="toggleClipboard"
        >
          <Icon name="lucide:clipboard" class="size-3.5" aria-hidden="true" />
          {{ t('screen.clipboard') }}
          <span v-if="clipUnread" class="absolute -right-1 -top-1 size-2 rounded-full bg-accent" aria-hidden="true" />
        </button>

        <!-- Dosya aktarimi (panel <-> uzak makine) -->
        <button
          v-if="isScreen2 && control"
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="showFiles ? 'text-accent' : 'text-fg-muted'"
          :aria-expanded="showFiles"
          :title="t('screen.filesHint')"
          @click="toggleFiles"
        >
          <Icon name="lucide:folder-up" class="size-3.5" aria-hidden="true" />
          {{ t('screen.files') }}
        </button>

        <!-- Dokunmatik: surukleme kilidi, sigdir (yerel yakinlastirmayi sifirla), sanal klavye -->
        <template v-if="isTouchDevice && control && state === 'open'">
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
            :class="touchLock ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="touchLock"
            :title="t('screen.touchLockHint')"
            data-testid="screen-touch-lock"
            @click="touchLock = !touchLock"
          >
            <Icon name="lucide:move" class="size-3.5" aria-hidden="true" />{{ t('screen.touchLock') }}
          </button>
          <button
            v-if="isZoomed"
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors duration-150 hover:bg-surface"
            :title="t('screen.touchFitHint')"
            data-testid="screen-touch-fit"
            @click="fitZoom"
          >
            <Icon name="lucide:scan" class="size-3.5" aria-hidden="true" />{{ t('screen.touchFit', { pct: zoomPct }) }}
          </button>
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
            :class="showVkb ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="showVkb"
            :title="t('screen.touchKeyboardHint')"
            data-testid="screen-vkb"
            @click="toggleVkb"
          >
            <Icon name="lucide:keyboard" class="size-3.5" aria-hidden="true" />{{ t('screen.touchKeyboard') }}
          </button>
        </template>

        <!-- Kisayollar: Ctrl+Alt+Del ve tarayicinin yakalayamadigi kombinasyonlar -->
        <div v-if="control && !isFullscreen" class="relative">
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors duration-150 hover:bg-surface"
            :aria-expanded="showShortcuts"
            aria-haspopup="menu"
            :title="t('screen.shortcutsHint')"
            @click="showShortcuts = !showShortcuts"
            @keydown.esc="showShortcuts = false"
          >
            <Icon name="lucide:keyboard" class="size-3.5" aria-hidden="true" />
            {{ t('screen.shortcuts') }}
            <Icon name="lucide:chevron-down" class="size-3" aria-hidden="true" />
          </button>
          <div
            v-if="showShortcuts"
            role="menu"
            class="absolute right-0 z-20 mt-1 min-w-48 rounded-lg border border-line bg-bg p-1 shadow-lg"
            @keydown.esc="showShortcuts = false"
          >
            <button
              v-for="sc in SHORTCUTS"
              :key="sc.id"
              type="button"
              role="menuitem"
              class="flex w-full cursor-pointer items-center gap-2 rounded px-2.5 py-1.5 text-left font-mono text-[11px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
              :data-testid="'screen-shortcut-' + sc.id"
              @click="sendShortcut(sc.id)"
            >
              <Icon :name="sc.id === 'sas' ? 'lucide:shield' : 'lucide:command'" class="size-3.5 text-fg-subtle" aria-hidden="true" />
              {{ t('screen.' + sc.labelKey) }}
            </button>
          </div>
        </div>

        <button
          v-if="fullscreenSupported"
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-fg-muted transition-colors duration-150 hover:bg-surface hover:text-fg"
          :title="t('screen.fullscreenHint')"
          data-testid="screen-fullscreen"
          @click="toggleFullscreen"
        >
          <Icon name="lucide:maximize" class="size-3.5" aria-hidden="true" />
          {{ t('screen.fullscreen') }}
        </button>

        <button
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface disabled:cursor-not-allowed disabled:opacity-60"
          :class="control ? 'text-accent' : 'text-fg-muted'"
          :aria-pressed="control"
          :disabled="viewOnlyLocked"
          :title="viewOnlyLocked ? t('screen.viewOnlyLocked') : t('screen.toggleControl')"
          @click="toggleControl"
        >
          <Icon :name="control ? 'lucide:mouse-pointer-click' : 'lucide:eye'" class="size-3.5" />
          {{ control ? t('screen.controlOn') : t('screen.watchOnly') }}
        </button>

        <button
          v-if="state === 'error' || state === 'closed'"
          type="button"
          class="cursor-pointer rounded border border-line bg-surface px-2.5 py-1 font-mono text-[11px] text-accent transition-colors hover:border-accent/40"
          @click="open(selectedWidth || 0)"
        >
          <Icon name="lucide:refresh-cw" class="mr-1 inline size-3" />{{ t('screen.reconnect') }}
        </button>

        <NuxtLink to="/clients" class="cursor-pointer font-mono text-[11px] text-accent hover:underline">
          <Icon name="lucide:arrow-left" class="mr-1 inline size-3" />{{ t('screen.backToClients') }}
        </NuxtLink>
      </div>
    </header>

    <!-- Metrik ayrinti paneli -->
    <div
      v-if="showStats && stats"
      class="grid gap-x-6 gap-y-2 rounded-lg border border-line bg-surface/60 px-4 py-3 font-mono text-[11px] sm:grid-cols-3 lg:grid-cols-6"
    >
      <div>
        <p class="text-fg-subtle">{{ t('screen.mFps') }}</p>
        <p class="text-fg">{{ stats.fps }}</p>
      </div>
      <div>
        <p class="text-fg-subtle">{{ t('screen.mBitrate') }}</p>
        <p class="text-fg">{{ stats.kbps }} kbps</p>
      </div>
      <div v-if="viewerState" :title="t('screen.layerHint')">
        <p class="text-fg-subtle">{{ t('screen.mLayer') }}</p>
        <p class="text-fg">{{ layerLabel }} · {{ viewerState.fps }} fps</p>
      </div>
      <div v-if="isScreen2" :title="t('screen.mLastFrameHelp')">
        <p class="text-fg-subtle">{{ t('screen.mLastFrame') }}</p>
        <p class="text-fg">{{ lastFrameKB }} KB</p>
      </div>
      <div :title="t('screen.mIntervalHelp')">
        <p class="text-fg-subtle">{{ t('screen.mInterval') }}</p>
        <p class="text-fg">
          {{ stats.intervalMs }} ms
          <span :class="stats.maxIntervalMs > 500 ? 'text-warn' : 'text-fg-subtle'">
            / {{ stats.maxIntervalMs }}
          </span>
        </p>
      </div>
      <div :title="t('screen.mDroppedHelp')">
        <p class="text-fg-subtle">{{ t('screen.mDropped') }}</p>
        <p :class="stats.dropped ? 'text-danger' : 'text-fg'">
          {{ stats.dropped }} / {{ stats.frames }}
        </p>
      </div>
      <div :title="t('screen.mCaptureHelp')">
        <p class="text-fg-subtle">{{ t('screen.mCapture') }}</p>
        <p class="text-fg">{{ stats.captureMs ? stats.captureMs + ' ms' : '—' }}</p>
      </div>
      <div :title="t('screen.mEncodeHelp')">
        <p class="text-fg-subtle">{{ t('screen.mEncode') }}</p>
        <p class="text-fg">{{ stats.encodeMs ? stats.encodeMs + ' ms' : '—' }}</p>
      </div>
    </div>

    <div
      v-if="degradedText"
      class="flex items-center gap-2 rounded-lg border border-warn/30 bg-warn/10 px-4 py-2 text-xs text-warn"
      role="status"
    >
      <Icon name="lucide:shield-alert" class="size-3.5 shrink-0" aria-hidden="true" />
      <span>{{ degradedText }}</span>
    </div>

    <div
      v-if="sasNotice"
      class="flex items-center justify-between gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-2 text-xs text-danger"
      role="alert"
    >
      <span>{{ sasNotice.text }}</span>
      <button type="button" class="cursor-pointer text-danger/80 hover:text-danger" :aria-label="t('common.close')" @click="sasNotice = null">
        <Icon name="lucide:x" class="size-3.5" aria-hidden="true" />
      </button>
    </div>

    <!-- Pano paneli: gonder (panel -> uzak) ve al (uzak -> panel) -->
    <div
      v-if="showClipboard && control && isScreen2"
      class="grid gap-4 rounded-lg border border-line bg-surface/60 px-4 py-3 md:grid-cols-2"
    >
      <p v-if="!caps.clipboard" class="text-xs text-fg-subtle md:col-span-2">
        <Icon name="lucide:info" class="mr-1 inline size-3.5" aria-hidden="true" />{{ t('screen.clipboardUnsupported') }}
      </p>
      <template v-else>
        <div class="md:col-span-2">
          <label class="flex cursor-pointer items-start gap-2 text-xs text-fg">
            <input v-model="autoClip" type="checkbox" class="mt-0.5 cursor-pointer accent-accent" data-testid="screen-autoclip">
            <span>
              {{ t('screen.clipboardAuto') }}
              <span class="mt-0.5 block text-[11px] text-fg-subtle">{{ t('screen.clipboardAutoHint') }}</span>
              <span v-if="clipReadBlocked" class="mt-0.5 block text-[11px] text-danger">{{ t('screen.clipboardAutoBlocked') }}</span>
            </span>
          </label>
          <label v-if="caps.clipboardImage" class="mt-2 flex cursor-pointer items-start gap-2 text-xs text-fg">
            <input v-model="pasteImages" type="checkbox" class="mt-0.5 cursor-pointer accent-accent" data-testid="screen-pasteimg">
            <span>
              {{ t('screen.clipboardImages') }}
              <span class="mt-0.5 block text-[11px] text-fg-subtle">{{ t('screen.clipboardImagesHint') }}</span>
            </span>
          </label>
        </div>
        <div class="space-y-2">
          <p class="font-mono text-[11px] text-fg-subtle">{{ t('screen.clipboardSendTitle') }}</p>
          <textarea
            v-model="clipText"
            rows="4"
            maxlength="65536"
            class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs text-fg focus:border-accent"
            :placeholder="t('screen.clipboardSendPlaceholder')"
            @paste="onClipPaste"
          />
          <div class="flex flex-wrap items-center gap-3">
            <button
              type="button"
              class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-accent transition-colors hover:bg-surface disabled:cursor-not-allowed disabled:opacity-60"
              :disabled="!clipText"
              @click="sendClipboard"
            >
              <Icon name="lucide:send" class="size-3.5" aria-hidden="true" />{{ t('screen.clipboardSend') }}
            </button>
            <label class="flex cursor-pointer items-center gap-1.5 font-mono text-[11px] text-fg-muted">
              <input v-model="clipAutoSend" type="checkbox" class="cursor-pointer accent-accent">
              {{ t('screen.clipboardAutoSend') }}
            </label>
            <span class="font-mono text-[10px] text-fg-subtle">{{ t('screen.clipboardLimit') }}</span>
          </div>
          <p
            v-if="clipStatus"
            class="font-mono text-[11px]"
            :class="clipStatus.ok ? 'text-accent' : 'text-danger'"
            role="status"
          >
            {{ clipStatus.text }}
          </p>
        </div>
        <div class="space-y-2">
          <p class="font-mono text-[11px] text-fg-subtle">{{ t('screen.clipboardRecvTitle') }}</p>
          <textarea
            :value="remoteClip"
            readonly
            rows="4"
            class="w-full rounded border border-line bg-bg px-2 py-1.5 font-mono text-xs text-fg"
            :placeholder="t('screen.clipboardRecvEmpty')"
          />
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-accent transition-colors hover:bg-surface disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="!remoteClip"
            @click="copyRemoteClip"
          >
            <Icon :name="clipCopied ? 'lucide:check' : 'lucide:copy'" class="size-3.5" aria-hidden="true" />
            {{ clipCopied ? t('screen.clipboardCopied') : t('screen.clipboardCopy') }}
          </button>

          <!-- Uzak panodaki son gorsel: onizleme + kopyala + indir -->
          <div v-if="remoteImage" class="space-y-2 border-t border-line pt-2" data-testid="screen-remote-image">
            <p class="font-mono text-[11px] text-fg-subtle">{{ t('screen.clipImgRecvTitle') }}</p>
            <img :src="remoteImage.url" :alt="t('screen.clipImgAlt')" class="max-h-40 max-w-full rounded border border-line bg-bg object-contain" data-testid="screen-remote-image-preview">
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-accent transition-colors hover:bg-surface"
                data-testid="screen-remote-image-copy"
                @click="copyRemoteImage"
              >
                <Icon :name="imgCopied ? 'lucide:check' : 'lucide:copy'" class="size-3.5" aria-hidden="true" />
                {{ imgCopied ? t('screen.clipboardCopied') : t('screen.clipboardCopy') }}
              </button>
              <button
                type="button"
                class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-accent transition-colors hover:bg-surface"
                data-testid="screen-remote-image-download"
                @click="downloadRemoteImage"
              >
                <Icon name="lucide:download" class="size-3.5" aria-hidden="true" />{{ t('screen.filesDownload') }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- Dosya paneli: gonder (panel -> uzak) ve al (uzak -> panel) -->
    <div
      v-if="showFiles && control && isScreen2"
      class="grid gap-4 rounded-lg border border-line bg-surface/60 px-4 py-3 md:grid-cols-2"
    >
      <p v-if="!caps.files" class="text-xs text-fg-subtle md:col-span-2">
        <Icon name="lucide:info" class="mr-1 inline size-3.5" aria-hidden="true" />{{ t('screen.filesUnsupported') }}
      </p>
      <p v-else-if="!filesState.controller" class="text-xs text-fg-subtle md:col-span-2">
        <Icon name="lucide:info" class="mr-1 inline size-3.5" aria-hidden="true" />{{ t('screen.filesViewOnly') }}
      </p>
      <template v-else>
        <div class="space-y-2">
          <p class="font-mono text-[11px] text-fg-subtle">{{ t('screen.filesSendTitle') }}</p>
          <div class="flex flex-wrap items-center gap-3 rounded border border-dashed border-line px-3 py-3 text-xs text-fg-muted">
            <Icon name="lucide:upload" class="size-4 text-fg-subtle" aria-hidden="true" />
            <span>{{ t('screen.filesDropHint') }}</span>
            <button
              type="button"
              class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] text-accent transition-colors hover:bg-surface"
              @click="fileInput?.click()"
            >
              <Icon name="lucide:file-up" class="size-3.5" aria-hidden="true" />{{ t('screen.filesPick') }}
            </button>
            <input ref="fileInput" type="file" multiple class="hidden" @change="onPickFiles">
          </div>
          <p class="font-mono text-[10px] text-fg-subtle">{{ t('screen.filesLimit', { max: formatBytes(MAX_FILE_BYTES) }) }}</p>
          <ul v-if="xfers.length" class="space-y-2" aria-live="polite">
            <li v-for="x in xfers" :key="x.id" class="space-y-1 rounded border border-line bg-bg px-2.5 py-2">
              <div class="flex items-center justify-between gap-2 font-mono text-[11px]">
                <span class="min-w-0 truncate text-fg" :title="x.file.name">{{ x.file.name }}</span>
                <span class="shrink-0 text-fg-subtle">{{ formatBytes(x.file.size) }}</span>
              </div>
              <div
                v-if="x.status === 'uploading' || x.status === 'queued'"
                class="h-1.5 overflow-hidden rounded bg-line"
                role="progressbar"
                :aria-valuenow="x.file.size ? Math.round((x.loaded / x.file.size) * 100) : 0"
                aria-valuemin="0"
                aria-valuemax="100"
              >
                <div class="h-full bg-accent transition-[width] duration-150" :style="{ width: (x.file.size ? (x.loaded / x.file.size) * 100 : 0) + '%' }" />
              </div>
              <div class="flex items-center justify-between gap-2 text-[11px]">
                <span
                  :class="x.status === 'error' ? 'text-danger' : x.status === 'done' ? 'text-accent' : 'text-fg-subtle'"
                  role="status"
                >
                  <template v-if="x.status === 'queued'">{{ t('screen.filesQueued') }}</template>
                  <template v-else-if="x.status === 'uploading'">{{ t('screen.filesUploading', { pct: x.file.size ? Math.round((x.loaded / x.file.size) * 100) : 0 }) }}</template>
                  <template v-else-if="x.status === 'done'">{{ t('screen.filesSavedAs', { name: x.savedAs }) }}</template>
                  <template v-else-if="x.status === 'canceled'">{{ t('screen.filesCanceled') }}</template>
                  <template v-else>{{ x.error }}</template>
                </span>
                <button
                  v-if="x.status === 'uploading' || x.status === 'queued'"
                  type="button"
                  class="flex cursor-pointer items-center gap-1 rounded border border-line px-2 py-1 font-mono text-[11px] text-fg-muted hover:bg-surface"
                  @click="cancelXfer(x)"
                >
                  <Icon name="lucide:x" class="size-3" aria-hidden="true" />{{ t('screen.filesCancel') }}
                </button>
              </div>
            </li>
          </ul>
          <button
            v-if="xfers.some(x => x.status !== 'uploading' && x.status !== 'queued')"
            type="button"
            class="cursor-pointer font-mono text-[11px] text-fg-subtle underline-offset-2 hover:underline"
            @click="clearFinishedXfers"
          >
            {{ t('screen.filesClear') }}
          </button>
        </div>
        <div class="space-y-2">
          <div class="flex items-center justify-between gap-2">
            <p class="font-mono text-[11px] text-fg-subtle">{{ t('screen.filesRecvTitle') }}</p>
            <button
              type="button"
              class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2 py-1 font-mono text-[11px] text-fg-muted hover:bg-surface disabled:cursor-not-allowed disabled:opacity-60"
              :disabled="filesLoading"
              @click="refreshRemoteFiles"
            >
              <Icon name="lucide:refresh-cw" class="size-3" :class="filesLoading ? 'animate-spin' : ''" aria-hidden="true" />{{ t('screen.filesRefresh') }}
            </button>
          </div>
          <p v-if="remoteDir" class="break-all font-mono text-[10px] text-fg-subtle">{{ t('screen.filesDir', { dir: remoteDir }) }}</p>
          <p v-if="filesListError || downloadError" class="font-mono text-[11px] text-danger" role="alert">{{ filesListError || downloadError }}</p>
          <p v-else-if="!remoteFiles.length && !filesLoading" class="text-xs text-fg-subtle">{{ t('screen.filesEmpty') }}</p>
          <ul v-if="remoteFiles.length" class="max-h-56 space-y-1 overflow-auto">
            <li v-for="f in remoteFiles" :key="f.name" class="flex items-center justify-between gap-2 rounded border border-line bg-bg px-2.5 py-1.5 font-mono text-[11px]">
              <span class="min-w-0 truncate text-fg" :title="f.name">{{ f.name }}</span>
              <span class="flex shrink-0 items-center gap-2">
                <span class="text-fg-subtle">{{ formatBytes(f.size) }}</span>
                <button
                  type="button"
                  class="flex cursor-pointer items-center gap-1 rounded border border-line px-2 py-1 text-accent hover:bg-surface"
                  :title="t('screen.filesDownload')"
                  @click="downloadRemote(f)"
                >
                  <Icon name="lucide:download" class="size-3" aria-hidden="true" />{{ t('screen.filesDownload') }}
                </button>
              </span>
            </li>
          </ul>
        </div>
      </template>
    </div>

    <div
      v-if="control"
      class="rounded-lg border border-warn/30 bg-warn/5 px-4 py-2 text-xs text-warn"
      role="note"
    >
      <Icon name="lucide:triangle-alert" class="mr-1 inline size-3.5" />{{ t('screen.controlWarn') }}
    </div>

    <div
      v-if="showServiceNotice"
      class="flex items-start gap-2 rounded-lg border border-line bg-surface px-4 py-3 text-sm text-fg"
      role="status"
    >
      <Icon name="lucide:info" class="mt-0.5 size-4 shrink-0" />
      <span>{{ t('screen.serviceMode') }}</span>
    </div>

    <div
      v-if="state === 'error'"
      class="flex items-center justify-between gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-sm text-danger"
      role="alert"
    >
      <span>
        {{ errorMsg }}
        <span v-if="reconnect" class="mt-0.5 block text-xs opacity-90" data-testid="screen-reconnect">
          {{ t('screen.reconnecting', { n: reconnect.attempt, max: RECONNECT_MAX_ATTEMPTS, sec: reconnect.inSec }) }}
        </span>
      </span>
      <span class="flex shrink-0 items-center gap-2">
        <button
          v-if="reconnect"
          type="button"
          class="cursor-pointer rounded border border-danger/40 px-2.5 py-1 text-xs font-semibold hover:bg-danger/20"
          @click="stopRetrying"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="cursor-pointer rounded border border-danger/40 bg-danger/20 px-2.5 py-1 text-xs font-semibold hover:bg-danger/30"
          data-testid="screen-retry"
          @click="manualRetry"
        >
          {{ t('common.retry') }}
        </button>
      </span>
    </div>

    <!-- Ekran sahnesi: img (MJPEG) veya video (H.264) -->
    <div
      ref="stage"
      class="relative grid min-h-[300px] touch-none select-none place-items-center overflow-hidden bg-black outline-none"
      :class="[control ? 'cursor-crosshair' : 'cursor-default', isFullscreen ? 'h-screen w-screen' : 'rounded-xl border border-line']"
      tabindex="0"
      data-testid="screen-stage"
      @pointermove="(e) => { onPointerMove(e); onStagePointerMoveFs(e) }"
      @pointerdown="onPointerDown"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
      @mouseup.prevent
      @auxclick.prevent
      @contextmenu.prevent
      @wheel="onWheel"
      @dragenter="onDragOver"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <img ref="img" hidden :style="zoomStyle" :alt="t('screen.remoteScreenAlt')" class="max-w-full object-contain" :class="isFullscreen ? 'max-h-screen' : 'max-h-[80vh]'" draggable="false">
      <video ref="video" hidden :style="zoomStyle" muted playsinline class="max-w-full object-contain" :class="isFullscreen ? 'max-h-screen' : 'max-h-[80vh]'" />
      <canvas ref="canvas" hidden :style="zoomStyle" class="max-w-full object-contain" :class="isFullscreen ? 'max-h-screen' : 'max-h-[80vh]'" />

      <!-- Sahne notu: pano/dosya aktarimi ve yeniden baglanma durumu (tam ekranda da gorunur) -->
      <div
        v-if="stageNote || (isFullscreen && (reconnect || state === 'connecting'))"
        class="pointer-events-none absolute bottom-3 left-1/2 z-20 max-w-[90%] -translate-x-1/2 rounded-lg border px-3 py-1.5 text-center font-mono text-[11px] shadow-lg"
        :class="stageNote?.tone === 'error' ? 'border-danger/40 bg-danger/90 text-white' : 'border-line bg-surface/95 text-fg'"
        role="status"
        data-testid="screen-stage-note"
      >
        <template v-if="stageNote">{{ stageNote.text }}</template>
        <template v-else-if="reconnect">{{ t('screen.reconnecting', { n: reconnect.attempt, max: RECONNECT_MAX_ATTEMPTS, sec: reconnect.inSec }) }}</template>
        <template v-else>{{ t('screen.connecting') }}</template>
      </div>

      <!-- Tam ekran cubugu: girince ve fare ust kenara gelince gorunur; tiklamalar uzak makineye gitmez -->
      <div
        v-if="isFullscreen"
        class="absolute left-1/2 top-2 z-20 flex max-w-[96%] -translate-x-1/2 flex-wrap items-center justify-center gap-2 rounded-lg border border-line bg-surface/95 px-2.5 py-1.5 shadow-lg transition-opacity duration-200"
        :class="fsBar ? 'opacity-100' : 'pointer-events-none opacity-0'"
        data-testid="screen-fs-bar"
        @pointerdown.stop
        @pointerup.stop
        @pointermove.stop="showFsBar()"
        @wheel.stop
        @contextmenu.stop
      >
        <span
          class="font-mono text-[11px]"
          :class="kbLock === 'unsupported' ? 'text-warn' : 'text-fg-muted'"
          role="status"
          data-testid="screen-fs-kblock"
          :data-kblock="kbLock"
        >{{ kbLock === 'unsupported' ? t('screen.kbLockUnsupported') : t('screen.fullscreenExitHint') }}</span>

        <span
          v-if="rttView"
          class="inline-flex items-center gap-1 rounded border border-line px-2 py-0.5 font-mono text-[10px]"
          :class="[rttView.cls, rttStale ? 'opacity-40' : '']"
          role="status"
          :aria-label="t('screen.rttLabel')"
          :title="rttView.title"
          data-testid="screen-fs-rtt"
        >
          <Icon name="lucide:signal" class="size-3" aria-hidden="true" />{{ rttView.text }}
        </span>

        <div v-if="caps.displays > 1" class="flex items-center gap-1" role="group" :aria-label="t('screen.displayLabel')" :title="t('screen.displayHint')">
          <Icon name="lucide:monitor-smartphone" class="size-3.5 text-fg-subtle" aria-hidden="true" />
          <button
            v-for="i in caps.displays"
            :key="i"
            type="button"
            class="cursor-pointer rounded border px-2 py-0.5 font-mono text-[11px] transition-colors hover:bg-bg"
            :class="caps.display === i - 1 ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="caps.display === i - 1"
            :aria-label="t('screen.displayN', { n: i })"
            :data-testid="'screen-fs-display-' + (i - 1)"
            @click="selectDisplay(i - 1)"
          >
            {{ i }}
          </button>
        </div>

        <template v-if="isTouchDevice && control">
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border px-2 py-1 font-mono text-[11px] transition-colors hover:bg-bg"
            :class="touchLock ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="touchLock"
            :title="t('screen.touchLockHint')"
            @click="touchLock = !touchLock"
          >
            <Icon name="lucide:move" class="size-3.5" aria-hidden="true" />{{ t('screen.touchLock') }}
          </button>
          <button
            v-if="isZoomed"
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2 py-1 font-mono text-[11px] text-fg-muted transition-colors hover:bg-bg"
            :title="t('screen.touchFitHint')"
            @click="fitZoom"
          >
            <Icon name="lucide:scan" class="size-3.5" aria-hidden="true" />{{ t('screen.touchFit', { pct: zoomPct }) }}
          </button>
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border px-2 py-1 font-mono text-[11px] transition-colors hover:bg-bg"
            :class="showVkb ? 'border-accent text-accent' : 'border-line text-fg-muted'"
            :aria-pressed="showVkb"
            :title="t('screen.touchKeyboardHint')"
            @click="toggleVkb"
          >
            <Icon name="lucide:keyboard" class="size-3.5" aria-hidden="true" />{{ t('screen.touchKeyboard') }}
          </button>
        </template>

        <!-- Kisayollar: tam ekranda da ayni menu (klavye kilidi yoksa Alt+Tab/Win/Esc buradan gider) -->
        <div v-if="control" class="relative">
          <button
            type="button"
            class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2 py-1 font-mono text-[11px] text-fg-muted transition-colors hover:bg-bg"
            :aria-expanded="showShortcuts"
            aria-haspopup="menu"
            :title="t('screen.shortcutsHint')"
            data-testid="screen-fs-shortcuts"
            @click="showShortcuts = !showShortcuts; showFsBar()"
            @keydown.esc="showShortcuts = false"
          >
            <Icon name="lucide:keyboard" class="size-3.5" aria-hidden="true" />
            {{ t('screen.shortcuts') }}
            <Icon name="lucide:chevron-down" class="size-3" aria-hidden="true" />
          </button>
          <div
            v-if="showShortcuts"
            role="menu"
            class="absolute right-0 top-full z-30 mt-1 min-w-52 rounded-lg border border-line bg-bg p-1 shadow-lg"
            @keydown.esc="showShortcuts = false"
          >
            <button
              v-for="sc in SHORTCUTS"
              :key="sc.id"
              type="button"
              role="menuitem"
              class="flex w-full cursor-pointer items-center gap-2 rounded px-2.5 py-1.5 text-left font-mono text-[11px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
              :data-testid="'screen-fs-shortcut-' + sc.id"
              @click="sendShortcut(sc.id); stage?.focus()"
            >
              <Icon :name="sc.id === 'sas' ? 'lucide:shield' : 'lucide:command'" class="size-3.5 text-fg-subtle" aria-hidden="true" />
              {{ t('screen.' + sc.labelKey) }}
            </button>
          </div>
        </div>

        <button
          type="button"
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2 py-1 font-mono text-[11px] text-fg transition-colors hover:bg-bg"
          data-testid="screen-fs-exit"
          @click="toggleFullscreen"
        >
          <Icon name="lucide:minimize" class="size-3.5" aria-hidden="true" />{{ t('screen.fullscreenExit') }}
        </button>
      </div>

      <!-- Sanal klavye ek tus seridi (dokunmatik): Ctrl/Alt yapiskan; yazim gizli IME kutusuyla -->
      <div
        v-if="showVkb && control && isTouchDevice"
        class="absolute inset-x-0 bottom-0 z-20 flex flex-wrap items-center justify-center gap-1.5 border-t border-line bg-surface/95 px-2 py-1.5"
        role="toolbar"
        :aria-label="t('screen.touchKeysLabel')"
        data-testid="screen-vkb-strip"
        @pointerdown.stop
        @pointerup.stop
        @pointermove.stop
        @wheel.stop
      >
        <button
          v-for="k in VKB_KEYS"
          :key="k.key"
          type="button"
          class="flex min-w-9 cursor-pointer items-center justify-center rounded border border-line px-2 py-1.5 font-mono text-[11px] text-fg transition-colors hover:bg-bg"
          :aria-label="k.label || k.key"
          :data-testid="'screen-vkb-' + k.key"
          @mousedown.prevent
          @click="tapKey(k.key, k.code, !!k.meta)"
        >
          <Icon v-if="k.icon" :name="k.icon" class="size-3.5" aria-hidden="true" /><template v-else>{{ k.label }}</template>
        </button>
        <button
          v-for="m in (['ctrl', 'alt'] as const)"
          :key="m"
          type="button"
          class="min-w-9 cursor-pointer rounded border px-2 py-1.5 font-mono text-[11px] transition-colors hover:bg-bg"
          :class="sticky[m] ? 'border-accent text-accent' : 'border-line text-fg'"
          :aria-pressed="sticky[m]"
          :title="t('screen.touchStickyHint')"
          :data-testid="'screen-vkb-' + m"
          @mousedown.prevent
          @click="toggleSticky(m)"
        >
          {{ m === 'ctrl' ? 'Ctrl' : 'Alt' }}
        </button>
        <button
          type="button"
          class="min-w-9 cursor-pointer rounded border px-2 py-1.5 font-mono text-[11px] transition-colors hover:bg-bg"
          :class="vkbFKeys ? 'border-accent text-accent' : 'border-line text-fg'"
          :aria-expanded="vkbFKeys"
          data-testid="screen-vkb-fkeys"
          @mousedown.prevent
          @click="vkbFKeys = !vkbFKeys"
        >
          F1-F12
        </button>
        <div v-if="vkbFKeys" class="flex w-full flex-wrap items-center justify-center gap-1.5">
          <button
            v-for="k in F_KEYS"
            :key="k.key"
            type="button"
            class="min-w-9 cursor-pointer rounded border border-line px-2 py-1.5 font-mono text-[11px] text-fg transition-colors hover:bg-bg"
            :data-testid="'screen-vkb-' + k.key"
            @mousedown.prevent
            @click="tapKey(k.key, k.code)"
          >
            {{ k.label }}
          </button>
        </div>
      </div>

      <!-- Gizli IME kutusu: bilesim/emoji metni "text" olarak gider; normal yazim tus olaylariyla -->
      <textarea
        v-if="control"
        ref="imeInput"
        data-remote-ime
        class="pointer-events-none absolute left-0 top-0 size-px resize-none opacity-0"
        tabindex="-1"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        :aria-label="t('screen.imeHint')"
        @compositionstart="onCompositionStart"
        @compositionend="onCompositionEnd"
        @input="onImeInput"
      />

      <!-- Uzak imlec katmani: kareler imleci icermez; konum/bicim ayri gelir -->
      <div
        v-if="cursorStyle"
        class="pointer-events-none absolute left-0 top-0 z-10 will-change-transform"
        :style="cursorStyle"
        aria-hidden="true"
      >
        <img v-if="cursorShape" :src="cursorShape.url" alt="" draggable="false" class="size-full select-none">
        <Icon v-else name="lucide:mouse-pointer-2" class="size-full text-white drop-shadow" />
      </div>

      <!-- Performans katmani (ajan istatistigi + tarayici olcumleri) -->
      <div
        v-if="showPerf"
        class="absolute right-2 top-2 z-30 w-72 max-w-[calc(100%-1rem)] cursor-default rounded-lg border border-white/15 bg-black/80 p-3 font-mono text-[11px] text-white/90 shadow-lg backdrop-blur"
        role="region"
        :aria-label="t('screen.perf.title')"
        @pointerdown.stop
        @pointermove.stop
        @pointerup.stop
        @wheel.stop
      >
        <div class="mb-2 flex items-center justify-between gap-2">
          <p class="flex items-center gap-1.5 font-semibold">
            <Icon name="lucide:gauge" class="size-3.5" aria-hidden="true" />{{ t('screen.perf.title') }}
          </p>
          <button type="button" class="cursor-pointer text-white/60 hover:text-white" :aria-label="t('screen.perf.close')" @click="togglePerf">
            <Icon name="lucide:x" class="size-3.5" aria-hidden="true" />
          </button>
        </div>

        <template v-if="agentStats">
          <p class="mb-1 text-white/50">{{ t('screen.perf.agent') }}</p>
          <dl class="mb-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5">
            <dt class="text-white/60">{{ t('screen.perf.method') }}</dt>
            <dd>{{ agentStats.method || '—' }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.resolution') }}</dt>
            <dd>{{ agentStats.src_w }}×{{ agentStats.src_h }} → {{ agentStats.w }}×{{ agentStats.h }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.sentFps') }}</dt>
            <dd>{{ agentStats.fps }} / {{ perfTargetFps }}<span class="text-white/50"> ({{ agentStats.max_fps }})</span></dd>
            <template v-if="agentStats.srv">
              <dt class="text-white/60">{{ t('screen.perf.delivered') }}</dt>
              <dd :class="agentStats.srv.skipped > 0 ? 'text-amber-300' : ''">
                {{ agentStats.srv.fps }} / {{ agentStats.srv.skipped }}<span v-if="agentStats.srv.queue_ms" class="text-white/50"> · {{ agentStats.srv.queue_ms }} ms</span>
              </dd>
              <dt class="text-white/60">{{ t('screen.perf.feedback') }}</dt>
              <dd>{{ agentStats.srv.feedback ? t('screen.perf.feedbackOn') : t('screen.perf.feedbackOff') }}</dd>
            </template>
            <dt class="text-white/60">{{ t('screen.perf.captureFps') }}</dt>
            <dd>{{ agentStats.capture_fps }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.quality') }}</dt>
            <dd>{{ perfQuality ? perfQuality.value : agentStats.quality }}<span v-if="perfQuality && perfQuality.ceiling" class="text-white/50"> ({{ perfQuality.ceiling }})</span></dd>
            <dt class="text-white/60">{{ t('screen.perf.bitrate') }}</dt>
            <dd>{{ agentStats.kbps }} KB/s</dd>
            <dt class="text-white/60">{{ t('screen.perf.merged') }} / {{ t('screen.perf.idle') }}</dt>
            <dd>{{ agentStats.merged }} / {{ agentStats.idle }}</dd>
          </dl>
          <table class="mb-2 w-full">
            <thead>
              <tr class="text-left text-white/50">
                <th class="font-normal">{{ t('screen.perf.stage') }}</th>
                <th class="text-right font-normal">{{ t('screen.perf.avg') }}</th>
                <th class="text-right font-normal">{{ t('screen.perf.p95') }}</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="k in STAGE_KEYS" :key="k">
                <tr :class="bottleneck.slowest === k ? 'text-amber-300' : ''">
                  <td>{{ t('screen.perf.stages.' + k) }}</td>
                  <td class="text-right">{{ fmtMs(agentStats[k].avg) }}</td>
                  <td class="text-right">{{ fmtMs(agentStats[k].p95) }}</td>
                </tr>
                <tr aria-hidden="true">
                  <td colspan="3" class="pb-1">
                    <div class="h-0.5 w-full rounded bg-white/10">
                      <div class="h-0.5 rounded" :class="bottleneck.slowest === k ? 'bg-amber-300' : 'bg-white/50'" :style="{ width: stageBar(agentStats[k].avg) }" />
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </template>
        <p v-else class="mb-2 text-white/60">{{ t('screen.perf.noAgent') }}</p>

        <ScreenVideoPerf :info="videoInfo" :agent="agentStats" />

        <template v-if="stats">
          <p class="mb-1 text-white/50">{{ t('screen.perf.browser') }}</p>
          <dl class="mb-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5">
            <dt class="text-white/60">{{ t('screen.perf.recvFps') }}</dt>
            <dd>{{ stats.fps }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.drawFps') }}</dt>
            <dd>{{ stats.drawFps }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.decodeMs') }}</dt>
            <dd>{{ fmtMs(stats.decodeMs) }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.queue') }}</dt>
            <dd>{{ stats.queueDepth }}</dd>
            <dt class="text-white/60">{{ t('screen.perf.recvRate') }}</dt>
            <dd>{{ kbPerSec }} KB/s</dd>
          </dl>
        </template>

        <p class="border-t border-white/15 pt-2" :class="bottleneck.kind === 'none' || bottleneck.kind === 'idle' ? 'text-emerald-300' : 'text-amber-300'">
          <span class="text-white/50">{{ t('screen.perf.hintLabel') }}:</span> {{ t('screen.perf.hint.' + bottleneck.kind, { sent: bottleneck.sent ?? 0, got: bottleneck.got ?? 0 }) }}
        </p>
      </div>

      <p v-if="state === 'connecting'" class="font-mono text-sm text-fg-subtle">{{ t('screen.waitingScreen') }}</p>

      <!-- Dosya birakma katmani -->
      <div
        v-if="dragOver"
        class="pointer-events-none absolute inset-0 z-20 grid place-items-center border-2 border-dashed border-accent bg-black/60"
        aria-hidden="true"
      >
        <p class="flex items-center gap-2 font-mono text-sm text-white">
          <Icon name="lucide:upload" class="size-5" />{{ t('screen.filesDropOverlay') }}
        </p>
      </div>
    </div>
  </div>
</template>
