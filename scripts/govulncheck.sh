#!/usr/bin/env sh
# Ulasilabilir zafiyet taramasi (server + client + shared). Bulgu varsa cikis kodu != 0.
set -eu
cd "$(dirname "$0")/.."
command -v govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
PATH="$PATH:$(go env GOPATH)/bin"
rc=0
for m in server client shared; do
  echo "== $m"
  (cd "$m" && govulncheck ./...) || rc=1
done
exit $rc
