// Her dil dosyasindaki her mesaj vue-i18n derleyicisinden gecmeli.
// Neden: "*@ornek.com" gibi ham '@' (baglantili mesaj sozdizimi) ya da '|' (cogul)
// iceren bir metin calisma zaninda SyntaxError atar ve o metni kullanan bilesen
// (or. tunel MCP sekmesi) bos gorunur. Dogru yazim: {'@'} ve {'|'}.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { baseCompile } from '@intlify/message-compiler'

const here = path.dirname(fileURLToPath(import.meta.url))
const langs = ['tr', 'en']

function* walk(o: unknown, p = ''): Generator<[string, string]> {
  if (typeof o === 'string') { yield [p, o]; return }
  if (o && typeof o === 'object') for (const [k, v] of Object.entries(o)) yield* walk(v, p ? `${p}.${k}` : k)
}

for (const lang of langs) {
  test(`i18n ${lang}: tum mesajlar derlenir`, () => {
    const raw = readFileSync(path.join(here, '..', 'app', 'i18n', 'locales', `${lang}.json`), 'utf8').replace(/^﻿/, '')
    const bad: string[] = []
    for (const [key, msg] of walk(JSON.parse(raw))) {
      try {
        const r = baseCompile(msg, { onError: (e) => { throw e } })
        if (!r || !r.code) bad.push(`${key}: bos cikti`)
      } catch (e: any) {
        bad.push(`${key}: ${e?.message || e} -> ${msg.slice(0, 80)}`)
      }
    }
    assert.deepEqual(bad, [], `derlenemeyen mesajlar (${lang}):\n` + bad.join('\n'))
  })
}
