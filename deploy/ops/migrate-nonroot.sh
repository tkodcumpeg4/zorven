#!/usr/bin/env bash
#
# zorven-primary'yi root OLMAYAN kullaniciya (65532) + salt okunur kok dosya sistemine
# gecirir ya da geri alir. Idempotent; varsayilan --dry-run (hicbir sey degistirmez).
#
# Kipler:
#   --dry-run    (vars.) plani, mevcut sahiplikleri ve dogrulanmis compose'u yazar
#   --apply      sahiplikleri kaydeder -> birim/bind izinlerini hazirlar -> override ile
#                up -d -> saglik bekler; basarisizsa OTOMATIK geri alir
#   --rollback   override'siz up -d (root kip) + son kayittaki sahiplikleri geri yukler
#   --fix-perms  yalniz bind mount (certs, data) grup izinlerini yeniden uygular; yeniden
#                baslatma YOK. acme.sh --reloadcmd icinde, sertifika yenilendikten sonra.
#   --status     konteyner kullanicisi, read_only, saglik, baglamalar
#
# Secenekler (ortam degiskeni karsiligi parantezde):
#   --compose-dir DIR   (ZV_COMPOSE_DIR)  docker-compose.yml dizini; vars. bu betigin ../'si
#   --project NAME      (ZV_PROJECT)      compose proje adi; vars. compose'un kendi secimi
#   --env-file FILE     (ZV_ENV_FILE)     vars. <compose-dir>/.env (compose varsayilani)
#   --extra-compose F   (ZV_EXTRA_COMPOSE, ':' ayrik) tabandan sonra eklenecek ek dosya(lar);
#                       yalniz yerel deneme yigini icin (port/yol degisikligi)
#   --service NAME      (ZV_SERVICE)      vars. zorven-primary
#   --state-dir DIR     (ZV_STATE_DIR)    geri alma kayitlari; vars. /var/lib/zorven-nonroot-migrate
#                       (depo disinda: depo temizligi/yeniden klonlama kaydi silmesin)
#   --backup-dir DIR    (ZV_BACKUP_DIR)   son 26 saatte yedek var mi bakilir; vars. /opt/zorven/backups
#   --health-timeout S  (ZV_HEALTH_TIMEOUT) vars. 240 sn
#
# Ornek (sunucuda, deploy/ dizini /opt/zorven/deploy ise):
#   deploy/ops/migrate-nonroot.sh --compose-dir /opt/zorven/deploy            # dry-run
#   deploy/ops/migrate-nonroot.sh --compose-dir /opt/zorven/deploy --apply
#   deploy/ops/migrate-nonroot.sh --compose-dir /opt/zorven/deploy --rollback
#
# Izin islemleri host'ta degil, servisin KENDI imajindan kisa omurlu bir konteynerde
# (--user 0, --network none) yapilir: adlandirilmis birimlere host yolu bilmeden erisilir
# ve host'ta ek arac gerekmez. Docker erisimi zaten root esdegeridir.
#
# Kural: adlandirilmis birimler (client_bins) yalniz bu konteynere ait -> chown -R 65532.
# Bind mount'lar (certs, data) host sureclerine de ait (acme.sh, geo cron) -> sahip
# DEGISMEZ, yalniz grup 65532 + g+rX (anahtar 0600 -> 0640). zorven_state birimi yenidir
# ve imajdan sahipligi alir.
set -euo pipefail

NONROOT_UID=65532
NONROOT_GID=65532

