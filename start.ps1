# rpshell tunel sunucusunu TEK KOMUTLA baslatir (Windows).
#
# Calistirma:
#   PowerShell'de:   .\start.ps1
#   Cift tiklama:    start.bat (bu betigi cagirir)
#
# Yaptiklari:
#   1. Go var mi kontrol eder.
#   2. TLS sertifikasi yoksa uretir (server\certs\).
#   3. Dashboard gomulu degilse ve npm varsa derleyip gomer.
#   4. Sunucuyu baslatir. Ilk calistirmada ADMIN ANAHTARI ekrana yazilir.
#
# Dashboard + API + terminal/ekran hepsi ayni adreste servis edilir:
#   https://<makine-ip>:8443
#
# Ortam degiskenleri:  $env:ZORVEN_ADDR (varsayilan 0.0.0.0:8443), $env:ZORVEN_DB

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

$addr = $env:ZORVEN_ADDR
if ([string]::IsNullOrEmpty($addr)) { $addr = "0.0.0.0:8443" }
$dbDSN = $env:ZORVEN_DB_DSN
if ([string]::IsNullOrEmpty($dbDSN)) { $dbDSN = "postgres://rpshell:rpshell@localhost:5432/rpshell" }
$authUpstream = $env:ZORVEN_AUTH_UPSTREAM
if ([string]::IsNullOrEmpty($authUpstream)) { $authUpstream = "http://localhost:3000" }

# 1. Go kontrolu
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Host "HATA: Go bulunamadi. https://go.dev/dl/ adresinden Go 1.23+ kurun." -ForegroundColor Red
  exit 1
}

Set-Location (Join-Path $root "server")

# LAN IPv4 adreslerini tespit et. HEM sertifikaya (TLS) HEM kontrol hostu
# listesine eklenir; aksi halde LAN'dan gelen istek tunel sanilir -> TUNNEL_NOT_FOUND.
$hosts = "localhost,127.0.0.1,::1"
try {
  $ips = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop |
    Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } |
    Select-Object -ExpandProperty IPAddress
  foreach ($ip in $ips) { $hosts = "$hosts,$ip" }
} catch { }
Write-Host "Kontrol hostlari: $hosts"

# 2. Sertifika (LAN IP'leri dahil)
if (-not (Test-Path "certs\server.crt") -or -not (Test-Path "certs\server.key")) {
  Write-Host "TLS sertifikasi uretiliyor..."
  go run . cert --host $hosts
}

# 3. Dashboard gomulu mu?
if (-not (Test-Path "webdist\_nuxt")) {
  if (Get-Command npm -ErrorAction SilentlyContinue) {
    Write-Host "Dashboard gomulu degil, derleniyor (bir kez)..."
    Push-Location $root
    # build-dashboard.sh Git Bash gerektirir; yoksa elle npm run generate + kopyala.
    if (Get-Command bash -ErrorAction SilentlyContinue) {
      bash ./scripts/build-dashboard.sh
    } else {
      Write-Host "bash yok; dashboard elle derleniyor..."
      Push-Location (Join-Path $root "web")
      $env:NUXT_PUBLIC_WS_BASE = ""
      npm run generate
      Pop-Location
      $dest = Join-Path $root "server\webdist"
      Get-ChildItem $dest -Exclude "index.html" | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
      Copy-Item (Join-Path $root "web\.output\public\*") $dest -Recurse -Force
    }
    Pop-Location
  } else {
    Write-Host "UYARI: npm yok, dashboard derlenemiyor. Yer tutucu sayfa gosterilecek." -ForegroundColor Yellow
    Write-Host "       Dashboard icin Node.js kurup: .\scripts\build-dashboard.sh"
  }
}

# 4. Sunucu
Write-Host "Sunucu baslatiliyor: https://$addr" -ForegroundColor Green
Write-Host "  (Ctrl+C ile durdur)"
Write-Host ""
go run . --db-dsn $dbDSN serve --addr $addr --control-host $hosts --tls-cert certs\server.crt --tls-key certs\server.key --auth-upstream $authUpstream
