/**
 * Aktif proje secimi (FAZ 0 / F00).
 *
 * Secim localStorage'da tutulur. Sebep: proje degistirildiginde panel tam sayfa
 * yenileme yapar (tum liste state'ini ve useApi basliklarini tazelemek icin) ama
 * Nuxt useState yenilemeyi atlatamaz. Kalicilik olmadan secim her yenilemede
 * silinir ve loadProjects() kullaniciyi default projeye geri dusurur — yani
 * proje gecisi hic calismaz.
 *
 * Organizasyon secimi bu soruna girmez cunku Better Auth onu sunucu oturumunda
 * saklar; proje secimi ise yalnizca istemci tarafinda bir istek basligidir.
 */
const STORAGE_KEY = 'zorven_project'

function readStored(): string | null {
  if (import.meta.server) return null
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    // Gizli sekme / depolama kapali: kaliciliksiz devam et.
    return null
  }
}

function writeStored(slug: string | null) {
  if (import.meta.server) return
  try {
    if (slug) localStorage.setItem(STORAGE_KEY, slug)
    else localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Erisim yok: yalnizca bellek ici state ile devam.
  }
}

export function useActiveProject() {
  // Baslangic degeri localStorage'dan okunur; boylece ilk API istegi bile
  // dogru X-Zorven-Project basligini tasir (loadProjects'i beklemez).
  const activeProject = useState<string | null>('activeProject', readStored)

  /** Secimi hem state'e hem localStorage'a yazar. */
  function setActiveProject(slug: string | null) {
    activeProject.value = slug
    writeStored(slug)
  }

  /**
   * Secimi temizler. Organizasyon degisiminde ve cikista cagrilir: proje slug'i
   * kiraciya ozeldir, baska bir organizasyona tasinmamalidir.
   */
  function clearActiveProject() {
    setActiveProject(null)
  }

  return { activeProject, setActiveProject, clearActiveProject }
}
