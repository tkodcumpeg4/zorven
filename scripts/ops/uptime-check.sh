#!/usr/bin/env bash
#
# Dis erisilebilirlik kontrolu: sunucunun DISINDAKI bir makineden (or. gelistirme bilgisayari ya da
# ikinci VDS) cron ile calistirilir. Sunucunun kendisi coktugunde kendi uyarisini gonderemez; bu
# betik o durumu yakalar. Bagimliliksiz: bash, curl, python3, (sertifika icin) openssl.
#
# Kontroller:
#   readyz    GET <readyz-url> == 200 (vars. <base>/readyz; kontrol hostunda kimliksiz)
#   home      GET <base>/ == 200 (yonlendirmeler izlenir) ve govde bos degil
#   panel     GET <panel-url> == 200 (yonlendirmeler izlenir; verilmezse atlanir)
#   manifest  GET <base>/bin/manifest.json == 200, JSON ve "version" alani dolu; .sig == 200
#   cert      <base> host'unun TLS sertifikasi en az --cert-days (vars. 14) gun gecerli
# Uyarilar stdout + syslog (logger) + durum dosyasi; --mail-cmd ile dis komut (konu
# ZV_ALERT_SUBJECT ortaminda, govde stdin'de). Ayni uyari --repeat-min (vars. 30) dakikada bir;
# duzelince bir kez COZULDU. Tek bir anlik hata uyari uretmesin diye bir kontrol --fail-count
# (vars. 2) ardisik calismada basarisizsa uyarir.
#
# Kullanim:
#   scripts/ops/uptime-check.sh --base https://example.com --panel https://panel.example.com \
#       [--readyz-url URL] [--state-dir DIZIN] [--mail-cmd 'KOMUT'] [--timeout 10] [--no-syslog]
#       [--insecure]   (yalniz yerel deneme: sertifika dogrulamasi kapali)
# Cikis kodu: 0 = tum kontroller tamam, 1 = en az bir etkin uyari, 2 = kullanim hatasi.
set -uo pipefail

BASE=""; PANEL=""; READYZ=""; STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/zorven-uptime"
MAIL_CMD=""; REPEAT_MIN=30; TIMEOUT=10; SYSLOG=1; CERT_DAYS=14; FAIL_COUNT=2; INSECURE=0; RESOLVE=()

usage() { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="${2%/}"; shift 2 ;;
    --panel) PANEL="$2"; shift 2 ;;
    --readyz-url) READYZ="$2"; shift 2 ;;
    --state-dir) STATE_DIR="$2"; shift 2 ;;
    --mail-cmd) MAIL_CMD="$2"; shift 2 ;;
    --repeat-min) REPEAT_MIN="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --cert-days) CERT_DAYS="$2"; shift 2 ;;
    --fail-count) FAIL_COUNT="$2"; shift 2 ;;
    --resolve) RESOLVE+=(--resolve "$2"); shift 2 ;;
    --insecure) INSECURE=1; shift ;;
    --no-syslog) SYSLOG=0; shift ;;
    -h|--help) usage ;;
    *) echo "bilinmeyen secenek: $1" >&2; usage ;;
  esac
done
[ -n "$BASE" ] || { echo "--base gerekli" >&2; usage; }
for c in curl python3; do command -v $c >/dev/null || { echo "$c yok" >&2; exit 2; }; done
READYZ="${READYZ:-$BASE/readyz}"
mkdir -p "$STATE_DIR" || exit 2

CURL=(curl -s -m "$TIMEOUT" "${RESOLVE[@]}")
[ "$INSECURE" = 1 ] && CURL+=(-k)
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
: >"$TMP/res"
res() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >>"$TMP/res"; } # ad, ok|fail, ayrinti

code="$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$READYZ")"
[ "$code" = 200 ] && res readyz ok "$code" || res readyz fail "/readyz $code"

code="$("${CURL[@]}" -L --max-redirs 5 -o "$TMP/home" -w '%{http_code}' "$BASE/")"
if [ "$code" = 200 ] && [ -s "$TMP/home" ]; then res home ok "$code"; else res home fail "ana sayfa $code"; fi

if [ -n "$PANEL" ]; then
  code="$("${CURL[@]}" -L --max-redirs 5 -o /dev/null -w '%{http_code}' "$PANEL")"
  [ "$code" = 200 ] && res panel ok "$code" || res panel fail "panel $code"
fi

