import assert from 'node:assert/strict'
import { test } from 'node:test'
import { CLIPBOARD_MAX_BYTES, DeferredPaste, clipboardFits, isPasteCombo } from '../app/lib/screenClipboard.ts'

const k = (code: string, m: Partial<{ ctrlKey: boolean, metaKey: boolean, altKey: boolean }> = {}) =>
  ({ code, ctrlKey: false, metaKey: false, altKey: false, ...m })

test('isPasteCombo: Ctrl+V ve Cmd+V; AltGr+V ve duz V degil', () => {
  assert.equal(isPasteCombo(k('KeyV', { ctrlKey: true })), true)
  assert.equal(isPasteCombo(k('KeyV', { metaKey: true })), true)
  assert.equal(isPasteCombo(k('KeyV', { ctrlKey: true, altKey: true })), false)
  assert.equal(isPasteCombo(k('KeyV')), false)
  assert.equal(isPasteCombo(k('KeyC', { ctrlKey: true })), false)
})

test('clipboardFits: bos ve siniri asan metin gonderilmez (UTF-8 bayt)', () => {
  assert.equal(clipboardFits(''), false)
  assert.equal(clipboardFits('merhaba'), true)
  assert.equal(clipboardFits('a'.repeat(CLIPBOARD_MAX_BYTES)), true)
  assert.equal(clipboardFits('a'.repeat(CLIPBOARD_MAX_BYTES + 1)), false)
  // 3 baytlik karakterler: uzunluk sinirin altinda ama bayt olarak ustunde
  assert.equal(clipboardFits('\u3042'.repeat(30000)), false)
  assert.equal(clipboardFits('\u3042'.repeat(20000)), true)
})

test('DeferredPaste: keyup keydowndan sonra gonderilir; tekrar basis yok sayilir', () => {
  const d = new DeferredPaste<string>()
  assert.equal(d.keyup('up'), false, 'bekleyen yokken keyup yutulmaz')
  assert.equal(d.begin('down'), true)
  assert.equal(d.begin('down2'), false, 'oto-tekrar yok sayilir')
  assert.equal(d.active, true)
  assert.equal(d.keyup('up'), true)
  assert.deepEqual(d.finish(), ['down', 'up'])
  assert.equal(d.active, false)
  assert.deepEqual(d.finish(), [])
  // tus hala basiliyken pano hazir olursa yalniz keydown gider; keyup sonra normal yoldan
  assert.equal(d.begin('down'), true)
  assert.deepEqual(d.finish(), ['down'])
  assert.equal(d.keyup('up'), false)
})

test('collectPaste: dosya varsa metin yok sayilir; sinirlar uygulanir', async () => {
  const { collectPaste, PASTE_FILE_MAX_BYTES } = await import('../app/lib/screenClipboard.ts')
  const f = (name: string, size: number) => ({ name, size, type: 'application/octet-stream' })
  assert.deepEqual(collectPaste('merhaba', []), { text: 'merhaba', files: [], tooLarge: [], dropped: 0 })
  const r = collectPaste('a.txt\nb.txt', [f('a.txt', 10), f('buyuk.iso', PASTE_FILE_MAX_BYTES + 1), f('b.txt', 20)])
  assert.equal(r.text, '')
  assert.deepEqual(r.files.map(x => x.name), ['a.txt', 'b.txt'])
  assert.deepEqual(r.tooLarge, ['buyuk.iso'])
  const many = collectPaste('', Array.from({ length: 13 }, (_, i) => f(`f${i}`, 1)))
  assert.equal(many.files.length, 10)
  assert.equal(many.dropped, 3)
})

test('pastedFileName: panodan gelen gorsele zaman damgali ad', async () => {
  const { pastedFileName } = await import('../app/lib/screenClipboard.ts')
  const d = new Date(2026, 10, 17, 9, 5, 7)
  assert.equal(pastedFileName('image.png', 'image/png', d), 'pano-20261117-090507.png')
  assert.equal(pastedFileName('image.jpeg', 'image/jpeg', d), 'pano-20261117-090507.jpeg')
  assert.equal(pastedFileName('rapor.pdf', 'application/pdf', d), 'rapor.pdf')
  assert.equal(pastedFileName('tatil-image.png', 'image/png', d), 'tatil-image.png')
})

test('yeniden baglanma: bekleme sureleri ve hangi hatalarda denenir', async () => {
  const { reconnectDelayMs, shouldAutoRetry } = await import('../app/lib/screenClipboard.ts')
  assert.deepEqual([1, 2, 3, 4, 5, 6, 9].map(reconnectDelayMs), [1000, 2000, 4000, 8000, 15000, 15000, 15000])
  assert.equal(shouldAutoRetry(undefined, undefined), true)
  assert.equal(shouldAutoRetry(502, 'client_offline'), true)
  assert.equal(shouldAutoRetry(503, undefined), true)
  assert.equal(shouldAutoRetry(429, undefined), true)
  assert.equal(shouldAutoRetry(403, undefined), false)
  assert.equal(shouldAutoRetry(404, 'client_not_found'), false)
  assert.equal(shouldAutoRetry(402, 'screen_stream_limit_reached'), false)
  assert.equal(shouldAutoRetry(500, 'forbidden'), false)
})
