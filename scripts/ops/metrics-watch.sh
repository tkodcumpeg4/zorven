#!/usr/bin/env bash
#
# Hafif metrik izleme (Prometheus YOK): zorven-server /metrics ve /readyz'i okur, secili serileri
# zaman damgali JSON satirlari olarak gunluk dosyalara yazar (varsayilan 7 gun, dosya basina boyut
# siniri), esik asiminda uyarir. Cron/systemd-timer ile dakikada bir calistirilmak icindir; tek
# calisma birkac saniye surer (argon2 doygunlugunda 30 sn tekrar olcum yapar).
#
# Neden boyle: 2.9 GB bellekli tek dugumde ayri bir Prometheus konteyneri agir; sunucuda e-posta
# gonderen kimlikli bir "ops uyarisi" ucu da yok ve eklenmedi (yeni saldiri yuzeyi). Uyarilar
# stdout + syslog (logger) + durum dosyasina yazilir; e-posta icin --mail-cmd ile dis komut (msmtp,
# sendmail, mail) cagrilir. Ayni uyari --repeat-min (vars. 30) dakikada bir tekrarlanir; duzelince
# bir kez "COZULDU" yazilir.
#
# Kullanim:
#   scripts/ops/metrics-watch.sh --compose-service zorven-primary [--token-file /root/.zorven-metrics-token]
#   scripts/ops/metrics-watch.sh --url http://127.0.0.1:9102            (yerel/test: konteynersiz)
# Secenekler:
#   --container AD        metrikler `docker exec AD curl ...` ile konteyner icinden (127.0.0.1:9102)
#   --compose-service AD  konteyneri compose servis etiketinden bul (or. zorven-primary; konteyner adi
#                         "<proje>-zorven-primary-1" bicimindedir ve compose proje adina gore degisir)
#   --metrics-addr H:P    konteyner icindeki metrik adresi (vars. 127.0.0.1:9102)
#   --url URL             dogrudan curl ile (konteyner yok); /metrics ve /readyz bu kokten
#   --token-file DOSYA    Bearer token dosyasi (ya da ZORVEN_METRICS_TOKEN ortam degiskeni); opsiyonel
#   --state-dir DIZIN     veri + durum dizini (vars. /var/lib/zorven-metrics-watch)
#   --keep-days N         veri dosyasi saklama (vars. 7)    --max-file-mb N  gunluk dosya tavani (vars. 20)
#   --disk-path YOL       doluluk olculecek yol (vars. /), birden cok kez verilebilir
#   --mail-cmd 'KOMUT'    uyari basina `sh -c KOMUT` (konu ZV_ALERT_SUBJECT ortaminda, govde stdin'de)
#   --repeat-min N        ayni uyarinin tekrar suresi (vars. 30)
#   --no-syslog           logger cagirma          --quiet  yalniz uyarilari yaz
#   --set ESIK=DEGER      esik degistir (asagidaki adlar), birden cok kez
# Esikler (docs/operasyon-gozlemlenebilirlik.md §5 ile ayni):
#   mem_bytes=1395864371 (1.3 GiB)  argon_hold_sec=30  auth_fail_per_sec=5  api_5xx_ratio=0.01
#   api_min_requests=20  db_empty_per_sec=1  router_reload_sec=2  disk_pct=85
#   panics / reqlog_dropped / restart_count: her artis uyari
# Cikis kodu: 0 = uyari yok, 1 = en az bir etkin uyari, 2 = kullanim/olcum hatasi.
set -uo pipefail

CONTAINER=""; COMPOSE_SVC=""; METRICS_ADDR="127.0.0.1:9102"; URL=""; TOKEN_FILE=""
STATE_DIR="/var/lib/zorven-metrics-watch"; KEEP_DAYS=7; MAX_FILE_MB=20
DISK_PATHS=(); MAIL_CMD=""; REPEAT_MIN=30; SYSLOG=1; QUIET=0; SETS=()

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --container) CONTAINER="$2"; shift 2 ;;
    --compose-service) COMPOSE_SVC="$2"; shift 2 ;;
    --metrics-addr) METRICS_ADDR="$2"; shift 2 ;;
    --url) URL="${2%/}"; shift 2 ;;
    --token-file) TOKEN_FILE="$2"; shift 2 ;;
    --state-dir) STATE_DIR="$2"; shift 2 ;;
    --keep-days) KEEP_DAYS="$2"; shift 2 ;;
    --max-file-mb) MAX_FILE_MB="$2"; shift 2 ;;
    --disk-path) DISK_PATHS+=("$2"); shift 2 ;;
    --mail-cmd) MAIL_CMD="$2"; shift 2 ;;
    --repeat-min) REPEAT_MIN="$2"; shift 2 ;;
    --no-syslog) SYSLOG=0; shift ;;
    --quiet) QUIET=1; shift ;;
    --set) SETS+=("$2"); shift 2 ;;
    -h|--help) usage ;;
    *) echo "bilinmeyen secenek: $1" >&2; usage ;;
  esac
