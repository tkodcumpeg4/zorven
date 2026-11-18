import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  CLIP_IMAGE_MARKER, CLIP_IMAGE_MAX_BYTES, buildPing, decodeClipboardImage, encodeClipboardImage,
  isClipboardImageMessage, looksLikePng, parseCapsExtra, parsePong, shouldMeasureRtt,
} from '../app/composables/screenWire.ts'

// Gecerli imza + IHDR'li (en az 33 bayt) sahte PNG.
function fakePng(extra = 0): Uint8Array {
  const b = new Uint8Array(33 + extra)
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  return b
}

const toBuf = async (b: Blob) => await b.arrayBuffer()

test('encodeClipboardImage: marker + PNG; bos ve 8 MiB ustu reddedilir', async () => {
  const png = fakePng(100)
  const msg = encodeClipboardImage(new Blob([png]))
  assert.ok(msg)
  const buf = new Uint8Array(await toBuf(msg!))
  assert.equal(buf[0], CLIP_IMAGE_MARKER)
  assert.equal(buf.length, png.length + 1)
  assert.deepEqual([...buf.slice(1, 9)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

  assert.equal(encodeClipboardImage(new Blob([])), null)
  assert.equal(encodeClipboardImage(new Blob([new Uint8Array(CLIP_IMAGE_MAX_BYTES + 1)])), null)
  assert.ok(encodeClipboardImage(new Blob([new Uint8Array(CLIP_IMAGE_MAX_BYTES)])))
})

test('decodeClipboardImage: gidis-donus ve gecersiz girdiler', async () => {
  const png = fakePng(50)
  const wire = await toBuf(encodeClipboardImage(new Blob([png]))!)
  assert.equal(isClipboardImageMessage(wire), true)
  const out = decodeClipboardImage(wire)
  assert.ok(out)
  assert.equal(out!.type, 'image/png')
  assert.deepEqual(new Uint8Array(await toBuf(out!)), png)

  // Ekran karesi (ilk bayt 6 / 9) pano gorseli degildir.
  for (const first of [6, 9, 1, 0]) {
    const b = new Uint8Array(40)
    b[0] = first
    assert.equal(isClipboardImageMessage(b.buffer), false)
    assert.equal(decodeClipboardImage(b.buffer), null)
  }
  // Marker var ama PNG imzasi yok / cok kisa.
  const bad = new Uint8Array(40)
  bad[0] = CLIP_IMAGE_MARKER
  assert.equal(decodeClipboardImage(bad.buffer), null)
  assert.equal(isClipboardImageMessage(new Uint8Array([CLIP_IMAGE_MARKER]).buffer), false)
  assert.equal(looksLikePng(new Uint8Array(10)), false)
})

test('parseCapsExtra: eski sunucu/ajanda alan yok; gecerli monitor listesi', () => {
  // Eski sunucu: hicbir yeni alan yok -> displays undefined (panel 1 varsayar).
  assert.deepEqual(parseCapsExtra({}), { clipboardImage: false })
  assert.deepEqual(parseCapsExtra({ clipboard_image: false }), { clipboardImage: false })
  assert.deepEqual(parseCapsExtra({ clipboard_image: true }), { clipboardImage: true })
  assert.deepEqual(parseCapsExtra({ displays: 1, display: 0 }), { clipboardImage: false, displays: 1, display: 0 })
  assert.deepEqual(parseCapsExtra({ displays: 3, display: 2 }), { clipboardImage: false, displays: 3, display: 2 })
  // Aralik disi secili monitor 0'a duser.
  assert.equal(parseCapsExtra({ displays: 2, display: 5 }).display, 0)
  assert.equal(parseCapsExtra({ displays: 2, display: -1 }).display, 0)
  assert.equal(parseCapsExtra({ displays: 2 }).display, 0)
  // Gecersiz sayi: yok sayilir.
  for (const bad of [0, -1, 33, 2.5, '2', null, Number.NaN]) {
    assert.equal(parseCapsExtra({ displays: bad, display: 0 }).displays, undefined, String(bad))
  }
  // clipboard_image yalniz tam true.
  assert.equal(parseCapsExtra({ clipboard_image: 1 }).clipboardImage, false)
})

test('buildPing / parsePong: t yankisindan gidis-donus', () => {
  const p = JSON.parse(buildPing(1234.5))
  assert.deepEqual(p, { type: 'ping', t: 1234.5 })
  assert.deepEqual(parsePong({ t: 1000 }, 1042.5), { ms: 42.5, agentMs: undefined })
  assert.deepEqual(parsePong({ t: 1000, agent_ms: 12.3 }, 1042.5), { ms: 42.5, agentMs: 12.3 })
  // agent_ms gecersizse yok sayilir.
  assert.equal(parsePong({ t: 1000, agent_ms: -1 }, 1010)!.agentMs, undefined)
  assert.equal(parsePong({ t: 1000, agent_ms: 'x' }, 1010)!.agentMs, undefined)
  // Gecersiz t: gelecek, 60 sn'den eski, sayi degil.
  assert.equal(parsePong({ t: 2000 }, 1000), null)
  assert.equal(parsePong({ t: 0 }, 61_000), null)
  assert.equal(parsePong({ t: '5' }, 10), null)
  assert.equal(parsePong({}, 10), null)
  assert.equal(parsePong({ t: Number.NaN }, 10), null)
})

test('shouldMeasureRtt: yalniz acik WS ve gorunur sekme', () => {
  assert.equal(shouldMeasureRtt('visible', true), true)
  assert.equal(shouldMeasureRtt('hidden', true), false)
  assert.equal(shouldMeasureRtt('visible', false), false)
  assert.equal(shouldMeasureRtt('prerender', true), false)
})
