export function useFormat() {
  /** "3s önce", "12dk önce" — canlı tablolar için */
  function relativeTime(isoString?: string): string {
    if (!isoString) return '—'
    const diff = Date.now() - new Date(isoString).getTime()
    const s = Math.floor(diff / 1000)
    if (s < 5) return 'şimdi'
    if (s < 60) return `${s}sn önce`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}dk önce`
    const h = Math.floor(m / 60)
    if (h < 24) return `${h}sa önce`
    return `${Math.floor(h / 24)}g önce`
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
    return new Date(isoString).toLocaleTimeString('tr-TR', { hour12: false })
  }

  return { relativeTime, bytes, duration, clock }
}
