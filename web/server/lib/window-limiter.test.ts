// Calistirma: cd web && node --test server/lib/window-limiter.test.ts
import { test } from "node:test"
import assert from "node:assert/strict"
import { createWindowLimiter } from "./window-limiter.ts"

const H = 3_600_000

test("saatte 3 hak: dorduncu reddedilir, kalan sure bildirilir", () => {
  const l = createWindowLimiter({ max: 3, windowMs: H })
  const t0 = 1_000_000
  assert.deepEqual(l.take("u1", t0), { ok: true, retryAfterSec: 0 })
  assert.equal(l.take("u1", t0 + 60_000).ok, true)
  assert.equal(l.take("u1", t0 + 120_000).ok, true)
  const r = l.take("u1", t0 + 180_000)
  assert.equal(r.ok, false)
  // en eski hak t0'da; pencere t0+H'de acilir -> kalan = H - 180s
  assert.equal(r.retryAfterSec, Math.ceil((H - 180_000) / 1000))
})

test("pencere kayar: en eski hak dusunce yeniden hak acilir", () => {
  const l = createWindowLimiter({ max: 2, windowMs: 1000 })
  assert.equal(l.take("k", 0).ok, true)
  assert.equal(l.take("k", 400).ok, true)
  assert.equal(l.take("k", 900).ok, false)
  assert.equal(l.take("k", 1001).ok, true) // t=0 hakki dustu
  assert.equal(l.take("k", 1002).ok, false) // 400 ve 1001 duruyor
  assert.equal(l.take("k", 1401).ok, true)
})

test("anahtarlar birbirinden bagimsiz (ayni IP arkasindaki kullanicilar birbirini kilitlemez)", () => {
  const l = createWindowLimiter({ max: 1, windowMs: H })
  assert.equal(l.take("a", 0).ok, true)
  assert.equal(l.take("b", 0).ok, true)
  assert.equal(l.take("a", 1).ok, false)
})

test("reddedilen deneme hak tuketmez", () => {
  const l = createWindowLimiter({ max: 1, windowMs: 1000 })
  assert.equal(l.take("k", 0).ok, true)
  for (let i = 1; i < 50; i++) assert.equal(l.take("k", i).ok, false)
  assert.equal(l.take("k", 999).ok, false) // t=0 hakki pencere icinde
  assert.equal(l.take("k", 1000).ok, true) // pencere doldu; reddedilenler pencereyi uzatmadi
})

test("bellek tavani: cok anahtarda eski kayitlar atilir", () => {
  const l = createWindowLimiter({ max: 1, windowMs: H, maxKeys: 3 })
  for (let i = 0; i < 10; i++) l.take("k" + i, i)
  // en eski anahtar atilmis olmali: yeniden hak alabilir
  assert.equal(l.take("k0", 11).ok, true)
})