say()  { printf '[migrate-nonroot] %s\n' "$*"; }
warn() { printf '[migrate-nonroot] UYARI: %s\n' "$*" >&2; }
die()  { printf '[migrate-nonroot] HATA: %s\n' "$*" >&2; exit 1; }
step() { printf '\n[migrate-nonroot] === %s ===\n' "$*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE=dry-run
COMPOSE_DIR="${ZV_COMPOSE_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
PROJECT="${ZV_PROJECT:-}"
ENV_FILE="${ZV_ENV_FILE:-}"
EXTRA="${ZV_EXTRA_COMPOSE:-}"
SERVICE="${ZV_SERVICE:-zorven-primary}"
STATE_DIR="${ZV_STATE_DIR:-}"
BACKUP_DIR="${ZV_BACKUP_DIR:-/opt/zorven/backups}"
HEALTH_TIMEOUT="${ZV_HEALTH_TIMEOUT:-240}"

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)   MODE=dry-run; shift ;;
    --apply)     MODE=apply; shift ;;
    --rollback)  MODE=rollback; shift ;;
    --fix-perms) MODE=fix-perms; shift ;;
    --status)    MODE=status; shift ;;
    --compose-dir)    COMPOSE_DIR="$2"; shift 2 ;;
    --project)        PROJECT="$2"; shift 2 ;;
    --env-file)       ENV_FILE="$2"; shift 2 ;;
    --extra-compose)  EXTRA="${EXTRA:+$EXTRA:}$2"; shift 2 ;;
    --service)        SERVICE="$2"; shift 2 ;;
    --state-dir)      STATE_DIR="$2"; shift 2 ;;
    --backup-dir)     BACKUP_DIR="$2"; shift 2 ;;
    --health-timeout) HEALTH_TIMEOUT="$2"; shift 2 ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) die "bilinmeyen arguman: $1 (--help)" ;;
  esac
done

COMPOSE_DIR="$(cd "$COMPOSE_DIR" 2>/dev/null && pwd)" || die "compose dizini yok"
BASE_FILE="$COMPOSE_DIR/docker-compose.yml"
NONROOT_FILE="$COMPOSE_DIR/docker-compose.nonroot.yml"
STATE_DIR="${STATE_DIR:-/var/lib/zorven-nonroot-migrate}"
[ -f "$BASE_FILE" ] || die "taban compose yok: $BASE_FILE"
[ -f "$NONROOT_FILE" ] || die "nonroot override yok: $NONROOT_FILE (dal birlestirildi mi?)"
case "$HEALTH_TIMEOUT" in ''|*[!0-9]*) die "--health-timeout sayi olmali" ;; esac

command -v docker >/dev/null || die "docker yok"

# compose argumanlari: taban + ek dosyalar (+ istege bagli nonroot).
base_args=()
[ -n "$PROJECT" ] && base_args+=(-p "$PROJECT")
[ -n "$ENV_FILE" ] && base_args+=(--env-file "$ENV_FILE")
base_args+=(-f "$BASE_FILE")
if [ -n "$EXTRA" ]; then
  IFS=':' read -r -a _extra <<<"$EXTRA"
  for f in "${_extra[@]}"; do
    [ -f "$f" ] || die "ek compose dosyasi yok: $f"
    base_args+=(-f "$f")
  done
fi
dc_root()    { (cd "$COMPOSE_DIR" && docker compose "${base_args[@]}" "$@"); }
dc_nonroot() { (cd "$COMPOSE_DIR" && docker compose "${base_args[@]}" -f "$NONROOT_FILE" "$@"); }

# --- yardimcilar -------------------------------------------------------------

compose_version_ok() {
  # `!reset` etiketi compose 2.24.0 ile geldi.
  local v; v="$(docker compose version --short 2>/dev/null | sed 's/^v//; s/[+-].*//')"
  [ -n "$v" ] || return 1
  local maj min; maj="${v%%.*}"; min="${v#*.}"; min="${min%%.*}"
  [ "$maj" -gt 2 ] || { [ "$maj" -eq 2 ] && [ "$min" -ge 24 ]; }
}

cid() { dc_root ps -q "$SERVICE" 2>/dev/null | head -n1; }

