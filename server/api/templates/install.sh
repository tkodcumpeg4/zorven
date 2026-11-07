#!/bin/sh
# ==============================================================================
# Zorven - Tek Komutla Istemci Kurulum Scripti (Linux & macOS, POSIX sh)
#
# Kurulum : curl -fsSL https://<sunucu>/install.sh | sh -s -- --token zrv_live_...
# Kaldirma: curl -fsSL https://<sunucu>/install.sh | sh -s -- --uninstall [--purge]
# Root degilseniz ve sudo varsa ayricalikli adimlar sudo ile calistirilir.
# ==============================================================================
set -eu

# Sunucu tarafindan sablon olarak doldurulan degerler. Sentinel PARCALI yazilir
# ki sunucudaki ReplaceAll bu karsilastirmalari bozmasin.
INJECTED_TOKEN='__INJECTED_TOKEN__'
INJECTED_SERVER='__INJECTED_SERVER__'
SENTINEL_TOKEN="__INJECTED""_TOKEN__"
SENTINEL_SERVER="__INJECTED""_SERVER__"

if [ -t 1 ]; then
    RED=$(printf '\033[0;31m'); GREEN=$(printf '\033[0;32m'); YELLOW=$(printf '\033[1;33m')
    CYAN=$(printf '\033[0;36m'); BOLD=$(printf '\033[1m'); NC=$(printf '\033[0m')
else
    RED=''; GREEN=''; YELLOW=''; CYAN=''; BOLD=''; NC=''
fi

say()  { printf '%s\n' "$*"; }
info() { printf '%s%s%s\n' "$CYAN" "$*" "$NC"; }
ok()   { printf '   %s[OK]%s %s\n' "$GREEN" "$NC" "$*"; }
warn() { printf '%s[UYARI]%s %s\n' "$YELLOW" "$NC" "$*" >&2; }
die()  { printf '%s[HATA]%s %s\n' "$RED" "$NC" "$*" >&2; exit 1; }

TOKEN="${ZORVEN_TOKEN:-}"
SERVER=""
INSECURE=false
UNINSTALL=false
PURGE=false

while [ "$#" -gt 0 ]; do
    case "$1" in
        --token|-t) [ "$#" -ge 2 ] || die "$1 icin deger gerekli."; TOKEN="$2"; shift ;;
        --server|-s) [ "$#" -ge 2 ] || die "$1 icin deger gerekli."; SERVER="$2"; shift ;;
        --insecure|-k) INSECURE=true ;;
        --uninstall) UNINSTALL=true ;;
        --purge) PURGE=true ;;
        *) warn "Bilinmeyen parametre: $1" ;;
    esac
    shift
done

if [ -z "$TOKEN" ] && [ "$INJECTED_TOKEN" != "$SENTINEL_TOKEN" ] && [ -n "$INJECTED_TOKEN" ]; then
    TOKEN="$INJECTED_TOKEN"
fi
if [ -z "$SERVER" ] && [ "$INJECTED_SERVER" != "$SENTINEL_SERVER" ] && [ -n "$INJECTED_SERVER" ]; then
    SERVER="$INJECTED_SERVER"
fi

printf '%s%s\n' "$CYAN" "$BOLD"
say "=================================================================="
say "    ZORVEN - Guvenli Tunel & Istemci Otomatik Kurulumu"
say "=================================================================="
printf '%s' "$NC"

# Ayricalik: root degilse sudo ile devam et (pipe ile calistigi icin script
# kendini yeniden calistiramaz; ayricalikli komutlar tek tek sudo'lanir).
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
        warn "Root degilsiniz; sistem servisi kurmak icin sudo kullanilacak (parola istenebilir)."
    else
        die "Bu kurulum root yetkisi gerektirir ve sudo bulunamadi. Root olarak calistirin."
    fi
fi

OS_NAME=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS_NAME" in
    linux|darwin) ;;
    *) die "Desteklenmeyen isletim sistemi: $OS_NAME" ;;
esac

INSTALL_DIR="/usr/local/bin"
INSTALL_PATH="$INSTALL_DIR/zorven"

if [ "$OS_NAME" = "darwin" ]; then SVC_KIND="launchd"; else SVC_KIND="systemd"; fi

svc_installed() {
    [ -x "$INSTALL_PATH" ] && $SUDO "$INSTALL_PATH" service status >/dev/null 2>&1
}

