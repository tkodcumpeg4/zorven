// Calistirma: node --test --experimental-strip-types web/tests/i18nParity.test.ts
// tr / en / ja / zh dil dosyalarinin anahtar kumesi ve yer tutucularinin ayni oldugunu dogrular.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const LANGS = ['tr', 'en'] as const
const load = (l: string) => JSON.parse(readFileSync(new URL(`../app/i18n/locales/${l}.json`, import.meta.url), 'utf8'))

function flat(o: Record<string, unknown>, p = '', out: Record<string, string> = {}): Record<string, string> {
  for (const [k, v] of Object.entries(o)) {
    if (v && typeof v === 'object') flat(v as Record<string, unknown>, `${p}${k}.`, out)
    else out[`${p}${k}`] = String(v)
  }
  return out
}

// {name}, {'@'}, @:link, %{p} ve HTML etiketleri
const tokens = (s: string): string[] =>
  (s.match(/\{[^}]*\}|@[.:][\w.]+|<\/?(?:b|i|em|strong|code|br|a|span|kbd|p|ul|li)\b[^>]*>/g) ?? []).sort()

const data = Object.fromEntries(LANGS.map(l => [l, flat(load(l))]))

for (const l of LANGS) {
  if (l === 'en') continue
  test(`${l}: anahtar kumesi en ile ayni`, () => {
    const a = Object.keys(data.en).sort()
    const b = Object.keys(data[l]).sort()
    assert.deepEqual(a.filter(k => !(k in data[l])), [], 'eksik anahtarlar')
    assert.deepEqual(b.filter(k => !(k in data.en)), [], 'fazla anahtarlar')
  })
  test(`${l}: yer tutucular korunmus`, () => {
    const bad = Object.keys(data.en).filter(k => k in data[l] && tokens(data.en[k]).join('|') !== tokens(data[l][k]).join('|'))
    assert.deepEqual(bad, [])
  })
}
