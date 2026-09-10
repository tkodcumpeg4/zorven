#!/bin/bash
# ==============================================================================
# Zorven — Tek Komutla Istemci Kurulum Scripti (Linux & macOS)
# ==============================================================================
set -e

# Renkler
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

echo -e "${CYAN}${BOLD}"
echo "=================================================================="
echo "    ZORVEN — Guvenli Tunel & Istemci Otomatik Kurulumu           "
echo "=================================================================="
echo -e "${NC}"

# Sunucu tarafindan sablon olarak doldurulan varsayilan degerler.
# NOT: sentinel (__INJECTED_..__) sunucuda ReplaceAll ile degistirilir; guard'da
# sentinel'i PARCALI yaziyoruz ("__INJECTED""_..__") ki degismesin — aksi halde
# "deger != deger" hep dogru olmaz ve enjekte edilen deger kullanilmaz.
INJECTED_TOKEN="__INJECTED_TOKEN__"
INJECTED_SERVER="__INJECTED_SERVER__"
SENTINEL_TOKEN="__INJECTED""_TOKEN__"
SENTINEL_SERVER="__INJECTED""_SERVER__"

TOKEN=""
SERVER=""
INSECURE=false

# Parametreleri isle
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --token|-t) TOKEN="$2"; shift ;;
        --server|-s) SERVER="$2"; shift ;;
        --insecure|-k) INSECURE=true ;;
        *) echo -e "${YELLOW}Bilinmeyen parametre: $1${NC}" ;;
    esac
    shift
done

# Enjekte edilen degerler varsa ve arguman verilmemisse kullan
if [ -z "$TOKEN" ] && [ "$INJECTED_TOKEN" != "$SENTINEL_TOKEN" ] && [ -n "$INJECTED_TOKEN" ]; then
    TOKEN="$INJECTED_TOKEN"
fi

if [ -z "$SERVER" ] && [ "$INJECTED_SERVER" != "$SENTINEL_SERVER" ] && [ -n "$INJECTED_SERVER" ]; then
    SERVER="$INJECTED_SERVER"
fi

# Root / Sudo kontrolu
if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}[HATA] Bu kurulum arka plan sistem servisi (systemd) olusturacagi icin root/sudo yetkisi gerektirir.${NC}"
    echo -e "Lutfen su sekilde calistirin:"
    echo -e "  ${BOLD}curl -fsSL <URL> | sudo bash${NC}"
    exit 1
fi

# Token kontrolu (yoksa interaktif sor)
if [ -z "$TOKEN" ]; then
    echo -e "${YELLOW}Zorven istemci token'i bulunamadi.${NC}"
    read -p "Zorven Istemci Token'inizi girin (zrv_live_...): " TOKEN
fi

if [ -z "$TOKEN" ]; then
    echo -e "${RED}[HATA] Token bos olamaz. Kurulum iptal edildi.${NC}"
    exit 1
fi

if [ -z "$SERVER" ]; then
    SERVER="localhost:8443"
fi

# Isletim sistemi ve mimari tespiti
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH_RAW=$(uname -m)

case "$ARCH_RAW" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    armv7l|armhf) ARCH="arm" ;;
    *) echo -e "${RED}[HATA] Desteklenmeyen mimari: $ARCH_RAW${NC}"; exit 1 ;;
esac

case "$OS" in
    linux) OS_NAME="linux" ;;
    darwin) OS_NAME="darwin" ;;
    *) echo -e "${RED}[HATA] Desteklenmeyen isletim sistemi: $OS${NC}"; exit 1 ;;
esac

BIN_NAME="zorven-${OS_NAME}-${ARCH}"
INSTALL_PATH="/usr/local/bin/zorven"

echo -e "-> Isletim Sistemi: ${GREEN}${OS_NAME}${NC}"
echo -e "-> Mimari:         ${GREEN}${ARCH}${NC}"
echo -e "-> Sunucu:         ${GREEN}${SERVER}${NC}"
echo -e "-> Kurulum Yolu:   ${GREEN}${INSTALL_PATH}${NC}"

# Indirme protokolü
SCHEME="https"
CURL_FLAGS="-fsSL"
if [ "$INSECURE" = true ] || [[ "$SERVER" =~ ^(localhost|127\.0\.0\.1) ]]; then
    CURL_FLAGS="-kfsSL"
fi

DOWNLOAD_URL="${SCHEME}://${SERVER}/bin/${BIN_NAME}"

echo -e "\n${CYAN}1. Zorven istemci ikili dosyasi indiriliyor...${NC}"
echo -e "   Kaynak: $DOWNLOAD_URL"

mkdir -p /usr/local/bin
if curl $CURL_FLAGS "$DOWNLOAD_URL" -o "$INSTALL_PATH"; then
    chmod +x "$INSTALL_PATH"
    echo -e "   ${GREEN}[OK] Indirildi ve calistirma izni verildi.${NC}"
else
    echo -e "${RED}[HATA] Zorven ikilisi indirilemedi. Lutfen sunucu baglantinizi kontrol edin.${NC}"
    exit 1
fi

echo -e "\n${CYAN}2. Sistem yapilandirmasi kaydediliyor...${NC}"
mkdir -p /etc/zorven
"$INSTALL_PATH" config set-server "$SERVER" --system
"$INSTALL_PATH" config set-token "$TOKEN" --system
echo -e "   ${GREEN}[OK] /etc/zorven/config.json olusturuldu.${NC}"

echo -e "\n${CYAN}3. Arka plan sistem servisi kuruluyor ve baslatiliyor...${NC}"
"$INSTALL_PATH" service install || true
"$INSTALL_PATH" service start || true

echo -e "\n${GREEN}${BOLD}=================================================================="
echo "    TEBRIKLER! ZORVEN ISTEMCISI BASARIYLA KURULDU VE BASLATILDI   "
echo "==================================================================${NC}"
echo -e "Servis Durumu : ${GREEN}Aktif (systemd servisi arka planda calisiyor)${NC}"
echo -e "Yonetim Komutlari:"
echo -e "  - Durum:      ${BOLD}zorven service status${NC}"
echo -e "  - Yeniden:    ${BOLD}zorven service restart${NC}"
echo -e "  - Durdur:     ${BOLD}zorven service stop${NC}"
echo -e "  - Kaldir:     ${BOLD}zorven service uninstall${NC}"
echo -e "  - Canli Port: ${BOLD}zorven 8080${NC}"
echo -e "\nZorven Dashboard'unuzdan bu makinenin durumunu ve canli CPU/RAM metriklerini gorebilirsiniz."