# ------------------------------------------------------------------ kaldirma
if [ "$UNINSTALL" = true ]; then
    info "Zorven kaldiriliyor..."
    if [ -x "$INSTALL_PATH" ]; then
        if svc_installed; then
            $SUDO "$INSTALL_PATH" service stop >/dev/null 2>&1 || true
            $SUDO "$INSTALL_PATH" service uninstall || die "Servis kaldirilamadi; ikili dosya silinmedi."
            ok "Servis ($SVC_KIND) kaldirildi."
        else
            say "   Kayitli servis bulunamadi."
        fi
        $SUDO rm -f "$INSTALL_PATH" || die "$INSTALL_PATH silinemedi."
        ok "$INSTALL_PATH silindi."
    else
        say "   $INSTALL_PATH bulunamadi; yapilacak bir sey yok."
    fi
    if [ "$PURGE" = true ]; then
        $SUDO rm -rf /etc/zorven || die "/etc/zorven silinemedi."
        ok "Yapilandirma (/etc/zorven) silindi."
    else
        say "   Yapilandirma korundu (/etc/zorven). Silmek icin --purge kullanin."
    fi
    printf '%s%sZorven kaldirildi.%s\n' "$GREEN" "$BOLD" "$NC"
    exit 0
fi

# ------------------------------------------------------------------ dogrulama
if [ -z "$SERVER" ]; then
    die "Sunucu adresi belirlenemedi. Scripti sunucudan (https://<sunucu>/install.sh) indirin ya da --server host[:port] verin."
fi
case "$SERVER" in
    *[!A-Za-z0-9.:-]*|-*) die "Gecersiz sunucu adresi: $SERVER" ;;
esac

if [ -z "$TOKEN" ]; then
    warn "Zorven istemci token'i bulunamadi."
    # curl | sh ile stdin boru hattidir; terminalden (/dev/tty) okunur.
    if [ -t 0 ]; then
        printf "Zorven Istemci Token'inizi girin (zrv_live_...): "
        read -r TOKEN || TOKEN=""
    elif [ -r /dev/tty ] && { : < /dev/tty; } 2>/dev/null; then
        printf "Zorven Istemci Token'inizi girin (zrv_live_...): " > /dev/tty
        read -r TOKEN < /dev/tty || TOKEN=""
    else
        die "Token verilmedi ve etkilesimli terminal yok. --token zrv_live_... ile tekrar calistirin."
    fi
fi
TOKEN=$(printf '%s' "$TOKEN" | tr -d '[:space:]')
[ -n "$TOKEN" ] || die "Token bos olamaz. Kurulum iptal edildi."
case "$TOKEN" in
    zrv_live_*) ;;
    rpsh_live_*) warn "Eski bicimli (rpsh_live_) token kullaniliyor; yeni token'lar 'zrv_live_' ile baslar." ;;
    *) die "Gecersiz token bicimi: token 'zrv_live_' ile baslamalidir." ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    armv7l|armv6l|armhf) ARCH="arm" ;;
    *) die "Desteklenmeyen mimari: $(uname -m)" ;;
esac
if [ "$OS_NAME" = "darwin" ] && [ "$ARCH" = "arm" ]; then
    die "macOS icin 32-bit ARM ikilisi yok."
fi

# Gecici dizin (0700); cikista token dosyasi dahil temizlenir.
TMPDIR_Z=$(mktemp -d "${TMPDIR:-/tmp}/zorven-install.XXXXXX") || die "Gecici dizin olusturulamadi."
cleanup() { rm -rf "$TMPDIR_Z"; }
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

# HTTP istemcisi (curl, yoksa wget). TLS dogrulamasi YALNIZ --insecure ile kapanir.
BASE="https://${SERVER}/bin"
fetch() { # fetch URL DEST
    if command -v curl >/dev/null 2>&1; then
        if [ "$INSECURE" = true ]; then
            curl -ksSf --connect-timeout 15 --max-time 600 -o "$2" "$1"
        else
            curl -sSf --connect-timeout 15 --max-time 600 -o "$2" "$1"
        fi
    elif command -v wget >/dev/null 2>&1; then
        if [ "$INSECURE" = true ]; then
            wget -q --no-check-certificate -O "$2" "$1"
        else
            wget -q -O "$2" "$1"
        fi
    else
        die "curl veya wget gerekli."
    fi
}
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
    elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 "$1" | sed 's/^.*= *//'
    else die "SHA-256 araci (sha256sum, shasum veya openssl) bulunamadi; dogrulama yapilamadigi icin kurulum durduruldu."
    fi
}

if [ "$INSECURE" = true ]; then
    warn "--insecure: TLS sertifika dogrulamasi YALNIZ bu kurulumun indirmeleri icin kapali (self-hosted/gelistirme)."
fi

say "-> Isletim Sistemi: ${GREEN}${OS_NAME}${NC}"
say "-> Mimari:          ${GREEN}${ARCH}${NC}"
say "-> Sunucu:          ${GREEN}${SERVER}${NC}"
say "-> Kurulum Yolu:    ${GREEN}${INSTALL_PATH}${NC}"

# ------------------------------------------------------------------ 1. indir + dogrula
info "
1. Surum bilgisi ve ikili dosya indiriliyor..."
fetch "$BASE/manifest.json" "$TMPDIR_Z/manifest.json" || die "manifest.json indirilemedi ($BASE/manifest.json). Sunucu baglantinizi kontrol edin."

