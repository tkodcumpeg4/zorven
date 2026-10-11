// Calistirma: cd web && node --test server/lib/auth-hooks.test.ts
// (Node >= 22.18; TypeScript tip silme yerlesik.) Gercek DB gerekmez: memory adapter.
import { test } from "node:test"
import assert from "node:assert/strict"
import { betterAuth } from "better-auth"
import { memoryAdapter } from "better-auth/adapters/memory"
import { twoFactor } from "better-auth/plugins"
import { createOTP } from "@better-auth/utils/otp"
import { authHooks, _resetTotpReplayStore } from "./auth-hooks.ts"

const ORIGIN = "http://localhost:3000"

// totpURI'deki secret parametresi, Better Auth'un uretdigi metnin base32 halidir.
function base32ToText(b32: string): string {
  const A = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
  let bits = ""
  for (const ch of b32.replace(/=+$/, "").toUpperCase()) bits += A.indexOf(ch).toString(2).padStart(5, "0")
  const bytes: number[] = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  return Buffer.from(bytes).toString("utf8")
}

function makeAuth() {
  return betterAuth({
    database: memoryAdapter({ user: [], session: [], account: [], verification: [], twoFactor: [] }),
    secret: "test-secret-test-secret-test-secret-1234",
    baseURL: ORIGIN,
    trustedOrigins: [ORIGIN],
    emailAndPassword: { enabled: true, requireEmailVerification: false },
    hooks: authHooks,
    plugins: [twoFactor({ issuer: "t" })],
  })
}

class Jar {
  c = new Map<string, string>()
  header() {
    return [...this.c].map(([k, v]) => `${k}=${v}`).join("; ")
  }
  absorb(res: Response) {
    for (const sc of res.headers.getSetCookie()) {
      const [kv] = sc.split(";")
      const i = kv.indexOf("=")
      const k = kv.slice(0, i)
      const v = kv.slice(i + 1)
      if (!v || /max-age=0/i.test(sc)) this.c.delete(k)
      else this.c.set(k, v)
    }
  }
}

async function call(auth: any, jar: Jar, method: string, path: string, body?: unknown) {
  const res: Response = await auth.handler(
    new Request(ORIGIN + "/api/auth" + path, {
      method,
      headers: { "content-type": "application/json", origin: ORIGIN, cookie: jar.header() },
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  )
  jar.absorb(res)
  const text = await res.text()
  let json: any = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: res.status, json }
}

test("F-49: get-session govdesinde ham token yok, oturum cerezle calisir", async () => {
  const auth = makeAuth()
  const jar = new Jar()
  const su = await call(auth, jar, "POST", "/sign-up/email", {
    email: "a@example.com",
    password: "password-1234",
    name: "A",
  })
  assert.equal(su.status, 200)
  const gs = await call(auth, jar, "GET", "/get-session")
  assert.equal(gs.status, 200)
  assert.equal(gs.json.user.email, "a@example.com")
  assert.ok(gs.json.session.id)
  assert.equal("token" in gs.json.session, false, "session.token govdede olmamali")
})

test("F-46: kullanilmis TOTP kodu yeni giriste/ayni pencerede tekrar kabul edilmez", async () => {
  _resetTotpReplayStore()
  const auth = makeAuth()
  const jar = new Jar()
  await call(auth, jar, "POST", "/sign-up/email", {
    email: "b@example.com",
    password: "password-1234",
    name: "B",
  })
  const en = await call(auth, jar, "POST", "/two-factor/enable", { password: "password-1234" })
  assert.equal(en.status, 200)
  const secret = base32ToText(new URL(en.json.totpURI).searchParams.get("secret")!)
  const otp = createOTP(secret, { digits: 6, period: 30 })

  // Etkinlestirme dogrulamasi (oturumlu): ilk kullanim kabul.
  const code = await otp.totp()
  const v1 = await call(auth, jar, "POST", "/two-factor/verify-totp", { code })
  assert.equal(v1.status, 200, JSON.stringify(v1.json))
  // Ayni oturum, ayni kod: reddedilir.
  const v1b = await call(auth, jar, "POST", "/two-factor/verify-totp", { code })
  assert.equal(v1b.status, 400, "ayni kod ayni oturumda tekrar kabul edilmemeli")

  // Yeni giris denemesi (farkli two_factor cerezi), ayni kod: reddedilir.
  const jar2 = new Jar()
  const si = await call(auth, jar2, "POST", "/sign-in/email", {
    email: "b@example.com",
    password: "password-1234",
  })
  assert.equal(si.json?.twoFactorRedirect, true, JSON.stringify(si.json))
  const replay = await call(auth, jar2, "POST", "/two-factor/verify-totp", { code })
  assert.equal(replay.status, 400, "ayni kod yeni giriste tekrar kabul edilmemeli")
  assert.match(String(replay.json?.message), /zaten kullanildi/)

  // Komsu pencerenin farkli kodu bu kullanici icin hala gecerli (ileri pencere).
  const next = await createOTP(secret, { digits: 6, period: 30 }).totp()
  if (next !== code) {
    const ok = await call(auth, jar2, "POST", "/two-factor/verify-totp", { code: next })
    assert.equal(ok.status, 200, JSON.stringify(ok.json))
  }
})
