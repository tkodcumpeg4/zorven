import assert from 'node:assert/strict'
import { test } from 'node:test'
import { IMAGE_MAX_BYTES, isEchoImage, pickPasteImage, pngWithinLimit, remoteImageFileName, scaledSize } from '../app/lib/screenImagePaste.ts'

const f = (name: string, type: string, size = 100) => ({ name, type, size })
const item = (type: string, file: ReturnType<typeof f> | null, kind = 'file') => ({ kind, type, getAsFile: () => file })

test('pickPasteImage: tek image dosyasi; birden cok / gorsel olmayan dosya => null', () => {
  const p = f('image.png', 'image/png')
  assert.equal(pickPasteImage([p], []), p)
  assert.equal(pickPasteImage([f('a.pdf', 'application/pdf')], []), null)
  assert.equal(pickPasteImage([p, f('b.png', 'image/png')], []), null)
  assert.equal(pickPasteImage([], []), null)
})

test('pickPasteImage: dosya listesi bossa tek image ogesi (items) kullanilir', () => {
  const p = f('image.png', 'image/png')
  assert.equal(pickPasteImage([], [item('text/plain', null, 'string'), item('image/png', p)]), p)
  assert.equal(pickPasteImage([], [item('image/png', p), item('image/jpeg', p)]), null)
  assert.equal(pickPasteImage([], [item('application/pdf', f('x', 'application/pdf'))]), null)
})

test('scaledSize: oran korunur, kucultme gerekmezse ayni', () => {
  assert.deepEqual(scaledSize(1000, 500, 2000), { w: 1000, h: 500 })
  assert.deepEqual(scaledSize(4000, 2000, 2000), { w: 2000, h: 1000 })
  assert.deepEqual(scaledSize(1000, 4000, 2000), { w: 500, h: 2000 })
  assert.deepEqual(scaledSize(1000, 500, 0), { w: 1000, h: 500 })
  assert.deepEqual(scaledSize(10000, 1, 100), { w: 100, h: 1 })
})

const blob = (size: number, type: string) => new Blob([new Uint8Array(size)], { type })

test('pngWithinLimit: sinir icindeki PNG oldugu gibi doner (render cagrilmaz)', async () => {
  const src = blob(1000, 'image/png')
  let calls = 0
  const out = await pngWithinLimit(src, async () => { calls++; return blob(1, 'image/png') })
  assert.equal(out, src)
  assert.equal(calls, 0)
})

test('pngWithinLimit: JPEG/WebP kucultmesiz PNG olur', async () => {
  const steps: number[] = []
  const out = await pngWithinLimit(blob(5000, 'image/jpeg'), async (_s, edge) => { steps.push(edge); return blob(2000, 'image/png') })
  assert.equal(out?.type, 'image/png')
  assert.deepEqual(steps, [0])
})

test('pngWithinLimit: buyuk PNG kucultmeye basamak basamak girer; kucultmesiz adim atlanir', async () => {
  const steps: number[] = []
  const out = await pngWithinLimit(blob(IMAGE_MAX_BYTES + 1, 'image/png'), async (_s, edge) => {
    steps.push(edge)
    return blob(edge === 2560 ? IMAGE_MAX_BYTES - 1 : IMAGE_MAX_BYTES + 5, 'image/png')
  })
  assert.ok(out && out.size === IMAGE_MAX_BYTES - 1)
  assert.deepEqual(steps, [3840, 2560])
})

test('pngWithinLimit: hicbir basamak sigmazsa ya da cozme basarisizsa null (dosya olarak yuklenir)', async () => {
  assert.equal(await pngWithinLimit(blob(10, 'image/bmp'), async () => blob(IMAGE_MAX_BYTES + 1, 'image/png')), null)
  assert.equal(await pngWithinLimit(blob(10, 'image/webp'), async () => { throw new Error('decode') }), null)
  assert.equal(await pngWithinLimit(blob(10, 'image/webp'), async () => blob(0, 'image/png')), null)
})

test('isEchoImage: ayni boyut + 15 sn icinde', () => {
  assert.equal(isEchoImage(null, 5, 100), false)
  assert.equal(isEchoImage({ size: 5, at: 0 }, 5, 14000), true)
  assert.equal(isEchoImage({ size: 5, at: 0 }, 5, 16000), false)
  assert.equal(isEchoImage({ size: 5, at: 0 }, 6, 1000), false)
})

test('remoteImageFileName', () => {
  assert.equal(remoteImageFileName(new Date(2026, 10, 17, 9, 5, 3)), 'zorven-pano-20261117-090503.png')
})
