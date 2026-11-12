// Kayit hunisi olcumu (Umami uyumlu). Acik surumde olcum betigi YUKLENMEZ: olaylar
// yalniz sayfada `window.umami` varsa gider (kendi Umami'nizi eklerseniz), yoksa no-op.
// Kisisel veri YOK: e-posta, ad, kiraci/tunel adi, IP asla gonderilmez; yalniz olay
// adi ve kaba nitelikler (yontem, plan kimligi). Saf mantik burada (node --test ile
// sinanir); Nuxt'a bagimli kod composable/plugin'dedir.

/** Olay adlari. */
export type FunnelEvent =
  | 'signup'
  | 'signup_verified'
  | 'login'
  | 'first_client_connected'
  | 'first_tunnel_online'
  | 'plan_upgrade_click'

/** Umami yuklenmediyse (red, reklam engelleyici, yerel) sessizce hicbir sey yapmaz. */
export function track(name: FunnelEvent, data?: Record<string, string | number | boolean>): void {
  try {
    const u = (globalThis as any).window?.umami
    if (u && typeof u.track === 'function') {
      if (data) u.track(name, data)
      else u.track(name)
    }
  } catch { /* olcum asla arayuzu bozmaz */ }
}

/** Kiraci kimliginin kisa ozeti (yalniz localStorage anahtari icin; Umami'ye gitmez). */
export function hashTenant(id: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h.toString(36)
}

export interface FunnelState {
  /** Tarayicida ilk gozlemde kiracinin hic istemcisi/tuneli yoktu. */
  fresh: boolean
  client: boolean
  tunnel: boolean
}

/**
 * Bir gozlemden sonra yeni durum ve gonderilecek olaylar.
 * prev yoksa (bu tarayicida kiraci ilk kez goruluyor): kiracida zaten istemci/tunel varsa
 * "ilk" ani gecmistedir, olay GONDERILMEZ (mevcut kiracilarin yanlis sayilmasini onler).
 */
export function funnelStep(
  prev: FunnelState | null,
  obs: { clients: number, tunnels: number, onlineClients: number, onlineTunnels: number },
): { state: FunnelState, events: FunnelEvent[] } {
  if (!prev) {
    const fresh = obs.clients === 0 && obs.tunnels === 0
    return {
      state: { fresh, client: !fresh || obs.onlineClients > 0, tunnel: !fresh || obs.onlineTunnels > 0 },
      events: [],
    }
  }
  const events: FunnelEvent[] = []
  const state = { ...prev }
  if (!state.client && obs.onlineClients > 0) { state.client = true; events.push('first_client_connected') }
  if (!state.tunnel && obs.onlineTunnels > 0) { state.tunnel = true; events.push('first_tunnel_online') }
  return { state, events }
}
