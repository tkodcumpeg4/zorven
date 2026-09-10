# ==============================================================================
# Zorven — Tek Komutla Istemci Kurulum Scripti (Windows PowerShell)
# ==============================================================================
param(
    [string]$Token = "",
    [string]$Server = "",
    [switch]$Insecure
)

$ErrorActionPreference = "Stop"

# Sunucu tarafindan sablon olarak doldurulan varsayilan degerler
$InjectedToken = "__INJECTED_TOKEN__"
$InjectedServer = "__INJECTED_SERVER__"

if (-not $Token -and $InjectedToken -ne "__INJECTED_TOKEN__" -and $InjectedToken) {
    $Token = $InjectedToken
}
if (-not $Server -and $InjectedServer -ne "__INJECTED_SERVER__" -and $InjectedServer) {
    $Server = $InjectedServer
}

Write-Host "==================================================================" -ForegroundColor Cyan
Write-Host "    ZORVEN — Guvenli Tunel & Istemci Otomatik Kurulumu           " -ForegroundColor Cyan
Write-Host "==================================================================" -ForegroundColor Cyan

# 1. Yonetici (Administrator) yetkisi kontrolu
$currentPrincipal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Host "`n[UYARI] Windows Servisi olarak kurabilmek icin Yonetici yetkisi gereklidir." -ForegroundColor Yellow
    Write-Host "Lutfen PowerShell'i 'Yonetici Olarak Calistir' ile acip komutu tekrar calistirin.`n" -ForegroundColor Red
    return
}

# 2. Token kontrolu
if (-not $Token) {
    Write-Host "Zorven istemci token'i bulunamadi." -ForegroundColor Yellow
    $Token = Read-Host "Zorven Istemci Token'inizi girin (zrv_live_...)"
}

if (-not $Token) {
    Write-Host "[HATA] Token bos olamaz. Kurulum iptal edildi." -ForegroundColor Red
    return
}

if (-not $Server) {
    $Server = "localhost:8443"
}

# 3. Mimari tespiti
$arch = "amd64"
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    $arch = "arm64"
} elseif (-not [System.Environment]::Is64BitOperatingSystem) {
    $arch = "386"
}

$installDir = "$env:ProgramFiles\Zorven"
$exePath = "$installDir\zorven.exe"
$binName = "zorven-windows-$arch.exe"

Write-Host "-> Platform:       Windows ($arch)" -ForegroundColor Green
Write-Host "-> Sunucu:         $Server" -ForegroundColor Green
Write-Host "-> Kurulum Yolu:   $installDir" -ForegroundColor Green

# 4. Indirme islemi
$scheme = "https"
if ($Insecure -or $Server.StartsWith("localhost") -or $Server.StartsWith("127.0.0.1")) {
    [System.Net.ServicePointManager]::ServerCertificateValidationCallback = {$true}
}

$downloadUrl = "$scheme://$Server/bin/$binName"
Write-Host "`n1. Zorven istemci ikilisi indiriliyor..." -ForegroundColor Cyan
Write-Host "   Kaynak: $downloadUrl"

New-Item -ItemType Directory -Force -Path $installDir | Out-Null

try {
    # Guvenli indirme
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls13
    $webClient = New-Object System.Net.WebClient
    $webClient.DownloadFile($downloadUrl, $exePath)
    Write-Host "   [OK] $exePath indirildi." -ForegroundColor Green
} catch {
    Write-Host "[HATA] Zorven ikilisi indirilemedi: $_" -ForegroundColor Red
    return
}

# 5. PATH ortam degiskenine ekle
Write-Host "`n2. Sistem PATH ortam degiskeni yapilandiriliyor..." -ForegroundColor Cyan
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($machinePath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$machinePath;$installDir", "Machine")
    $env:Path = "$env:Path;$installDir"
    Write-Host "   [OK] $installDir sistem PATH'ine eklendi." -ForegroundColor Green
} else {
    Write-Host "   [OK] PATH zaten tanimli." -ForegroundColor Green
}

# 6. Sistem yapilandirmasini kaydet
Write-Host "`n3. Sistem ayarlari kaydediliyor..." -ForegroundColor Cyan
& "$exePath" config set-server "$Server" --system
& "$exePath" config set-token "$Token" --system
Write-Host "   [OK] ProgramData/zorven/config.json olusturuldu." -ForegroundColor Green

# 7. Windows Servisini kur ve baslat
Write-Host "`n4. Windows Servisi (Zorven Service) kuruluyor ve baslatiliyor..." -ForegroundColor Cyan
& "$exePath" service install
Start-Sleep -Seconds 1
& "$exePath" service start

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "    TEBRIKLER! ZORVEN WINDOWS SERVISI BASARIYLA KURULDU VE BASLADI" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green
Write-Host "Servis Durumu : Aktif (Windows Service arka planda 7/24 calisiyor)" -ForegroundColor Green
Write-Host "Yonetim Komutlari (Terminalden dogrudan calistirabilirsiniz):"
Write-Host "  - Durum:      zorven service status" -ForegroundColor White
Write-Host "  - Yeniden:    zorven service restart" -ForegroundColor White
Write-Host "  - Durdur:     zorven service stop" -ForegroundColor White
Write-Host "  - Kaldir:     zorven service uninstall" -ForegroundColor White
Write-Host "  - Canli Port: zorven 8080" -ForegroundColor White
Write-Host "`nZorven Dashboard'unuzda bu makinenin canli baglanti durumunu ve CPU/RAM metriklerini gorebilirsiniz.`n"
