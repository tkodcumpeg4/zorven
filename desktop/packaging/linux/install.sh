#!/bin/sh
# Zorven masaustu uygulamasini kullanici dizinine kurar (yonetici yetkisi gerekmez).
#   ./install.sh            kur / guncelle
#   ./install.sh --uninstall kaldir
# Gerekli calisma kitapliklari: GTK 3 ve WebKitGTK 4.1
#   Debian/Ubuntu: sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0
#   Fedora:        sudo dnf install gtk3 webkit2gtk4.1
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
BIN="$HOME/.local/bin"
APPS="$HOME/.local/share/applications"
ICONS="$HOME/.local/share/icons/hicolor/512x512/apps"

if [ "${1:-}" = "--uninstall" ]; then
  rm -f "$BIN/zorven-desktop" "$APPS/zorven-desktop.desktop" "$ICONS/zorven.png"
  echo "Zorven masaustu kaldirildi (ayarlar ~/.config/zorven altinda duruyor)."
  exit 0
fi

mkdir -p "$BIN" "$APPS" "$ICONS"
install -m 755 "$DIR/zorven-desktop" "$BIN/zorven-desktop"
install -m 644 "$DIR/zorven.png" "$ICONS/zorven.png"
sed "s|^Exec=.*|Exec=$BIN/zorven-desktop|" "$DIR/zorven-desktop.desktop" > "$APPS/zorven-desktop.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" >/dev/null 2>&1 || true
echo "Zorven masaustu kuruldu: $BIN/zorven-desktop (uygulama menusunde 'Zorven')."
if ! ldconfig -p 2>/dev/null | grep -q libwebkit2gtk-4.1; then
  echo "Uyari: WebKitGTK 4.1 bulunamadi. Debian/Ubuntu: sudo apt install libwebkit2gtk-4.1-0"
fi
