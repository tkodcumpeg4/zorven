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
      try { localStorage.setItem('zorven_theme', newTheme) } catch { /* depolama engelli: tercih kalici olmaz */ }
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

    let saved: ThemeMode | null = null
    try { saved = localStorage.getItem('zorven_theme') as ThemeMode | null } catch { /* depolama engelli */ }
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
