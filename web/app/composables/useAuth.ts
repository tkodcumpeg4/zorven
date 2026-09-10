/**
 * Birleşik kimlik doğrulama durumu.
 *
 * Üç giriş yöntemini tek bir "authed" durumunda toplar:
 *   - Better Auth    : Email/Şifre, Takım/Organizasyon, GitHub OAuth, oturum çerezi / bearer
 *   - Admin anahtarı : localStorage'da saklanır (useAdminKey), Bearer başlığıyla gönderilir
 *   - Legacy GitHub  : rpshell-server GitHub OAuth oturum çerezi
 */
import { authClient } from '~/lib/auth-client'
import { gt } from '~/plugins/i18n'

export type AuthMethod = 'key' | 'github' | 'better-auth' | null

export interface AuthUserInfo {
  id: string
  email: string
  name: string
  role?: string
}

export interface AuthTenantInfo {
  id: string
  slug: string
}

export interface OrganizationItem {
  id: string
  name: string
  slug: string
  logo?: string | null
}

const authed = ref(false)
const method = ref<AuthMethod>(null)
const user = ref<AuthUserInfo | null>(null)
const tenant = ref<AuthTenantInfo | null>(null)
const organizations = ref<OrganizationItem[]>([])
const platformAdmin = ref(false)
const githubEnabled = ref(false)
const ready = ref(false)
// Giriste sifre dogru ama 2FA gerekiyorsa true: arayuz ikinci adim ekranini gosterir.
const needs2FA = ref(false)
// Mevcut kullanici 2FA'yi acmis mi (ayarlar ekrani icin).
const twoFactorEnabled = ref(false)

const tenantSlug = computed(() => tenant.value?.slug ?? '')
const userLogin = computed(() => user.value?.name || user.value?.email || '')

let initPromise: Promise<void> | null = null

