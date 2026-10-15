/**
 * screen2 ikili mesaj ayristirici (saf fonksiyon; DOM'a bagimli degil).
 * Duzen: docs/screen2-protocol.md. Tum sayilar big-endian.
 */
export const SCREEN2_FRAME_TYPE = 6
export const SCREEN2_VERSION = 1
export const SCREEN2_MAX_MESSAGE = 2 << 20
export const SCREEN2_MAX_RECT_PAYLOAD = 1 << 20
export const SCREEN2_MAX_RECTS = 4096

export const SCREEN2_FLAG_EOF = 1
export const SCREEN2_FLAG_KEYFRAME = 2
export const SCREEN2_FLAG_FIRST = 4

export const ENC_JPEG = 1
export const ENC_PNG = 2
export const ENC_SOLID = 3

const HEADER = 21
const RECT_HEADER = 13

export interface Screen2Rect {
  x: number
  y: number
  w: number
  h: number
  enc: number
  /** Mesaj tamponunun GORUNUMU (kopya degil). */
  payload: Uint8Array
}

export interface Screen2Msg {
  streamID: bigint
  seq: number
  eof: boolean
  keyframe: boolean
  first: boolean
  screenW: number
  screenH: number
  rects: Screen2Rect[]
}

/** Bozuk/siniri asan mesaj icin null doner; asla firlatmaz. */
export function parseScreen2(buf: ArrayBuffer): Screen2Msg | null {
  if (buf.byteLength < HEADER || buf.byteLength > SCREEN2_MAX_MESSAGE) return null
  const dv = new DataView(buf)
  if (dv.getUint8(0) !== SCREEN2_FRAME_TYPE) return null
  if (dv.getUint8(14) !== SCREEN2_VERSION) return null
  const flags = dv.getUint8(13)
  const screenW = dv.getUint16(15)
  const screenH = dv.getUint16(17)
  const n = dv.getUint16(19)
  if (screenW === 0 || screenH === 0 || n > SCREEN2_MAX_RECTS) return null

  const rects: Screen2Rect[] = []
  let off = HEADER
  for (let i = 0; i < n; i++) {
    if (off + RECT_HEADER > buf.byteLength) return null
    const x = dv.getUint16(off)
    const y = dv.getUint16(off + 2)
    const w = dv.getUint16(off + 4)
    const h = dv.getUint16(off + 6)
    const enc = dv.getUint8(off + 8)
    const len = dv.getUint32(off + 9)
    off += RECT_HEADER
    if (w === 0 || h === 0 || x + w > screenW || y + h > screenH) return null
    if (len > SCREEN2_MAX_RECT_PAYLOAD || off + len > buf.byteLength) return null
    if (enc === ENC_SOLID && len !== 4) return null
    if (enc !== ENC_JPEG && enc !== ENC_PNG && enc !== ENC_SOLID) return null // 4 ve bilinmeyenler reddedilir
    rects.push({ x, y, w, h, enc, payload: new Uint8Array(buf, off, len) })
    off += len
  }
  if (off !== buf.byteLength) return null

  return {
    streamID: dv.getBigUint64(1),
    seq: dv.getUint32(9),
    eof: (flags & SCREEN2_FLAG_EOF) !== 0,
    keyframe: (flags & SCREEN2_FLAG_KEYFRAME) !== 0,
    first: (flags & SCREEN2_FLAG_FIRST) !== 0,
    screenW,
    screenH,
    rects,
  }
}
