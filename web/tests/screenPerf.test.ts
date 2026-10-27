// Calistirma: node --test --experimental-strip-types web/tests/screenPerf.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { captureBudgetMs, effectiveQuality, findBottleneck, layerTargetFps, parseAgentStats } from '../app/lib/screenPerf.ts'
import type { AgentStats, BrowserPerf } from '../app/lib/screenPerf.ts'

const st = (avg: number) => ({ avg, p95: avg, n: 10 })

function mk(over: Partial<AgentStats> = {}): AgentStats {
  return {
    window_ms: 1000, fps: 24, capture_fps: 24, target_fps: 24, max_fps: 30, quality: 100, merged: 0, idle: 0, kbps: 1271,
    method: 'dxgi', src_w: 3840, src_h: 2160, w: 800, h: 450,
    capture: st(16), scale: st(5), diff: st(0.2), encode: st(0.9), pack: st(0.1), write: st(0.4),
    layer: 1, layers: [], srv: null, mode: 'tiles', video_kbps: 0, video_bitrate: 0, backend: '',
    ...over,
  }
}
const br = (over: Partial<BrowserPerf> = {}): BrowserPerf => ({ recvFps: 10, drawFps: 10, decodeMs: 26, queueDepth: 0, recvKBps: 556, ...over })

test('gercek kalite: katman girdisi ve tavan', () => {
  const a = mk({ quality: 100, layers: [{ id: 1, quality: 40, spec_quality: 40, target_fps: 13 }] })
  assert.deepEqual(effectiveQuality(a), { value: 40, ceiling: 40 })
  // geri bildirim indirimi: spec 40, gercek 30
  const b = mk({ layers: [{ id: 1, quality: 30, spec_quality: 40, target_fps: 3 }] })
  assert.deepEqual(effectiveQuality(b), { value: 30, ceiling: 40 })
  assert.equal(layerTargetFps(b), 3)
  // eski ajan: katman bilgisi yok => genel kalite, tavan bilinmez
  assert.deepEqual(effectiveQuality(mk({ quality: 70 })), { value: 70, ceiling: null })
  // katmansiz akis (-1): ilk girdi, tavan yok
  const c = mk({ layer: -1, layers: [{ id: -1, quality: 55, spec_quality: 0, target_fps: 20 }] })
  assert.deepEqual(effectiveQuality(c), { value: 55, ceiling: null })
  assert.equal(layerTargetFps(mk()), 24)
})

test('dxgi iken GDI ipucu yok; 4K yakalama 16 ms normal', () => {
  assert.ok(captureBudgetMs(3840, 2160) >= 24 && captureBudgetMs(3840, 2160) <= 26)
  assert.equal(captureBudgetMs(1920, 1080), 8)
  // Yakalama yavas gorunen ama 4K icin normal: captureSrc, asla captureGdi
  const slowCap = mk({ fps: 10, capture: st(40), capture_fps: 12 })
  const normal = mk({ fps: 10, target_fps: 24, capture: st(24), scale: st(3), encode: st(2), write: st(1), capture_fps: 12, idle: 0 })
  assert.equal(findBottleneck(normal, null).kind, 'captureSrc')
  // butceyi asarsa: dxgi => genel 'capture' (GDI demez)
  assert.equal(findBottleneck(slowCap, null).kind, 'capture')
  // yontem GDI iken GDI ipucu
  assert.equal(findBottleneck(mk({ ...slowCap, method: 'gdi' }), null).kind, 'captureGdi')
  assert.equal(findBottleneck(mk({ ...slowCap, method: 'kbinani' }), null).kind, 'captureGdi')
  // 1080p'de 20 ms yakalama yavas sayilir
  const small = mk({ src_w: 1920, src_h: 1080, fps: 8, capture: st(20), capture_fps: 9 })
  assert.equal(findBottleneck(small, null).kind, 'capture')
})

test('ag/izleyici ipucu: ajan 24 gonderir, izleyiciye 10 ulasir', () => {
  const a = mk({ fps: 24, srv: { fps: 10, skipped: 14, queue_ms: 420, feedback: true } })
  const r = findBottleneck(a, br())
  assert.equal(r.kind, 'viewer')
  assert.equal(r.sent, 24)
  assert.equal(r.got, 10)
  // sunucu verisi yoksa tarayici alim hizina bakilir
  const b = mk({ fps: 24, srv: null })
  const r2 = findBottleneck(b, br({ recvFps: 10, drawFps: 10 }))
  assert.equal(r2.kind, 'viewer')
  assert.equal(r2.got, 10)
  // atlama yok => ipucu yok (kendi fps tercihi)
  const c = mk({ fps: 24, srv: { fps: 24, skipped: 0, queue_ms: 0, feedback: true } })
  assert.equal(findBottleneck(c, br({ recvFps: 24, drawFps: 24 })).kind, 'none')
  // tarayici cozme yetismiyorsa once browser
  assert.equal(findBottleneck(a, br({ queueDepth: 6 })).kind, 'browser')
  // az kare gonderildiginde (bosta ekran) viewer onerilmez
  const d = mk({ fps: 2, target_fps: 24, srv: { fps: 1, skipped: 1, queue_ms: 0, feedback: true } })
  assert.notEqual(findBottleneck(d, null).kind, 'viewer')
})

test('parseAgentStats: katman ve srv alanlari guvenle cozulur, eski paket de olur', () => {
  const old = parseAgentStats({ type: 'stats', fps: 12, layer: -1 })
  assert.ok(old)
  assert.deepEqual(old.layers, [])
  assert.equal(old.srv, null)
  const full = parseAgentStats({
    fps: 24,
    layer: 1,
    layers: [{ id: 1, quality: 40, spec_quality: 40, target_fps: 13 }, 'bozuk', { id: 'x', quality: -5 }],
    srv: { fps: 9.5, skipped: 7, queue_ms: 320, feedback: true },
  })
  assert.ok(full)
  assert.equal(full.layers.length, 2)
  assert.deepEqual(full.layers[0], { id: 1, quality: 40, spec_quality: 40, target_fps: 13 })
  assert.equal(full.layers[1].quality, 0)
  assert.deepEqual(full.srv, { fps: 9.5, skipped: 7, queue_ms: 320, feedback: true })
  assert.equal(parseAgentStats({ srv: 'x', layers: 5 })?.srv, null)
  assert.equal(parseAgentStats(null), null)
})
