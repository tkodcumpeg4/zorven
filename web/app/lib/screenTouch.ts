// Uzak ekran: dokunmatik hareketler icin saf durum makinesi (DOM'a bagimli degil; node --test ile test edilir).
// Sayfa pointer olaylarini (pointerType === 'touch') buraya besler, donen eylemleri uzak girdiye / yerel yakinlastirmaya cevirir.
//
// Hareketler:
//   tek dokunus            = sol tik          (ikinci dokunus 350 ms icinde yakinsa count=2 => cift tik)
//   uzun basma (500 ms)    = sag tik          (hareketsiz)
//   tek parmak surukleme   = imleci parmagin altina tasi; "surukleme kilidi" aciksa sol dugme basili
//   iki parmak surukleme   = tekerlek (dikey/yatay)
//   iki parmak kistirma    = YEREL yakinlastirma (uzak cozunurluk degismez)

export type TouchAction =
  | { type: 'move', x: number, y: number }
  | { type: 'click', button: 'left' | 'right', x: number, y: number, count: 1 | 2 }
  | { type: 'down', button: 'left', x: number, y: number }
  | { type: 'up', button: 'left', x: number, y: number }
  | { type: 'wheel', dx: number, dy: number }
  | { type: 'zoom', factor: number, cx: number, cy: number, panX: number, panY: number }

export interface TouchOptions {
  longPressMs?: number
  /** Bu kadar px'ten fazla hareket "surukleme" sayilir (tik/uzun basma iptal). */
  slopPx?: number
  doubleTapMs?: number
  doubleTapDistPx?: number
  /** Iki parmak arasi mesafe bu kadar degisirse kistirma sayilir. */
  pinchPx?: number
  /** Surukleme kilidi (sol dugme basili surukleme) acik mi; her surukleme basinda okunur. */
  dragLock?: () => boolean
}

interface Finger { x: number, y: number }

export class TouchGestures {
  private o: Required<TouchOptions>
  private fingers = new Map<number, Finger>()
  private mode: 'idle' | 'one' | 'two' | 'ignored' = 'idle'
  // tek parmak
  private one: { id: number, sx: number, sy: number, t: number, sub: 'pending' | 'dragging' | 'longpressed', btnDown: boolean } | null = null
  private lastTap: { t: number, x: number, y: number } | null = null
  // iki parmak
  private two: { a: number, b: number, mode: 'undecided' | 'scroll' | 'pinch', startDist: number, startCx: number, startCy: number, prevDist: number, prevCx: number, prevCy: number } | null = null

  constructor(opts: TouchOptions = {}) {
    this.o = {
      longPressMs: opts.longPressMs ?? 500,
      slopPx: opts.slopPx ?? 10,
      doubleTapMs: opts.doubleTapMs ?? 350,
      doubleTapDistPx: opts.doubleTapDistPx ?? 30,
      pinchPx: opts.pinchPx ?? 12,
      dragLock: opts.dragLock ?? (() => false),
    }
  }

  /** Uzun basma icin sayfanin tick() cagirmasi gereken zaman (ms, olaylarla ayni saat); yoksa null. */
  get nextDeadline(): number | null {
    return this.mode === 'one' && this.one?.sub === 'pending' ? this.one.t + this.o.longPressMs : null
  }

  get active(): boolean { return this.mode !== 'idle' }

