// Calistirma: cd web && node --test server/lib/account-export.test.ts
// Postgres bolumu icin: ZORVEN_TEST_PG_DSN=postgres://...zorven_test node --test ... (sema Go
// migration'lariyla kurulmus bir TEST veritabani; yoksa o test atlanir).
import { test } from "node:test"
import assert from "node:assert/strict"
import { randomUUID } from "node:crypto"
import { buildUserExport, exportFileName, type Db } from "./account-export.ts"

const NOW = new Date("2026-10-30T12:00:00.000Z")
const EMAIL = "ayse@example.com"

type Rows = Record<string, any[]>

// Tablo adini SQL metninden bulan sahte veritabani; failTables'daki tablolar hata verir.
function fakeDb(rows: Rows, failTables: string[] = []) {
  const calls: { sql: string; values: unknown[] }[] = []
  const db: Db = {
    query: async (sql, values = []) => {
      calls.push({ sql, values })
      const t = /FROM\s+"?([A-Za-z_]+)"?/i.exec(sql)?.[1] ?? ""
      if (failTables.includes(t)) throw new Error(`relation "${t}" does not exist`)
      return { rows: rows[t] ?? [] }
    },
  }
  return { db, calls }
}

const base: Rows = {
  user: [
    {
      id: "usr_1",
      name: "Ayşe",
      email: EMAIL,
      emailVerified: true,
      image: null,
      createdAt: new Date("2026-01-02T03:04:05Z"),
      updatedAt: new Date("2026-02-03T04:05:06Z"),
      twoFactorEnabled: true,
    },
  ],
  account: [
    { providerId: "credential", accountId: "usr_1", scope: null, hasPassword: true, createdAt: new Date("2026-01-02T03:04:05Z"), updatedAt: new Date("2026-01-02T03:04:05Z") },
    { providerId: "github", accountId: "12345", scope: "read:user", hasPassword: false, createdAt: new Date("2026-03-01T00:00:00Z"), updatedAt: new Date("2026-03-01T00:00:00Z") },
  ],
  twoFactor: [{ verified: true }],
  session: [
    { id: "ses_a", createdAt: new Date("2026-10-30T11:00:00Z"), updatedAt: new Date("2026-10-30T11:30:00Z"), expiresAt: new Date("2026-11-06T11:00:00Z"), ipAddress: "203.0.113.7", userAgent: "Firefox", activeOrganizationId: "org_1" },
    { id: "ses_b", createdAt: new Date("2026-09-01T00:00:00Z"), updatedAt: new Date("2026-09-01T00:00:00Z"), expiresAt: new Date("2026-09-08T00:00:00Z"), ipAddress: "198.51.100.2", userAgent: "Chrome", activeOrganizationId: null },
  ],
  member: [{ id: "org_1", name: "Ayşe", slug: "ayse-1", role: "owner", createdAt: new Date("2026-01-02T03:04:05Z") }],
  api_tokens: [
    { id: "tok_1", tenant_id: "org_1", name: "ci", token_prefix: "zrv_api_", scopes: ["read"], last_used_at: null, expires_at: null, revoked_at: null, created_at: new Date("2026-04-01T00:00:00Z"), service_account: false },
  ],
  audit_log: [
    { id: "aud_1", tenant_id: "org_1", action: "tunnel.create", target: "tun_1", detail: "", ip: "203.0.113.7", created_at: new Date("2026-10-01T00:00:00Z") },
  ],
  invitation: [
    { id: "inv_1", organizationId: "org_1", email: "yeni@example.com", role: "member", status: "pending", expiresAt: new Date("2026-11-01T00:00:00Z"), createdAt: new Date("2026-10-20T00:00:00Z") },
  ],
  clients: [
    { id: "cli_1", tenant_id: "org_1", name: "ev-pc", hostname: "EV-PC", os: "windows", arch: "amd64", agent_version: "0.3.2", created_at: new Date("2026-05-01T00:00:00Z"), last_seen_at: new Date("2026-10-30T10:00:00Z") },
  ],
  announcement_prefs: [{ subscribed: false, unsubscribed_at: new Date("2026-06-01T00:00:00Z"), lang: "ja", updated_at: new Date("2026-06-01T00:00:00Z") }],
  user_risk: [{ signup_ip: "192.0.2.10", signup_country: "TR", created_at: new Date("2026-01-02T03:04:05Z") }],
  oauth_grants: [
    { id: "og_1", tenant_id: "org_1", client_name: "Claude", resource: "https://zorven.app/mcp", scope: "read", created_at: new Date("2026-07-01T00:00:00Z"), last_used_at: null, revoked_at: null },
  ],
}

