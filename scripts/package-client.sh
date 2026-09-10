#!/usr/bin/env bash
#
# Baska bir Windows PC'ye kopyalanacak "istemci kurulum paketi" hazirlar.
#
# Ciktisi (dist/client-setup/) sunucunun sertifikasini ve LAN adresini icerir;
# o klasoru diger PC'ye kopyalayip install.bat'a cift tiklamak yeterli. Uygulama
# acilinca CA sertifikasi ve sunucu adresi ONCEDEN DOLU gelir; kullanici yalnizca
# kendi token'ini (rpsh_live_...) yapistirir.
#
# Kullanim:  ./scripts/package-client.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist/client-setup"
CERT="$ROOT/server/certs/server.crt"
EXE="$ROOT/desktop/build/bin/rpshell-desktop.exe"

# 1. Sertifika var mi
if [ ! -f "$CERT" ]; then
  echo "HATA: $CERT yok. Once sunucuyu bir kez calistir (start.sh) veya:" >&2
  echo "      cd server && go run . cert --host \"localhost,127.0.0.1,<LAN-IP>,::1\"" >&2
  exit 1
fi

# 2. Masaustu exe var mi (yoksa derlemeyi dene)
if [ ! -f "$EXE" ]; then
  echo "Masaustu uygulamasi derleniyor (wails build)..."
  export PATH="$PATH:$(go env GOPATH)/bin"
  ( cd "$ROOT/desktop" && wails build -platform windows/amd64 -o rpshell-desktop.exe )
fi

# 3. Sunucu LAN adresini tespit et (server.txt'e yazilir -> app onceden doldurur)
# Elle vermek icin:  ZORVEN_SERVER_IP=192.168.1.180 ./scripts/package-client.sh
LAN_IP="${ZORVEN_SERVER_IP:-}"
if [ -z "$LAN_IP" ]; then
  # Tum aday IPv4'leri topla, sonra ONCELIK sirasiyla sec:
  # 192.168.* (ev/ofis LAN) > 10.* > 172.* (Docker/Hyper-V genelde 172.x, en son).
  case "$(uname -s 2>/dev/null)" in
    Linux)  CANDS="$(hostname -I 2>/dev/null | tr ' ' '\n')" ;;
    Darwin) CANDS="$(ipconfig getifaddr en0 2>/dev/null; ipconfig getifaddr en1 2>/dev/null)" ;;
    MINGW*|MSYS*|CYGWIN*)
      CANDS="$(ipconfig 2>/dev/null | grep -a 'IPv4' | sed 's/.*: *//' | tr -d '\r')" ;;
    *) CANDS="" ;;
  esac
  CANDS="$(printf '%s\n' "$CANDS" | grep -vE '^127\.|^169\.254\.|^$' || true)"
  for pat in '^192\.168\.' '^10\.' '^172\.'; do
    LAN_IP="$(printf '%s\n' "$CANDS" | grep -E "$pat" | head -1 || true)"
    [ -n "$LAN_IP" ] && break
  done
fi
PORT="${ZORVEN_PORT:-8443}"

# 4. Paketi olustur
rm -rf "$OUT"
mkdir -p "$OUT"
cp "$EXE" "$OUT/rpshell-desktop.exe"
cp "$CERT" "$OUT/server.crt"
cp "$ROOT/installer/install.ps1" "$OUT/"
cp "$ROOT/installer/uninstall.ps1" "$OUT/"
cp "$ROOT/installer/install.bat" "$OUT/"

if [ -n "$LAN_IP" ]; then
  echo "${LAN_IP}:${PORT}" > "$OUT/server.txt"
  echo "Sunucu adresi paketlendi: ${LAN_IP}:${PORT}"
else
  echo "UYARI: LAN IP tespit edilemedi; server.txt yazilmadi."
  echo "       Diger PC'de sunucu adresini elle gir (or. 192.168.1.180:${PORT})."
fi

cat > "$OUT/OKU.txt" <<EOF
rpshell istemci kurulumu (baska PC icin)

1. Bu klasoru hedef Windows PC'ye kopyala.
2. install.bat'a cift tikla.
3. Acilan uygulamada:
   - Tunel sunucusu adresi: ONCEDEN DOLU (${LAN_IP:-<sunucu-ip>}:${PORT})
   - CA sertifikasi: ONCEDEN DOLU (paketteki server.crt)
   - Istemci token'i: dashboard'dan bu PC icin olusturup yapistir (rpsh_live_...)
4. Durum sekmesi > Baglan.

Sunucunun bu PC'den erisilebilir oldugundan emin ol (ayni ag, guvenlik duvari :${PORT}).
EOF

# 5. ZIP'le — tek dosya olarak baska PC'ye kolayca tasinsin.
ZIP="$ROOT/dist/rpshell-client-setup.zip"
rm -f "$ZIP" 2>/dev/null || true
if command -v zip >/dev/null 2>&1; then
  ( cd "$OUT" && zip -q -r "$ZIP" . )
elif command -v powershell >/dev/null 2>&1; then
  # Windows: yerlesik Compress-Archive. PowerShell unix-stili yolu anlamaz;
  # cygpath ile Windows yoluna cevir (yoksa oldugu gibi dene).
  OUT_WIN="$(cygpath -w "$OUT" 2>/dev/null || echo "$OUT")"
  ZIP_WIN="$(cygpath -w "$ZIP" 2>/dev/null || echo "$ZIP")"
  powershell -NoProfile -Command "Compress-Archive -Path '$OUT_WIN\\*' -DestinationPath '$ZIP_WIN' -Force"
fi

echo
echo "Paket hazir: $OUT"
ls -1 "$OUT"
if [ -f "$ZIP" ]; then
  echo
  echo "ZIP hazir: $ZIP"
  echo "  -> Bu ZIP'i diger PC'ye kopyala, ac, install.bat'a cift tikla."
else
  echo
  echo "ZIP olusturulamadi (zip/powershell yok). $OUT klasorunu elle kopyala."
fi