# mounts <cid>: "tur|ad|kaynak|hedef|rw" satirlari (yalniz bind + volume). Ayirac '|':
# TAB IFS'te bosluk sayilir, bos alan (bind'in adi yok) iki TAB'i birlestirip alanlari kaydirirdi.
mounts() {
  docker inspect -f '{{range .Mounts}}{{.Type}}|{{.Name}}|{{.Source}}|{{.Destination}}|{{.RW}}{{"\n"}}{{end}}' "$1" \
    | awk -F'|' '$1=="bind" || $1=="volume"'
}

# helper <mount-spec> <sh-komutu>: servis imajinda root olarak, aga kapali.
HELPER_IMAGE=""
helper() {
  local spec="$1"; shift
  docker run --rm --network none --user 0:0 --entrypoint /bin/sh \
    --mount "$spec" "$HELPER_IMAGE" -c "$*"
}

# Bir baglamanin helper --mount belirtimi. `-v` DEGIL: -v olmayan host yolunu sessizce
# (root'a ait bos dizin olarak) yaratir; --mount type=bind yoksa hata verir.
mount_spec() { # <tur> <ad> <kaynak>
  if [ "$1" = volume ]; then
    docker volume inspect "$2" >/dev/null 2>&1 || die "birim yok: $2"
    printf 'type=volume,src=%s,dst=/m' "$2"
  else
    [ -e "$3" ] || die "bind kaynagi yok: $3"
    printf 'type=bind,src=%s,dst=/m' "$3"
  fi
}

# Bu betigin dokunmadigi baglamalar: yeni durum birimi ve Docker'in kendi dosyalari.
skip_mount() { # <hedef>
  case "$1" in
    /app/state) return 0 ;;
  esac
  return 1
}

summary_owners() { # <spec>
  helper "$1" 'find /m -xdev \( -type f -o -type d \) -exec stat -c "%u:%g %a" {} + 2>/dev/null | sort | uniq -c | sort -rn | head -n 8'
}

# Bir baglama icin uygulanacak komut (dry-run'da yazilir, apply'da calisir).
perm_cmd() { # <tur>
  if [ "$1" = volume ]; then
    printf 'chown -R %s:%s /m && chmod -R u+rwX /m' "$NONROOT_UID" "$NONROOT_GID"
  else
    # Sahip korunur; grup 65532 okur. Diger (o) bitlerine dokunulmaz: 0600 anahtar 0640 olur.
    printf 'chgrp -R %s /m && chmod -R g+rX,g-w /m' "$NONROOT_GID"
  fi
}

wait_healthy() { # <cid> <timeout>
  local c="$1" t="$2" s waited=0
  while [ "$waited" -lt "$t" ]; do
    s="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$c" 2>/dev/null || echo yok)"
    case "$s" in
      healthy) say "saglik: healthy (${waited} sn)"; return 0 ;;
      unhealthy|exited|dead|yok) say "saglik: $s"; return 1 ;;
    esac
    sleep 5; waited=$((waited + 5))
  done
  say "saglik: zaman asimi (${t} sn, son durum: $s)"
  return 1
}

verify_running() { # <cid> <beklenen-uid>
  local c="$1" want="$2" uid ro code
  uid="$(docker exec "$c" id -u 2>/dev/null || echo '?')"
  ro="$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$c")"
  say "konteyner: uid=$uid read_only=$ro"
  [ "$uid" = "$want" ] || { warn "beklenen uid $want, gorulen $uid"; return 1; }
  code="$(docker exec "$c" curl -sk -o /dev/null -w '%{http_code}' --max-time 5 https://127.0.0.1:443/api/v1/health || true)"
  say "https://127.0.0.1:443/api/v1/health -> $code"
  [ "$code" = 200 ] || return 1
  code="$(docker exec "$c" curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:80/ || true)"
  say "http://127.0.0.1:80/ -> $code (dinleniyor mu)"
  [ "$code" != 000 ] || return 1
  # Once degiskene: `docker logs | grep -q` pipefail altinda SIGPIPE ile yanlis sonuc verir.
  # Yalniz bu betigin son `up`'indan beri: yeniden yaratilmayan (zaten nonroot) konteynerde
  # eski, cozulmus satirlar (or. --fix-perms oncesi sertifika uyarisi) yanlis alarm vermesin.
  local bad
  bad="$(docker logs --since "${VERIFY_SINCE:-0}" "$c" 2>&1 | grep -Ei 'permission denied|read-only file system' | tail -n 5 || true)"
  if [ -n "$bad" ]; then
    warn "loglarda izin/salt-okunur hatasi var:"
    printf '%s\n' "$bad" >&2
    return 1
  fi
  return 0
}

