import assert from 'node:assert/strict'
import { test } from 'node:test'
import { RTT_STALE_MS, RTT_WINDOW, RttWindow, formatRtt, isRttStale, rttTone } from '../app/lib/screenLatency.ts'

test('RttWindow: son 5 olcumun hareketli ortalamasi; ajan bacagi varsa toplam', () => {
  const w = new RttWindow()
  assert.equal(w.average(), null)
  assert.deepEqual(w.push(10, 20), { ms: 10, agentMs: 20, total: 30 })
  w.push(20, 40)
  assert.deepEqual(w.average(), { ms: 15, agentMs: 30, total: 45 })
  // pencere asilinca en eski dusur
  for (let i = 0; i < RTT_WINDOW; i++) w.push(100, 0)
  assert.equal(w.count, RTT_WINDOW)
  assert.deepEqual(w.average(), { ms: 100, agentMs: 0, total: 100 })
})

test('RttWindow: eski ajanda (agentMs yok) toplam = panel-sunucu; karisik ornekler yalniz olculenleri ortalar', () => {
  const w = new RttWindow()
  w.push(40)
  assert.deepEqual(w.average(), { ms: 40, agentMs: undefined, total: 40 })
  w.push(60, 10)
  assert.deepEqual(w.average(), { ms: 50, agentMs: 10, total: 60 })
})

test('RttWindow: gecersiz olcum yok sayilir; reset temizler', () => {
  const w = new RttWindow()
  w.push(10)
  w.push(NaN)
  w.push(-5)
  assert.equal(w.count, 1)
  w.reset()
  assert.equal(w.average(), null)
  assert.equal(w.push(Infinity), null)
})

test('rttTone: iyi < 80, orta < 200, kotu', () => {
  assert.equal(rttTone(0), 'good')
  assert.equal(rttTone(79.9), 'good')
  assert.equal(rttTone(80), 'fair')
  assert.equal(rttTone(199), 'fair')
  assert.equal(rttTone(200), 'bad')
})

test('isRttStale: 6 sn olcum yoksa ya da hic olcum yoksa eski', () => {
  assert.equal(isRttStale(1000, null), true)
  assert.equal(isRttStale(1000 + RTT_STALE_MS, 1000), false)
  assert.equal(isRttStale(1000 + RTT_STALE_MS + 1, 1000), true)
})

test('formatRtt', () => {
  assert.equal(formatRtt(41.6), '42 ms')
})
