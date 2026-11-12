#!/usr/bin/env sh
# Ulasilabilir zafiyet taramasi (server + client + shared + desktop). CI (security.yml) ile ayni
# kapi: yalniz bilinen-istisna listesinde (scripts/govulncheck-gate.py) olmayan CAGRILAN bulgu
# cikis kodunu != 0 yapar.
set -eu
cd "$(dirname "$0")/.."
command -v govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
PATH="$PATH:$(go env GOPATH)/bin"
rc=0
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
for m in server client shared desktop; do
  echo "== $m"
  if (cd "$m" && govulncheck -format json ./... >"$tmp"); then
    python3 scripts/govulncheck-gate.py "$m" <"$tmp" || rc=1
  else
    echo "$m: govulncheck calismadi" >&2
    rc=1
  fi
done
exit $rc
