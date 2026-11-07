// Calistirma: node --test web/tests/ephemeral.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  DEFAULT_TTL_SEC, MAX_TTL_SEC, MIN_TTL_SEC, TTL_PRESETS_SEC,
  checkTunnelName, isExpiringSoon, isValidTtl, normalizeTarget, randomEphemeralName,
  remainingLabel, remainingMs, tunnelUrl, usedTunnelLabels,
} from '../app/lib/ephemeral.ts'

test('sure secenekleri sunucu araliginda; varsayilan 2 saat listede', () => {
  assert.equal(MIN_TTL_SEC, 60)
  assert.equal(MAX_TTL_SEC, 86400)
  for (const s of TTL_PRESETS_SEC) assert.ok(isValidTtl(s), `${s} gecerli olmali`)
  assert.ok(TTL_PRESETS_SEC.includes(DEFAULT_TTL_SEC))
  // Sirali ve tekrarsiz
  assert.deepEqual([...TTL_PRESETS_SEC], [...TTL_PRESETS_SEC].sort((a, b) => a - b))
  assert.equal(new Set(TTL_PRESETS_SEC).size, TTL_PRESETS_SEC.length)
})

test('isValidTtl: sinirlar ve gecersiz degerler', () => {
  assert.equal(isValidTtl(59), false)
  assert.equal(isValidTtl(60), true)
  assert.equal(isValidTtl(86400), true)
  assert.equal(isValidTtl(86401), false)
  assert.equal(isValidTtl(0), false)
  assert.equal(isValidTtl(-5), false) // sunucu negatifi sessizce kalici tunel sayar
  assert.equal(isValidTtl(90.5), false)
  assert.equal(isValidTtl(Number.NaN), false)
})

test('normalizeTarget: CLI kisayollari', () => {
  assert.equal(normalizeTarget('3000'), 'http://localhost:3000')
  assert.equal(normalizeTarget(' :8080 '), 'http://localhost:8080')
  assert.equal(normalizeTarget('127.0.0.1:8080'), 'http://127.0.0.1:8080')
  assert.equal(normalizeTarget('localhost:5173/app'), 'http://localhost:5173/app')
  assert.equal(normalizeTarget('http://localhost:8000'), 'http://localhost:8000')
  assert.equal(normalizeTarget('https://intranet.test:8443/'), 'https://intranet.test:8443')
  assert.equal(normalizeTarget('[::1]:3000'), 'http://[::1]:3000')
})

test('normalizeTarget: sema kucuk harfe cevrilir (sunucu onek denetimi buyuk/kucuk duyarli)', () => {
  assert.equal(normalizeTarget('HTTP://Localhost:3000'), 'http://Localhost:3000')
  assert.equal(normalizeTarget('HTTPS://x.test'), 'https://x.test')
})

test('normalizeTarget: gecersiz girdiler bos doner', () => {
  for (const bad of ['', '   ', '0', '65536', '99999', 'a b', 'localhost:99999', 'http://', 'ftp://x', 'tcp://x:1', 'http://ho st:1', '://x']) {
    assert.equal(normalizeTarget(bad), '', `${JSON.stringify(bad)} reddedilmeli`)
  }
})

test('checkTunnelName: etiket kurallari ve kullanimdaki adlar', () => {
  const used = new Set(['api'])
  assert.equal(checkTunnelName('', used), 'required')
  assert.equal(checkTunnelName('   ', used), 'required')
  assert.equal(checkTunnelName('tmp-a1b2', used), '')
  assert.equal(checkTunnelName('  TMP-A1B2 ', used), '') // kirpilir ve kucultulur
  assert.equal(checkTunnelName('api', used), 'taken')
  assert.equal(checkTunnelName('API', used), 'taken')
  assert.equal(checkTunnelName('a--b', used), 'invalid') // "--" kiraci ayraci
  assert.equal(checkTunnelName('-abc', used), 'invalid')
  assert.equal(checkTunnelName('abc-', used), 'invalid')
  assert.equal(checkTunnelName('a_b', used), 'invalid')
  assert.equal(checkTunnelName('a.b', used), 'invalid')
  assert.equal(checkTunnelName('x'.repeat(63), used), '')
  assert.equal(checkTunnelName('x'.repeat(64), used), 'invalid')
})

