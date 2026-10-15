<script setup lang="ts">
import type { ScreenCursorPos, ScreenFilesState, ScreenHandle, ScreenInputStatus, ScreenStats, ScreenViewerState } from '~/composables/useScreen'
import { FileTransferError, MAX_FILE_BYTES } from '~/composables/useScreenFiles'
import type { RemoteFile } from '~/composables/useScreenFiles'

const route = useRoute()
const clientID = route.params.id as string

const { connect, sendInput } = useScreen()
const { openUpgrade, formatBytes } = useBilling()
const { t } = useI18n()

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

function toggleControl() {
  if (viewOnlyLocked.value) return
  control.value = !control.value
  handle?.sendViewOnly(!control.value)
}

// --- Imlec katmani, pano, kisayollar -----------------------------------------
const caps = ref({ cursor: false, clipboard: false, files: false })
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

function sendClipboard() {
  if (!handle || !clipText.value) return
  handle.sendClipboard(clipText.value)
}

function onClipPaste() {
  if (!clipAutoSend.value) return
  // paste olayi metin kutusu guncellenmeden once gelir.
  void nextTick(() => setTimeout(sendClipboard, 0))
}

function onClipboardStatus(ok: boolean, reason: string) {
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
    }).catch((e: unknown) => {
      const code = e instanceof FileTransferError ? e.code : 'io'
      if (code === 'canceled') {
        x.status = 'canceled'
      } else {
        x.status = 'error'
        x.error = fileErrText(code)
      }
    }).finally(() => {
      x.abort = null
      pumpUploads()
      if (showFiles.value && !xfers.value.some(i => i.status === 'uploading' || i.status === 'queued')) void refreshRemoteFiles()
    })
  }
}

