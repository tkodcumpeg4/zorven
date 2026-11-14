// Calistirma: cd web && node --test server/lib/account-plugin.test.ts
// GERCEK Better Auth 1.7.3 (memory adapter) ile: etkin oturumlar, kisisel veri disa aktarma ve hesap
// silme akisi (taze oturum, parola, e-posta onay baglantisi, Go ic cagrisi, hata yonlendirmesi).
// Gercek e-posta GONDERILMEZ (send sahte); Go'ya ag cagrisi YAPILMAZ (account sahte).
import { test } from "node:test"
import assert from "node:assert/strict"
import { betterAuth } from "better-auth"
import { memoryAdapter } from "better-auth/adapters/memory"
import { bearer } from "better-auth/plugins"
import { accountPlugin } from "./account-plugin.ts"
import { deleteUserOptions, DELETE_SESSION_FRESH_MS, DELETE_TOKEN_EXPIRES_SECONDS } from "./account-delete.ts"
import { AccountBlockedError, AccountServiceError, type AccountClient, type AccountPlan } from "./account-internal.ts"
import { pickEmailLangFrom } from "./email-lang.ts"
import { deleteAccountEmail } from "./email-account.ts"
import type { OutgoingMail } from "./email-callbacks.ts"
import { createWindowLimiter } from "./window-limiter.ts"

const ORIGIN = "http://localhost:3000"
const PW = "password-1234"

