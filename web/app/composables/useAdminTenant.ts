/**
 * Admin anahtari modunda "Yonetime Gec" ile secilen kiraci.
 *
 * Admin anahtarinin oturumu yoktur; sunucu kiraciyi yalnizca X-Tenant-ID
 * basligindan bilir. Secim localStorage'da tutulur (sayfa yenilemesini
 * atlatsin diye) ve useApi/useAuth her istekte basliga cevirir. Bos = varsayilan
 * kiraci. Platform admin hedef kiracida uyelik YAZMAZ (hayalet); bu yalnizca
 * istemci tarafi bir kapsam secimidir.
 */
const STORAGE_KEY = 'zorven.admin_tenant'

function readStored(): string {
  if (import.meta.server) return ''
  try {
    return localStorage.getItem(STORAGE_KEY) || ''
  } catch {
    return ''
  }
}

function writeStored(id: string) {
  if (import.meta.server) return
  try {
    if (id) localStorage.setItem(STORAGE_KEY, id)
    else localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Erisim yok: yalnizca bellek ici state ile devam.
  }
}

export function useAdminTenant() {
  const adminTenant = useState<string>('adminTenant', readStored)

  function setAdminTenant(id: string) {
    adminTenant.value = id
    writeStored(id)
  }

  function clearAdminTenant() {
    setAdminTenant('')
  }

  return { adminTenant, setAdminTenant, clearAdminTenant }
}