export function useAuth() {
  const { key, set: setKey, clear: clearKey } = useAdminKey()

  /** Organizasyonları Better Auth üzerinden çeker */
  async function loadOrganizations(): Promise<OrganizationItem[]> {
    try {
      const res = await authClient.organization.list()
      if (res && res.data && Array.isArray(res.data)) {
        organizations.value = res.data.map((o: any) => ({
          id: o.id,
          name: o.name,
          slug: o.slug,
          logo: o.logo ?? null,
        }))
      }
    } catch {
      // sessizce geç
    }
    return organizations.value
  }

  /** /api/v1/me ile mevcut yetkiyi doğrular */
  async function check(): Promise<boolean> {
    try {
      const headers: Record<string, string> = {}
      if (key.value) headers.Authorization = `Bearer ${key.value}`
      const me = await $fetch<{
        authenticated: boolean
        method?: Exclude<AuthMethod, null>
        tenant?: { id: string, slug: string }
        user?: { id?: string, email?: string, name?: string, role?: string, github_login?: string }
        platform_admin?: boolean
      }>('/api/v1/me', { headers })

      authed.value = !!me.authenticated
      method.value = me.method ?? (key.value ? 'key' : null)
      platformAdmin.value = !!me.platform_admin
      tenant.value = me.tenant ?? null

      if (me.user) {
        user.value = {
          id: me.user.id ?? '',
          email: me.user.email ?? '',
          name: me.user.name ?? me.user.github_login ?? '',
          role: me.user.role ?? '',
        }
      } else {
        user.value = null
      }

      if (authed.value && method.value === 'better-auth') {
        await loadOrganizations()
      }

      return authed.value
    } catch {
      authed.value = false
      method.value = null
      user.value = null
      tenant.value = null
      organizations.value = []
      platformAdmin.value = false
      return false
    }
  }

  /** Uygulama açılışında bir kez: config'i yükler, mevcut oturumu doğrular. */
  async function init(): Promise<void> {
    if (initPromise) return initPromise
    initPromise = (async () => {
      try {
        const c = await $fetch<{ github_enabled: boolean }>('/api/v1/auth/config')
        githubEnabled.value = !!c.github_enabled
      } catch {
        // Config alınamazsa buton gizli kalır
      }
      const ok = await check()
      if (!ok && key.value) clearKey()
      ready.value = true
    })()
    return initPromise
  }

  /**
   * Email & Şifre ile giriş.
   * Dönüş: 2FA gerekiyorsa { twoFactor: true } — arayüz ikinci adım ekranını açar,
   * oturum HENÜZ açılmaz. Aksi halde oturum açılır ve { twoFactor: false } döner.
   */
  async function loginWithEmail(email: string, password: string): Promise<{ twoFactor: boolean }> {
    const res = await authClient.signIn.email({ email, password })
    if (res?.error) {
      throw new Error(res.error.message || gt('auth.thrownSignin'))
    }
    // Better Auth: 2FA açık kullanıcıda oturum açılmaz, twoFactorRedirect döner.
    if ((res?.data as any)?.twoFactorRedirect) {
      needs2FA.value = true
      return { twoFactor: true }
    }
    needs2FA.value = false
    await check()
    return { twoFactor: false }
  }

  // --- İki Adımlı Doğrulama (2FA) -------------------------------------------

  /** Giriş 2. adımı: authenticator (TOTP) kodunu doğrula → oturum açılır. */
  async function verifyTotp(code: string, trustDevice = false): Promise<void> {
    const res = await authClient.twoFactor.verifyTotp({ code, trustDevice })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownCodeVerify'))
    needs2FA.value = false
    await check()
  }

  /** Giriş 2. adımı: e-posta kodu gönder (kendi mail sunucumuzdan). */
  async function sendEmailOtp(): Promise<void> {
    const res = await authClient.twoFactor.sendOtp()
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownOtpSend'))
  }

  /** Giriş 2. adımı: e-posta ile gelen OTP kodunu doğrula → oturum açılır. */
  async function verifyEmailOtp(code: string, trustDevice = false): Promise<void> {
    const res = await authClient.twoFactor.verifyOtp({ code, trustDevice })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownCodeVerify'))
    needs2FA.value = false
    await check()
  }

  /** Giriş 2. adımı: yedek kod ile doğrula → oturum açılır. */
  async function verifyBackupCode(code: string): Promise<void> {
    const res = await authClient.twoFactor.verifyBackupCode({ code })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownBackupVerify'))
    needs2FA.value = false
    await check()
  }

  /**
   * 2FA'yı aç: şifre ile doğrula, TOTP secret (otpauth URI) + yedek kodlar döner.
   * Kullanıcı authenticator'a ekleyip bir kod ile confirmTotpSetup çağırmalı.
   */
  async function enableTwoFactor(password: string): Promise<{ totpURI: string; backupCodes: string[] }> {
    const res = await authClient.twoFactor.enable({ password })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrown2faEnable'))
    return {
      totpURI: (res?.data as any)?.totpURI ?? '',
      backupCodes: (res?.data as any)?.backupCodes ?? [],
    }
  }

  /** 2FA kurulumunu bir TOTP kodu ile onayla (enable sonrası). */
  async function confirmTotpSetup(code: string): Promise<void> {
    const res = await authClient.twoFactor.verifyTotp({ code })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownSetupVerify'))
    twoFactorEnabled.value = true
    await refreshTwoFactorState()
  }

  /** 2FA'yı kapat (şifre gerekir). */
  async function disableTwoFactor(password: string): Promise<void> {
    const res = await authClient.twoFactor.disable({ password })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrown2faDisable'))
    twoFactorEnabled.value = false
    await refreshTwoFactorState()
  }

  /** Mevcut oturumdaki kullanıcının 2FA durumunu Better Auth'tan tazeler. */
  async function refreshTwoFactorState(): Promise<void> {
    try {
      const s = await authClient.getSession()
      twoFactorEnabled.value = !!(s?.data as any)?.user?.twoFactorEnabled
    } catch {
      // sessizce geç
    }
  }

  // --- Profil (Better Auth) --------------------------------------------------

  /** Görünen adı (nick) değiştir. */
  async function updateName(name: string): Promise<void> {
    const res = await authClient.updateUser({ name })
    if ((res as any)?.error) throw new Error((res as any).error.message || gt('auth.thrownNameUpdate'))
    await check()
  }

  /**
   * Hesap e-postasını değiştir. Onay linki MEVCUT adrese gider; kullanıcı
   * tıklayana kadar e-posta değişmez. callbackURL onay sonrası dönülecek sayfa.
   */
  async function changeEmail(newEmail: string): Promise<void> {
    const res = await authClient.changeEmail({
      newEmail,
      callbackURL: '/profile',
    })
    if ((res as any)?.error) throw new Error((res as any).error.message || gt('auth.thrownEmailChange'))
  }

  /** Şifre değiştir (mevcut şifre gerekir). revokeOther: diğer oturumları kapat. */
  async function changePassword(currentPassword: string, newPassword: string, revokeOther = true): Promise<void> {
    const res = await authClient.changePassword({
      currentPassword,
      newPassword,
      revokeOtherSessions: revokeOther,
    })
    if ((res as any)?.error) throw new Error((res as any).error.message || gt('auth.thrownPasswordChange'))
  }

  /**
   * Email & Şifre ile yeni hesap oluşturma.
   * Dönüş: e-posta doğrulama gerekiyorsa { needsVerification: true } — oturum
   * AÇILMAZ, kullanıcı e-postasındaki linke tıklamalı. Aksi halde oturum açılır.
   */
  async function signUpWithEmail(email: string, password: string, name: string): Promise<{ needsVerification: boolean }> {
    const res = await authClient.signUp.email({ email, password, name })
    if (res?.error) {
      throw new Error(res.error.message || gt('auth.thrownSignup'))
    }
    // token null => e-posta doğrulama zorunlu, oturum açılmadı.
    if (!(res?.data as any)?.token) {
      return { needsVerification: true }
    }
    await check()
    return { needsVerification: false }
  }

  /** Better Auth GitHub ile giriş */
  async function loginWithBetterAuthGithub(): Promise<void> {
    await authClient.signIn.social({
      provider: 'github',
      callbackURL: window.location.origin,
    })
  }

  /** Admin anahtarıyla giriş: önce sunucuya doğrulat, sonra sakla. */
  async function loginWithKey(value: string): Promise<boolean> {
    try {
      await $fetch('/api/v1/me', { headers: { Authorization: `Bearer ${value}` } })
    } catch {
      return false
    }
    setKey(value)
    authed.value = true
    method.value = 'key'
    platformAdmin.value = true
    tenant.value = { id: 'ten_default', slug: 'default' }
    return true
  }

  /** Aktif organizasyonu değiştirir */
  async function switchOrganization(orgId: string): Promise<void> {
    const res = await authClient.organization.setActive({ organizationId: orgId })
    if (res?.error) {
      throw new Error(res.error.message || gt('auth.thrownOrgSwitch'))
    }
    await check()
  }

  /** Yeni bir organizasyon oluşturur */
  async function createOrganization(name: string, slug: string): Promise<void> {
    const res = await authClient.organization.create({ name, slug })
    if (res?.error) {
      throw new Error(res.error.message || gt('auth.thrownOrgCreate'))
    }
    if (res?.data?.id) {
      await switchOrganization(res.data.id)
    }
  }

  // --- Şifremi unuttum -------------------------------------------------------

  /** Şifre sıfırlama e-postası gönder (linkteki token /reset-password'a taşınır). */
  async function requestPasswordReset(email: string): Promise<void> {
    const res = await (authClient as any).forgetPassword({ email, redirectTo: '/reset-password' })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownResetSend'))
  }

  /** Yeni şifreyi ayarla (e-postadaki token ile). */
  async function resetPassword(token: string, newPassword: string): Promise<void> {
    const res = await (authClient as any).resetPassword({ token, newPassword })
    if (res?.error) throw new Error(res.error.message || gt('auth.thrownResetFail'))
  }

  /** Çıkış */
  async function logout(): Promise<void> {
    try {
      await authClient.signOut()
    } catch {}
    try {
      await $fetch('/api/v1/auth/logout', { method: 'POST' })
    } catch {}
    clearKey()
    authed.value = false
    method.value = null
    user.value = null
    tenant.value = null
    organizations.value = []
    platformAdmin.value = false
    needs2FA.value = false
    twoFactorEnabled.value = false
  }

  return {
    authed: readonly(authed),
    method: readonly(method),
    user: readonly(user),
    tenant: readonly(tenant),
    organizations: readonly(organizations),
    platformAdmin: readonly(platformAdmin),
    tenantSlug: readonly(tenantSlug),
    userLogin: readonly(userLogin),
    githubEnabled: readonly(githubEnabled),
    ready: readonly(ready),
    needs2FA: readonly(needs2FA),
    twoFactorEnabled: readonly(twoFactorEnabled),
    init,
    check,
    loginWithEmail,
    signUpWithEmail,
    loginWithBetterAuthGithub,
    loginWithKey,
    loadOrganizations,
    switchOrganization,
    createOrganization,
    logout,
    // 2FA
    verifyTotp,
    sendEmailOtp,
    verifyEmailOtp,
    verifyBackupCode,
    enableTwoFactor,
    confirmTotpSetup,
    disableTwoFactor,
    refreshTwoFactorState,
    // Profil
    updateName,
    changeEmail,
    changePassword,
    // Şifremi unuttum
    requestPasswordReset,
    resetPassword,
  }
}
