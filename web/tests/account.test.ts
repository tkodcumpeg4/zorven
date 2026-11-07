// Calistirma: cd web && node --test tests/account.test.ts
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import {
  ACCOUNT_ERROR_CODES, DELETE_LINK_MINUTES, DELETE_SESSION_FRESH_MS, accountErrorKey, contentCounts, errorCode, exportFileName,
  sessionIsFresh,
} from '../app/lib/account.ts'
import {
  DELETE_SESSION_FRESH_MS as SERVER_FRESH_MS, DELETE_TOKEN_EXPIRES_SECONDS,
} from '../server/lib/account-delete.ts'
import { exportFileName as serverExportFileName } from '../server/lib/account-export.ts'

test('tazelik suresi ve baglanti omru sunucu ile ayni', () => {
  assert.equal(DELETE_SESSION_FRESH_MS, SERVER_FRESH_MS)
  assert.equal(DELETE_LINK_MINUTES * 60, DELETE_TOKEN_EXPIRES_SECONDS)
})

test('indirme dosya adi sunucu ile ayni', () => {
  const d = new Date('2026-10-30T23:59:59Z')
  assert.equal(exportFileName(d), 'zorven-data-20261030.json')
  assert.equal(exportFileName(d), serverExportFileName(d))
})

test('oturum tazeligi', () => {
  const now = Date.parse('2026-10-30T12:00:00Z')
  assert.equal(sessionIsFresh('2026-10-30T11:30:00Z', now), true)
  assert.equal(sessionIsFresh(new Date('2026-10-30T11:00:01Z'), now), true)
  assert.equal(sessionIsFresh('2026-10-30T11:00:00Z', now), false) // tam 1 saat: taze degil
  assert.equal(sessionIsFresh('2026-10-29T12:00:00Z', now), false)
  assert.equal(sessionIsFresh(undefined, now), false)
  assert.equal(sessionIsFresh('bozuk', now), false)
})

test('hata kodu -> i18n anahtari; bilinmeyen kod genel metne duser', () => {
  assert.equal(accountErrorKey('ACCOUNT_DELETE_OWNER_BLOCKED'), 'account.errors.ACCOUNT_DELETE_OWNER_BLOCKED')
  assert.equal(accountErrorKey('INVALID_PASSWORD'), 'account.errors.INVALID_PASSWORD')
  assert.equal(accountErrorKey('???'), 'account.errors.generic')
  assert.equal(accountErrorKey(undefined), 'account.errors.generic')
  assert.equal(accountErrorKey(''), 'account.errors.generic')
})

test('her hata kodunun dort dilde metni var (+ generic)', () => {
  for (const lang of ['tr', 'en']) {
    const l = JSON.parse(readFileSync(new URL(`../app/i18n/locales/${lang}.json`, import.meta.url), 'utf8'))
    for (const c of [...ACCOUNT_ERROR_CODES, 'generic']) {
      assert.ok(typeof l.account?.errors?.[c] === 'string' && l.account.errors[c].length > 5, `${lang}: account.errors.${c}`)
    }
  }
})

test('sunucunun surekli kullandigi hata kodlari panel listesinde', () => {
  const src = readFileSync(new URL('../server/lib/account-delete.ts', import.meta.url), 'utf8')
  const codes = [...src.matchAll(/"(ACCOUNT_DELETE_[A-Z_]+)"/g)].map(m => m[1])
  assert.ok(codes.length >= 5)
  for (const c of new Set(codes)) assert.ok((ACCOUNT_ERROR_CODES as readonly string[]).includes(c!), `panelde yok: ${c}`)
})

test('icerik sayilari: hepsi 0 ise null', () => {
  assert.equal(contentCounts({}), null)
  assert.equal(contentCounts({ tunnels: 0, clients: 0 }), null)
  assert.deepEqual(contentCounts({ tunnels: 2, apps: 1 }), { tunnels: 2, clients: 0, domains: 0, apps: 1 })
})

test('Better Auth hata nesnesinden kod', () => {
  assert.equal(errorCode({ code: 'INVALID_PASSWORD' }), 'INVALID_PASSWORD')
  assert.equal(errorCode({ error: { code: 'X' } }), 'X')
  assert.equal(errorCode(null), '')
  assert.equal(errorCode(new Error('x')), '')
})
