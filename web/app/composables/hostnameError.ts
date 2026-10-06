import type { ApiError } from '~/types/api'
import { gt } from '~/plugins/i18n'

/**
 * Sunucudan gelen ad hatalarını kullanıcıya anlaşılır Türkçeye çevirir.
 *
 * Neden kod eşlemesi: sunucunun `message` alanı geliştiriciye yöneliktir
 * (ör. "ad ardisik tire (--) iceremez"). Kullanıcının gördüğü metin burada
 * yaşasın ki sunucu mesajını değiştirmek arayüzü bozmasın. Bilinmeyen bir kod
 * gelirse sunucunun mesajını göstermek, sessizce yutmaktan iyidir.
 */
const KNOWN_CODES = ['name_reserved', 'hostname_taken', 'tunnel_not_found', 'client_not_found', 'no_platform_domain', 'invalid_target', 'missing_fields', 'invalid_domain', 'verification_failed', 'domain_taken']

/**
 * Hata kodunu iki yanıt biçiminden de okur:
 *  - standart:     { error: { code, message } }
 *  - entitlement:  { code, error: "mesaj", resource, limit, current } (plan sınırı)
 * Yükseltme penceresi (plan_limit_reached / feature_not_available) ikinci
 * biçimde geldiği için yalnızca `error.code` okumak onu hiç açmıyordu.
 */
export function apiErrorCode(e: unknown): string {
  const data = (e as { data?: any })?.data
  if (!data) return ''
  if (data.error && typeof data.error === 'object') return data.error.code ?? ''
  return typeof data.code === 'string' ? data.code : ''
}

export function hostnameError(e: unknown, fallback: string): string {
  const data = (e as { data?: any })?.data as (ApiError & { code?: string }) | undefined
  // Entitlement biçimi: mesaj düz metin olarak `error` alanında.
  if (data && typeof (data as any).error === 'string') {
    return (data as any).error || fallback
  }
  const code = data?.error?.code
  if (code) {
    // invalid_name: kural metnini sunucudan alıyoruz — hangi kuralın
    // çiğnendiğini ("--" mı, geçersiz karakter mi) yalnızca o biliyor.
    if (code === 'invalid_name') return data?.error?.message ?? fallback
    if (KNOWN_CODES.includes(code)) return gt('hostnameErr.' + code)
    return data?.error?.message ?? fallback
  }
  return e instanceof Error ? e.message : fallback
}