test("tam disa aktarma: bolumler, tarih bicimi, mevcut oturum bayragi", async () => {
  const { db } = fakeDb(base)
  const x = await buildUserExport(db, "usr_1", { now: NOW, currentSessionId: "ses_a" })
  assert.equal(x.format, "zorven-personal-data-export")
  assert.equal(x.version, 1)
  assert.equal(x.generatedAt, "2026-10-30T12:00:00.000Z")
  assert.deepEqual(x.profile, {
    id: "usr_1",
    name: "Ayşe",
    email: EMAIL,
    emailVerified: true,
    image: null,
    createdAt: "2026-01-02T03:04:05.000Z",
    updatedAt: "2026-02-03T04:05:06.000Z",
  })
  assert.deepEqual(
    x.signInMethods.map((m: any) => [m.provider, m.hasPassword]),
    [["credential", true], ["github", false]],
  )
  assert.deepEqual(x.twoFactor, { enabled: true, authenticatorConfigured: true })
  const sess = x.sessions as any[]
  assert.equal(sess.length, 2)
  assert.deepEqual([sess[0].id, sess[0].current, sess[0].active, sess[0].ipAddress, sess[0].userAgent], ["ses_a", true, true, "203.0.113.7", "Firefox"])
  assert.deepEqual([sess[1].id, sess[1].current, sess[1].active], ["ses_b", false, false])
  assert.deepEqual(x.organizations, [{ id: "org_1", name: "Ayşe", slug: "ayse-1", role: "owner", joinedAt: "2026-01-02T03:04:05.000Z" }])
  assert.equal((x.apiKeys[0] as any).prefix, "zrv_api_")
  assert.equal((x.apiKeys[0] as any).name, "ci")
  assert.equal((x.auditLog.entries[0] as any).action, "tunnel.create")
  assert.equal(x.auditLog.truncated, false)
  assert.equal((x.invitationsSent[0] as any).email, "yeni@example.com")
  assert.equal((x.devices[0] as any).hostname, "EV-PC")
  assert.deepEqual((x.preferences as any).productAnnouncements.language, "ja")
  assert.deepEqual(x.signup, { ip: "192.0.2.10", country: "TR", at: "2026-01-02T03:04:05.000Z" })
  assert.equal((x.authorizedApps[0] as any).appName, "Claude")
  assert.deepEqual(x.unavailable, [])
  assert.match(x.excluded, /password hashes/)
})

test("GIZLI DEGER YOK: sorgular token/parola/sir/ozet sutunlarini secmez", async () => {
  const { db, calls } = fakeDb(base)
  await buildUserExport(db, "usr_1", { now: NOW })
  assert.ok(calls.length >= 11, "tum bolumler sorgulanmali")
  const forbidden = [
    /"?accessToken"?/,
    /"?refreshToken"?/,
    /"?idToken"?/,
    /"?backupCodes"?/,
    /\btoken_hash\b/,
    /\btoken_id\b/,
    /"?secret"?\s*[,\s]/i, // twoFactor.secret
    /"session"\."token"|\bs\."token"|"token"\s*[,\s]/,
  ]
  for (const { sql } of calls) {
    // "password" yalniz var/yok bayragi olarak kullanilabilir.
    const cleaned = sql.replace(/\("password" IS NOT NULL\) AS "hasPassword"/g, "")
    assert.ok(!/"password"/.test(cleaned), "parola ozeti secilmis:\n" + sql)
    for (const re of forbidden) assert.ok(!re.test(cleaned), `yasakli sutun ${re} su sorguda:\n${sql}`)
  }
})

test("cikti ANAHTARLARINDA jeton/sir/ozet yok (hasPassword disinda parola yok)", async () => {
  const { db } = fakeDb(base)
  const x = await buildUserExport(db, "usr_1", { now: NOW })
  const keys: string[] = []
  const walk = (v: unknown): void => {
    if (Array.isArray(v)) v.forEach(walk)
    else if (v && typeof v === "object") for (const [k, val] of Object.entries(v)) (keys.push(k), walk(val))
  }
  walk(x)
  for (const k of keys) {
    assert.ok(!/token|secret|hash|backup|accessToken|refreshToken|idToken/i.test(k), "yasakli anahtar: " + k)
    if (/password/i.test(k)) assert.equal(k, "hasPassword")
  }
})

