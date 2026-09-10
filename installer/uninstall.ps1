# rpshell masaustu istemcisini kaldirir (KULLANICI duzeyinde).
#
# install.ps1 bu betigi kurulum dizinine kopyalar ve Program Ekle/Kaldir'a
# UninstallString olarak kaydeder.

$ErrorActionPreference = "SilentlyContinue"
$AppName = "Reverse Proxy Shell"
$AppId   = "rpshell"

Write-Host "$AppName kaldiriliyor..."

# Calisan uygulamayi kapat
Get-Process -Name "rpshell-desktop" -ErrorAction SilentlyContinue | Stop-Process -Force

$installDir = Join-Path $env:LOCALAPPDATA "Programs\$AppId"

# Kisayollar
Remove-Item (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\$AppName.lnk") -Force
Remove-Item (Join-Path ([Environment]::GetFolderPath("Desktop")) "$AppName.lnk") -Force

# Autostart kaydi (masaustu uygulamasi eklemis olabilir)
Remove-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run" -Name $AppId -ErrorAction SilentlyContinue

# Program Ekle/Kaldir kaydi
Remove-Item -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$AppId" -Recurse -Force

# Kurulum dizini. NOT: kullanici ayarlari (%AppData%\rpshell\config.json) BILEREK
# silinmez — yeniden kurulumda token'lar korunur. Tamamen temizlemek isteyen
# o klasoru elle silebilir.
#
# Bu betik kurulum dizininin ICINDEN calisiyor olabilir; dosyayi kilitlememek
# icin silmeyi ayri bir surece birak.
Start-Process powershell -ArgumentList @(
  "-NoProfile","-WindowStyle","Hidden","-Command",
  "Start-Sleep 1; Remove-Item -Recurse -Force '$installDir'"
)

Write-Host "Kaldirildi. (Kullanici ayarlari %AppData%\rpshell\ altinda korundu.)" -ForegroundColor Green
