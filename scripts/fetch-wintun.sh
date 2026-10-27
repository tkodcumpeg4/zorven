#!/usr/bin/env bash
#
# Wintun (WireGuard LLC) imzali, DEGISTIRILMEMIS wintun.dll'lerini resmi
# kaynaktan indirir, SHA256 ile dogrular ve <cikti-dizini> altina koyar:
#   wintun-amd64.dll  wintun-arm64.dll  wintun-LICENSE.txt
#
# YALNIZ derleme/yayin zamaninda, yerel makinede calistirilir; dosyalar depoya
# KONMAZ (dist/ gitignore'da). Lisans ("Prebuilt Binaries License"): DLL
# degistirilmeden, wintun.h'deki API'yi kullanan yazilimla birlikte dagitilabilir;
# lisans metni DLL ile birlikte tasinir. Bkz. docs/zorven-net-share.md.
#
# Kullanim:  scripts/fetch-wintun.sh [cikti-dizini=dist]
# Ortam:     WINTUN_ZIP_FILE=<yol>   indirmek yerine yerel zip kullan (yine dogrulanir)
#            WINTUN_ZIP_URL=<url>    kaynak adresi (varsayilan resmi)
#            WINTUN_TEST_SHA256=<hex> YALNIZ testler: zip sha256 beklentisini degistirir
set -euo pipefail

WINTUN_VERSION="0.14.1"
WINTUN_URL="${WINTUN_ZIP_URL:-https://www.wintun.net/builds/wintun-${WINTUN_VERSION}.zip}"
# wintun.net'te yayinlanan degerler (wintun-0.14.1.zip) ve zip icindeki DLL'ler.
WINTUN_ZIP_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
WINTUN_AMD64_SHA256="e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
WINTUN_ARM64_SHA256="f7ba89005544be9d85231a9e0d5f23b2d15b3311667e2dad0debd344918a3f80"
[ -n "${WINTUN_TEST_SHA256:-}" ] && WINTUN_ZIP_SHA256="$WINTUN_TEST_SHA256"

OUT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/dist}"
mkdir -p "$OUT"

sha_of() { sha256sum "$1" 2>/dev/null | awk '{print $1}' || shasum -a 256 "$1" | awk '{print $1}'; }
fail() { echo "HATA: $*" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [ -n "${WINTUN_ZIP_FILE:-}" ]; then
  cp "$WINTUN_ZIP_FILE" "$TMP/wintun.zip" || fail "zip okunamadi: $WINTUN_ZIP_FILE"
else
  echo "wintun ${WINTUN_VERSION} indiriliyor: $WINTUN_URL"
  curl -fsSL --proto '=https' --tlsv1.2 -m 120 -o "$TMP/wintun.zip" "$WINTUN_URL" || fail "wintun indirilemedi"
fi

got="$(sha_of "$TMP/wintun.zip")"
if [ "$got" != "$WINTUN_ZIP_SHA256" ]; then
  fail "wintun zip SHA256 uyusmuyor (beklenen $WINTUN_ZIP_SHA256, alinan $got); dosya reddedildi"
fi

mkdir -p "$TMP/x"
python3 -I - "$TMP/wintun.zip" "$TMP/x" <<'PY' || fail "zip acilamadi"
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
want = ["wintun/LICENSE.txt", "wintun/bin/amd64/wintun.dll", "wintun/bin/arm64/wintun.dll"]
for n in want:
    open(sys.argv[2] + "/" + n.replace("/", "_"), "wb").write(z.read(n))
PY

check() { # dosya beklenen-sha
  local g; g="$(sha_of "$1")"
  [ "$g" = "$2" ] || fail "$(basename "$1") SHA256 uyusmuyor (beklenen $2, alinan $g)"
}
check "$TMP/x/wintun_bin_amd64_wintun.dll" "$WINTUN_AMD64_SHA256"
check "$TMP/x/wintun_bin_arm64_wintun.dll" "$WINTUN_ARM64_SHA256"

cp "$TMP/x/wintun_bin_amd64_wintun.dll" "$OUT/wintun-amd64.dll"
cp "$TMP/x/wintun_bin_arm64_wintun.dll" "$OUT/wintun-arm64.dll"
cp "$TMP/x/wintun_LICENSE.txt" "$OUT/wintun-LICENSE.txt"
chmod 644 "$OUT"/wintun-amd64.dll "$OUT"/wintun-arm64.dll "$OUT"/wintun-LICENSE.txt
echo "wintun ${WINTUN_VERSION} hazir -> $OUT (wintun-amd64.dll, wintun-arm64.dll, wintun-LICENSE.txt)"