  pointerDown(id: number, x: number, y: number, t: number): TouchAction[] {
    if (this.fingers.size === 0) { this.mode = 'idle'; this.one = null; this.two = null }
    this.fingers.set(id, { x, y })
    if (this.mode === 'idle') {
      this.mode = 'one'
      this.one = { id, sx: x, sy: y, t, sub: 'pending', btnDown: false }
      return [{ type: 'move', x, y }]
    }
    if (this.mode === 'one' && this.fingers.size === 2) {
      const out: TouchAction[] = []
      const o = this.one!
      if (o.btnDown) { const f = this.fingers.get(o.id)!; out.push({ type: 'up', button: 'left', x: f.x, y: f.y }) }
      this.one = null
      this.mode = 'two'
      const ids = [...this.fingers.keys()]
      const a = this.fingers.get(ids[0]!)!
      const b = this.fingers.get(ids[1]!)!
      const d = Math.hypot(a.x - b.x, a.y - b.y)
      const cx = (a.x + b.x) / 2
      const cy = (a.y + b.y) / 2
      this.two = { a: ids[0]!, b: ids[1]!, mode: 'undecided', startDist: d, startCx: cx, startCy: cy, prevDist: d, prevCx: cx, prevCy: cy }
      return out
    }
    // ucuncu parmak ya da hareket sonrasi kalan parmak: yok say
    if (this.mode !== 'two') this.mode = 'ignored'
    return []
  }

  pointerMove(id: number, x: number, y: number, _t: number): TouchAction[] {
    const f = this.fingers.get(id)
    if (!f) return []
    f.x = x
    f.y = y
    if (this.mode === 'one' && this.one && this.one.id === id) {
      const o = this.one
      if (o.sub === 'pending') {
        if (Math.hypot(x - o.sx, y - o.sy) <= this.o.slopPx) return []
        o.sub = 'dragging'
        const out: TouchAction[] = []
        if (this.o.dragLock()) { o.btnDown = true; out.push({ type: 'down', button: 'left', x: o.sx, y: o.sy }) }
        out.push({ type: 'move', x, y })
        return out
      }
      if (o.sub === 'dragging') return [{ type: 'move', x, y }]
      return []
    }
    if (this.mode === 'two' && this.two && (id === this.two.a || id === this.two.b)) {
      const w = this.two
      const a = this.fingers.get(w.a)!
      const b = this.fingers.get(w.b)!
      const d = Math.hypot(a.x - b.x, a.y - b.y)
      const cx = (a.x + b.x) / 2
      const cy = (a.y + b.y) / 2
      if (w.mode === 'undecided') {
        const dd = Math.abs(d - w.startDist)
        const cm = Math.hypot(cx - w.startCx, cy - w.startCy)
        if (dd > this.o.pinchPx && dd >= cm) w.mode = 'pinch'
        else if (cm > this.o.slopPx) w.mode = 'scroll'
        else return []
      }
      const out: TouchAction[] = []
      if (w.mode === 'scroll') {
        const dx = cx - w.prevCx
        const dy = cy - w.prevCy
        if (dx || dy) out.push({ type: 'wheel', dx: -dx, dy: -dy })
      } else if (w.prevDist > 0) {
        out.push({ type: 'zoom', factor: d / w.prevDist, cx, cy, panX: cx - w.prevCx, panY: cy - w.prevCy })
      }
      w.prevDist = d
      w.prevCx = cx
      w.prevCy = cy
      return out
    }
    return []
  }

  /** Uzun basma suresi dolduysa sag tik uretir (sayfa nextDeadline'da cagirir). */
  tick(t: number): TouchAction[] {
    const o = this.one
    if (this.mode !== 'one' || !o || o.sub !== 'pending' || t - o.t < this.o.longPressMs) return []
    o.sub = 'longpressed'
    const f = this.fingers.get(o.id)!
    return [{ type: 'click', button: 'right', x: f.x, y: f.y, count: 1 }]
  }

  pointerUp(id: number, t: number, cancelled = false): TouchAction[] {
    const f = this.fingers.get(id)
    this.fingers.delete(id)
    let out: TouchAction[] = []
    if (this.mode === 'one' && this.one && this.one.id === id && f) {
      const o = this.one
      if (o.sub === 'pending' && !cancelled) {
        // Zamanlayici gec kaldiysa da uzun basma sag tik sayilir.
        if (t - o.t >= this.o.longPressMs) {
          out = [{ type: 'click', button: 'right', x: f.x, y: f.y, count: 1 }]
          this.lastTap = null
        } else {
          const lt = this.lastTap
          const dbl = !!lt && t - lt.t <= this.o.doubleTapMs && Math.hypot(f.x - lt.x, f.y - lt.y) <= this.o.doubleTapDistPx
          this.lastTap = dbl ? null : { t, x: f.x, y: f.y }
          out = [{ type: 'click', button: 'left', x: f.x, y: f.y, count: dbl ? 2 : 1 }]
        }
      } else if (o.btnDown) {
        out = [{ type: 'up', button: 'left', x: f.x, y: f.y }]
      }
      this.one = null
      this.mode = 'idle'
    } else if (this.mode === 'two') {
      this.two = null
      this.mode = this.fingers.size === 0 ? 'idle' : 'ignored'
    }
    if (this.fingers.size === 0) this.mode = 'idle'
    return out
  }

