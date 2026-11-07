// Calistirma: cd web && node --test server/lib/account-internal.test.ts
// Go ile CAPRAZ DIL vektoru: ayni sabitler server/secretderive_test.go (anahtar turetme) ve
// server/api/account_test.go (imza) icinde de var. Bir taraf sapmamali.
import { test } from "node:test"
import assert from "node:assert/strict"
import {
  ACCOUNT_KEY_PURPOSE,
  AccountBlockedError,
  AccountServiceError,
  accountKey,
  accountSignature,
  createAccountClient,
} from "./account-internal.ts"

const MASTER = "zorven-test-master-secret-0123456789"
const KEY_HEX = "fb8ccf24670e3f11f573f12307f799fc3d4ddffb95509cfc5a66b8c6d79b6891"
const BODY = '{"user_id":"usr_8f3a1c","dry_run":false}'
const SIG = "fa8e62c135735ecf3b26bda03024cc8ebaab23e6ada91e5428a5501beec6439d"

test("anahtar turetme ve imza: Go ile ayni vektor", () => {
  assert.equal(ACCOUNT_KEY_PURPOSE, "zorven-account-internal-v1")
  const key = accountKey(MASTER)
  assert.equal(key.toString("hex"), KEY_HEX)
  assert.equal(accountSignature(key, "1800000000", BODY), SIG)
})

type Call = { url: string; init: any }

function fakeFetch(status: number, json: unknown, calls: Call[]) {
  return async (url: string, init?: any) => {
    calls.push({ url, init })
    return new Response(json === undefined ? "" : JSON.stringify(json), {
      status,
      headers: { "content-type": "application/json" },
    })
  }
}

test("plan: dry_run govdesi imzali gonderilir, plan doner", async () => {
  const calls: Call[] = []
  const plan = { user_id: "usr_1", delete_orgs: [], leave_orgs: [], blocking_orgs: [], can_delete: true }
  const c = createAccountClient({
    master: MASTER,
    baseUrl: "https://zorven.app/",
    fetch: fakeFetch(200, plan, calls),
    now: () => 1_800_000_000_000,
  })
  assert.deepEqual(await c.plan("usr_1"), plan)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, "https://zorven.app/api/v1/internal/account/purge")
  const sent = calls[0].init
  assert.equal(sent.method, "POST")
  assert.equal(sent.body, '{"user_id":"usr_1","dry_run":true}')
  assert.equal(sent.headers["x-zorven-ts"], "1800000000")
  assert.equal(sent.headers["x-zorven-sig"], accountSignature(accountKey(MASTER), "1800000000", sent.body))
})

test("purge: 200 sessizce biter; govde dry_run=false", async () => {
  const calls: Call[] = []
  const c = createAccountClient({ master: MASTER, baseUrl: "http://go:8443", fetch: fakeFetch(200, { pseudonym: "x" }, calls) })
  await c.purge("usr_1")
  assert.equal(calls[0].init.body, '{"user_id":"usr_1","dry_run":false}')
})

test("purge: 409 -> AccountBlockedError(plan); diger durumlar AccountServiceError", async () => {
  const plan = {
    user_id: "usr_1",
    delete_orgs: [],
    leave_orgs: [],
    blocking_orgs: [{ id: "o", slug: "ortak", name: "Ortak", role: "owner", members: 3, reason: "sole_owner" }],
    can_delete: false,
  }
  const blocked = createAccountClient({ master: MASTER, baseUrl: "http://go", fetch: fakeFetch(409, { error: {}, plan }, []) })
  await assert.rejects(blocked.purge("usr_1"), (e: any) => e instanceof AccountBlockedError && e.plan.blocking_orgs[0].slug === "ortak")

  for (const st of [401, 500, 503]) {
    const c = createAccountClient({ master: MASTER, baseUrl: "http://go", fetch: fakeFetch(st, { error: { code: "x" } }, []) })
    await assert.rejects(c.purge("usr_1"), (e: any) => e instanceof AccountServiceError && e.status === st, String(st))
    await assert.rejects(c.plan("usr_1"), (e: any) => e instanceof AccountServiceError && e.status === st, "plan " + st)
  }
})

test("ag hatasi ve eksik sir: AccountServiceError (silme guvenli tarafta durur)", async () => {
  const down = createAccountClient({
    master: MASTER,
    baseUrl: "http://go",
    fetch: async () => {
      throw new Error("ECONNREFUSED")
    },
  })
  await assert.rejects(down.purge("usr_1"), (e: any) => e instanceof AccountServiceError && /ECONNREFUSED/.test(e.message))

  const noSecret = createAccountClient({ master: "", baseUrl: "http://go", fetch: fakeFetch(200, {}, []) })
  await assert.rejects(noSecret.plan("usr_1"), AccountServiceError)
})