KEY="$OS_NAME/$ARCH"
ENTRY=$(grep "\"$KEY\":" "$TMPDIR_Z/manifest.json" | head -n 1 || true)
[ -n "$ENTRY" ] || die "Bu platform ($KEY) icin yayinlanmis ikili yok."
BIN_NAME=$(printf '%s' "$ENTRY" | sed -n 's/.*"name": *"\([^"]*\)".*/\1/p')
WANT_SHA=$(printf '%s' "$ENTRY" | sed -n 's/.*"sha256": *"\([0-9a-fA-F]*\)".*/\1/p' | tr '[:upper:]' '[:lower:]')
case "$BIN_NAME" in
    zorven-*) ;;
    *) die "Manifestte gecersiz dosya adi." ;;
esac
case "$BIN_NAME" in
    *[!A-Za-z0-9._-]*) die "Manifestte gecersiz dosya adi." ;;
esac
[ "${#WANT_SHA}" -eq 64 ] || die "Manifestte $KEY icin gecerli sha256 yok; kurulum durduruldu."
VERSION=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$TMPDIR_Z/manifest.json" | head -n 1)

say "   Kaynak: $BASE/$BIN_NAME ${VERSION:+(surum $VERSION)}"
NEWBIN="$TMPDIR_Z/zorven.new"
fetch "$BASE/$BIN_NAME" "$NEWBIN" || die "Zorven ikilisi indirilemedi. Sunucu baglantinizi kontrol edin."
GOT_SHA=$(sha256_of "$NEWBIN")
if [ "$GOT_SHA" != "$WANT_SHA" ]; then
    die "SHA-256 uyusmuyor (beklenen $WANT_SHA, alinan $GOT_SHA). Dosya bozuk veya degistirilmis; kurulum iptal edildi."
fi
ok "SHA-256 dogrulandi."
chmod 755 "$NEWBIN"

# ------------------------------------------------------------------ 2. atomik degistirme
info "
2. Ikili dosya kuruluyor..."
UPGRADE=false
if svc_installed; then
    UPGRADE=true
    say "   Mevcut kurulum bulundu; yukseltiliyor."
    $SUDO "$INSTALL_PATH" service stop >/dev/null 2>&1 || true
    # Yeni token/sunucu ile temiz kayit icin eski servis tanimi kaldirilir.
    $SUDO "$INSTALL_PATH" service uninstall || die "Mevcut servis kaldirilamadi; yukseltme durduruldu."
fi
$SUDO mkdir -p "$INSTALL_DIR" || die "$INSTALL_DIR olusturulamadi."
STAGED="$INSTALL_DIR/.zorven.new.$$"
$SUDO cp "$NEWBIN" "$STAGED" || die "$STAGED yazilamadi."
$SUDO chmod 755 "$STAGED"
$SUDO mv -f "$STAGED" "$INSTALL_PATH" || { $SUDO rm -f "$STAGED"; die "$INSTALL_PATH degistirilemedi."; }
ok "$INSTALL_PATH yazildi."

# ------------------------------------------------------------------ 3. servis
info "
3. Arka plan servisi ($SVC_KIND) kuruluyor ve baslatiliyor..."
# Token komut satirina/surec listesine yazilmaz; 0600 dosyadan okunur ve CLI siler.
TOKEN_FILE="$TMPDIR_Z/token"
( umask 077; printf '%s' "$TOKEN" > "$TOKEN_FILE" ) || die "Gecici token dosyasi yazilamadi."
TOKEN=""
$SUDO mkdir -p /etc/zorven
$SUDO "$INSTALL_PATH" service install --server "$SERVER" --token-file "$TOKEN_FILE" \
    || die "Servis kurulamadi. Ayrinti icin yukaridaki ciktiya bakin."
rm -f "$TOKEN_FILE"
ok "Servis kaydedildi."
$SUDO "$INSTALL_PATH" service start || die "Servis baslatilamadi. 'zorven service status' ile kontrol edin."
ok "Servis baslatildi."

printf '\n%s%s==================================================================\n' "$GREEN" "$BOLD"
if [ "$UPGRADE" = true ]; then
    say "    ZORVEN ISTEMCISI YUKSELTILDI VE BASLATILDI"
else
    say "    TEBRIKLER! ZORVEN ISTEMCISI BASARIYLA KURULDU VE BASLATILDI"
fi
printf '==================================================================%s\n' "$NC"
say "Servis Durumu : ${GREEN}Aktif ($SVC_KIND servisi arka planda calisiyor)${NC}"
say "Yonetim Komutlari:"
say "  - Durum:      ${BOLD}zorven service status${NC}"
say "  - Yeniden:    ${BOLD}zorven service restart${NC}"
say "  - Durdur:     ${BOLD}zorven service stop${NC}"
say "  - Kaldir:     ${BOLD}curl -fsSL https://${SERVER}/install.sh | sh -s -- --uninstall${NC}"
say "  - Canli Port: ${BOLD}zorven 8080${NC}"
say ""
say "Zorven Dashboard'unuzdan bu makinenin durumunu ve canli CPU/RAM metriklerini gorebilirsiniz."
