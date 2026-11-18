// Uzak ekran gecikme rozeti icin saf mantik (DOM/Nuxt'a bagimli degil; node --test ile test edilir).
// Olcumler useScreen.onRtt'den gelir: ms = panel<->sunucu, agentMs = sunucu<->istemci (yoksa undefined).

/** Hareketli ortalama penceresi (olcum sayisi). */
export const RTT_WINDOW = 5
/** Bu kadar sure olcum gelmezse rozet soluklasir (ms). */
export const RTT_STALE_MS = 6000
/** Renk esikleri (toplam ms): iyi < 80, orta < 200, kotu >= 200. */
export const RTT_GOOD_MS = 80
export const RTT_FAIR_MS = 200

export type RttTone = 'good' | 'fair' | 'bad'

export interface RttAverage {
  /** panel<->sunucu ortalamasi (ms) */
  ms: number
  /** sunucu<->istemci ortalamasi (ms); hic olcum yoksa undefined */
  agentMs?: number
  /** ms + (agentMs ?? 0) */
  total: number
}

function round1(n: number): number { return Math.round(n * 10) / 10 }

/** Son N olcumun hareketli ortalamasi. Tek bir sicrama rozeti ziplatmasin diye. */
export class RttWindow {
  private samples: { ms: number, agentMs?: number }[] = []
  private readonly size: number

  constructor(size = RTT_WINDOW) { this.size = size }

  /** Olcum ekler ve guncel ortalamayi doner (gecersiz olcum yok sayilir; hic olcum yoksa null). */
  push(ms: number, agentMs?: number): RttAverage | null {
    if (!Number.isFinite(ms) || ms < 0) return this.average()
    const a = typeof agentMs === 'number' && Number.isFinite(agentMs) && agentMs >= 0 ? agentMs : undefined
    this.samples.push({ ms, agentMs: a })
    if (this.samples.length > this.size) this.samples.shift()
    return this.average()
  }

  get count(): number { return this.samples.length }

  reset(): void { this.samples = [] }

  /** Olcum yoksa null. agentMs yalniz agentMs'li ornekler uzerinden ortalanir. */
  average(): RttAverage | null {
    if (!this.samples.length) return null
    const ms = this.samples.reduce((s, x) => s + x.ms, 0) / this.samples.length
    const ag = this.samples.filter(x => x.agentMs !== undefined)
    const agentMs = ag.length ? ag.reduce((s, x) => s + (x.agentMs as number), 0) / ag.length : undefined
    return { ms: round1(ms), agentMs: agentMs === undefined ? undefined : round1(agentMs), total: round1(ms + (agentMs ?? 0)) }
  }
}

/** Toplam gecikmeye gore ton. */
export function rttTone(totalMs: number): RttTone {
  if (totalMs < RTT_GOOD_MS) return 'good'
  if (totalMs < RTT_FAIR_MS) return 'fair'
  return 'bad'
}

/** Son olcumun uzerinden RTT_STALE_MS gectiyse (ya da hic olcum yoksa) rozet eski sayilir. */
export function isRttStale(now: number, lastAt: number | null, staleMs = RTT_STALE_MS): boolean {
  return lastAt === null || now - lastAt > staleMs
}

/** Rozet metni: tam sayiya yuvarlanmis toplam ("42 ms"). */
export function formatRtt(totalMs: number): string {
  return `${Math.round(totalMs)} ms`
}
