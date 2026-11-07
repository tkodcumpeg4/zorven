/**
 * Hesap yasam dongusu: etkin oturumlar, kisisel veri disa aktarma ve hesap silme.
 *
 * Uclar Better Auth eklentisidir (web/server/lib/account-plugin.ts): /api/auth/zorven/*.
 * Go yalnizca /api/auth/* yolunu web konteynerine aktardigi icin burada `authClient.$fetch`
 * kullanilir (taban /api/auth). Hesap silme kutuphane ucunu (`deleteUser`) kullanir; hata
 * kodlari ~/lib/account.ts ile panel metinlerine eslenir.
 */
import { authClient } from '~/lib/auth-client'
import { exportFileName } from '~/lib/account'

export interface AccountSession {
  id: string
  /** Istegi yapan oturum ("bu oturum"). */
  current: boolean
  createdAt: string
  /** Son etkinlik (Better Auth oturumu yenilediginde guncellenir; ~1 gun hassasiyet). */
  updatedAt: string
  expiresAt: string
  ipAddress: string | null
  userAgent: string | null
}

/** Sunucu hatasi: `code` ~/lib/account.ts accountErrorKey ile metne eslenir. */
export class AccountApiError extends Error {
  code: string
  status: number
  constructor(message: string, code = '', status = 0) {
    super(message)
    this.code = code
    this.status = status
  }
}

function toError(err: any): AccountApiError {
  return new AccountApiError(
    String(err?.message || err?.statusText || 'request failed'),
    typeof err?.code === 'string' ? err.code : '',
    Number(err?.status ?? 0),
  )
}

async function call<T>(path: string, method: 'GET' | 'POST' = 'GET', body?: unknown): Promise<T> {
  const res: any = await (authClient as any).$fetch(path, { method, ...(body !== undefined ? { body } : {}) })
  if (res?.error) throw toError(res.error)
  return res?.data as T
}

/** Tarayiciya JSON dosyasi indirtir. */
function downloadJson(name: string, data: unknown): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}

export function useAccount() {
  /** Etkin oturumlar (en son etkin olan ustte). */
  async function listSessions(): Promise<AccountSession[]> {
    return (await call<AccountSession[]>('/zorven/sessions')) ?? []
  }

  /** Tek oturumu sonlandirir (kendi oturumun haric: cikis yap). */
  async function revokeSession(id: string): Promise<void> {
    await call('/zorven/sessions/revoke', 'POST', { id })
  }

  /** Bu oturum disindaki tum oturumlari sonlandirir (Better Auth revoke-other-sessions). */
  async function revokeOtherSessions(): Promise<void> {
    const res: any = await authClient.revokeOtherSessions()
    if (res?.error) throw toError(res.error)
  }

  /** Kisisel veriyi JSON olarak indirir (kullanici basina saatte 3). */
  async function exportData(): Promise<void> {
    const data = await call<unknown>('/zorven/account/export', 'POST', {})
    downloadJson(exportFileName(), data)
  }

  /**
   * Hesap silme istegi: parola (parolali hesaplarda) dogrulanir ve onay baglantisi e-postayla
   * gider. Hesap, baglantiya tiklaninca silinir. callbackURL silme sonrasi acilan sayfadir.
   */
  async function requestDeletion(password: string): Promise<void> {
    const res: any = await authClient.deleteUser({
      ...(password ? { password } : {}),
      callbackURL: '/?accountDeleted=1',
    })
    if (res?.error) throw toError(res.error)
  }

  /** Hesabin parolasi var mi? Bilinmiyorsa null (arayuz parola alanini gosterir; sunucu kararini verir). */
  async function hasPassword(): Promise<boolean | null> {
    try {
      const res: any = await authClient.listAccounts()
      if (res?.error || !Array.isArray(res?.data)) return null
      return res.data.some((a: any) => a?.providerId === 'credential')
    } catch {
      return null
    }
  }

  /** Su anki oturumun acilis zamani (silme icin tazelik on kontrolu); bilinmiyorsa null. */
  async function sessionCreatedAt(): Promise<string | null> {
    try {
      const res: any = await authClient.getSession()
      const c = res?.data?.session?.createdAt
      return c ? new Date(c).toISOString() : null
    } catch {
      return null
    }
  }

  return { listSessions, revokeSession, revokeOtherSessions, exportData, requestDeletion, hasPassword, sessionCreatedAt }
}
