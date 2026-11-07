// Calistirma: cd web && node --test server/lib/mailer.test.ts
import { test } from "node:test"
import assert from "node:assert/strict"
import { unsubscribeHeaders } from "./mailer.ts"

test("https baglantisi: List-Unsubscribe + RFC 8058 List-Unsubscribe-Post", () => {
  const h = unsubscribeHeaders("<https://zorven.app/u/announcements?token=a.b.c&lang=ja>")
  assert.equal(h["List-Unsubscribe"], "<https://zorven.app/u/announcements?token=a.b.c&lang=ja>")
  assert.equal(h["List-Unsubscribe-Post"], "List-Unsubscribe=One-Click")
})

test("parantezsiz https degeri parantezlenir (RFC 2369 bicimi)", () => {
  const h = unsubscribeHeaders("https://zorven.app/u/announcements?token=x")
  assert.equal(h["List-Unsubscribe"], "<https://zorven.app/u/announcements?token=x>")
  assert.equal(h["List-Unsubscribe-Post"], "List-Unsubscribe=One-Click")
})

test("mailto: yalniz List-Unsubscribe, tek tik basligi YOK (RFC 8058 yalniz https)", () => {
  const h = unsubscribeHeaders("<mailto:unsubscribe@zorven.app?subject=https://x>")
  assert.equal(h["List-Unsubscribe"], "<mailto:unsubscribe@zorven.app?subject=https://x>")
  assert.equal("List-Unsubscribe-Post" in h, false)
})

test("http (sifresiz) baglanti tek tik basligi almaz", () => {
  const h = unsubscribeHeaders("<http://zorven.app/u/announcements?token=x>")
  assert.equal("List-Unsubscribe-Post" in h, false)
})

test("mailto + https birlikte: tek tik basligi eklenir", () => {
  const h = unsubscribeHeaders("<mailto:u@zorven.app>, <https://zorven.app/u/announcements?token=x>")
  assert.equal(h["List-Unsubscribe-Post"], "List-Unsubscribe=One-Click")
})

test("bos/yok/satir sonlu deger: baslik yok (enjeksiyon engeli)", () => {
  assert.deepEqual(unsubscribeHeaders(undefined), {})
  assert.deepEqual(unsubscribeHeaders(""), {})
  assert.deepEqual(unsubscribeHeaders("   "), {})
  assert.deepEqual(unsubscribeHeaders("<https://a.example/>\r\nBcc: x@y.z"), {})
})
