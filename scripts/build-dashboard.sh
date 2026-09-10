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

echo "Dashboard statik derleniyor (nuxt generate)..."
# NUXT_PUBLIC_WS_BASE bos ZORLANIR: gomulu dashboard ayni origin'den servis
# edilir, tarayici WS icin sunucunun kendi adresine baglanir. web/.env dev icin
# bunu doldurmus olabilir; burada bos env process'e once konur ki .env EZILMESIN
# (dotenv mevcut env degiskenlerini ezmez).
( cd "$ROOT/web" && NUXT_PUBLIC_WS_BASE= npm run generate )

OUT="$ROOT/web/.output/public"
if [ ! -f "$OUT/index.html" ]; then
  echo "HATA: $OUT/index.html bulunamadi; nuxt generate basarisiz mi?" >&2
  exit 1
fi

echo "server/webdist/ temizleniyor ve dolduruluyor..."
# index.html disindaki eski uretilmis dosyalari temizle (yer tutucu index.html
# uzerine yazilacak).
find "$DEST" -mindepth 1 -not -name '.gitignore' -delete 2>/dev/null || true
cp -r "$OUT"/. "$DEST"/

echo
echo "Tamam. Simdi sunucuyu yeniden derleyip calistirin:"
echo "  cd server && go run . serve --tls-cert certs/server.crt --tls-key certs/server.key --addr 0.0.0.0:8443"
echo
echo "Dashboard artik https://<sunucu>:8443 adresinde ayni origin'den servis edilir."