code="$("${CURL[@]}" -o "$TMP/manifest" -w '%{http_code}' "$BASE/bin/manifest.json")"
sig="$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$BASE/bin/manifest.json.sig")"
ver="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('version',''))" "$TMP/manifest" 2>/dev/null)"
if [ "$code" = 200 ] && [ -n "$ver" ] && [ "$sig" = 200 ]; then
  res manifest ok "$ver"
else
  res manifest fail "manifest $code surum='${ver}' imza $sig"
fi

if command -v openssl >/dev/null; then
  hp="$(printf '%s' "$BASE" | sed -E 's#^https://##; s#/.*##')"
  host="${hp%%:*}"; port="${hp#*:}"; [ "$port" = "$hp" ] && port=443
  end="$(echo | timeout "$TIMEOUT" openssl s_client -connect "$host:$port" -servername "$host" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)"
  if [ -n "$end" ]; then
    left=$(( ($(date -d "$end" +%s) - $(date +%s)) / 86400 ))
    [ "$left" -ge "$CERT_DAYS" ] && res cert ok "$left gun" || res cert fail "TLS sertifikasi $left gun kaldi (< $CERT_DAYS)"
  else
    res cert fail "TLS sertifikasi okunamadi"
  fi
fi

python3 - "$TMP/res" "$STATE_DIR" "$REPEAT_MIN" "$FAIL_COUNT" "$BASE" >"$TMP/out" <<'PY'
import json, os, sys, time
res_path, sd, repeat, need, base = sys.argv[1], sys.argv[2], float(sys.argv[3]) * 60, int(sys.argv[4]), sys.argv[5]
now = time.time()
sp = os.path.join(sd, "state.json")
try:
    st = json.load(open(sp))
except (OSError, ValueError):
    st = {}
fails, sent, active = st.get("fails", {}), st.get("sent", {}), set(st.get("active", []))
checks, alerts = {}, {}
for line in open(res_path):
    name, status, detail = line.rstrip("\n").split("\t", 2)
    checks[name] = {"ok": status == "ok", "detail": detail}
    fails[name] = 0 if status == "ok" else fails.get(name, 0) + 1
    if fails[name] >= need:
        alerts[name] = "%s (%d ardisik)" % (detail, fails[name])
out = []
for k, msg in sorted(alerts.items()):
    if now - sent.get(k, 0) >= repeat:
        out.append(("UYARI", k, msg))
        sent[k] = now
for k in sorted(active - set(alerts)):
    out.append(("COZULDU", k, "duzeldi"))
    sent.pop(k, None)
json.dump({"fails": fails, "sent": sent, "active": sorted(alerts)}, open(sp + ".tmp", "w"))
os.replace(sp + ".tmp", sp)
stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now))
json.dump({"checked_at": stamp, "base": base, "ok": not alerts, "checks": checks, "alerts": alerts},
          open(os.path.join(sd, "status.json.tmp"), "w"), indent=1)
os.replace(os.path.join(sd, "status.json.tmp"), os.path.join(sd, "status.json"))
for kind, k, msg in out:
    print("%s\t%s\t%s" % (kind, k, msg))
print("OZET\t-\t" + " ".join("%s=%s" % (n, "ok" if c["ok"] else "HATA") for n, c in checks.items()))
sys.exit(1 if alerts else 0)
PY
RC=$?
[ "$RC" -le 1 ] || { echo "analiz hatasi" >&2; exit 2; }
HOSTN="$(hostname 2>/dev/null || echo host)"
while IFS=$'\t' read -r kind key msg; do
  [ -n "$kind" ] || continue
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [ "$kind" = OZET ]; then echo "$ts zorven-uptime $msg"; continue; fi
  line="$kind [$key] $BASE: $msg"
  echo "$ts zorven-uptime $line"
  if [ "$SYSLOG" = 1 ] && command -v logger >/dev/null; then
    logger -t zorven-uptime -p "user.$([ "$kind" = UYARI ] && echo warning || echo notice)" -- "$line"
  fi
  if [ -n "$MAIL_CMD" ]; then
    printf '%s\n\nKontrol eden: %s\nZaman: %s\n' "$line" "$HOSTN" "$ts" |
      ZV_ALERT_SUBJECT="[zorven-uptime] $kind: $key" ZV_ALERT_KEY="$key" ZV_ALERT_KIND="$kind" sh -c "$MAIL_CMD" ||
      echo "$ts zorven-uptime --mail-cmd basarisiz ($key)" >&2
  fi
done <"$TMP/out"
exit "$RC"
