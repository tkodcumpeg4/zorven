// Arayuz sozluklerinin tutarliligini denetler:  node frontend/i18n_check.js
//  - tr/en anahtar kumeleri ayni, hicbir deger bos degil
//  - {param} yer tutucular uc dilde ayni
//  - index.html'deki data-i18n* ve app.js'teki t('...') anahtarlari tanimli
const fs = require('fs');
const path = require('path');
const dist = path.join(__dirname, 'dist');
const { DICT, LANGS } = require(path.join(dist, 'i18n.js'));

let bad = 0;
const fail = (m) => { console.error('HATA: ' + m); bad++; };

const base = Object.keys(DICT.tr).sort();
for (const l of LANGS) {
  const keys = Object.keys(DICT[l]).sort();
  for (const k of base) if (!keys.includes(k)) fail(`${l}: eksik anahtar ${k}`);
  for (const k of keys) if (!base.includes(k)) fail(`${l}: fazla anahtar ${k}`);
  for (const k of keys) if (!String(DICT[l][k]).trim()) fail(`${l}: bos deger ${k}`);
}
const ph = (s) => (s.match(/\{\w+\}/g) || []).sort().join(',');
for (const k of base) {
  for (const l of ['en']) {
    if (DICT[l][k] != null && ph(DICT[l][k]) !== ph(DICT.tr[k])) fail(`${l}: yer tutucular farkli ${k}`);
  }
}

const html = fs.readFileSync(path.join(dist, 'index.html'), 'utf8');
const js = fs.readFileSync(path.join(dist, 'app.js'), 'utf8');
const used = new Set();
for (const m of html.matchAll(/data-i18n(?:-html|-placeholder|-title)?="([^"]+)"/g)) used.add(m[1]);
for (const m of js.matchAll(/\bt\(\s*'([^']+)'/g)) used.add(m[1]);
for (const m of js.matchAll(/key:\s*'([^']+)'/g)) used.add(m[1]);
for (const m of js.matchAll(/t\(\s*\w+(?:\.\w+)?\s*\?\s*'([^']+)'\s*:\s*'([^']+)'/g)) { used.add(m[1]); used.add(m[2]); }
for (const k of used) if (!base.includes(k)) fail(`kullanilan anahtar sozlukte yok: ${k}`);

console.log(`${base.length} anahtar x ${LANGS.length} dil, ${used.size} kullanilan anahtar` + (bad ? `, ${bad} hata` : ' - tamam'));
process.exit(bad ? 1 : 0);