record_perms() { # <kayit-dizini> <cid>
  local dir="$1" c="$2" typ name src dst rw spec i=0
  : >"$dir/mounts.tsv"
  while IFS='|' read -r typ name src dst rw; do
    skip_mount "$dst" && continue
    i=$((i + 1))
    printf '%s|%s|%s|%s|%s|%s\n' "$i" "$typ" "$name" "$src" "$dst" "$rw" >>"$dir/mounts.tsv"
    spec="$(mount_spec "$typ" "$name" "$src")"
    # uid:gid:mod:yol (yol sonda; ':' iceren adlari read son degiskene toplar).
    helper "$spec" 'find /m -xdev -exec stat -c "%u:%g:%a:%n" {} +' >"$dir/perms-$i.txt"
    say "kaydedildi: $dst ($typ) -> perms-$i.txt ($(wc -l <"$dir/perms-$i.txt") girdi)"
  done < <(mounts "$c")
}

restore_perms() { # <kayit-dizini>
  local dir="$1" i typ name src dst rw spec
  [ -f "$dir/mounts.tsv" ] || { warn "kayit yok: $dir/mounts.tsv"; return 1; }
  while IFS='|' read -r i typ name src dst rw; do
    spec="$(mount_spec "$typ" "$name" "$src")"
    say "geri yukleniyor: $dst ($typ) <- perms-$i.txt"
    # Kayit dosyasi stdin ile verilir; kayittan sonra eklenen dosyalara dokunulmaz.
    docker run --rm -i --network none --user 0:0 --entrypoint /bin/sh --mount "$spec" "$HELPER_IMAGE" -c '
      while IFS=: read -r u g m p; do
        [ -e "$p" ] || [ -L "$p" ] || continue
        chown -h "$u:$g" "$p"
        [ -L "$p" ] || chmod "$m" "$p"
      done' <"$dir/perms-$i.txt"
  done <"$dir/mounts.tsv"
}

VERIFY_SINCE=""
up_root() {
  VERIFY_SINCE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  say "root kip: docker compose ${base_args[*]} up -d --no-deps --no-build $SERVICE"
  dc_root up -d --no-deps --no-build "$SERVICE"
}
up_nonroot() {
  VERIFY_SINCE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  say "nonroot kip: docker compose ${base_args[*]} -f $(basename "$NONROOT_FILE") up -d --no-deps --no-build $SERVICE"
  dc_nonroot up -d --no-deps --no-build "$SERVICE"
}

latest_record() { [ -L "$STATE_DIR/latest" ] && readlink -f "$STATE_DIR/latest" || true; }

# --- ortak on kontroller -----------------------------------------------------

step "on kontroller ($MODE)"
say "compose dizini: $COMPOSE_DIR"
say "servis: $SERVICE  proje: ${PROJECT:-<compose varsayilani>}"
compose_version_ok || die "docker compose >= 2.24 gerekli (!reset etiketi); bulunan: $(docker compose version --short 2>/dev/null || echo yok)"
dc_nonroot config -q || die "compose + nonroot override dogrulanamadi"
say "compose + override yapilandirmasi gecerli"