test("denetim kaydi sorgusu: aktor = kucuk harf e-posta ve kullanici kimligi; tavan asilirsa truncated", async () => {
  const many = Array.from({ length: 6 }, (_, i) => ({ id: "aud_" + i, tenant_id: "", action: "a", target: "", detail: "", ip: "", created_at: new Date(2026, 9, 30 - i) }))
  const { db, calls } = fakeDb({ ...base, audit_log: many, user: [{ ...base.user[0], email: "Ayse@Example.com" }] })
  const x = await buildUserExport(db, "usr_1", { now: NOW, auditLimit: 5 })
  assert.equal(x.auditLog.entries.length, 5)
  assert.equal(x.auditLog.truncated, true)
  const q = calls.find((c) => /FROM audit_log/.test(c.sql))!
  assert.deepEqual(q.values[0], ["ayse@example.com", "usr_1"])
  assert.equal(q.values[1], 6) // limit + 1: kesilme tespiti
  // bos alanlar null olur
  assert.deepEqual([(x.auditLog.entries[0] as any).organizationId, (x.auditLog.entries[0] as any).target, (x.auditLog.entries[0] as any).ip], [null, null, null])
})

test("tablo yok / sorgu hatasi: yalniz o bolum atlanir ve acikca soylenir", async () => {
  const { db } = fakeDb(base, ["audit_log", "user_risk", "oauth_grants", "announcement_prefs"])
  const x = await buildUserExport(db, "usr_1", { now: NOW })
  assert.deepEqual(x.unavailable.sort(), ["auditLog", "authorizedApps", "preferences", "signup"])
  assert.deepEqual(x.auditLog, { entries: [], truncated: false })
  assert.equal(x.signup, null)
  assert.equal(x.preferences, null)
  assert.deepEqual(x.authorizedApps, [])
  // diger bolumler saglam
  assert.equal(x.organizations.length, 1)
  assert.equal(x.sessions.length, 2)
})

test("kullanici yoksa hata (profil zorunlu)", async () => {
  const { db } = fakeDb({ ...base, user: [] })
  await assert.rejects(buildUserExport(db, "usr_x", { now: NOW }), /kullanici bulunamadi/)
})

test("indirme dosya adi", () => {
  assert.equal(exportFileName(new Date("2026-10-30T23:59:59Z")), "zorven-data-20261030.json")
})

// --- Gercek Postgres (Go migration'lariyla kurulmus TEST veritabani) -----------------------

