#!/usr/bin/env bash
#
# Zorven veritabani yedekleme.
#
# postgres konteynerindeki her DB'yi (rpshell + umami) pg_dump ile alir,
# gzip'ler ve retention gunu asilan eski yedekleri siler. Cron ile gunluk
# calistirilmasi onerilir (kurulum: `deploy/backup.sh --install-cron`).
#
# Ortam degiskenleri (opsiyonel):
#   COMPOSE_DIR    (vars: /opt/zorven/deploy)
#   BACKUP_DIR     (vars: /opt/zorven/backups)
#   RETENTION_DAYS (vars: 14)
#   DBS            (vars: "rpshell umami")
#   PGUSER_DUMP    (vars: rpshell)  — postgres superuser (local trust)
set -euo pipefail

COMPOSE_DIR="${COMPOSE_DIR:-/opt/zorven/deploy}"
BACKUP_DIR="${BACKUP_DIR:-/opt/zorven/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
DBS="${DBS:-rpshell umami}"
PGUSER_DUMP="${PGUSER_DUMP:-rpshell}"

# --install-cron: bu scripti her gun 03:30'da calistiracak bir cron satiri ekler.
if [ "${1:-}" = "--install-cron" ]; then
  SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  LINE="30 3 * * * $SELF >> /var/log/zorven-backup.log 2>&1"
  ( crontab -l 2>/dev/null | grep -vF "$SELF" ; echo "$LINE" ) | crontab -
  echo "Cron kuruldu: $LINE"
  exit 0
fi

TS="$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"
cd "$COMPOSE_DIR"

rc=0
for db in $DBS; do
  out="$BACKUP_DIR/zorven-${db}-${TS}.sql.gz"
  if docker compose --env-file .env exec -T postgres pg_dump -U "$PGUSER_DUMP" -d "$db" 2>/dev/null | gzip > "$out"; then
    # Bos/bozuk cikti kontrolu (en az birkac yuz bayt olmali).
    if [ "$(stat -c%s "$out" 2>/dev/null || echo 0)" -lt 200 ]; then
      echo "HATA: $db yedegi cok kucuk/bos, siliniyor" >&2
      rm -f "$out"; rc=1
    else
      echo "OK   $out ($(du -h "$out" | cut -f1))"
    fi
  else
    echo "HATA: $db yedeklenemedi" >&2
    rm -f "$out"; rc=1
  fi
done

# Retention: RETENTION_DAYS'ten eski yedekleri sil.
deleted=$(find "$BACKUP_DIR" -name 'zorven-*.sql.gz' -type f -mtime +"$RETENTION_DAYS" -print -delete | wc -l)
echo "Retention: $deleted eski yedek silindi (>$RETENTION_DAYS gun)."
echo "Toplam yedek: $(ls -1 "$BACKUP_DIR"/zorven-*.sql.gz 2>/dev/null | wc -l) dosya, $(du -sh "$BACKUP_DIR" 2>/dev/null | cut -f1)."
exit $rc