C="$(cid)"
[ -n "$C" ] || die "$SERVICE calismiyor (once root kipte ayaga kaldirin: docker compose up -d $SERVICE)"
HELPER_IMAGE="$(docker inspect -f '{{.Image}}' "$C")"
IMG_USER="$(docker image inspect -f '{{.Config.User}}' "$HELPER_IMAGE")"
CUR_USER="$(docker inspect -f '{{.Config.User}}' "$C")"
CUR_RO="$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$C")"
say "konteyner: ${C:0:12}  imaj kullanicisi: '${IMG_USER:-root}'  konteyner kullanicisi: '${CUR_USER:-<imaj>}'  read_only: $CUR_RO"

case "$MODE" in
  status)
    docker inspect -f 'saglik: {{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}  yeniden baslama: {{.RestartCount}}' "$C"
    docker exec "$C" id 2>/dev/null || true
    say "baglamalar:"; mounts "$C" | sed 's/^/  /'
    say "son kayit: $(latest_record || echo yok)"
    exit 0 ;;
esac

if [ "$MODE" = apply ] || [ "$MODE" = dry-run ]; then
  [ "$IMG_USER" = "$NONROOT_UID:$NONROOT_GID" ] \
    || die "calisan imaj eski (USER '$IMG_USER'); once bu daldaki imaji root kipte (user 0:0) dagitin"
  docker run --rm --network none --entrypoint /bin/sh "$HELPER_IMAGE" -c 'test -d /app/state && test -O /app/state' \
    || die "imajda 65532'ye ait /app/state yok"
fi

if [ "$MODE" = apply ] || [ "$MODE" = rollback ]; then
  # Izinler konteynerden degisir; host'ta root sart degil ama docker erisimi sart.
  docker info >/dev/null 2>&1 || die "docker daemon'a erisilemiyor"
fi

# --- fix-perms ---------------------------------------------------------------

if [ "$MODE" = fix-perms ]; then
  step "bind mount grup izinleri (yeniden baslatma yok)"
  while IFS='|' read -r typ name src dst rw; do
    skip_mount "$dst" && continue
    [ "$typ" = bind ] || continue
    spec="$(mount_spec "$typ" "$name" "$src")"
    say "$dst <- $src: $(perm_cmd bind)"
    helper "$spec" "$(perm_cmd bind)"
  done < <(mounts "$C")
  # Sunucu sertifika dosyasini izler; yine de anlik yuklesin.
  docker kill --signal=HUP "$C" >/dev/null && say "SIGHUP gonderildi (sertifika yeniden yuklenir)"
  exit 0
fi

# --- rollback ----------------------------------------------------------------

if [ "$MODE" = rollback ]; then
  step "geri alma: root kip"
  REC="$(latest_record)"
  up_root
  C="$(cid)"
  wait_healthy "$C" "$HEALTH_TIMEOUT" || warn "root kip saglikli olmadi; loglara bakin: docker logs ${C:0:12}"
  if [ -n "$REC" ]; then
    step "sahiplikleri geri yukle ($REC)"
    restore_perms "$REC"
  else
    warn "geri alma kaydi yok; sahiplikler oldugu gibi (root kip icin sorun degil: DAC_OVERRIDE)"
  fi
  verify_running "$C" 0 || die "geri alma sonrasi dogrulama basarisiz"
  say "TAMAM: root kipe donuldu. zorven_state birimi silinmedi (zararsiz; yeniden gecis ayni birimi kullanir)."
  exit 0
fi

# --- dry-run / apply: plan ---------------------------------------------------

step "yedek kontrolu"
if [ -d "$BACKUP_DIR" ] && [ -n "$(find "$BACKUP_DIR" -maxdepth 2 -type f -mmin -1560 2>/dev/null | head -n1)" ]; then
  say "son 26 saatte yedek var: $BACKUP_DIR"
else
  warn "son 26 saatte $BACKUP_DIR altinda yedek bulunamadi. Devam etmeden once yedek alin (deploy/dr/server-backup.sh + yerel cekim)."
fi

