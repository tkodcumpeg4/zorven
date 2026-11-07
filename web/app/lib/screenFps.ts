/**
 * Ekran kare hizi (FPS) secenekleri. Saf fonksiyonlar; DOM/Vue bagimliligi yok.
 *
 * Liste planin (ve ajanin) fps tavanina bagli: 45 ve 60 yalniz tavan izin
 * veriyorsa gorunur (Team/Enterprise plani + 60 fps destekleyen istemci).
 * Zorlama SUNUCUDADIR; burasi yalnizca panelin gostermedigi secenekleri belirler.
 */

/** Panelin sunabilecegi tum kare hizlari (artan). */
export const FPS_STEPS = [5, 10, 15, 20, 30, 45, 60] as const

/** Panelin baslangic secimi: hicbir planda 30'un ustunde baslamaz (60 acikca secilir). */
export const FPS_DEFAULT = 30

/** Plan ya da ajan tavan bildirmediginde (eski sunucu/istemci) gecerli tavan. */
export const FPS_BASE_LIMIT = 30

/** En yuksek tavan (sunucuyla ayni: Screen2FPSMax). */
export const FPS_MAX_LIMIT = 60

/** Gecerli tavan: sayi degilse ya da 0'sa 30; 60'in ustu 60. */
export function normalizeFpsLimit(v: unknown): number {
  const n = typeof v === 'number' && Number.isFinite(v) ? Math.floor(v) : 0
  if (n <= 0) return FPS_BASE_LIMIT
  return Math.min(n, FPS_MAX_LIMIT)
}

/** Tavani asmayan secenekler (artan); tavan en kucuk adimin altindaysa tavanin kendisi. */
export function fpsOptions(limit: unknown): number[] {
  const lim = normalizeFpsLimit(limit)
  const opts = FPS_STEPS.filter(f => f <= lim)
  return opts.length ? opts : [lim]
}

/**
 * Secimi tavana indirir. Gecersiz secim varsayilana (en fazla tavan) doner;
 * listede olmayan tavanlarda (ornegin 25) tavani asmayan en yuksek secenek alinir.
 */
export function clampFps(sel: number, limit: unknown): number {
  const opts = fpsOptions(limit)
  const want = Number.isFinite(sel) && sel > 0 ? Math.min(sel, opts[opts.length - 1]!) : FPS_DEFAULT
  const fit = opts.filter(f => f <= want)
  return fit.length ? fit[fit.length - 1]! : opts[0]!
}

/**
 * Panelde gecerli tavan: biletteki plan degeri ile (varsa) WS'teki caps.max_fps'in
 * asgarisi. caps.max_fps sunucunun hesapladigi plan + ajan yetenegi tavanidir;
 * eski ajanlarda 30 gelir ve 45/60 secenekleri gizlenir.
 */
export function effectiveFpsLimit(plan: unknown, capsMax?: unknown): number {
  const p = normalizeFpsLimit(plan)
  if (typeof capsMax === 'number' && Number.isFinite(capsMax) && capsMax > 0) {
    return Math.min(p, normalizeFpsLimit(capsMax))
  }
  return p
}
