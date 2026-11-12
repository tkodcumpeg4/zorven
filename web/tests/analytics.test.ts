// Calistirma: cd web && node --test tests/analytics.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { funnelStep, hashTenant, track } from '../app/lib/analytics.ts'

test('track: umami yoksa sessiz, varsa olay adi ve nitelikle cagrilir', () => {
  ;(globalThis as any).window = {}
  track('login')
  const calls: unknown[][] = []
  ;(globalThis as any).window = { umami: { track: (...a: unknown[]) => calls.push(a) } }
  track('signup', { verify: true })
  track('login')
  assert.deepEqual(calls, [['signup', { verify: true }], ['login']])
  delete (globalThis as any).window
})

test('hashTenant sabit ve kimligi acik etmez', () => {
  assert.equal(hashTenant('ten_abc'), hashTenant('ten_abc'))
  assert.notEqual(hashTenant('ten_abc'), hashTenant('ten_abd'))
  assert.ok(!hashTenant('ten_abc').includes('abc'))
})

test('huni: yeni kiracida 0 -> cevrimici gecisi olay uretir, bir kez', () => {
  const zero = { clients: 0, tunnels: 0, onlineClients: 0, onlineTunnels: 0 }
  let r = funnelStep(null, zero)
  assert.deepEqual(r.events, [])
  assert.equal(r.state.fresh, true)
  r = funnelStep(r.state, { clients: 1, tunnels: 0, onlineClients: 1, onlineTunnels: 0 })
  assert.deepEqual(r.events, ['first_client_connected'])
  r = funnelStep(r.state, { clients: 1, tunnels: 1, onlineClients: 1, onlineTunnels: 1 })
  assert.deepEqual(r.events, ['first_tunnel_online'])
  r = funnelStep(r.state, { clients: 1, tunnels: 1, onlineClients: 1, onlineTunnels: 1 })
  assert.deepEqual(r.events, [])
})

test('huni: mevcut kiraci (ilk gozlemde veri var) olay uretmez', () => {
  let r = funnelStep(null, { clients: 2, tunnels: 1, onlineClients: 0, onlineTunnels: 0 })
  assert.equal(r.state.fresh, false)
  r = funnelStep(r.state, { clients: 2, tunnels: 1, onlineClients: 1, onlineTunnels: 1 })
  assert.deepEqual(r.events, [])
})