test('usedTunnelLabels: yalniz kapsamli adlardan etiket cikarir', () => {
  const set = usedTunnelLabels([
    { fqdn: 'api--acme.zorven.app', type: 'scoped' },
    { fqdn: 'Web--acme.zorven.app' }, // tip yok: kapsamli sayilir
    { fqdn: 'shop.example.com', type: 'custom' },
    { fqdn: 'a--b.example.com', type: 'custom' }, // ozel alan adi: etiket cakismaz
    { fqdn: 'kisa.zorven.app', type: 'global' },
  ])
  assert.deepEqual([...set].sort(), ['api', 'web'])
})

test('randomEphemeralName: gecerli etiket uretir', () => {
  assert.equal(randomEphemeralName(() => 0), 'tmp-aaaa')
  assert.equal(randomEphemeralName(() => 0.999999), 'tmp-9999')
  for (let i = 0; i < 200; i++) {
    const n = randomEphemeralName()
    assert.equal(checkTunnelName(n), '', n)
    assert.ok(!n.includes('zorven'))
  }
})

test('remainingMs: gecersiz ve eksik bitis', () => {
  const now = Date.parse('2026-10-30T12:00:00Z')
  assert.equal(remainingMs(undefined, now), null)
  assert.equal(remainingMs('', now), null)
  assert.equal(remainingMs('bozuk', now), null)
  assert.equal(remainingMs('2026-10-30T14:00:00Z', now), 2 * 3600 * 1000)
  assert.equal(remainingMs('2026-10-30T11:59:00Z', now), -60 * 1000)
})

test('remainingLabel: dakika en yakin dakikaya yuvarlanir', () => {
  const m = 60 * 1000
  assert.deepEqual(remainingLabel(0), { kind: 'expired', h: 0, m: 0, s: 0 })
  assert.deepEqual(remainingLabel(-1), { kind: 'expired', h: 0, m: 0, s: 0 })
  assert.deepEqual(remainingLabel(45_000), { kind: 's', h: 0, m: 0, s: 45 })
  assert.deepEqual(remainingLabel(59_001), { kind: 'm', h: 0, m: 1, s: 0 }) // 60 sn'ye yuvarlanir: "1 dk"
  assert.deepEqual(remainingLabel(1), { kind: 's', h: 0, m: 0, s: 1 })
  assert.deepEqual(remainingLabel(61_000), { kind: 'm', h: 0, m: 1, s: 0 })
  assert.deepEqual(remainingLabel(150_000), { kind: 'm', h: 0, m: 3, s: 0 })
  assert.deepEqual(remainingLabel(15 * m), { kind: 'm', h: 0, m: 15, s: 0 })
  assert.deepEqual(remainingLabel(15 * m - 500), { kind: 'm', h: 0, m: 15, s: 0 })
  assert.deepEqual(remainingLabel(60 * m), { kind: 'h', h: 1, m: 0, s: 0 })
  assert.deepEqual(remainingLabel(2 * 3600_000 - 300), { kind: 'h', h: 2, m: 0, s: 0 })
  assert.deepEqual(remainingLabel(90.5 * m), { kind: 'hm', h: 1, m: 31, s: 0 })
  assert.deepEqual(remainingLabel(24 * 3600_000), { kind: 'h', h: 24, m: 0, s: 0 })
})

test('remainingLabel: acilis aninda zamanlayici/saat farki sicrama yaratmaz', () => {
  const m = 60 * 1000
  // 30 dk'lik tunel: sayac sunucudan 0.5-5 sn geride olsa da "30 dk" kalir (ceil olsa "31 dk" gorunurdu).
  for (const lagMs of [0, 500, 1000, 5000]) {
    assert.deepEqual(remainingLabel(30 * m + lagMs), { kind: 'm', h: 0, m: 30, s: 0 }, `${lagMs} ms gecikme`)
    assert.deepEqual(remainingLabel(2 * 3600_000 + lagMs), { kind: 'h', h: 2, m: 0, s: 0 }, `${lagMs} ms gecikme (2 sa)`)
  }
  // 2 sa'lik tunelin ilk saniyeleri "1 sa 59 dk" olmaz
  assert.deepEqual(remainingLabel(2 * 3600_000 - 20_000), { kind: 'h', h: 2, m: 0, s: 0 })
})

test('isExpiringSoon ve tunnelUrl', () => {
  assert.equal(isExpiringSoon(0), false)
  assert.equal(isExpiringSoon(-10), false)
  assert.equal(isExpiringSoon(5 * 60 * 1000), true)
  assert.equal(isExpiringSoon(5 * 60 * 1000 + 1), false)
  assert.equal(tunnelUrl('tmp-ab12--acme.zorven.app'), 'https://tmp-ab12--acme.zorven.app')
})