function enqueueFiles(list: File[]) {
  if (!canSendFiles.value) return
  for (const file of list) {
    const x: Xfer = { id: ++xferSeq, file, loaded: 0, status: 'queued', error: '', savedAs: '', abort: null }
    if (file.size > MAX_FILE_BYTES) {
      x.status = 'error'
      x.error = fileErrText('too_large')
    }
    xfers.value.push(x)
  }
  showFiles.value = true
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

function onInputStatusMsg(s: ScreenInputStatus) {
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
const selectedFps = ref(15) // hedef kare hizi (sunucu 30'da sinirlar)
const FPS_OPTIONS = [5, 10, 15, 20, 30]

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
function normPoint(e: MouseEvent): { x: number, y: number } | null {
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
  if (!control.value || !handle) return
  const p = normPoint(e)
  if (!p) return
  e.preventDefault()
  lastPoint = p
  pendingMove = { x: p.x, y: p.y, button: 0, ...mods(e) }
  if (!moveRAF) moveRAF = requestAnimationFrame(flushMove)
}

function onPointerDown(e: PointerEvent) {
  if (!control.value || !handle) return
  const p = normPoint(e)
  if (!p) return
  e.preventDefault()
  // Pointer capture: fare sahne DISINA ciksa da olaylar gelmeye devam eder.
  // Olmadan surukleme (pencere tasima, metin secme) yarida kopuyordu.
  try { (e.currentTarget as Element).setPointerCapture(e.pointerId) } catch { /* destegi yoksa yok say */ }
  lastPoint = p
  // Bekleyen hareketi once bosalt: tiklama dogru konumda olmali.
  flushMove()
  sendInput(handle.ws, { kind: 'mousedown', x: p.x, y: p.y, button: e.button, ...mods(e) })
}

function onPointerUp(e: PointerEvent) {
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

function onKey(kind: string, e: KeyboardEvent) {
  if (!control.value || !handle) return
  // Sayfanin kendi form elemanlarinda (cozunurluk secimi) yazarken tuslari
  // uzak makineye gondermeyelim.
  const el = e.target as HTMLElement | null
  if (el && /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) return

  // Tarayici kisayollarini (F5, Ctrl+W...) uzak makineye gecirmek icin engelle.
  e.preventDefault()
  sendInput(handle.ws, {
    kind,
    key: e.key,
    // code FIZIKSEL tustur: ozel tuslar ve kisayollar bununla eslenir.
    code: e.code,
    repeat: e.repeat,
    ...mods(e),
  })
}

function onKeyDown(e: KeyboardEvent) { if (control.value) onKey('keydown', e) }
function onKeyUp(e: KeyboardEvent) { if (control.value) onKey('keyup', e) }

/**
 * Odak kaybinda basili modifier'lari birak.
 *
 * NEDEN: Alt+Tab ile baska pencereye gecince keyup olayi BU sayfaya hic
 * gelmez; uzak makinede Alt sonsuza dek basili kalir ve sonraki her tus
 * Alt+<tus> olur. Sekmeden ciktigimizda hepsini acikca birakiyoruz.
 */
function releaseModifiers() {
  if (!handle) return
  for (const [key, code] of [['Control', 'ControlLeft'], ['Shift', 'ShiftLeft'], ['Alt', 'AltLeft'], ['Meta', 'MetaLeft']] as const) {
    sendInput(handle.ws, { kind: 'keyup', key, code, ctrl: false, shift: false, alt: false, meta: false })
  }
}

/**
 * Akisi (yeniden) acar. Cozunurluk degistiginde ScreenOpen yeniden gonderilmeli,
 * cunku olcekleme istemci tarafinda yapiliyor — bu yuzden WS'i kapatip aciyoruz.
 */
async function open(maxWidth: number) {
  handle?.close()
  handle = null
  state.value = 'connecting'
  codec.value = ''
  errorMsg.value = ''
  stats.value = null // yeni oturum: eski sayaclar yaniltmasin
  inputStatus.value = null
  viewerState.value = null
  viewers.value = null
  caps.value = { cursor: false, clipboard: false, files: false }
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
      onInputStatus: onInputStatusMsg,
      onCaps: (c) => { caps.value = c },
      onFiles: (f) => { filesState.value = f },
      onCursor: (c) => { cursorPos.value = c; schedulePlaceCursor() },
      onCursorShape: (s) => {
        const url = shapeToUrl(s.w, s.h, s.rgba)
        cursorShape.value = url ? { url, w: s.w, h: s.h, hotX: s.hotX, hotY: s.hotY } : null
        schedulePlaceCursor()
      },
      onClipboard: (text) => {
        remoteClip.value = text
        if (!showClipboard.value) clipUnread.value = true
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
      onOpen: () => { state.value = 'open' },
      onError: (m) => { state.value = 'error'; errorMsg.value = m },
      onClose: (code, reason) => {
        if (state.value === 'connecting' || (code && code !== 1000)) {
          state.value = 'error'
          errorMsg.value = t('screen.errClosed', { code: code ?? 1006, reason: reason ? ' · ' + reason : '' })
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
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  // Sekme/pencere odagi kaybolunca basili modifier'lari birak (Alt+Tab).
  window.addEventListener('blur', releaseModifiers)
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
            :title="t('screen.fpsHint')"
            @change="onResolutionChange"
          >
            <option v-for="f in FPS_OPTIONS" :key="f" :value="f">{{ f }} FPS</option>
          </select>
        </label>

        <span
          v-if="frameW"
          class="rounded border border-line px-2 py-0.5 font-mono text-[10px] text-fg-subtle"
          :title="`Oran ${ratioLabel}`"
        >
          {{ frameW }}×{{ frameH }} · {{ ratioLabel }}
        </span>

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

        <!-- Kisayollar: Ctrl+Alt+Del ve tarayicinin yakalayamadigi kombinasyonlar -->
        <div v-if="control" class="relative">
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
              @click="sendShortcut(sc.id)"
            >
              <Icon :name="sc.id === 'sas' ? 'lucide:shield' : 'lucide:command'" class="size-3.5 text-fg-subtle" aria-hidden="true" />
              {{ t('screen.' + sc.labelKey) }}
            </button>
          </div>
        </div>

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
      v-if="state === 'error'"
      class="flex items-center justify-between gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-sm text-danger"
      role="alert"
    >
      <span>{{ errorMsg }}</span>
      <button
        type="button"
        class="cursor-pointer rounded border border-danger/40 bg-danger/20 px-2.5 py-1 text-xs font-semibold hover:bg-danger/30"
        @click="open(selectedWidth || 0)"
      >
        {{ t('common.retry') }}
      </button>
    </div>

    <!-- Ekran sahnesi: img (MJPEG) veya video (H.264) -->
    <div
      ref="stage"
      class="relative grid min-h-[300px] place-items-center overflow-hidden rounded-xl border border-line bg-black"
      :class="control ? 'cursor-crosshair' : 'cursor-default'"
      tabindex="0"
      @pointermove="onPointerMove"
      @pointerdown="onPointerDown"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
      @contextmenu.prevent
      @wheel="onWheel"
      @dragenter="onDragOver"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <img ref="img" hidden :alt="t('screen.remoteScreenAlt')" class="max-h-[80vh] max-w-full object-contain" draggable="false">
      <video ref="video" hidden muted playsinline class="max-h-[80vh] max-w-full object-contain" />
      <canvas ref="canvas" hidden class="max-h-[80vh] max-w-full object-contain" />

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