done
if [ -n "$COMPOSE_SVC" ]; then
  CONTAINER="$(docker ps -aq --filter "label=com.docker.compose.service=$COMPOSE_SVC" | head -1)"
  # Bulunamazsa olcum basarisiz olur: "metrik ucu okunamadi" + saglik "missing" uyarisi.
  [ -n "$CONTAINER" ] || CONTAINER="zorven-yok-$COMPOSE_SVC"
fi
[ -n "$CONTAINER" ] || [ -n "$URL" ] || { echo "--container, --compose-service ya da --url gerekli" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 yok" >&2; exit 2; }
[ ${#DISK_PATHS[@]} -gt 0 ] || DISK_PATHS=(/)
mkdir -p "$STATE_DIR" || exit 2
chmod 700 "$STATE_DIR" 2>/dev/null

TOKEN="${ZORVEN_METRICS_TOKEN:-}"
if [ -n "$TOKEN_FILE" ]; then
  TOKEN="$(tr -d ' \r\n' <"$TOKEN_FILE")" || { echo "token dosyasi okunamadi" >&2; exit 2; }
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
# Token'i komut satirina (ps) koymamak icin curl'e basligi dosyadan/stdin'den veriyoruz.
fetch() { # $1 = yol; govde stdout'a, son satir HTTP kodu
  local path=$1
  if [ -n "$CONTAINER" ]; then
    if [ -n "$TOKEN" ]; then
      printf 'Authorization: Bearer %s\n' "$TOKEN" | docker exec -i "$CONTAINER" \
        curl -s -m 10 -H @- -w '\n%{http_code}' "http://$METRICS_ADDR$path"
    else
      docker exec "$CONTAINER" curl -s -m 10 -w '\n%{http_code}' "http://$METRICS_ADDR$path"
    fi
  else
    if [ -n "$TOKEN" ]; then
      printf 'Authorization: Bearer %s\n' "$TOKEN" | curl -sk -m 10 -H @- -w '\n%{http_code}' "$URL$path"
    else
      curl -sk -m 10 -w '\n%{http_code}' "$URL$path"
    fi
  fi
}

sample() { # $1 = hedef dosya oneki
  fetch /metrics >"$1.metrics" 2>/dev/null || true
  fetch /readyz >"$1.readyz" 2>/dev/null || true
}
sample "$TMP/s1"

# Konteyner sagligi ve yeniden baslatma sayisi (yalniz --container ile).
HEALTH=""; RESTARTS=""
if [ -n "$CONTAINER" ]; then
  # Konteyner yoksa docker inspect bos satir basar: "missing" say (uyari uretir).
  read -r HEALTH RESTARTS < <(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}} {{.RestartCount}}' "$CONTAINER" 2>/dev/null | head -1)
  [ -n "$HEALTH" ] || { HEALTH="missing"; RESTARTS=""; }
fi
: >"$TMP/disk"
for p in "${DISK_PATHS[@]}"; do
  df -P "$p" 2>/dev/null | awk -v p="$p" 'NR==2 {gsub("%","",$5); print p "\t" $5}' >>"$TMP/disk"
done

# argon2 kapisi doluysa argon_hold_sec sonra tekrar olc (30 sn+ doygunluk kurali).
ARGON_HOLD="$(printf '%s\n' "${SETS[@]:-}" | sed -n 's/^argon_hold_sec=//p' | tail -1)"
ARGON_HOLD="${ARGON_HOLD:-30}"
if python3 - "$TMP/s1.metrics" <<'PY'
import re, sys
v = {}
for line in open(sys.argv[1], errors="replace"):
    m = re.match(r"^(zorven_argon2_(?:inflight|capacity))(?:\{[^}]*\})?\s+(\S+)", line)
    if m:
        v[m.group(1)] = float(m.group(2))
cap = v.get("zorven_argon2_capacity", 0)
sys.exit(0 if cap > 0 and v.get("zorven_argon2_inflight", 0) >= cap else 1)
PY
then
  sleep "$ARGON_HOLD"
  sample "$TMP/s2"
fi

export ZV_STATE_DIR="$STATE_DIR" ZV_KEEP_DAYS="$KEEP_DAYS" ZV_MAX_FILE_MB="$MAX_FILE_MB" ZV_REPEAT_MIN="$REPEAT_MIN"
export ZV_HEALTH="$HEALTH" ZV_RESTARTS="$RESTARTS" ZV_TMP="$TMP" ZV_SOURCE="${CONTAINER:-$URL}"
export ZV_SETS="$(printf '%s\n' "${SETS[@]:-}")" ZV_QUIET="$QUIET"
python3 - >"$TMP/out" <<'PY'
import glob, json, os, re, sys, time, traceback


def _hook(t, v, tb):
    # Beklenmeyen hata: cikis 3 (1 = "uyari var" ile karismasin).
    traceback.print_exception(t, v, tb)
    os._exit(3)


sys.excepthook = _hook
T = os.environ["ZV_TMP"]
SD = os.environ["ZV_STATE_DIR"]
now = time.time()
TH = {"mem_bytes": 1.3 * 1024 ** 3, "argon_hold_sec": 30, "auth_fail_per_sec": 5.0, "api_5xx_ratio": 0.01,
      "api_min_requests": 20, "db_empty_per_sec": 1.0, "router_reload_sec": 2.0, "disk_pct": 85}
for kv in os.environ.get("ZV_SETS", "").split("\n"):
    if "=" in kv:
        k, v = kv.split("=", 1)
        if k.strip() not in TH:
            print("UYARI-KULLANIM bilinmeyen esik: " + k, file=sys.stderr)
            continue
        TH[k.strip()] = float(v)


def parse(path):
    """Prometheus metin bicimi -> {(ad, etiketler): deger}; son satir HTTP kodu."""
    try:
        lines = open(path, errors="replace").read().rstrip("\n").split("\n")
    except OSError:
        return None, 0
    code = int(lines[-1]) if lines and lines[-1].isdigit() else 0
    out = {}
    for line in lines[:-1]:
        if not line or line.startswith("#"):
            continue
        m = re.match(r"^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?\s+(\S+)", line)
        if not m:
            continue
        try:
            out[(m.group(1), m.group(2) or "")] = float(m.group(3))
        except ValueError:
            pass
    return out, code


def readyz(path):
    try:
        lines = open(path, errors="replace").read().rstrip("\n").split("\n")
        return int(lines[-1]) if lines[-1].isdigit() else 0
    except OSError:
        return 0


m1, code = parse(os.path.join(T, "s1.metrics"))
rz = readyz(os.path.join(T, "s1.readyz"))
m2 = None
if os.path.exists(os.path.join(T, "s2.metrics")):
    m2, _ = parse(os.path.join(T, "s2.metrics"))
    rz = readyz(os.path.join(T, "s2.readyz")) or rz


def total(m, name, match=None):
    s = 0.0
    for (n, lab), v in (m or {}).items():
        if n == name and (match is None or match in lab):
            s += v
    return s


def one(m, name):
    for (n, _), v in (m or {}).items():
        if n == name:
            return v
    return None


cur = {}
if m1 and code == 200:
    cur = {
        "rss": one(m1, "process_resident_memory_bytes"),
        "argon_inflight": one(m1, "zorven_argon2_inflight"),
        "argon_capacity": one(m1, "zorven_argon2_capacity"),
        "auth_failures": total(m1, "zorven_auth_failures_total"),
        "api_total": total(m1, "zorven_api_requests_total"),
        "api_5xx": total(m1, "zorven_api_requests_total", 'code_class="5xx"'),
        "panics": total(m1, "zorven_panics_total"),
        "reqlog_dropped": total(m1, "zorven_reqlog_persist_dropped_total"),
        "db_empty": total(m1, "zorven_db_pool_empty_acquires_total"),
        "router_reload_sec": one(m1, "zorven_router_reload_last_duration_seconds"),
        "goroutines": one(m1, "go_goroutines"),
        "open_fds": one(m1, "process_open_fds"),
        "connected_clients": one(m1, "zorven_connected_clients"),
        "errors_logged": total(m1, "zorven_log_messages_total", 'level="error"'),
    }
cur["readyz"] = rz
cur["metrics_http"] = code
disks = {}
for line in open(os.path.join(T, "disk")):
    p, pct = line.rstrip("\n").split("\t")
    disks[p] = float(pct)
cur["disk_pct"] = disks
if os.environ.get("ZV_HEALTH"):
    cur["health"] = os.environ["ZV_HEALTH"]
    cur["restarts"] = int(os.environ.get("ZV_RESTARTS") or -1)

state_path = os.path.join(SD, "state.json")
try:
    state = json.load(open(state_path))
except (OSError, ValueError):
    state = {}
prev, prev_ts = state.get("last") or {}, state.get("last_ts")
dt = (now - prev_ts) if prev_ts else None


def inc(key):
    """Sayac artisi; sayac geri gitti (surec yeniden basladi) ise mevcut deger artis sayilir."""
    if key not in cur or cur[key] is None or prev.get(key) is None:
        return None
    d = cur[key] - prev[key]
    return cur[key] if d < 0 else d


alerts = {}  # anahtar -> mesaj
if code != 200 or not m1:
    alerts["metrics_unreachable"] = "metrik ucu okunamadi (HTTP %s)" % code
else:
    if cur["rss"] is not None and cur["rss"] > TH["mem_bytes"]:
        alerts["mem"] = "RSS %.0f MiB > %.0f MiB" % (cur["rss"] / 2 ** 20, TH["mem_bytes"] / 2 ** 20)
    cap = cur.get("argon_capacity") or 0
    if m2 is not None and cap > 0:
        if (one(m2, "zorven_argon2_inflight") or 0) >= (one(m2, "zorven_argon2_capacity") or cap):
            alerts["argon2_saturated"] = "argon2 kapisi %d sn+ dolu (inflight >= capacity=%d)" % (TH["argon_hold_sec"], cap)
    if dt and dt > 0:
        d = inc("auth_failures")
        if d is not None and d / dt > TH["auth_fail_per_sec"]:
            alerts["auth_failures"] = "basarisiz kimlik %.1f/sn > %.1f/sn (%d yeni)" % (d / dt, TH["auth_fail_per_sec"], d)
        dt_all, d5 = inc("api_total"), inc("api_5xx")
        if dt_all and d5 is not None and dt_all >= TH["api_min_requests"] and d5 / dt_all > TH["api_5xx_ratio"]:
            alerts["api_5xx"] = "API 5xx orani %%%.1f (%d/%d) > %%%.1f" % (100 * d5 / dt_all, d5, dt_all, 100 * TH["api_5xx_ratio"])
        d = inc("db_empty")
        if d is not None and d / dt > TH["db_empty_per_sec"]:
            alerts["db_pool_empty"] = "DB havuzu bos alim %.1f/sn > %.1f/sn" % (d / dt, TH["db_empty_per_sec"])
    for key, label in (("panics", "panik"), ("reqlog_dropped", "istek logu kaybi")):
        d = inc(key)
        if d is None and not prev and (cur.get(key) or 0) > 0:
            d = cur[key]  # ilk calisma: sayac zaten > 0
        if d:
            alerts[key] = "%s: %d yeni (toplam %d)" % (label, d, cur[key])
    rr = cur.get("router_reload_sec")
    if rr is not None and rr > TH["router_reload_sec"]:
        alerts["router_reload"] = "yonlendirme yenileme %.2f sn > %.1f sn" % (rr, TH["router_reload_sec"])
if rz != 200:
    alerts["readyz"] = "/readyz %s (beklenen 200)" % (rz or "yanit yok")
for p, pct in disks.items():
    if pct > TH["disk_pct"]:
        alerts["disk:" + p] = "disk %s %%%d dolu > %%%d" % (p, pct, TH["disk_pct"])
if "health" in cur:
    if cur["health"] != "healthy":
        alerts["health"] = "konteyner sagligi '%s' (beklenen healthy)" % cur["health"]
    # -1 = konteyner yoktu; yeniden ortaya cikmasi "yeniden baslatma" sayilmaz.
    p_r = prev.get("restarts")
    d = inc("restarts") if cur["restarts"] >= 0 and p_r is not None and p_r >= 0 else None
    if d:
        alerts["restarts"] = "konteyner %d kez yeniden basladi (RestartCount %d)" % (d, cur["restarts"])

# Bastirma: ayni uyari repeat_min'de bir; duzelen uyari bir kez COZULDU.
repeat = float(os.environ.get("ZV_REPEAT_MIN") or 30) * 60
sent = state.get("sent") or {}
active_prev = set(state.get("active") or [])
emit = []
for k, msg in sorted(alerts.items()):
    if now - sent.get(k, 0) >= repeat:
        emit.append(("UYARI", k, msg))
        sent[k] = now
for k in sorted(active_prev - set(alerts)):
    emit.append(("COZULDU", k, "duzeldi"))
    sent.pop(k, None)

# Veri dosyasi: gunluk JSON satiri, boyut siniri, eski gunleri sil.
day = time.strftime("%Y%m%d", time.gmtime(now))
data_path = os.path.join(SD, "metrics-%s.jsonl" % day)
limit = float(os.environ.get("ZV_MAX_FILE_MB") or 20) * 2 ** 20
rec = {"ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now)), "v": cur, "alerts": sorted(alerts)}
if not os.path.exists(data_path) or os.path.getsize(data_path) < limit:
    with open(data_path, "a") as f:
        f.write(json.dumps(rec, separators=(",", ":")) + "\n")
keep = float(os.environ.get("ZV_KEEP_DAYS") or 7)
for p in glob.glob(os.path.join(SD, "metrics-*.jsonl")):
    if now - os.path.getmtime(p) > keep * 86400:
        os.remove(p)

state = {"last": cur, "last_ts": now, "sent": sent, "active": sorted(alerts)}
tmp = state_path + ".tmp"
json.dump(state, open(tmp, "w"))
os.replace(tmp, state_path)
status = {"checked_at": rec["ts"], "source": os.environ.get("ZV_SOURCE"), "ok": not alerts,
          "alerts": [{"key": k, "message": v, "since_last_sent_sec": int(now - sent.get(k, now))} for k, v in sorted(alerts.items())],
          "values": cur}
tmp = os.path.join(SD, "status.json.tmp")
json.dump(status, open(tmp, "w"), indent=1)
os.replace(tmp, os.path.join(SD, "status.json"))

for kind, k, msg in emit:
    print("%s\t%s\t%s" % (kind, k, msg))
if os.environ.get("ZV_QUIET") != "1":
    print("OZET\t-\tuyari=%d rss=%s readyz=%s 5xx=%s panik=%s" % (
        len(alerts), "%.0fMiB" % (cur["rss"] / 2 ** 20) if cur.get("rss") else "?", rz,
        cur.get("api_5xx", "?"), cur.get("panics", "?")))
sys.exit(1 if alerts else 0)
PY
RC=$?
[ "$RC" -le 1 ] || { cat "$TMP/out"; echo "analiz hatasi (rc=$RC)" >&2; exit 2; }

HOSTN="$(hostname 2>/dev/null || echo host)"
while IFS=$'\t' read -r kind key msg; do
  [ -n "$kind" ] || continue
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [ "$kind" = "OZET" ]; then
    echo "$ts zorven-metrics-watch $msg"
    continue
  fi
  line="$kind [$key] $msg"
  echo "$ts zorven-metrics-watch $line"
  if [ "$SYSLOG" = 1 ] && command -v logger >/dev/null; then
    if [ "$kind" = "UYARI" ]; then logger -t zorven-metrics-watch -p user.warning -- "$line"; else logger -t zorven-metrics-watch -p user.notice -- "$line"; fi
  fi
  if [ -n "$MAIL_CMD" ]; then
    printf '%s\n\nKaynak: %s\nZaman: %s\nDurum dosyasi: %s/status.json\n' "$line" "${CONTAINER:-$URL}" "$ts" "$STATE_DIR" |
      ZV_ALERT_SUBJECT="[zorven $HOSTN] $kind: $key" ZV_ALERT_KEY="$key" ZV_ALERT_KIND="$kind" sh -c "$MAIL_CMD" ||
      echo "$ts zorven-metrics-watch --mail-cmd basarisiz ($key)" >&2
  fi
done <"$TMP/out"
exit "$RC"
