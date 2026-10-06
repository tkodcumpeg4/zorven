<script setup lang="ts">
import type { ScreenHandle, ScreenStats } from '~/composables/useScreen'

const route = useRoute()
const clientID = route.params.id as string

const { connect, sendInput } = useScreen()
const { openUpgrade } = useBilling()
const { t } = useI18n()

const img = ref<HTMLImageElement>()
const video = ref<HTMLVideoElement>()
const stage = ref<HTMLDivElement>()

const state = ref<'connecting' | 'open' | 'error' | 'closed'>('connecting')
const errorMsg = ref('')
const codec = ref('')
const control = ref(true) // fare/klavye kontrolu acik mi

// --- Akis metrikleri --------------------------------------------------------
const stats = ref<ScreenStats | null>(null)
const showStats = ref(false)

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
  const el = (codec.value === 'h264' ? video.value : img.value)
  if (!el) return null
  const rect = el.getBoundingClientRect()

  const natW = codec.value === 'h264'
    ? (video.value?.videoWidth || rect.width)
    : (img.value?.naturalWidth || rect.width)
  const natH = codec.value === 'h264'
    ? (video.value?.videoHeight || rect.height)
    : (img.value?.naturalHeight || rect.height)

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
    const msg = e?.data?.error?.message || (e instanceof Error ? e.message : t('screen.errConnect'))
    errorMsg.value = msg
    if (code === 'screen_stream_limit_reached') {
      openUpgrade(msg, 'screen')
    }
  }
}

function onResolutionChange() {
  void open(selectedWidth.value)
}

onMounted(async () => {
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  // Sekme/pencere odagi kaybolunca basili modifier'lari birak (Alt+Tab).
  window.addEventListener('blur', releaseModifiers)
  await nextTick()
  // Ilk baglanti TAM cozunurlukte: native boyutu ancak boyle ogrenebiliriz.
  await open(0)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('keyup', onKeyUp, true)
  window.removeEventListener('blur', releaseModifiers)
  if (moveRAF) cancelAnimationFrame(moveRAF)
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

      <div class="flex items-center gap-2">
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
          {{ codec === 'h264' ? t('screen.codecHw') : 'MJPEG' }}
        </span>

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
          {{ stats.fps }} fps · {{ stats.kbps }} kbps
          <span v-if="stats.dropped" class="text-danger">· {{ stats.dropped }} ↓</span>
        </button>

        <button
          class="flex cursor-pointer items-center gap-1.5 rounded border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors duration-150 hover:bg-surface"
          :class="control ? 'text-accent' : 'text-fg-muted'"
          :aria-pressed="control"
          :title="t('screen.toggleControl')"
          @click="control = !control"
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
    >
      <img ref="img" hidden :alt="t('screen.remoteScreenAlt')" class="max-h-[80vh] max-w-full object-contain" draggable="false">
      <video ref="video" hidden muted playsinline class="max-h-[80vh] max-w-full object-contain" />

      <p v-if="state === 'connecting'" class="font-mono text-sm text-fg-subtle">{{ t('screen.waitingScreen') }}</p>
    </div>
  </div>
</template>
