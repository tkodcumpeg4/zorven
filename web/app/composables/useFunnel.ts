// Panel huni olaylari: kayit/giris izleri ve "ilk istemci / ilk tunel cevrimici" anlari.
import { funnelStep, hashTenant, track, type FunnelState } from '~/lib/analytics'

const PENDING_VERIFY = 'zv_fn_verify'   // e-posta dogrulamasi bekleyen kayit (bu tarayici)
const PENDING_SOCIAL = 'zv_fn_social'   // OAuth yonlendirmesi (donuste login olayi)

function safely(op: () => void) { try { op() } catch { /* depolama yok */ } }

/** Kayit sonrasi dogrulama bekleniyorsa isaretle (signup_verified icin). */
export function markSignupPendingVerify() { safely(() => localStorage.setItem(PENDING_VERIFY, '1')) }
export function markSocialLoginStarted() { safely(() => sessionStorage.setItem(PENDING_SOCIAL, '1')) }

/** Basarili Better Auth oturumundan sonra (check) bekleyen isaretleri olaya cevirir. */
export function flushAuthMarkers() {
  safely(() => {
    if (localStorage.getItem(PENDING_VERIFY)) {
      localStorage.removeItem(PENDING_VERIFY)
      track('signup_verified')
    }
    if (sessionStorage.getItem(PENDING_SOCIAL)) {
      sessionStorage.removeItem(PENDING_SOCIAL)
      track('login', { method: 'social' })
    }
  })
}

/**
 * Kiracinin ilk istemci/tunel cevrimici anini yakalar. Kiraci basina tek sefer
 * (kiraci kimliginin ozetiyle anahtarlanan localStorage bayragi). Sinirlama: bayrak
 * tarayici basinadir; kiraci baska tarayicidan ilk kez acilirsa ve zaten istemci/tunel
 * varsa olay gonderilmez (yanlis sayim yerine eksik sayim tercih edilir).
 */
export function useFunnel() {
  const api = useApi()
  const { tenant, method } = useAuth()
  let done = false

  async function observe(): Promise<boolean> {
    if (done || method.value !== 'better-auth' || !tenant.value?.id) return true
    const key = `zv_fn_${hashTenant(tenant.value.id)}`
    let prev: FunnelState | null = null
    safely(() => { const v = localStorage.getItem(key); if (v) prev = JSON.parse(v) })
    const p = prev as FunnelState | null
    if (p && p.client && p.tunnel) { done = true; return true }
    const [clients, tunnels] = await Promise.all([api.listClients(), api.listTunnels()])
    const online = new Set(clients.filter(c => c.status === 'online').map(c => c.id))
    const r = funnelStep(p, {
      clients: clients.length,
      tunnels: tunnels.length,
      onlineClients: online.size,
      onlineTunnels: tunnels.filter(t => t.enabled && online.has(t.client_id)).length,
    })
    safely(() => localStorage.setItem(key, JSON.stringify(r.state)))
    for (const e of r.events) track(e)
    if (r.state.client && r.state.tunnel) done = true
    return true
  }

  return { observe }
}
