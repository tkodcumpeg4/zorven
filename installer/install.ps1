# rpshell masaustu istemcisini Windows'a kurar (KULLANICI duzeyinde; admin GEREKMEZ).
#
# Yaptiklari:
#   1. rpshell-desktop.exe'yi %LOCALAPPDATA%\Programs\rpshell\ altina kopyalar
#   2. Baslat menusu + masaustu kisayolu olusturur
#   3. "Program Ekle/Kaldir" listesine kaydeder (kaldirici ile)
#   4. Uygulamayi baslatir
#
# Calistirma:  install.bat'a cift tikla  (ya da: powershell -File install.ps1)

$ErrorActionPreference = "Stop"
$AppName    = "Reverse Proxy Shell"
$AppId      = "rpshell"
$ScriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path

# --- 1. Kaynak exe'yi bul ---------------------------------------------------
# Once betigin yaninda, sonra repo build ciktisinda ara.
$candidates = @(
  (Join-Path $ScriptDir "rpshell-desktop.exe"),
  (Join-Path $ScriptDir "..\desktop\build\bin\rpshell-desktop.exe")
)
$source = $null
foreach ($c in $candidates) {
  if (Test-Path $c) { $source = (Resolve-Path $c).Path; break }
}
if (-not $source) {
  Write-Host "HATA: rpshell-desktop.exe bulunamadi." -ForegroundColor Red
  Write-Host "Once uygulamayi derleyin:  cd desktop; wails build" -ForegroundColor Yellow
  Write-Host "veya rpshell-desktop.exe'yi bu betigin yanina koyun."
  exit 1
}

# --- 2. Hedef dizine kopyala ------------------------------------------------
$installDir = Join-Path $env:LOCALAPPDATA "Programs\$AppId"
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$targetExe = Join-Path $installDir "rpshell-desktop.exe"

Write-Host "Kuruluyor: $installDir"
Copy-Item $source $targetExe -Force

# --- 2b. Sertifikayi tasi ve ilk config'i olustur ---------------------------
# Installer'in yaninda server.crt varsa kurulum dizinine kopyala; boylece baska
# PC'de uygulama acilinca CA sertifikasi yolu HAZIR gelir (elle girmek gerekmez).
# server.txt varsa (host:port) sunucu adresi de onceden doldurulur.
$installedCert = ""
$certSrc = Join-Path $ScriptDir "server.crt"
if (Test-Path $certSrc) {
  $installedCert = Join-Path $installDir "server.crt"
  Copy-Item $certSrc $installedCert -Force
  Write-Host "  CA sertifikasi tasindi: $installedCert"
}

$serverAddr = ""
$serverTxt = Join-Path $ScriptDir "server.txt"
if (Test-Path $serverTxt) { $serverAddr = ((Get-Content $serverTxt -Raw).Trim()) }

# Config'i YALNIZCA yoksa yaz (yeniden kurulumda kullanicinin token'ini ezme).
$cfgDir  = Join-Path $env:APPDATA $AppId
$cfgFile = Join-Path $cfgDir "config.json"
if ((-not (Test-Path $cfgFile)) -and (($installedCert -ne "") -or ($serverAddr -ne ""))) {
  New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
  $cfg = [ordered]@{
    server_addr  = $serverAddr
    token        = ""
    local_url    = "http://localhost:8000"
    ca_cert_path = $installedCert
    insecure     = $false
    auto_connect = $true
  }
  # BOM'SUZ UTF-8 yaz: Set-Content -Encoding UTF8 (PS 5.1) BOM ekler ve Go'nun
  # json.Unmarshal'i BOM'u reddeder -> config parse edilemez, seed kaybolurdu.
  $json = ($cfg | ConvertTo-Json)
  [System.IO.File]::WriteAllText($cfgFile, $json, (New-Object System.Text.UTF8Encoding $false))
  Write-Host "  Baslangic ayari yazildi: $cfgFile"
}

# --- 3. Kisayollar ----------------------------------------------------------
$wsh = New-Object -ComObject WScript.Shell

$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$startLnk  = Join-Path $startMenu "$AppName.lnk"
$sc = $wsh.CreateShortcut($startLnk)
$sc.TargetPath = $targetExe
$sc.WorkingDirectory = $installDir
$sc.Description = "rpshell tunel istemcisi"
$sc.Save()

$desktopLnk = Join-Path ([Environment]::GetFolderPath("Desktop")) "$AppName.lnk"
$dc = $wsh.CreateShortcut($desktopLnk)
$dc.TargetPath = $targetExe
$dc.WorkingDirectory = $installDir
$dc.Description = "rpshell tunel istemcisi"
$dc.Save()

# --- 4. Program Ekle/Kaldir kaydi (HKCU) ------------------------------------
$uninstallScript = Join-Path $installDir "uninstall.ps1"
Copy-Item (Join-Path $ScriptDir "uninstall.ps1") $uninstallScript -Force

$regKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppId"
New-Item -Path $regKey -Force | Out-Null
Set-ItemProperty -Path $regKey -Name "DisplayName"     -Value $AppName
Set-ItemProperty -Path $regKey -Name "DisplayIcon"     -Value $targetExe
Set-ItemProperty -Path $regKey -Name "DisplayVersion"  -Value "0.1.0"
Set-ItemProperty -Path $regKey -Name "Publisher"       -Value "rpshell"
Set-ItemProperty -Path $regKey -Name "InstallLocation" -Value $installDir
Set-ItemProperty -Path $regKey -Name "NoModify"        -Value 1 -Type DWord
Set-ItemProperty -Path $regKey -Name "NoRepair"        -Value 1 -Type DWord
$uninstCmd = "powershell -NoProfile -ExecutionPolicy Bypass -File `"$uninstallScript`""
Set-ItemProperty -Path $regKey -Name "UninstallString" -Value $uninstCmd

Write-Host ""
Write-Host "Kurulum tamamlandi." -ForegroundColor Green
Write-Host "  Baslat menusu / masaustu: $AppName"
Write-Host "  Kaldirmak: Ayarlar > Uygulamalar, veya $uninstallScript"
Write-Host ""

# --- 5. Baslat --------------------------------------------------------------
Write-Host "Uygulama baslatiliyor..."
Start-Process $targetExe