const DSN = process.env.ZORVEN_TEST_PG_DSN
test("gercek Postgres semasi: bolumler dolar, hicbir gizli deger JSON'a sizmaz", { skip: !DSN }, async () => {
  const pg = (await import("pg")).default
  const dbName = new URL(DSN!).pathname.slice(1)
  assert.ok(dbName.includes("test"), "ZORVEN_TEST_PG_DSN bir TEST veritabani olmali")
  const pool = new pg.Pool({ connectionString: DSN })
  const id = randomUUID().replaceAll("-", "").slice(0, 10)
  const uid = `usr_x${id}`
  const org = `ten_x${id}`
  const email = `export_${id}@example.com`
  const SECRETS = [`PWHASH${id}`, `GHTOKEN${id}`, `GHREFRESH${id}`, `GHID${id}`, `TOTPSECRET${id}`, `BACKUP${id}`, `SESSTOKEN${id}`, `APIHASH${id}`, `APITOKID${id}`, `CLITOKHASH${id}`]
  const q = (sql: string, v: unknown[] = []) => pool.query(sql, v)
  try {
    await q(`INSERT INTO tenants (id, slug, created_at) VALUES ($1,$2,now())`, [org, `x-${id}`])
    await q(`INSERT INTO "user" ("id","name","email","emailVerified","twoFactorEnabled") VALUES ($1,'Test',$2,true,true)`, [uid, email])
    await q(`INSERT INTO "member" ("id","organizationId","userId","role") VALUES ($1,$2,$3,'owner')`, [`mem_${id}`, org, uid])
    await q(`INSERT INTO "account" ("id","accountId","providerId","userId","password") VALUES ($1,$2,'credential',$2,$3)`, [`acc_${id}`, uid, SECRETS[0]])
    await q(
      `INSERT INTO "account" ("id","accountId","providerId","userId","accessToken","refreshToken","idToken","scope") VALUES ($1,'777','github',$2,$3,$4,$5,'read:user')`,
      [`acg_${id}`, uid, SECRETS[1], SECRETS[2], SECRETS[3]],
    )
    await q(`INSERT INTO "twoFactor" ("id","secret","backupCodes","userId") VALUES ($1,$2,$3,$4)`, [`tf_${id}`, SECRETS[4], SECRETS[5], uid])
    await q(`INSERT INTO "session" ("id","expiresAt","token","userId","ipAddress","userAgent") VALUES ($1, now() + interval '1 day', $2, $3, '203.0.113.9', 'TestUA')`, [`ses_${id}`, SECRETS[6], uid])
    await q(`INSERT INTO api_tokens (id, tenant_id, user_id, name, token_id, token_hash) VALUES ($1,$2,$3,'anahtar',$4,$5)`, [`tok_${id}`, org, uid, SECRETS[8], SECRETS[7]])
    await q(`INSERT INTO audit_log (id, tenant_id, actor, action, target, ip) VALUES ($1,$2,$3,'tunnel.create','tun_1','203.0.113.9'), ($4,$2,'baska@example.com','tunnel.delete','tun_2','')`, [`aud_${id}`, org, email, `aud2_${id}`])
    await q(`INSERT INTO "invitation" ("id","organizationId","email","role","status","expiresAt","inviterId") VALUES ($1,$2,'davet@example.com','member','pending', now() + interval '1 day', $3)`, [`inv_${id}`, org, uid])
    await q(`INSERT INTO clients (id, tenant_id, user_id, name, token_id, token_hash, created_at, hostname) VALUES ($1,$2,$3,'ev-pc',$4,$5,now(),'EV-PC')`, [`cli_${id}`, org, uid, `ctid_${id}`, SECRETS[9]])
    await q(`INSERT INTO announcement_prefs (user_id, subscribed, lang) VALUES ($1,false,'zh')`, [uid])
    await q(`INSERT INTO user_risk (user_id, signup_ip, signup_country) VALUES ($1,'192.0.2.44','TR')`, [uid])
    await q(`INSERT INTO oauth_clients (client_id, client_name) VALUES ($1,'Claude')`, [`oc_${id}`])
    await q(`INSERT INTO oauth_grants (id, user_id, tenant_id, client_id, resource, scope) VALUES ($1,$2,$3,$4,'https://zorven.app/mcp','read')`, [`og_${id}`, uid, org, `oc_${id}`])

    const x = await buildUserExport(pool, uid, { currentSessionId: `ses_${id}` })
    assert.deepEqual(x.unavailable, [])
    assert.equal((x.profile as any).email, email)
    assert.deepEqual(x.signInMethods.map((m: any) => [m.provider, m.hasPassword]).sort(), [["credential", true], ["github", false]])
    assert.deepEqual(x.twoFactor, { enabled: true, authenticatorConfigured: true })
    assert.equal((x.sessions[0] as any).current, true)
    assert.equal((x.sessions[0] as any).ipAddress, "203.0.113.9")
    assert.equal((x.organizations[0] as any).role, "owner")
    assert.equal((x.apiKeys[0] as any).name, "anahtar")
    assert.deepEqual(x.auditLog.entries.map((e: any) => e.action), ["tunnel.create"], "yalniz kullanicinin kayitlari")
    assert.equal((x.invitationsSent[0] as any).email, "davet@example.com")
    assert.equal((x.devices[0] as any).name, "ev-pc")
    assert.equal((x.preferences as any).productAnnouncements.language, "zh")
    assert.equal((x.signup as any).country, "TR")
    assert.equal((x.authorizedApps[0] as any).appName, "Claude")

    const json = JSON.stringify(x)
    for (const s of SECRETS) assert.ok(!json.includes(s), `gizli deger sizdi: ${s}`)
  } finally {
    await q(`DELETE FROM oauth_clients WHERE client_id = $1`, [`oc_${id}`]).catch(() => {})
    await q(`DELETE FROM audit_log WHERE id IN ($1,$2)`, [`aud_${id}`, `aud2_${id}`]).catch(() => {})
    await q(`DELETE FROM tenants WHERE id = $1`, [org]).catch(() => {})
    await q(`DELETE FROM "organization" WHERE id = $1`, [org]).catch(() => {})
    await q(`DELETE FROM "user" WHERE id = $1`, [uid]).catch(() => {})
    await pool.end()
  }
})
