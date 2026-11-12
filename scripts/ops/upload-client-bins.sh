#!/usr/bin/env bash
#
# Istemci ikililerini sunucu konteynerine (/app/server/bin) yukler.
# Neden betik: sunucu artik root olmayan kullaniciyla (uid 65532) calisiyor. Duz
# `docker cp` kaynak dosyanin sahipligini ve kipini korur; 0600 ya da baska uid'li
# dosya sunucu tarafindan okunamaz ve indirme 403 doner. Burada dosyalar tar akisiyla,
# sahiplik 65532:65532 ve herkes-okur kipiyle yazilir.
#
# Kullanim (sunucuda): upload-client-bins.sh <kaynak-dizin> [konteyner] [onceki-surum]
#   onceki-surum verilirse mevcut dosyalar once prev-<surum>/ ve prev-desktop-<surum>/ altina kopyalanir.
set -euo pipefail
SRC="${1:?kaynak dizin gerekli}"; C="${2:-deploy-zorven-primary-1}"; PREV="${3:-}"
[ -d "$SRC" ] || { echo "kaynak dizin yok: $SRC" >&2; exit 2; }
UIDGID="${ZV_BIN_OWNER:-65532:65532}"
if [ -n "$PREV" ]; then
  case "$PREV" in *[!0-9.]*) echo "gecersiz surum: $PREV" >&2; exit 2 ;; esac
  docker exec "$C" sh -c "cd /app/server/bin && mkdir -p prev-$PREV prev-desktop-$PREV \
    && cp zorven-* manifest.json manifest.json.sig SHA256SUMS prev-$PREV/ 2>/dev/null; \
    cp Zorven-* desktop.json desktop.json.sig prev-desktop-$PREV/ 2>/dev/null; true"
fi
tar -C "$SRC" --owner="${UIDGID%%:*}" --group="${UIDGID##*:}" --mode='u+rw,go+r,go-w' \
    --exclude='*.sha' -cf - . | docker cp - "$C":/app/server/bin/
# Dogrulama: sunucu kullanicisi her dosyayi okuyabiliyor mu.
BAD="$(docker exec "$C" sh -c 'cd /app/server/bin && for f in *; do [ -f "$f" ] && [ ! -r "$f" ] && echo "$f"; done; true')"
[ -z "$BAD" ] || { echo "okunamayan dosyalar: $BAD" >&2; exit 1; }
echo "yuklendi: $(find "$SRC" -maxdepth 1 -type f ! -name '*.sha' | wc -l) dosya -> $C:/app/server/bin"
