// Sunucu joker alan adini (*.alan.com) 422 invalid_domain ile reddeder; mesaji
// "joker alan adlari (*.alan.com) desteklenmiyor; ..." biciminde gelir
// (server/domain/verifier.go ErrWildcardDomain). Panel bu durumda genel
// "gecersiz alan adi" yerine ozel metin gosterir.
export function isWildcardDomainError(code: string | undefined, message: string | undefined, input?: string): boolean {
  if (code !== 'invalid_domain') return false
  if (typeof input === 'string' && input.includes('*')) return true
  const m = (message ?? '').toLowerCase()
  return m.includes('joker') || m.includes('wildcard') || m.includes('*.')
}
