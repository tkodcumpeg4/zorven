/**
 * Rol bazli arayuz yardimcisi.
 *
 * Sunucu member icin yikici/guvenlik islemlerini 403 ile reddeder; arayuz ayni
 * kurala uyar: member okuma + olusturma yapar, silme / token rotate / guvenlik
 * ayarlari yalniz owner/admin (platform admin her zaman) icindir.
 */
export function useRole() {
  const { user, platformAdmin } = useAuth()

  const role = computed(() => (user.value?.role || '').toLowerCase())
  const isPrivileged = computed(() => platformAdmin.value || role.value === 'owner' || role.value === 'admin')
  const isMember = computed(() => !isPrivileged.value)

  return { role, isPrivileged, isMember, isPlatformAdmin: platformAdmin }
}
