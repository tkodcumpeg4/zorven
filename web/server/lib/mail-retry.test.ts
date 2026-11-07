// Calistirma: cd web && node --test server/lib/mail-retry.test.ts
import { test } from "node:test"
import assert from "node:assert/strict"
import { withMailRetry, isTransientMailError } from "./mail-retry.ts"

const noSleep = async () => {}

test("gecici hata tekrar denenir ve basarir", async () => {
  let n = 0
  const waits: number[] = []
  const out = await withMailRetry(
    async () => {
      n++
      if (n < 3) throw Object.assign(new Error("t"), { code: "ETIMEDOUT" })
      return "ok"
    },
    { sleep: async (ms) => void waits.push(ms), random: () => 0.999999, baseMs: 100, maxMs: 150 },
  )
  assert.equal(out, "ok")
  assert.equal(n, 3)
  assert.deepEqual(waits, [100, 150]) // tam jitter tavani: min(max, base*2^i)
})

test("SMTP 5xx kalici: tekrar yok", async () => {
  let n = 0
  await assert.rejects(
    withMailRetry(async () => {
      n++
      throw Object.assign(new Error("550"), { responseCode: 550 })
    }, { sleep: noSleep }),
  )
  assert.equal(n, 1)
})

test("SMTP 4xx gecici: deneme sayisi kadar tekrar", async () => {
  let n = 0
  await assert.rejects(
    withMailRetry(async () => {
      n++
      throw Object.assign(new Error("451"), { responseCode: 451 })
    }, { sleep: noSleep, attempts: 4 }),
  )
  assert.equal(n, 4)
})

test("siniflandirma", () => {
  assert.equal(isTransientMailError(Object.assign(new Error(), { code: "ECONNRESET" })), true)
  assert.equal(isTransientMailError(new Error("bilinmeyen")), false)
  assert.equal(isTransientMailError(null), false)
})
