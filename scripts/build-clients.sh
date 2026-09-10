#!/usr/bin/env bash
#
# rpshell-client'i tum hedef platformlar icin derler.
#
# Go'nun cross-compile'i sayesinde tek makineden hepsi uretilir; cgo
# KULLANILMADIGI icin harici derleyici gerekmez (modernc.org/sqlite yalnizca
# sunucuda kullaniliyor, istemcide veritabani yok).
#
# Kullanim:  ./scripts/build-clients.sh [surum]
set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist"

# GOOS/GOARCH/uzanti/insan-okunur-ad
TARGETS=(
  "windows/amd64/.exe/Windows (Intel/AMD 64-bit)"
  "windows/arm64/.exe/Windows (ARM64)"
  "darwin/arm64//macOS (Apple Silicon)"
  "darwin/amd64//macOS (Intel)"
  "linux/amd64//Linux (x86_64)"
  "linux/arm64//Linux (ARM64)"
  "linux/arm//Linux ARMv7 (Raspberry Pi 32-bit)"
)

rm -rf "$OUT"
mkdir -p "$OUT"

echo "rpshell-client derleniyor (surum: $VERSION)"
echo

for target in "${TARGETS[@]}"; do
  IFS='/' read -r goos goarch ext label <<< "$target"

  name="rpshell-client-${goos}-${goarch}${ext}"
  printf '  %-34s ' "$label"

  env GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -C "$ROOT/client" \
      -trimpath \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o "$OUT/$name" .

  size=$(du -h "$OUT/$name" | cut -f1 | tr -d ' ')
  printf '%-42s %s\n' "$name" "$size"
done

echo
echo "Kontrol toplamlari yaziliyor..."
(cd "$OUT" && sha256sum ./* > SHA256SUMS 2>/dev/null || shasum -a 256 ./* > SHA256SUMS)
# SHA256SUMS kendini listelemesin
(cd "$OUT" && grep -v 'SHA256SUMS' SHA256SUMS > .tmp && mv .tmp SHA256SUMS)

echo
echo "Tamamlandi -> $OUT"
ls -1 "$OUT"
