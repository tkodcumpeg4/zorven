/**
 * Ekran oturumunda dosya aktarimi (panel <-> uzak makine). Sunucu uclari:
 *   POST /api/v1/clients/{id}/screen-files?name=      yukleme (govde = ham dosya)
 *   GET  /api/v1/clients/{id}/screen-files            izinli klasor listesi
 *   GET  /api/v1/clients/{id}/screen-files/download   indirme (akis)
 * Her istek, ekran WS'inin {"type":"files"} mesajindaki izleyiciye ozel belirteci
 * (X-File-Token) tasir; belirteci yalniz kontrolcu izleyici alir.
 */
export interface RemoteFile { name: string, size: number, mod_time: number }
export interface UploadResult { name: string, size: number, sha256: string }

/** Tek dosya boyut tavani (sunucu ile ayni; istemci daha dusuk secebilir). */
export const MAX_FILE_BYTES = 2 * 1024 * 1024 * 1024

export class FileTransferError extends Error {
  code: string
  constructor(code: string, message = '') {
    super(message || code)
    this.code = code
  }
}

export function useScreenFiles(clientID: string) {
  const { key } = useAdminKey()
  const base = `/api/v1/clients/${encodeURIComponent(clientID)}/screen-files`

  function authHeaders(token: string): Record<string, string> {
    const h: Record<string, string> = { 'X-File-Token': token }
    if (key.value) h.Authorization = `Bearer ${key.value}`
    return h
  }

  function errorFrom(status: number, body: string): FileTransferError {
    try {
      const j = JSON.parse(body)
      if (j?.error?.code) return new FileTransferError(j.error.code, j.error.message)
    } catch { /* JSON degil */ }
    return new FileTransferError(status === 413 ? 'too_large' : 'io')
  }

  /** Yukleme: ilerleme ve iptal (abort) destekler. Tarayici dosyayi akitir (bellege alinmaz). */
  function upload(
    file: File,
    token: string,
    onProgress: (loaded: number, total: number) => void,
  ): { promise: Promise<UploadResult>, abort: () => void } {
    const xhr = new XMLHttpRequest()
    const promise = new Promise<UploadResult>((resolve, reject) => {
      if (file.size > MAX_FILE_BYTES) {
        reject(new FileTransferError('too_large'))
        return
      }
      xhr.open('POST', `${base}?name=${encodeURIComponent(file.name)}`)
      for (const [k, v] of Object.entries(authHeaders(token))) xhr.setRequestHeader(k, v)
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded, e.total)
      }
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText) as UploadResult)
          } catch {
            reject(new FileTransferError('io'))
          }
        } else {
          reject(errorFrom(xhr.status, xhr.responseText))
        }
      }
      xhr.onerror = () => reject(new FileTransferError('network'))
      xhr.onabort = () => reject(new FileTransferError('canceled'))
      xhr.send(file)
    })
    return { promise, abort: () => xhr.abort() }
  }

  async function list(token: string): Promise<{ dir: string, files: RemoteFile[] }> {
    try {
      return await $fetch<{ dir: string, files: RemoteFile[] }>(base, { headers: authHeaders(token) })
    } catch (e: unknown) {
      const err = e as { data?: { error?: { code?: string, message?: string } } }
      throw new FileTransferError(err?.data?.error?.code || 'io', err?.data?.error?.message)
    }
  }

  /**
   * Indirme. Oturum cerezi ile girildiyse tarayicinin kendi indirme yoneticisi
   * akitir (buyuk dosyalar bellege alinmaz). Yalniz Bearer anahtariyla girildiyse
   * (cerez yok) fetch ile blob olarak toplanir; kucuk dosyalar icin uygundur.
   */
  async function download(name: string, token: string): Promise<void> {
    const url = `${base}/download?name=${encodeURIComponent(name)}`
    if (!key.value) {
      const a = document.createElement('a')
      a.href = `${url}&token=${encodeURIComponent(token)}`
      a.download = name
      a.rel = 'noopener'
      document.body.appendChild(a)
      a.click()
      a.remove()
      return
    }
    const res = await fetch(url, { headers: authHeaders(token) })
    if (!res.ok) throw errorFrom(res.status, await res.text())
    const blob = await res.blob()
    const obj = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = obj
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(obj), 10_000)
  }

  return { upload, list, download }
}
