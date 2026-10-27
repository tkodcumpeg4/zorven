#!/bin/sh
# DB-IP Lite Country (CC BY 4.0) ulke veritabanini gunceller (VDS'te, root).
# Kaynak: https://db-ip.com/db/download/ip-to-country-lite  — atif: "IP Geolocation by DB-IP".
# Ayda bir yeterli; dosya atomik degistirilir, sunucu en gec 30 sn icinde yeniden yukler.
set -eu
DEST=${ZORVEN_GEO_DIR:-/opt/zorven/data}/GeoLite2-Country.mmdb
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
for M in "$(date -u +%Y-%m)" "$(date -u -d '-1 month' +%Y-%m)"; do
  if curl -fsS --max-time 300 -o "$TMP/c.mmdb.gz" "https://download.db-ip.com/free/dbip-country-lite-$M.mmdb.gz"; then
    break
  fi
done
gzip -t "$TMP/c.mmdb.gz"
gunzip "$TMP/c.mmdb.gz"
# Gecerli MMDB: dosya sonundaki meta veri isareti olmali ve boyut makul.
tail -c 200000 "$TMP/c.mmdb" | grep -q "MaxMind.com"
[ "$(wc -c < "$TMP/c.mmdb")" -gt 1000000 ]
install -m 644 "$TMP/c.mmdb" "$DEST.new"
mv "$DEST.new" "$DEST"
echo "guncellendi: $DEST ($M)"
