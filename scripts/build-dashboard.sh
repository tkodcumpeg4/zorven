#!/usr/bin/env bash
#
# Nuxt dashboard'ini statik derler ve tunel sunucusuna gomulmek uzere
# server/webdist/ icine kopyalar.
#
# Sonrasinda sunucu yeniden derlenip calistirildiginda dashboard ayni origin'den
# (:8443) servis edilir; dev proxy ve NUXT_PUBLIC_WS_BASE GEREKMEZ.
#
# Kullanim:  ./scripts/build-dashboard.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="$ROOT/server/webdist"

# Koruma: app.vue sablonu (NuxtLayout/NuxtPage) kaybolursa panel siyah ekranda kalir
# (2026-10-08 olayi). Derlemeden once dogrula.
if ! grep -q "<NuxtPage" "$ROOT/web/app/app.vue"; then
  echo "HATA: web/app/app.vue icinde <NuxtPage /> yok; panel bos ekran olur." >&2
  exit 1
fi

echo "Dashboard statik derleniyor (nuxt generate)..."
# NUXT_PUBLIC_WS_BASE bos ZORLANIR: gomulu dashboard ayni origin'den servis
# edilir, tarayici WS icin sunucunun kendi adresine baglanir. web/.env dev icin
# bunu doldurmus olabilir; burada bos env process'e once konur ki .env EZILMESIN
# (dotenv mevcut env degiskenlerini ezmez).
#
# Eski cikti once silinir: generate basarisiz olup 0 donerse eski .output
# sessizce kopyalanip yayina gidiyordu (2026-10-09: eksik @xterm eklentileri,
# terminal arayuzu derlemeye girmedi).
OUT="$ROOT/web/.output/public"
rm -rf "$ROOT/web/.output"
LOG="$(mktemp)"
( cd "$ROOT/web" && NUXT_PUBLIC_WS_BASE= npm run generate ) 2>&1 | tee "$LOG"
if grep -q "Build failed\|Nuxt build error" "$LOG"; then
  echo "HATA: nuxt generate basarisiz (eksik paket icin: cd web && npm install)." >&2
  rm -f "$LOG"
  exit 1
fi
rm -f "$LOG"
if [ ! -f "$OUT/index.html" ]; then
  echo "HATA: $OUT/index.html bulunamadi; nuxt generate basarisiz mi?" >&2
  exit 1
fi

echo "server/webdist/ temizleniyor ve dolduruluyor..."
# index.html disindaki eski uretilmis dosyalari temizle (yer tutucu index.html
# uzerine yazilacak).
find "$DEST" -mindepth 1 -not -name '.gitignore' -not -name '.keep' -delete 2>/dev/null || true
cp -r "$OUT"/. "$DEST"/

echo
echo "Tamam. Simdi sunucuyu yeniden derleyip calistirin:"
echo "  cd server && go run . serve --tls-cert certs/server.crt --tls-key certs/server.key --addr 0.0.0.0:8443"
echo
echo "Dashboard artik https://<sunucu>:8443 adresinde ayni origin'den servis edilir."