class Jar {
  c = new Map<string, string>()
  header(extra = "") {
    const own = [...this.c].map(([k, v]) => `${k}=${v}`).join("; ")
    return [own, extra].filter(Boolean).join("; ")
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

const okPlan = (over: Partial<AccountPlan> = {}): AccountPlan => ({
  user_id: "x",
  delete_orgs: [{ id: "org_p", slug: "kisisel", name: "Kisisel", role: "owner", members: 1, tunnels: 2 }],
  leave_orgs: [],
  blocking_orgs: [],
  can_delete: true,
  ...over,
})

function setup(opts: { rateLimit?: boolean } = {}) {
  const sent: OutgoingMail[] = []
  const db = { user: [] as any[], session: [] as any[], account: [] as any[], verification: [] as any[] }
  const goCalls: string[] = []
  const state = {
    plan: okPlan() as AccountPlan | Error,
    purge: null as null | Error,
    clock: 0, // sinama: ms ekle
  }
  const account: AccountClient = {
    async plan(userId) {
      goCalls.push("plan:" + userId)
      if (state.plan instanceof Error) throw state.plan
      return { ...state.plan, user_id: userId }
    },
    async purge(userId) {
      goCalls.push("purge:" + userId)
      if (state.purge) throw state.purge
    },
  }
  const exportDbCalls: string[] = []
  const exportDb = {
    query: async (sql: string, values: unknown[] = []) => {
      exportDbCalls.push(sql)
      const t = /FROM\s+"?([A-Za-z_]+)"?/i.exec(sql)?.[1] ?? ""
      if (t === "user") {
        const u = db.user.find((x) => x.id === values[0])
        return { rows: u ? [{ ...u, twoFactorEnabled: false }] : [] }
      }
      if (t === "session") {
        return { rows: db.session.filter((s) => s.userId === values[0]).map((s) => ({ ...s })) }
      }
      return { rows: [] }
    },
  }
  const deps = {
    db: exportDb,
    account,
    langFor: async (userId: string | undefined, source: unknown) => pickEmailLangFrom(source, null),
    send: async (m: OutgoingMail) => void sent.push(m),
    now: () => Date.now() + state.clock,
    exportLimiter: createWindowLimiter({ max: 3, windowMs: 3_600_000 }),
  }
  const auth = betterAuth({
    database: memoryAdapter(db),
    secret: "test-secret-test-secret-test-secret-1234",
    baseURL: ORIGIN,
    trustedOrigins: [ORIGIN],
    emailAndPassword: { enabled: true, requireEmailVerification: false },
    user: { deleteUser: deleteUserOptions(deps) },
    plugins: [accountPlugin(deps), bearer()], // auth.ts ile ayni sira
    ...(opts.rateLimit
      ? { rateLimit: { enabled: true, window: 60, max: 1000 }, advanced: { ipAddress: { ipAddressHeaders: ["x-forwarded-for"] } } }
      : {}),
  })

  async function call(
    jar: Jar,
    method: string,
    path: string,
    body?: unknown,
    h: { bearer?: string; cookie?: string; acceptLanguage?: string; origin?: string | null; ip?: string } = {},
  ) {
    const headers: Record<string, string> = { "content-type": "application/json" }
    if (h.origin !== null) headers.origin = h.origin ?? ORIGIN
    const cookie = jar.header(h.cookie)
    if (cookie) headers.cookie = cookie
    if (h.bearer) headers.authorization = `Bearer ${h.bearer}`
    if (h.acceptLanguage) headers["accept-language"] = h.acceptLanguage
    if (h.ip) headers["x-forwarded-for"] = h.ip
    const res: Response = await auth.handler(
      new Request(ORIGIN + "/api/auth" + path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        redirect: "manual",
      }),
    )
    jar.absorb(res)
    const text = await res.text()
    let json: any = null
    try {
      json = JSON.parse(text)
    } catch {}
    return { status: res.status, json, headers: res.headers, text }
  }

  async function signUp(email: string, name = "Ayse"): Promise<Jar> {
    const jar = new Jar()
    const r = await call(jar, "POST", "/sign-up/email", { email, password: PW, name })
    assert.equal(r.status, 200, JSON.stringify(r.json))
    return jar
  }
  async function signIn(email: string): Promise<Jar> {
    const jar = new Jar()
    const r = await call(jar, "POST", "/sign-in/email", { email, password: PW })
    assert.equal(r.status, 200, JSON.stringify(r.json))
    return jar
  }
  return { sent, db, call, signUp, signIn, goCalls, state, exportDbCalls, deps }
}

// --- Etkin oturumlar -----------------------------------------------------------------------

test("oturum listesi: jeton yok, tek 'bu oturum', alanlar dolu; yetkisiz 401", async () => {
  const { call, signUp, signIn } = setup()
  const a = await signUp("a@example.com")
  const b = await signIn("a@example.com")

  const unauth = await call(new Jar(), "GET", "/zorven/sessions")
  assert.equal(unauth.status, 401)

  const r = await call(a, "GET", "/zorven/sessions")
  assert.equal(r.status, 200, r.text)
  assert.equal(r.headers.get("cache-control"), "no-store")
  assert.equal(r.json.length, 2)
  assert.equal(r.json.filter((s: any) => s.current).length, 1)
  for (const s of r.json) {
    assert.ok(s.id && s.createdAt && s.updatedAt && s.expiresAt, "alanlar: " + JSON.stringify(s))
    assert.ok(!("token" in s), "ham jeton sizmamali")
  }
  // b'den bakinca 'bu oturum' ters taraf olmali
  const rb = await call(b, "GET", "/zorven/sessions")
  const curA = r.json.find((s: any) => s.current).id
  const curB = rb.json.find((s: any) => s.current).id
  assert.notEqual(curA, curB)
})

test("tek oturumu sonlandir: o oturum calismaz, digeri calisir; kendi oturumu reddedilir", async () => {
  const { call, signUp, signIn } = setup()
  const a = await signUp("a@example.com")
  const b = await signIn("a@example.com")
  const list = (await call(a, "GET", "/zorven/sessions")).json
  const idB = (await call(b, "GET", "/zorven/sessions")).json.find((s: any) => s.current).id
  const idA = list.find((s: any) => s.current).id

  // kendi oturumu: cikis kullanilmali
  const self = await call(a, "POST", "/zorven/sessions/revoke", { id: idA })
  assert.equal(self.status, 400)
  assert.equal(self.json.code, "CANNOT_REVOKE_CURRENT_SESSION")

  assert.equal((await call(a, "POST", "/zorven/sessions/revoke", { id: idB })).json.status, true)
  assert.equal((await call(a, "GET", "/zorven/sessions")).json.length, 1)
  // b'nin oturumu artik gecersiz
  assert.equal((await call(b, "GET", "/get-session")).json, null)
  assert.ok((await call(a, "GET", "/get-session")).json?.user, "a'nin oturumu etkilenmemeli")

  // gecersiz govde
  assert.equal((await call(a, "POST", "/zorven/sessions/revoke", {})).status, 400)
  assert.equal((await call(a, "POST", "/zorven/sessions/revoke", { id: 5 })).status, 400)
})

test("baska kullanicinin oturumu sonlandirilamaz (idempotent basari, etkisiz)", async () => {
  const { call, signUp } = setup()
  const a = await signUp("a@example.com")
  const mallory = await signUp("m@example.com", "Mallory")
  const idA = (await call(a, "GET", "/zorven/sessions")).json[0].id
  const r = await call(mallory, "POST", "/zorven/sessions/revoke", { id: idA })
  assert.equal(r.status, 200)
  assert.ok((await call(a, "GET", "/get-session")).json?.user, "a'nin oturumu kalmali")
  assert.equal((await call(a, "GET", "/zorven/sessions")).json.length, 1)
})

test("diger tum oturumlari sonlandir (kutuphane ucu): yalniz mevcut kalir", async () => {
  const { call, signUp, signIn } = setup()
  const a = await signUp("a@example.com")
  const b = await signIn("a@example.com")
  const c = await signIn("a@example.com")
  assert.equal((await call(a, "GET", "/zorven/sessions")).json.length, 3)
  const r = await call(a, "POST", "/revoke-other-sessions", {})
  assert.equal(r.status, 200, r.text)
  assert.equal((await call(a, "GET", "/zorven/sessions")).json.length, 1)
  assert.equal((await call(b, "GET", "/get-session")).json, null)
  assert.equal((await call(c, "GET", "/get-session")).json, null)
})

test("kutuphanenin /list-sessions yaniti da jetonsuz (F-49)", async () => {
  const { call, signUp, signIn } = setup()
  const a = await signUp("a@example.com")
  await signIn("a@example.com")
  const r = await call(a, "GET", "/list-sessions")
  assert.equal(r.status, 200, r.text)
  assert.ok(Array.isArray(r.json) && r.json.length === 2)
  for (const s of r.json) assert.ok(!("token" in s), "jeton sizdi: " + JSON.stringify(s))
})

// --- Kisisel veri disa aktarma ---------------------------------------------------------------

test("disa aktarma: indirme basliklari, profil, mevcut oturum; saatte 3, kullanici basina", async () => {
  const { call, signUp, exportDbCalls } = setup()
  const a = await signUp("a@example.com")
  const b = await signUp("b@example.com", "Bob")

  const unauth = await call(new Jar(), "POST", "/zorven/account/export", {})
  assert.equal(unauth.status, 401)

  const r = await call(a, "POST", "/zorven/account/export", {})
  assert.equal(r.status, 200, r.text)
  assert.match(r.headers.get("content-disposition") ?? "", /^attachment; filename="zorven-data-\d{8}\.json"$/)
  assert.equal(r.headers.get("cache-control"), "no-store")
  assert.equal(r.json.format, "zorven-personal-data-export")
  assert.equal(r.json.profile.email, "a@example.com")
  assert.equal(r.json.sessions.length, 1)
  assert.equal(r.json.sessions[0].current, true)
  assert.ok(exportDbCalls.length > 5)

  assert.equal((await call(a, "POST", "/zorven/account/export", {})).status, 200)
  assert.equal((await call(a, "POST", "/zorven/account/export", {})).status, 200)
  const limited = await call(a, "POST", "/zorven/account/export", {})
  assert.equal(limited.status, 429)
  assert.equal(limited.json.code, "EXPORT_RATE_LIMITED")
  assert.ok(Number(limited.headers.get("retry-after")) > 0)
  // baska kullanici (ayni IP) etkilenmez
  assert.equal((await call(b, "POST", "/zorven/account/export", {})).status, 200)
})

test("disa aktarma: baska siteden (Origin) ve GET ile cagrilamaz", async () => {
  const { call, signUp } = setup()
  const a = await signUp("a@example.com")
  const evil = await call(a, "POST", "/zorven/account/export", {}, { origin: "https://evil.example" })
  assert.equal(evil.status, 403, evil.text)
  const get = await call(a, "GET", "/zorven/account/export")
  assert.ok(get.status === 404 || get.status === 405, "GET uc yok: " + get.status)
})

// --- Hesap silme ---------------------------------------------------------------------------

function linkOf(m: OutgoingMail): { url: URL; token: string } {
  const line = m.text.split("\n").find((l) => l.startsWith("http"))!
  const url = new URL(line)
  return { url, token: url.searchParams.get("token")! }
}

test("silme istegi: parola zorunlu, yanlis parola reddedilir; e-posta gonderilmez", async () => {
  const { call, signUp, sent, goCalls } = setup()
  const a = await signUp("a@example.com")
  const noPw = await call(a, "POST", "/delete-user", { callbackURL: "/" })
  assert.equal(noPw.status, 400)
  assert.equal(noPw.json.code, "ACCOUNT_DELETE_PASSWORD_REQUIRED")
  const empty = await call(a, "POST", "/delete-user", { password: "", callbackURL: "/" })
  assert.equal(empty.json.code, "ACCOUNT_DELETE_PASSWORD_REQUIRED")
  const wrong = await call(a, "POST", "/delete-user", { password: "yanlis-parola-1", callbackURL: "/" })
  assert.equal(wrong.status, 400)
  assert.equal(wrong.json.code, "INVALID_PASSWORD")
  assert.equal(sent.length, 0)
  assert.ok(!goCalls.some((c) => c.startsWith("purge")), "hicbir sey silinmemeli")
})

test("silme istegi: oturum taze degilse yeniden giris ister", async () => {
  const { call, signUp, sent, state } = setup()
  const a = await signUp("a@example.com")
  state.clock = DELETE_SESSION_FRESH_MS + 60_000
  const r = await call(a, "POST", "/delete-user", { password: PW, callbackURL: "/" })
  assert.equal(r.status, 403)
  assert.equal(r.json.code, "ACCOUNT_DELETE_SESSION_NOT_FRESH")
  assert.equal(sent.length, 0)
})

test("silme istegi: baska uyeli org'un tek sahibi engellenir (acik mesaj); platform ayri kod", async () => {
  const { call, signUp, sent, state } = setup()
  const a = await signUp("a@example.com")
  state.plan = okPlan({
    can_delete: false,
    blocking_orgs: [{ id: "org_t", slug: "ekip", name: "Ekip", role: "owner", members: 4, reason: "sole_owner" }],
  })
  const r = await call(a, "POST", "/delete-user", { password: PW, callbackURL: "/" }, { acceptLanguage: "en-US,en;q=0.9" })
  assert.equal(r.status, 409)
  assert.equal(r.json.code, "ACCOUNT_DELETE_OWNER_BLOCKED")
  assert.match(r.json.message, /Ekip \(ekip\)/)
  assert.match(r.json.message, /Transfer ownership or delete the organization first/)

  state.plan = okPlan({
    can_delete: false,
    blocking_orgs: [{ id: "ten_default", slug: "default", name: "default", role: "owner", members: 2, reason: "platform" }],
  })
  const p = await call(a, "POST", "/delete-user", { password: PW })
  assert.equal(p.status, 409)
  assert.equal(p.json.code, "ACCOUNT_DELETE_PLATFORM_BLOCKED")
  assert.equal(sent.length, 0)
})

test("silme istegi: Go ulasilamazsa guvenli taraf (503), e-posta yok", async () => {
  const { call, signUp, sent, state } = setup()
  const a = await signUp("a@example.com")
  state.plan = new AccountServiceError("sunucuya ulasilamadi")
  const r = await call(a, "POST", "/delete-user", { password: PW })
  assert.equal(r.status, 503)
  assert.equal(r.json.code, "ACCOUNT_DELETE_UNAVAILABLE")
  assert.equal(sent.length, 0)
})

test("TAM AKIS: istek -> e-posta (panel dilinde, 30 dk) -> baglanti -> Go silmesi -> kullanici gider", async () => {
  const { call, signUp, signIn, sent, goCalls, db } = setup()
  const a = await signUp("a@example.com", "Ayşe")
  const other = await signIn("a@example.com") // ikinci oturum
  const userId = db.user[0].id

  const req = await call(a, "POST", "/delete-user", { password: PW, callbackURL: "/?accountDeleted=1" }, { cookie: "zorven_lang=en", acceptLanguage: "tr" })
  assert.equal(req.status, 200, req.text)
  assert.equal(req.json.message, "Verification email sent")
  assert.equal(sent.length, 1)
  const mail = sent[0]
  assert.equal(mail.to, "a@example.com")
  const { url, token } = linkOf(mail)
  assert.ok(token.length >= 20)
  assert.equal(url.pathname, "/api/auth/delete-user/callback")
  assert.equal(url.searchParams.get("callbackURL"), "/?accountDeleted=1")
  const expected = deleteAccountEmail("Ayşe", "a@example.com", url.toString(), DELETE_TOKEN_EXPIRES_SECONDS / 60, "en")
  assert.equal(mail.subject, expected.subject)
  assert.match(mail.html, /<html lang="en">/)
  assert.ok(mail.text.includes("30"))

  // onay jetonunun omru 30 dakika
  const ver = db.verification.find((v: any) => v.identifier === `delete-account-${token}`)
  assert.ok(ver, "jeton kaydi")
  const ttl = new Date(ver.expiresAt).getTime() - Date.now()
  assert.ok(ttl > 29 * 60_000 && ttl <= 30 * 60_000 + 1000, "omur ~30 dk: " + ttl)

  // Baglanti: oturumsuz tarayicida acilirsa silinmez.
  const anon = await call(new Jar(), "GET", `/delete-user/callback?token=${token}&callbackURL=%2F`)
  assert.equal(anon.status, 302)
  assert.match(anon.headers.get("location") ?? "", /^\/profile\?accountDeleteError=/)
  assert.equal(db.user.length, 1)

  // Oturumlu tarayicida: Go silmesi kullanici silinmeden ONCE cagrilir, sonra kullanici gider.
  const cb = await call(a, "GET", `/delete-user/callback?token=${token}&callbackURL=${encodeURIComponent("/?accountDeleted=1")}`)
  assert.equal(cb.status, 302, cb.text)
  assert.equal(cb.headers.get("location"), "/?accountDeleted=1")
  assert.deepEqual(goCalls.filter((c) => c.startsWith("purge")), ["purge:" + userId])
  assert.equal(db.user.length, 0, "kullanici silinmeli")
  assert.equal(db.account.length, 0)
  assert.equal(db.session.length, 0, "tum oturumlar silinmeli")
  assert.equal((await call(other, "GET", "/get-session")).json, null)

  // Jeton tek kullanimlik: yeniden kullanilamaz.
  const again = await call(a, "GET", `/delete-user/callback?token=${token}&callbackURL=%2F`)
  assert.equal(again.status, 302)
  assert.match(again.headers.get("location") ?? "", /accountDeleteError=/)
})

test("baglanti tiklandiginda engel/Go sorunu: kullanici SILINMEZ, panele hata koduyla yonlendirilir", async () => {
  const { call, signUp, sent, goCalls, state, db } = setup()
  const a = await signUp("a@example.com")
  assert.equal((await call(a, "POST", "/delete-user", { password: PW })).status, 200)
  const { token } = linkOf(sent[0])

  // 1) arada baska uyeli org'un tek sahibi oldu
  state.purge = new AccountBlockedError(
    okPlan({ can_delete: false, blocking_orgs: [{ id: "o", slug: "ekip", name: "Ekip", role: "owner", members: 3, reason: "sole_owner" }] }),
  )
  const blocked = await call(a, "GET", `/delete-user/callback?token=${token}&callbackURL=%2F`)
  assert.equal(blocked.status, 302)
  assert.equal(blocked.headers.get("location"), "/profile?accountDeleteError=ACCOUNT_DELETE_OWNER_BLOCKED")
  assert.equal(db.user.length, 1, "engelde kullanici silinmemeli")

  // 2) Go cevap vermiyor (jeton tuketildi; yeni istek gerekir)
  assert.equal((await call(a, "POST", "/delete-user", { password: PW })).status, 200)
  const second = linkOf(sent[1])
  state.purge = new AccountServiceError("HTTP 503", 503)
  const down = await call(a, "GET", `/delete-user/callback?token=${second.token}&callbackURL=%2F`)
  assert.equal(down.headers.get("location"), "/profile?accountDeleteError=ACCOUNT_DELETE_UNAVAILABLE")
  assert.equal(db.user.length, 1)
  assert.equal(goCalls.filter((c) => c.startsWith("purge")).length, 2)
})

test("JSON API ile jetonla tamamlama (POST token): hata govdesi JSON, yonlendirme degil; basari silinir", async () => {
  const { call, signUp, sent, state, db, goCalls } = setup()
  const a = await signUp("a@example.com")
  assert.equal((await call(a, "POST", "/delete-user", { password: PW })).status, 200)
  const first = linkOf(sent[0])

  // Go engelliyor -> JSON 409 (tarayici GET'inin aksine 302 degil)
  state.purge = new AccountBlockedError(
    okPlan({ can_delete: false, blocking_orgs: [{ id: "o", slug: "ekip", name: "Ekip", role: "owner", members: 3, reason: "sole_owner" }] }),
  )
  const blocked = await call(a, "POST", "/delete-user", { token: first.token })
  assert.equal(blocked.status, 409, blocked.text)
  assert.equal(blocked.json.code, "ACCOUNT_DELETE_OWNER_BLOCKED")
  assert.equal(db.user.length, 1)

  // Engel kalkti; oturum taze olmasa da onay jetonu e-posta sahipligini kanitlar.
  state.purge = null
  assert.equal((await call(a, "POST", "/delete-user", { password: PW })).status, 200)
  const second = linkOf(sent[1])
  state.clock = DELETE_SESSION_FRESH_MS + 60_000
  const done = await call(a, "POST", "/delete-user", { token: second.token })
  assert.equal(done.status, 200, done.text)
  assert.equal(done.json.message, "User deleted")
  assert.equal(db.user.length, 0)
  assert.equal(goCalls.filter((c) => c.startsWith("purge")).length, 2)
})

test("suresi dolmus baglanti reddedilir", async () => {
  const { call, signUp, sent, db } = setup()
  const a = await signUp("a@example.com")
  assert.equal((await call(a, "POST", "/delete-user", { password: PW })).status, 200)
  const { token } = linkOf(sent[0])
  const ver = db.verification.find((v: any) => v.identifier === `delete-account-${token}`)
  ver.expiresAt = new Date(Date.now() - 1000)
  const r = await call(a, "GET", `/delete-user/callback?token=${token}&callbackURL=%2F`)
  assert.equal(r.status, 302)
  assert.match(r.headers.get("location") ?? "", /^\/profile\?accountDeleteError=/)
  assert.equal(db.user.length, 1)
})

test("parolasiz (yalniz sosyal giris) hesap: parola istenmez ama taze oturum sart", async () => {
  const { call, signUp, sent, db, state } = setup()
  const a = await signUp("a@example.com")
  db.account.length = 0 // kimlik bilgisi hesabi yok (yalniz sosyal giris gibi)
  state.clock = DELETE_SESSION_FRESH_MS + 1000
  const stale = await call(a, "POST", "/delete-user", { callbackURL: "/" })
  assert.equal(stale.json.code, "ACCOUNT_DELETE_SESSION_NOT_FRESH")
  state.clock = 0
  const ok = await call(a, "POST", "/delete-user", { callbackURL: "/" })
  assert.equal(ok.status, 200, ok.text)
  assert.equal(sent.length, 1)
})

test("Bearer ile de ayni on kontrol: eski oturum + parolasiz istek reddedilir, dogrulama satiri olusmaz", async () => {
  const { call, signUp, sent, db, state } = setup()
  const jar = await signUp("a@example.com")
  const token = db.session[0].token as string
  const signed = [...jar.c.values()][0] // imzali bicim (cerezdeki deger, yuzde-kodlu)
  state.clock = DELETE_SESSION_FRESH_MS + 1000
  assert.equal((await call(new Jar(), "POST", "/delete-user", {}, { bearer: signed })).status, 403)
  const stale = await call(new Jar(), "POST", "/delete-user", {}, { bearer: token })
  assert.equal(stale.status, 403, stale.text)
  assert.equal(stale.json.code, "ACCOUNT_DELETE_SESSION_NOT_FRESH")
  state.clock = 0
  const noPw = await call(new Jar(), "POST", "/delete-user", {}, { bearer: token })
  assert.equal(noPw.status, 400, noPw.text)
  assert.equal(noPw.json.code, "ACCOUNT_DELETE_PASSWORD_REQUIRED")
  assert.equal(db.verification.length, 0)
  assert.equal(sent.length, 0)
  const ok = await call(new Jar(), "POST", "/delete-user", { password: PW }, { bearer: token })
  assert.equal(ok.status, 200, ok.text)
  assert.equal(sent.length, 1)
})

test("hiz siniri: /delete-user IP basina saatte 5 (eklenti kurali)", async () => {
  const { call, signUp } = setup({ rateLimit: true })
  const a = await signUp("a@example.com")
  let last = 0
  for (let i = 0; i < 6; i++) {
    last = (await call(a, "POST", "/delete-user", {}, { ip: "198.51.100.77" })).status
    if (i < 5) assert.notEqual(last, 429, "istek " + (i + 1))
  }
  assert.equal(last, 429)
})

test("yanlis parola engel bilgisini sizdirmaz: Go plan sorgusu bile yapilmaz; dogru parolada engel bildirilir", async () => {
  const { call, signUp, sent, state, goCalls } = setup()
  const a = await signUp("a@example.com")
  state.plan = okPlan({
    can_delete: false,
    blocking_orgs: [{ id: "ten_x", slug: "x", name: "X", role: "owner", members: 3, reason: "owner" }],
  })
  const wrong = await call(a, "POST", "/delete-user", { password: "yanlis-parola-1", callbackURL: "/" })
  assert.equal(wrong.status, 400)
  assert.equal(wrong.json.code, "INVALID_PASSWORD")
  assert.ok(!/ten_x|X \(|owner/i.test(wrong.text), "engel bilgisi donmemeli: " + wrong.text)
  assert.ok(!goCalls.some((c) => c.startsWith("plan")), "parola dogrulanmadan plan sorgulanmamali")
  const right = await call(a, "POST", "/delete-user", { password: PW, callbackURL: "/" })
  assert.equal(right.status, 409)
  assert.equal(right.json.code, "ACCOUNT_DELETE_OWNER_BLOCKED")
  assert.equal(sent.length, 0)
})
