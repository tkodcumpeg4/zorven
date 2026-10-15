import { currentLang, dateLocale } from '~/plugins/i18n'

const REL = {
  tr: { now: 'şimdi', s: (n: number) => `${n}sn önce`, m: (n: number) => `${n}dk önce`, h: (n: number) => `${n}sa önce`, d: (n: number) => `${n}g önce` },
  en: { now: 'now', s: (n: number) => `${n}s ago`, m: (n: number) => `${n}m ago`, h: (n: number) => `${n}h ago`, d: (n: number) => `${n}d ago` },
  ja: { now: 'たった今', s: (n: number) => `${n}秒前`, m: (n: number) => `${n}分前`, h: (n: number) => `${n}時間前`, d: (n: number) => `${n}日前` },
}

export function useFormat() {
  /** "3s önce", "12dk önce" — canlı tablolar için */
  function relativeTime(isoString?: string): string {
    if (!isoString) return '—'
    const diff = Date.now() - new Date(isoString).getTime()
    const s = Math.floor(diff / 1000)
    const r = REL[currentLang()]
    if (s < 5) return r.now
    if (s < 60) return r.s(s)
    const m = Math.floor(s / 60)
    if (m < 60) return r.m(m)
    const h = Math.floor(m / 60)
    if (h < 24) return r.h(h)
    return r.d(Math.floor(h / 24))
  }

  function bytes(n: number): string {
    if (n < 1024) return `${n} B`
    if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`
    return `${(n / 1024 ** 2).toFixed(1)} MB`
  }

  function duration(ms: number): string {
    return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(2)}s`
  }

  function clock(isoString: string): string {
    return new Date(isoString).toLocaleTimeString(dateLocale(), { hour12: false })
  }

  return { relativeTime, bytes, duration, clock }
}

/** Yuzde bicimi dile gore: Turkce "%69", digerleri "69%". */
export function pct(v: number, digits = 0): string {
  const n = v.toFixed(digits)
  return currentLang() === 'tr' ? `%${n}` : `${n}%`
}