  /** Tum parmaklari birak (kontrol kapandi, odak gitti): basili dugme varsa birakir. */
  reset(): TouchAction[] {
    const out: TouchAction[] = []
    if (this.one?.btnDown) {
      const f = this.fingers.get(this.one.id)
      if (f) out.push({ type: 'up', button: 'left', x: f.x, y: f.y })
    }
    this.fingers.clear()
    this.mode = 'idle'
    this.one = null
    this.two = null
    this.lastTap = null
    return out
  }
}

// ---- Yerel yakinlastirma (CSS transform; uzak cozunurluk degismez) --------------------------------

export interface ZoomState { scale: number, tx: number, ty: number }
export const ZOOM_MIN = 1
export const ZOOM_MAX = 6
export const ZOOM_IDENTITY: ZoomState = { scale: 1, tx: 0, ty: 0 }

/** Icerik sahneyi kaplayacak sekilde kaydirmayi sinirla (olcek 1'de kaydirma yok). */
export function clampPan(s: ZoomState, w: number, h: number): ZoomState {
  const mx = ((s.scale - 1) * w) / 2
  const my = ((s.scale - 1) * h) / 2
  return { scale: s.scale, tx: Math.min(mx, Math.max(-mx, s.tx)), ty: Math.min(my, Math.max(-my, s.ty)) }
}

/**
 * Kistirma eylemini uygular. Orijin sahne merkezidir; (cx, cy) sahne icindeki parmak merkezi (px, sahne sol-ust'e gore).
 * Parmagin altindaki nokta sabit kalir; panX/panY merkez kaymasi kadar goruntu de kayar.
 */
export function applyPinch(s: ZoomState, a: { factor: number, cx: number, cy: number, panX: number, panY: number }, w: number, h: number): ZoomState {
  const ns = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, s.scale * a.factor))
  const px = a.cx - w / 2
  const py = a.cy - h / 2
  const r = ns / s.scale
  return clampPan({ scale: ns, tx: px - (px - s.tx) * r + a.panX, ty: py - (py - s.ty) * r + a.panY }, w, h)
}

/** CSS transform degeri; olcek 1'de bos (gereksiz katman olusturma). */
export function zoomTransform(s: ZoomState): string {
  return s.scale === 1 ? '' : `translate(${s.tx.toFixed(1)}px, ${s.ty.toFixed(1)}px) scale(${s.scale.toFixed(3)})`
}

// ---- Ek tus seridi (sanal klavye) -----------------------------------------------------------------

export interface StickyMods { ctrl: boolean, alt: boolean }

/** Yapiskan Ctrl/Alt: bir sonraki tusa uygulanir ve sifirlanir. */
export function consumeSticky(s: StickyMods): { mods: { ctrl: boolean, alt: boolean }, next: StickyMods } {
  return { mods: { ctrl: s.ctrl, alt: s.alt }, next: { ctrl: false, alt: false } }
}

/** Tek karakterden tus kodu (yapiskan Ctrl/Alt + harf icin): a -> KeyA, 5 -> Digit5; digerleri bos. */
export function codeForChar(ch: string): string {
  if (/^[a-z]$/i.test(ch)) return 'Key' + ch.toUpperCase()
  if (/^[0-9]$/.test(ch)) return 'Digit' + ch
  return ''
}
