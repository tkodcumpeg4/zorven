// Calistirma: node --test --experimental-strip-types web/tests/screenFps.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { FPS_DEFAULT, clampFps, effectiveFpsLimit, fpsOptions, normalizeFpsLimit } from '../app/lib/screenFps.ts'

test('tavan normallestirme: bilinmeyen 30, ust sinir 60', () => {
  assert.equal(normalizeFpsLimit(undefined), 30)
  assert.equal(normalizeFpsLimit(null), 30)
  assert.equal(normalizeFpsLimit(0), 30)
  assert.equal(normalizeFpsLimit(-5), 30)
  assert.equal(normalizeFpsLimit(Number.NaN), 30)
  assert.equal(normalizeFpsLimit('60'), 30) // sayi degil => bilinmiyor
  assert.equal(normalizeFpsLimit(30), 30)
  assert.equal(normalizeFpsLimit(60), 60)
  assert.equal(normalizeFpsLimit(120), 60)
  assert.equal(normalizeFpsLimit(45.9), 45)
})

test('secenekler: 45/60 yalniz tavan 60 ise gorunur', () => {
  assert.deepEqual(fpsOptions(30), [5, 10, 15, 20, 30])
  assert.deepEqual(fpsOptions(60), [5, 10, 15, 20, 30, 45, 60])
  // plan bilgisi yok (eski sunucu) => 30'luk liste
  assert.deepEqual(fpsOptions(undefined), [5, 10, 15, 20, 30])
  // 60'in ustu 60'ta kalir
  assert.deepEqual(fpsOptions(500), [5, 10, 15, 20, 30, 45, 60])
  // ara tavan: yalniz tavani asmayanlar
  assert.deepEqual(fpsOptions(45), [5, 10, 15, 20, 30, 45])
  assert.deepEqual(fpsOptions(20), [5, 10, 15, 20])
  // en kucuk adimin altindaki tavan: tavanin kendisi
  assert.deepEqual(fpsOptions(3), [3])
})

test('45 ve 60 yalniz 60 tavaninda listelenir', () => {
  for (const lim of [undefined, 0, 15, 30, 59]) {
    const o = fpsOptions(lim)
    assert.ok(!o.includes(60), `${lim} tavaninda 60 olmamali`)
  }
  assert.ok(fpsOptions(60).includes(45) && fpsOptions(60).includes(60))
  assert.ok(!fpsOptions(30).includes(45))
})

test('secim tavana indirilir', () => {
  assert.equal(clampFps(60, 30), 30)
  assert.equal(clampFps(45, 30), 30)
  assert.equal(clampFps(60, 60), 60)
  assert.equal(clampFps(45, 60), 45)
  assert.equal(clampFps(15, 60), 15)
  assert.equal(clampFps(30, undefined), 30)
  // gecersiz secim varsayilana (en fazla tavan) doner
  assert.equal(clampFps(Number.NaN, 60), FPS_DEFAULT)
  assert.equal(clampFps(0, 60), FPS_DEFAULT)
  assert.equal(clampFps(0, 10), 10)
  // listede olmayan tavan: tavani asmayan en yuksek secenek
  assert.equal(clampFps(30, 25), 20)
  assert.equal(clampFps(25, 60), 20)
})

test('varsayilan secim hicbir planda 30 ustu degil', () => {
  assert.equal(FPS_DEFAULT, 30)
  assert.equal(clampFps(FPS_DEFAULT, 60), 30)
})

test('etkin tavan: bilet plani ile WS caps.max_fps asgarisi', () => {
  // Team/Enterprise + yeni ajan
  assert.equal(effectiveFpsLimit(60, 60), 60)
  // Team/Enterprise + eski ajan (caps.max_fps = 30): 45/60 gizlenir
  assert.equal(effectiveFpsLimit(60, 30), 30)
  // Free/Hobby/Pro: plan 30
  assert.equal(effectiveFpsLimit(30, 60), 30)
  assert.equal(effectiveFpsLimit(30, 30), 30)
  // caps henuz gelmedi / eski sunucu: yalniz plan
  assert.equal(effectiveFpsLimit(60, undefined), 60)
  assert.equal(effectiveFpsLimit(30, undefined), 30)
  // plan bilgisi yok: 30
  assert.equal(effectiveFpsLimit(undefined, undefined), 30)
  assert.equal(effectiveFpsLimit(0, 60), 30)
  // gecersiz caps yok sayilir
  assert.equal(effectiveFpsLimit(60, 0), 60)
  assert.equal(effectiveFpsLimit(60, Number.NaN), 60)
})
