// Chunk yukleme hatasi (deploy sonrasi eski hash'li dosya yok): siyah ekran yerine
// bir kez otomatik yeniden yukle; yine olursa hata ekranina dus (dongu yok).
const FLAG = 'zorven_chunk_reload'

function tryReloadOnce(): boolean {
  try {
    if (sessionStorage.getItem(FLAG)) return false
    sessionStorage.setItem(FLAG, String(Date.now()))
  } catch {
    return false
  }
  window.location.reload()
  return true
}

export default defineNuxtPlugin((nuxtApp) => {
  // Basarili bir yuklemeden sonra bayragi temizle: sonraki deploy yine bir kez yenileyebilsin.
  window.addEventListener('load', () => {
    setTimeout(() => {
      try { sessionStorage.removeItem(FLAG) } catch { /* erisim yok */ }
    }, 10_000)
  })

  window.addEventListener('vite:preloadError', (ev) => {
    if (tryReloadOnce()) ev.preventDefault()
  })

  nuxtApp.hook('app:chunkError', () => {
    if (tryReloadOnce()) return
    showError({
      statusCode: 503,
      statusMessage: 'Chunk load failed',
      message: 'Chunk load failed',
    })
  })
})
