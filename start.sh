#!/usr/bin/env bash
#
# rpshell tunel sunucusunu TEK KOMUTLA baslatir (Linux / macOS).
#
# Yaptiklari (sirayla):
#   1. Go var mi kontrol eder.
#   2. TLS sertifikasi yoksa uretir (server/certs/).
#   3. Dashboard gomulu degilse ve npm varsa derleyip gomer.
#   4. Sunucuyu baslatir. Ilk calistirmada ADMIN ANAHTARI ekrana yazilir.
#
# Dashboard + API + terminal/ekran hepsi ayni adreste servis edilir:
#   https://<makine-ip>:8443
#
# Ayarlanabilir ortam degiskenleri:
#   ZORVEN_ADDR   dinlenecek adres (varsayilan 0.0.0.0:8443 = LAN'a acik)
#   ZORVEN_DB     veritabani yolu (varsayilan server/rpshell.db)
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADDR="${ZORVEN_ADDR:-0.0.0.0:8443}"

# 1. Go kontrolu
if ! command -v go >/dev/null 2>&1; then
  echo "HATA: Go bulunamadi. https://go.dev/dl/ adresinden Go 1.23+ kurun." >&2
  exit 1
fi

cd "$ROOT/server"

# LAN IPv4 adreslerini tespit et. HEM sertifikaya (TLS) HEM kontrol hostu
# listesine eklenir; aksi halde LAN'dan gelen istek tunel sanilir -> TUNNEL_NOT_FOUND.
HOSTS="localhost,127.0.0.1,::1"
LAN_IPS=""
case "$(uname -s 2>/dev/null)" in
  Linux)
    LAN_IPS="$(hostname -I 2>/dev/null)" ;;
  Darwin)
    LAN_IPS="$(ipconfig getifaddr en0 2>/dev/null; ipconfig getifaddr en1 2>/dev/null)" ;;
  MINGW*|MSYS*|CYGWIN*)
    # Windows Git Bash: ipconfig ciktisindan IPv4 satirlarini ayikla.
    LAN_IPS="$(ipconfig 2>/dev/null | grep -a 'IPv4' | sed 's/.*: *//' | tr -d '\r')" ;;
esac
for ip in $LAN_IPS; do
  case "$ip" in
    127.*|169.254.*|::1|"") ;;          # loopback/link-local/bos atla
    *) HOSTS="$HOSTS,$ip" ;;
  esac
done
echo "Kontrol hostlari: $HOSTS"

# 2. Sertifika (LAN IP'leri dahil)
if [ ! -f certs/server.crt ] || [ ! -f certs/server.key ]; then
  echo "TLS sertifikasi uretiliyor..."
  go run . cert --host "$HOSTS"
fi

# 3. Dashboard gomulu mu? (webdist/_nuxt gercek build'in isareti)
if [ ! -d webdist/_nuxt ]; then
  if command -v npm >/dev/null 2>&1; then
    echo "Dashboard gomulu degil, derleniyor (bir kez)..."
    ( cd "$ROOT" && ./scripts/build-dashboard.sh ) || \
      echo "UYARI: dashboard derlenemedi; yer tutucu sayfayla devam ediliyor."
  else
    echo "UYARI: npm yok, dashboard derlenemiyor. Yer tutucu sayfa gosterilecek."
    echo "       Dashboard icin Node.js kurup: ./scripts/build-dashboard.sh"
  fi
fi

# 4. Sunucu
echo "Sunucu baslatiliyor: https://${ADDR}"
echo "  (Ctrl+C ile durdur)"
echo
DB_DSN="${ZORVEN_DB_DSN:-postgres://rpshell:rpshell@localhost:5432/rpshell}"
AUTH_UPSTREAM="${ZORVEN_AUTH_UPSTREAM:-http://localhost:3000}"
exec go run . --db-dsn "$DB_DSN" serve \
  --addr "$ADDR" \
  --control-host "$HOSTS" \
  --tls-cert certs/server.crt \
  --tls-key certs/server.key \
  --auth-upstream "$AUTH_UPSTREAM"
