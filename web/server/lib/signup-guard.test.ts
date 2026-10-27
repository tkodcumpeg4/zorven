// Calistirma: cd web && node --test server/lib/signup-guard.test.ts
// Kayit CAPTCHA + gecici e-posta engeli (memory adapter; gercek DB/ag gerekmez).
import { test } from "node:test"
import assert from "node:assert/strict"
import { betterAuth } from "better-auth"
import { memoryAdapter } from "better-auth/adapters/memory"
import { createAuthHooks } from "./auth-hooks.ts"
import { pickLang, apiBase } from "./signup-guard.ts"

const ORIGIN = "http://localhost:3000"

function makeAuth(hooks: any) {
  return betterAuth({
    database: memoryAdapter({ user: [], session: [], account: [], verification: [] }),
    secret: "test-secret-test-secret-test-secret-1234",
    baseURL: ORIGIN,
    trustedOrigins: [ORIGIN],
    emailAndPassword: { enabled: true, requireEmailVerification: false },
    hooks,
  })
}

async function post(auth: any, path: string, body: unknown, headers: Record<string, string> = {}) {
  const res: Response = await auth.handler(
    new Request(ORIGIN + "/api/auth" + path, {
      method: "POST",
      headers: { "content-type": "application/json", origin: ORIGIN, ...headers },
      body: JSON.stringify(body),
    }),
  )
  const text = await res.text()
  let json: any = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: res.status, json }
}

type Call = { url: string; body: any }
function fakeFetch(calls: Call[], respond: (url: string, body: any) => unknown) {
  return async (url: string, init?: any) => {
    const body = JSON.parse(init.body)
    calls.push({ url, body })
    const out = respond(url, body)
    if (out instanceof Error) throw out
    return new Response(JSON.stringify(out), { status: 200 })
  }
}

const SIGNUP = { email: "c@example.com", password: "password-1234", name: "C" }

test("yardimcilar: dil secimi ve API tabani", () => {
  assert.equal(pickLang("ja,en;q=0.5"), "ja")
  assert.equal(pickLang("en-GB"), "en")
  assert.equal(pickLang("tr-TR"), "tr")
  assert.equal(pickLang(null), "tr")
  assert.equal(apiBase({ ZORVEN_API_INTERNAL_URL: "http://a:1/" }), "http://a:1")
  assert.equal(apiBase({ PLATFORM_DOMAIN: "x.dev" }), "https://x.dev")
  assert.equal(apiBase({}), "https://zorven.app")
})

test("CAPTCHA: gizli anahtar yoksa dogrulama atlanir (gelistirme)", async () => {
  const calls: Call[] = []
  const auth = makeAuth(createAuthHooks({ env: () => ({}), fetch: fakeFetch(calls, () => ({ success: false })) }))
  const r = await post(auth, "/sign-up/email", SIGNUP)
  assert.equal(r.status, 200, JSON.stringify(r.json))
  assert.equal(calls.length, 0)
})

test("CAPTCHA: gecerli token ile kayit gecer, siteverify dogru govdeyle cagrilir", async () => {
  const calls: Call[] = []
  const env = { ZORVEN_CAPTCHA_PLATFORM_SECRET: "zcsec_x", ZORVEN_API_INTERNAL_URL: "http://api.internal/" }
  const auth = makeAuth(createAuthHooks({ env: () => env, fetch: fakeFetch(calls, () => ({ success: true })) }))
  const r = await post(auth, "/sign-up/email", SIGNUP, {
    "x-zorven-captcha": "tok123",
    "x-forwarded-for": "203.0.113.9",
  })
  assert.equal(r.status, 200, JSON.stringify(r.json))
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "http://api.internal/api/v1/captcha/siteverify")
  assert.deepEqual(calls[0].body, { secret: "zcsec_x", response: "tok123", remoteip: "203.0.113.9" })
})

test("CAPTCHA: token yok / gecersiz / servis hatasi -> 400 captcha_failed (dil: accept-language)", async () => {
  const env = { ZORVEN_CAPTCHA_PLATFORM_SECRET: "zcsec_x" }
  let calls: Call[] = []
  let auth = makeAuth(createAuthHooks({ env: () => env, fetch: fakeFetch(calls, () => ({ success: true })) }))
  let r = await post(auth, "/sign-up/email", SIGNUP)
  assert.equal(r.status, 400)
  assert.equal(r.json.code, "captcha_failed")
  assert.match(r.json.message, /doğrulaması başarısız/)
  assert.equal(calls.length, 0, "token yoksa siteverify cagrilmaz")

  calls = []
  auth = makeAuth(
    createAuthHooks({
      env: () => env,
      fetch: fakeFetch(calls, () => ({ success: false, "error-codes": ["invalid-input-response"] })),
    }),
  )
  r = await post(auth, "/sign-up/email", SIGNUP, { "x-zorven-captcha": "bad", "accept-language": "en-US,en;q=0.9" })
  assert.equal(r.status, 400)
  assert.equal(r.json.code, "captcha_failed")
  assert.match(r.json.message, /Human verification failed/)

  calls = []
  auth = makeAuth(createAuthHooks({ env: () => env, fetch: fakeFetch(calls, () => new Error("ECONNREFUSED")) }))
  r = await post(auth, "/sign-up/email", SIGNUP, { "x-zorven-captcha": "t", "accept-language": "ja" })
  assert.equal(r.status, 400, "servis ulasilamazsa kapali tutulur")
  assert.equal(r.json.code, "captcha_failed")
  assert.match(r.json.message, /人間確認/)
})

test("CAPTCHA yalniz /sign-up/email yolunda: sign-in etkilenmez", async () => {
  const env = { ZORVEN_CAPTCHA_PLATFORM_SECRET: "zcsec_x" }
  const calls: Call[] = []
  const auth = makeAuth(createAuthHooks({ env: () => env, fetch: fakeFetch(calls, () => ({ success: true })) }))
  const su = await post(auth, "/sign-up/email", SIGNUP, { "x-zorven-captcha": "ok" })
  assert.equal(su.status, 200, JSON.stringify(su.json))
  calls.length = 0
  const si = await post(auth, "/sign-in/email", { email: SIGNUP.email, password: SIGNUP.password })
  assert.equal(si.status, 200, JSON.stringify(si.json))
  assert.equal(calls.length, 0)
})
