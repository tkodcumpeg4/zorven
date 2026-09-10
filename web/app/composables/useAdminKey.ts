/**
 * Admin anahtarini tarayicida saklar.
 *
 * MVP tek sahipli oldugu icin oturum/parola akisi yok: kullanici sunucunun
 * ilk calistirmada bastigi anahtari bir kez yapistirir, localStorage'da durur.
 * R4'te (multi-tenant) yerini gercek oturum yonetimi alacak.
 */
const STORAGE_KEY = 'zorven.admin_key'
const LEGACY_STORAGE_KEY = 'rpshell.admin_key'

const key = ref<string>('')
const loaded = ref(false)

export function useAdminKey() {
  if (!loaded.value && import.meta.client) {
    try {
      let val = localStorage.getItem(STORAGE_KEY)
      if (!val) {
        val = localStorage.getItem(LEGACY_STORAGE_KEY)
        if (val) {
          localStorage.setItem(STORAGE_KEY, val)
        }
      }
      key.value = val ?? ''
    } catch {
      // Gizli sekme veya site verisi kapaliysa okuma hata verebilir.
      key.value = ''
    }
    loaded.value = true
  }

  function set(value: string) {
    key.value = value.trim()
    try {
      if (key.value) localStorage.setItem(STORAGE_KEY, key.value)
      else localStorage.removeItem(STORAGE_KEY)
    } catch {
      // Saklanamazsa da bellekte tutulur; sayfa yenilenince sorulur.
    }
  }

  function clear() {
    set('')
  }

  return {
    key: readonly(key),
    isSet: computed(() => key.value.length > 0),
    set,
    clear,
  }
}
