// Hesap silme: web servisi (Better Auth) -> Go sunucusu ic cagrisi.
//
// Tek uyeli organizasyonlari tum kaynaklariyla silmek ve digerlerinden uyeligi kaldirmak
// Go'nun isidir (server/store/pgstore/account_delete.go; bellekteki canli durum da orada
// temizlenir). Go ucu: POST /api/v1/internal/account/purge (server/api/account.go).
// Oturum cerezi tasimaz; kimlik YALNIZCA imzayladir:
//
//   anahtar = HKDF-SHA256(ana sir, tuz yok, bilgi "zorven-account-internal-v1") -> 32 bayt
//             (Go: server/secretderive.go deriveKey + keyPurposeAccount)
//   imza    = hex(HMAC-SHA256(anahtar, "zorven-account-v1\n" + unixSaniye + "\n" + govde))
//             basliklar: X-Zorven-Ts, X-Zorven-Sig (Go: api/account.go accountSignature)
//
// Ana sir BETTER_AUTH_SECRET'tir (iki servise de ayni deger verilir).
// IKI TARAFIN AYNI CIKTIYI URETTIGI capraz dil vektoruyla sabitlenir:
// account-internal.test.ts <-> server/api/account_test.go (TestAccountSignatureVektoru) ve
// server/secretderive_test.go (TestDeriveKeyAccountVektoru).

import { createHmac, hkdfSync } from "node:crypto"

export const ACCOUNT_KEY_PURPOSE = "zorven-account-internal-v1"
const SIG_PREFIX = "zorven-account-v1"

export type FetchFn = (url: string, init?: any) => Promise<Response>

/** Ana sirdan ic cagri anahtari (Go deriveKey(master, keyPurposeAccount) ile ayni). */
export function accountKey(master: string): Buffer {
  return Buffer.from(hkdfSync("sha256", master, Buffer.alloc(0), ACCOUNT_KEY_PURPOSE, 32))
}

/** Istek imzasi (Go accountSignature ile birebir). */
export function accountSignature(key: Uint8Array, ts: string, body: string): string {
  return createHmac("sha256", key).update(`${SIG_PREFIX}\n${ts}\n${body}`).digest("hex")
}

/** Silme planindaki bir organizasyon (Go store.AccountOrg). */
export interface AccountOrg {
  id: string
  slug: string
  name: string
  role: string
  members: number
  /** Yalniz engellerde: "sole_owner" | "platform". */
  reason?: string
  tunnels?: number
  clients?: number
  domains?: number
  apps?: number
  secrets?: number
}

/** Hesap silinirse ne olacagi (Go planResponse). */
export interface AccountPlan {
  user_id: string
  delete_orgs: AccountOrg[]
  leave_orgs: AccountOrg[]
  blocking_orgs: AccountOrg[]
  can_delete: boolean
}

/** Go ulasilamadi/yapilandirilmamis/beklenmeyen yanit: silme GUVENLI tarafta durur. */
export class AccountServiceError extends Error {
  status?: number
  constructor(message: string, status?: number) {
    super(message)
    this.name = "AccountServiceError"
    this.status = status
  }
}

/** Plan silmeyi engelliyor (409). */
export class AccountBlockedError extends Error {
  plan: AccountPlan
  constructor(plan: AccountPlan) {
    super("account deletion blocked")
    this.name = "AccountBlockedError"
    this.plan = plan
  }
}

export interface AccountClient {
  /** Salt okunur on kontrol (dry_run). */
  plan(userId: string): Promise<AccountPlan>
  /** Silmeyi uygular; engel varsa AccountBlockedError, hizmet sorunuysa AccountServiceError. */
  purge(userId: string): Promise<void>
}

export interface AccountClientOptions {
  /** Ana sir (BETTER_AUTH_SECRET). */
  master: string
  /** Go API tabani (signup-guard.ts apiBase ile ayni kural). */
  baseUrl: string
  fetch?: FetchFn
  /** Saat (ms); testlerde enjekte edilir. */
  now?: () => number
  /** On kontrol zaman asimi (ms). */
  planTimeoutMs?: number
  /** Silme zaman asimi (ms): buyuk kiracilarda log silme uzun surebilir. */
  purgeTimeoutMs?: number
}

export function createAccountClient(opts: AccountClientOptions): AccountClient {
  const doFetch: FetchFn = opts.fetch ?? ((url, init) => fetch(url, init))
  const now = opts.now ?? Date.now
  const planTimeout = opts.planTimeoutMs ?? 10_000
  const purgeTimeout = opts.purgeTimeoutMs ?? 120_000

  async function call(userId: string, dryRun: boolean): Promise<{ status: number; json: any }> {
    if (!opts.master) throw new AccountServiceError("BETTER_AUTH_SECRET tanimli degil")
    const body = JSON.stringify({ user_id: userId, dry_run: dryRun })
    const ts = String(Math.floor(now() / 1000))
    const sig = accountSignature(accountKey(opts.master), ts, body)
    let res: Response
    try {
      res = await doFetch(`${opts.baseUrl.replace(/\/+$/, "")}/api/v1/internal/account/purge`, {
        method: "POST",
        headers: { "content-type": "application/json", "x-zorven-ts": ts, "x-zorven-sig": sig },
        body,
        signal: AbortSignal.timeout(dryRun ? planTimeout : purgeTimeout),
      })
    } catch (e) {
      throw new AccountServiceError(`sunucuya ulasilamadi: ${(e as Error)?.message ?? e}`)
    }
    let json: any = null
    try {
      json = await res.json()
    } catch {
      /* govde yok/JSON degil */
    }
    return { status: res.status, json }
  }

  return {
    async plan(userId) {
      const { status, json } = await call(userId, true)
      if (status !== 200 || !json || typeof json !== "object") {
        throw new AccountServiceError(`on kontrol basarisiz (HTTP ${status})`, status)
      }
      return json as AccountPlan
    },
    async purge(userId) {
      const { status, json } = await call(userId, false)
      if (status === 200) return
      if (status === 409 && json?.plan) throw new AccountBlockedError(json.plan as AccountPlan)
      throw new AccountServiceError(`silme basarisiz (HTTP ${status})`, status)
    },
  }
}
