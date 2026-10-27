#!/usr/bin/env bash
#
# fetch-wintun.sh dogrulama testi (ag gerektirmez). Sahte zip reddedilmeli;
# beklenen sha'si test icin eslestirilse bile ic DLL sha'lari yine reddetmeli.
# Gercek zip varsa (WINTUN_ZIP_FILE) kabul yolu da denenir.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
pass=0; failn=0
ok()   { echo "  ok   - $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL - $1"; failn=$((failn+1)); }

# 1) Sahte (icerigi rastgele) zip: ust duzey sha uyusmaz -> reddedilir, cikti dosyasi olusmaz.
python3 -I -c "
import zipfile,sys
z=zipfile.ZipFile(sys.argv[1],'w')
for n in ['wintun/LICENSE.txt','wintun/bin/amd64/wintun.dll','wintun/bin/arm64/wintun.dll']:
    z.writestr(n,'sahte')
z.close()" "$T/fake.zip"
out="$(WINTUN_ZIP_FILE="$T/fake.zip" "$ROOT/scripts/fetch-wintun.sh" "$T/o1" 2>&1)"; rc=$?
{ [ $rc -ne 0 ] && echo "$out" | grep -q "zip SHA256 uyusmuyor" && [ ! -e "$T/o1/wintun-amd64.dll" ]; } && ok "sahte zip (yanlis sha) reddedildi" || bad "sahte zip kabul edildi: $out"

# 2) Ust duzey sha eslestirilse de ic DLL sha'si yanlis -> reddedilir.
want="$(sha256sum "$T/fake.zip" | awk '{print $1}')"
out="$(WINTUN_TEST_SHA256="$want" WINTUN_ZIP_FILE="$T/fake.zip" "$ROOT/scripts/fetch-wintun.sh" "$T/o2" 2>&1)"; rc=$?
{ [ $rc -ne 0 ] && echo "$out" | grep -q "wintun.dll SHA256 uyusmuyor" && [ ! -e "$T/o2/wintun-amd64.dll" ]; } && ok "ic DLL sha uyusmazligi reddedildi" || bad "ic DLL dogrulanmadi: $out"

# 3) Gercek zip (varsa) kabul edilir.
if [ -n "${WINTUN_ZIP_FILE:-}" ] && [ -f "$WINTUN_ZIP_FILE" ]; then
  out="$("$ROOT/scripts/fetch-wintun.sh" "$T/o3" 2>&1)"; rc=$?
  { [ $rc -eq 0 ] && [ -s "$T/o3/wintun-amd64.dll" ] && [ -s "$T/o3/wintun-arm64.dll" ] && [ -s "$T/o3/wintun-LICENSE.txt" ]; } && ok "gercek zip kabul edildi" || bad "gercek zip reddedildi: $out"
else
  echo "  atlandi - gercek zip testi (WINTUN_ZIP_FILE verilmedi)"
fi

echo "sonuc: $pass basarili, $failn basarisiz"
[ $failn -eq 0 ]
