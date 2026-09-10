/**
 * Zorven Tema Yönetimi (Dark / Light Mode).
 *
 * - Varsayılan: dark (kurumsal Zorven koyu teması).
 * - Kullanıcı tercihi localStorage('zorven_theme') içinde saklanır.
 * - Sistem tercihi (prefers-color-scheme) kontrol edilir.
 * - `html.light` sınıfını dinamik olarak açıp kapatır.
 */

export type ThemeMode = 'dark' | 'light'

const theme = ref<ThemeMode>('dark')
const initialized = ref(false)

export function useTheme() {
  const isLight = computed(() => theme.value === 'light')

  function applyTheme(newTheme: ThemeMode) {
    theme.value = newTheme
    if (typeof window !== 'undefined') {
      localStorage.setItem('zorven_theme', newTheme)
      if (newTheme === 'light') {
        document.documentElement.classList.add('light')
        document.documentElement.classList.remove('dark')
      } else {
        document.documentElement.classList.add('dark')
        document.documentElement.classList.remove('light')
      }
    }
  }

  function toggleTheme() {
    applyTheme(theme.value === 'light' ? 'dark' : 'light')
  }

  function initTheme() {
    if (initialized.value || typeof window === 'undefined') return
    initialized.value = true

    const saved = localStorage.getItem('zorven_theme') as ThemeMode | null
    if (saved === 'light' || saved === 'dark') {
      applyTheme(saved)
      return
    }

    // Sistem tercihi
    const prefersLight = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches
    applyTheme(prefersLight ? 'light' : 'dark')
  }

  return {
    theme,
    isLight,
    toggleTheme,
    applyTheme,
    initTheme,
  }
}
