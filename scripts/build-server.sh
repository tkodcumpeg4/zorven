#!/usr/bin/env bash
#
# zorven-server ikilisini dagitim icin derler ve derleme surumunu gomer.
#
# Neden: dagitim komutu elle yaziliyordu (CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w")
# ve ikili hangi commit'ten geldigini soyleyemiyordu; zorven_build_info, /api/v1/health ve
# baslangic logu protokol surumunu (her derlemede ayni) gosteriyordu.
#
# Surum: ZORVEN_BUILD_VERSION verilmisse o; yoksa "v*" etiketinden git describe (yoksa kisa
# commit), kirli agacta "-dirty", sonuna UTC derleme tarihi: or. v0.3.8-4-gabc1234+20261111
# ya da abc1234-dirty+20261111.
#
# Kullanim: scripts/build-server.sh [cikti-yolu]       (varsayilan: dist/zorven-server)
# Ortam:    GOOS (vars. linux), GOARCH (vars. amd64), ZORVEN_BUILD_VERSION
# Ornek:    scripts/build-server.sh /tmp/zorven-server && /tmp/zorven-server --version
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/dist/zorven-server}"
case "$OUT" in /*) ;; *) OUT="$PWD/$OUT" ;; esac

version() {
  if [ -n "${ZORVEN_BUILD_VERSION:-}" ]; then
    printf '%s' "$ZORVEN_BUILD_VERSION"
    return
  fi
  local d
  d="$(git -C "$ROOT" describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || true)"
  [ -n "$d" ] || d="dev"
  printf '%s+%s' "$d" "$(date -u +%Y%m%d)"
}
VERSION="$(version)"
# -X degeri bosluk/tirnak icermemeli (ldflags tek arguman).
case "$VERSION" in *[[:space:]\'\"]*) echo "HATA: gecersiz surum: '$VERSION'" >&2; exit 1 ;; esac

mkdir -p "$(dirname "$OUT")"
echo "zorven-server $VERSION -> $OUT (${GOOS:-linux}/${GOARCH:-amd64})"
(cd "$ROOT/server" && CGO_ENABLED=0 GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}" \
  go build -trimpath -ldflags="-s -w -X main.buildVersion=$VERSION" -o "$OUT" .)
ls -l "$OUT"