step "plan"
while IFS='|' read -r typ name src dst rw; do
  if skip_mount "$dst"; then say "$dst: atlanir (yeni durum birimi)"; continue; fi
  spec="$(mount_spec "$typ" "$name" "$src")"
  say "$dst  [$typ ${name:-$src}, rw=$rw]"
  say "  mevcut sahiplik (adet uid:gid mod):"
  summary_owners "$spec" | sed 's/^/      /'
  say "  uygulanacak: $(perm_cmd "$typ")"
done < <(mounts "$C")
say "sonra: zorven_state birimi olusur (imajdan 65532:65532 0750), konteyner override ile yeniden yaratilir"
say "saglik bekleme: en fazla ${HEALTH_TIMEOUT} sn; basarisizsa otomatik geri alma"

if [ "$MODE" = dry-run ]; then
  step "dry-run bitti"
  say "hicbir sey degismedi. Uygulamak icin ayni komutu --apply ile calistirin."
  exit 0
fi

# --- apply -------------------------------------------------------------------

if [ "$CUR_USER" = "$NONROOT_UID:$NONROOT_GID" ] && [ "$CUR_RO" = true ]; then
  say "zaten nonroot kipte; izinler yeniden uygulanip saglik dogrulanacak (idempotent)"
fi

TS="$(date -u +%Y%m%dT%H%M%SZ)"
REC="$STATE_DIR/$TS"
step "1/4 mevcut sahiplikleri kaydet -> $REC"
mkdir -p "$REC"; chmod 700 "$STATE_DIR" "$REC"
# Zaten nonroot ise onceki kayit gercek "eski hal"dir; uzerine yazilmaz.
if [ "$CUR_USER" = "$NONROOT_UID:$NONROOT_GID" ] && [ -n "$(latest_record)" ]; then
  say "onceki kayit korunuyor: $(latest_record)"; rmdir "$REC"; REC="$(latest_record)"
else
  record_perms "$REC" "$C"
  ln -sfn "$TS" "$STATE_DIR/latest"
fi

rollback_auto() {
  warn "OTOMATIK GERI ALMA: $1"
  docker logs --tail 60 "$(cid)" >"$REC/fail-logs.txt" 2>&1 || true
  say "basarisiz konteyner loglari: $REC/fail-logs.txt"
  up_root || true
  local c2; c2="$(cid)"
  # Geri donuste kisa --health-timeout (deneme) yuzunden erken "basarisiz" denmesin.
  wait_healthy "$c2" $(( HEALTH_TIMEOUT > 120 ? HEALTH_TIMEOUT : 120 )) || warn "root kip de saglikli olmadi!"
  restore_perms "$REC" || true
  verify_running "$c2" 0 || warn "geri alma dogrulamasi basarisiz; elle bakin"
  die "gecis basarisiz, root kipe geri donuldu"
}

step "2/4 izinleri hazirla"
while IFS='|' read -r typ name src dst rw; do
  skip_mount "$dst" && continue
  spec="$(mount_spec "$typ" "$name" "$src")" || rollback_auto "baglama cozulemedi: $dst"
  say "$dst: $(perm_cmd "$typ")"
  helper "$spec" "$(perm_cmd "$typ")" || rollback_auto "izin ayarlanamadi: $dst"
done < <(mounts "$C")

step "3/4 nonroot kipte yeniden yarat"
up_nonroot || rollback_auto "compose up basarisiz"
C="$(cid)"

step "4/4 saglik ve dogrulama"
wait_healthy "$C" "$HEALTH_TIMEOUT" || rollback_auto "saglik kontrolu gecmedi"
verify_running "$C" "$NONROOT_UID" || rollback_auto "dogrulama basarisiz"
say "TAMAM: $SERVICE uid $NONROOT_UID, read_only=$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$C")."
say "geri alma kaydi: $REC"
say "geri almak icin: $0 --compose-dir $COMPOSE_DIR${PROJECT:+ --project $PROJECT}${ENV_FILE:+ --env-file $ENV_FILE} --state-dir $STATE_DIR --rollback"
