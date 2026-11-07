/**
 * Oturum listesi icin User-Agent ozeti ("Chrome · Windows"). Saf fonksiyon: DOM/Vue
 * bagimliligi yok (node --test ile sinanir). Surum numarasi ve ayrintili cihaz modeli
 * BILEREK cikarilmaz: liste insan icin "bu benim mi?" sorusunu yanitlamali.
 */
export interface UaInfo {
  /** Tarayici/istemci adi; taninmazsa ''. */
  browser: string
  /** Isletim sistemi; taninmazsa ''. */
  os: string
  /** Telefon (ikon secimi icin). Tablet ve masaustu false. */
  mobile: boolean
}

const BROWSERS: [RegExp, string][] = [
  [/Edg(?:e|A|iOS)?\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/SamsungBrowser\//, 'Samsung Internet'],
  [/Firefox\/|FxiOS\//, 'Firefox'],
  [/CriOS\/|Chrome\//, 'Chrome'],
  [/Safari\//, 'Safari'],
  [/curl\//i, 'curl'],
  [/Postman/i, 'Postman'],
  [/python-requests|aiohttp|httpx/i, 'Python'],
  [/Go-http-client/i, 'Go'],
  [/node-fetch|undici|axios/i, 'Node.js'],
]

const SYSTEMS: [RegExp, string][] = [
  [/Windows/, 'Windows'],
  [/Android/, 'Android'],
  [/iPhone|iPad|iPod/, 'iOS'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/CrOS/, 'ChromeOS'],
  [/Linux|X11/, 'Linux'],
]

/** Bos/tanimsiz User-Agent icin null. */
export function parseUserAgent(ua: string | null | undefined): UaInfo | null {
  const s = (ua ?? '').trim()
  if (!s) return null
  const browser = BROWSERS.find(([re]) => re.test(s))?.[1] ?? ''
  const os = SYSTEMS.find(([re]) => re.test(s))?.[1] ?? ''
  return { browser, os, mobile: /iPhone|iPod|Android.*Mobile|Mobile.*Android|Windows Phone/.test(s) }
}

/** "Chrome · Windows"; hicbiri taninmazsa '' (cagiran "bilinmeyen cihaz" yazar). */
export function deviceLabel(ua: string | null | undefined): string {
  const i = parseUserAgent(ua)
  if (!i) return ''
  return [i.browser, i.os].filter(Boolean).join(' · ')
}
