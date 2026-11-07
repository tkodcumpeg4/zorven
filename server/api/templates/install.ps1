# ==============================================================================
# Zorven - Tek Komutla Istemci Kurulum Scripti (Windows PowerShell)
#
# Kurulum : irm "https://<sunucu>/install.ps1?token=zrv_live_..." | iex
# Kaldirma: & ([scriptblock]::Create((irm "https://<sunucu>/install.ps1"))) -Uninstall [-Purge]
# Yonetici degilseniz UAC ile kendini yukseltir.
# ==============================================================================
param(
    [string]$Token = "",
    [string]$Server = "",
    [string]$TokenFile = "",
    [switch]$Insecure,
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$Elevated
)

$ErrorActionPreference = "Stop"

# Sunucu tarafindan sablon olarak doldurulan degerler. Sentinel PARCALI yazilir
# ki sunucudaki ReplaceAll bu karsilastirmalari bozmasin.
$InjectedToken = '__INJECTED_TOKEN__'
$InjectedServer = '__INJECTED_SERVER__'
$SentinelToken = "__INJECTED" + "_TOKEN__"
$SentinelServer = "__INJECTED" + "_SERVER__"

function Fail([string]$msg) {
    throw $msg
}

function Invoke-Zorven([string]$exe, [string[]]$cmdArgs, [switch]$Quiet) {
    # Native komutun cikis kodunu dondurur; stderr metni hata olarak firlatilmaz.
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        if ($Quiet) { & $exe @cmdArgs *> $null } else { & $exe @cmdArgs }
        return $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
}

function Get-ZorvenFile([string]$url, [string]$dest, [bool]$skipTls) {
    # TLS dogrulamasi YALNIZ -Insecure ile ve yalniz bu indirme suresince kapanir.
    $p = [Net.SecurityProtocolType]::Tls12
    try { $p = $p -bor [Net.SecurityProtocolType]'Tls13' } catch { }
    [Net.ServicePointManager]::SecurityProtocol = $p
    $prevCb = [Net.ServicePointManager]::ServerCertificateValidationCallback
    if ($skipTls) { [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true } }
    try {
        $wc = New-Object System.Net.WebClient
        $wc.DownloadFile($url, $dest)
    } finally {
        [Net.ServicePointManager]::ServerCertificateValidationCallback = $prevCb
    }
}

function Install-Zorven {
    if (-not $Token -and $InjectedToken -ne $SentinelToken -and $InjectedToken) { $script:Token = $InjectedToken }
    if (-not $Server -and $InjectedServer -ne $SentinelServer -and $InjectedServer) { $script:Server = $InjectedServer }

    Write-Host "==================================================================" -ForegroundColor Cyan
    Write-Host "    ZORVEN - Guvenli Tunel & Istemci Otomatik Kurulumu" -ForegroundColor Cyan
    Write-Host "==================================================================" -ForegroundColor Cyan

    # Token dosyasi (yukseltilmis surecte): oku ve hemen sil.
    if ($TokenFile) {
        if (-not (Test-Path -LiteralPath $TokenFile)) { Fail "Token dosyasi bulunamadi." }
        $script:Token = (Get-Content -LiteralPath $TokenFile -Raw)
        Remove-Item -Force -LiteralPath $TokenFile -ErrorAction SilentlyContinue
    }

    if (-not $Server -and -not $Uninstall) {
        Fail "Sunucu adresi belirlenemedi. Scripti sunucudan (https://<sunucu>/install.ps1) calistirin ya da -Server host[:port] verin."
    }
    if ($Server -and $Server -notmatch '^[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?$') { Fail "Gecersiz sunucu adresi: $Server" }

    # 1. Yonetici yetkisi; degilse UAC ile yeniden calistir.
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    $isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $isAdmin) {
        Write-Host "`n[UYARI] Windows Servisi kurmak icin Yonetici yetkisi gerekir; UAC istemi ile yeniden baslatiliyor..." -ForegroundColor Yellow
        if (-not $Uninstall -and -not $Token) {
            $script:Token = (Read-Host "Zorven Istemci Token'inizi girin (zrv_live_...)")
        }
        $scriptPath = $PSCommandPath
        if (-not $scriptPath) {
            if (-not $Server) { Fail "Yukseltme icin sunucu adresi gerekli. -Server verin veya PowerShell'i Yonetici olarak acin." }
            $scriptPath = Join-Path $env:TEMP ("zorven-install-" + [guid]::NewGuid().ToString("N") + ".ps1")
            Get-ZorvenFile "https://$Server/install.ps1" $scriptPath ([bool]$Insecure)
        }
        $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$scriptPath`"", "-Elevated")
        if ($Server) { $argList += @("-Server", $Server) }
        if ($Insecure) { $argList += "-Insecure" }
        if ($Uninstall) { $argList += "-Uninstall" }
        if ($Purge) { $argList += "-Purge" }
        $tf = ""
        if ($Token -and -not $Uninstall) {
            # Token komut satirinda tasinmaz; gecici dosyayla (kullanici profili) iletilir.
            $tf = Join-Path $env:TEMP ("zorven-" + [guid]::NewGuid().ToString("N") + ".tok")
            [IO.File]::WriteAllText($tf, ($Token -replace '\s', ''))
            $argList += @("-TokenFile", "`"$tf`"")
        }
        try {
            $proc = Start-Process -FilePath "powershell.exe" -ArgumentList $argList -Verb RunAs -Wait -PassThru
        } catch {
            if ($tf) { Remove-Item -Force -ErrorAction SilentlyContinue $tf }
            Fail "Yonetici yetkisi alinamadi (UAC reddedildi?): $_"
        }
        if ($tf) { Remove-Item -Force -ErrorAction SilentlyContinue $tf }
        if ($proc.ExitCode -ne 0) { Fail "Yukseltilmis kurulum basarisiz oldu (cikis kodu $($proc.ExitCode))." }
        return
    }

    $installDir = Join-Path $env:ProgramFiles "Zorven"
    $exePath = Join-Path $installDir "zorven.exe"

    # ---------------------------------------------------------------- kaldirma
    if ($Uninstall) {
        Write-Host "`nZorven kaldiriliyor..." -ForegroundColor Cyan
        if (Test-Path -LiteralPath $exePath) {
            if ((Invoke-Zorven $exePath @("service", "status") -Quiet) -eq 0) {
                [void](Invoke-Zorven $exePath @("service", "stop") -Quiet)
                if ((Invoke-Zorven $exePath @("service", "uninstall")) -ne 0) { Fail "Servis kaldirilamadi; dosyalar silinmedi." }
                Write-Host "   [OK] Servis kaldirildi." -ForegroundColor Green
            } else {
                Write-Host "   Kayitli servis bulunamadi."
            }
            Remove-Item -Recurse -Force -LiteralPath $installDir -ErrorAction Stop
            Write-Host "   [OK] $installDir silindi." -ForegroundColor Green
        } else {
            Write-Host "   $exePath bulunamadi; yapilacak bir sey yok."
        }
        # PATH temizligi
        $mp = [Environment]::GetEnvironmentVariable("Path", "Machine")
        if ($mp) {
            $kept = @($mp.Split(';') | Where-Object { $_ -and $_.TrimEnd('\') -ine $installDir.TrimEnd('\') })
            [Environment]::SetEnvironmentVariable("Path", ($kept -join ';'), "Machine")
        }
        $cfgDir = Join-Path $env:ProgramData "zorven"
        if ($Purge) {
            if (Test-Path -LiteralPath $cfgDir) { Remove-Item -Recurse -Force -LiteralPath $cfgDir -ErrorAction Stop }
            Write-Host "   [OK] Yapilandirma ($cfgDir) silindi." -ForegroundColor Green
        } else {
            Write-Host "   Yapilandirma korundu ($cfgDir). Silmek icin -Purge kullanin."
        }
        Write-Host "`nZorven kaldirildi." -ForegroundColor Green
        return
    }

    # 2. Token
    if (-not $Token) {
        Write-Host "Zorven istemci token'i bulunamadi." -ForegroundColor Yellow
        $script:Token = Read-Host "Zorven Istemci Token'inizi girin (zrv_live_...)"
    }
    if ($Token) { $script:Token = ($Token -replace '\s', '') }
    if (-not $Token) { Fail "Token bos olamaz. Kurulum iptal edildi." }
    if ($Token -like "rpsh_live_*") {
        Write-Host "[UYARI] Eski bicimli (rpsh_live_) token kullaniliyor; yeni token'lar 'zrv_live_' ile baslar." -ForegroundColor Yellow
    } elseif ($Token -notlike "zrv_live_*") {
        Fail "Gecersiz token bicimi: token 'zrv_live_' ile baslamalidir."
    }

    # 3. Mimari (386 yayinlanmaz)
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") {
        $arch = "arm64"
    } elseif ([System.Environment]::Is64BitOperatingSystem) {
        $arch = "amd64"
    } else {
        Fail "32-bit Windows desteklenmiyor (386 ikilisi yayinlanmiyor)."
    }

    $base = "https://$Server/bin"
    Write-Host "-> Platform:       Windows ($arch)" -ForegroundColor Green
    Write-Host "-> Sunucu:         $Server" -ForegroundColor Green
    Write-Host "-> Kurulum Yolu:   $installDir" -ForegroundColor Green
    if ($Insecure) {
        Write-Host "[UYARI] -Insecure: TLS dogrulamasi YALNIZ bu kurulumun indirmeleri icin kapali (self-hosted/gelistirme)." -ForegroundColor Yellow
    }

    # 4. Manifest + ikili indir ve SHA-256 dogrula
    Write-Host "`n1. Surum bilgisi ve ikili dosya indiriliyor..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $mfPath = Join-Path $installDir "manifest.json.tmp"
    $newExe = Join-Path $installDir "zorven.exe.new"
    try {
        try { Get-ZorvenFile "$base/manifest.json" $mfPath ([bool]$Insecure) } catch { Fail "manifest.json indirilemedi: $_" }
        $mf = Get-Content -LiteralPath $mfPath -Raw | ConvertFrom-Json
        # windows/amd64 yalniz "+cli" anahtariyla yayinlanir (masaustu exe ile karismasin).
        $key = if ($arch -eq "amd64") { "windows/amd64+cli" } else { "windows/$arch" }
        $entry = $mf.files.PSObject.Properties[$key]
        if (-not $entry) { Fail "Bu platform ($key) icin yayinlanmis ikili yok." }
        $entry = $entry.Value
        if ($entry.name -notmatch '^zorven-[A-Za-z0-9._-]+$') { Fail "Manifestte gecersiz dosya adi." }
        if ($entry.sha256 -notmatch '^[0-9a-fA-F]{64}$') { Fail "Manifestte gecerli sha256 yok; kurulum durduruldu." }
        Write-Host "   Kaynak: $base/$($entry.name) (surum $($mf.version))"
        try { Get-ZorvenFile "$base/$($entry.name)" $newExe ([bool]$Insecure) } catch { Fail "Zorven ikilisi indirilemedi: $_" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $newExe).Hash.ToLower()
        if ($got -ne $entry.sha256.ToLower()) {
            Fail "SHA-256 uyusmuyor (beklenen $($entry.sha256), alinan $got). Dosya bozuk veya degistirilmis; kurulum iptal edildi."
        }
        Write-Host "   [OK] SHA-256 dogrulandi." -ForegroundColor Green

        # 5. Mevcut kurulum varsa durdur, atomik degistir
        $upgrade = $false
        if ((Test-Path -LiteralPath $exePath) -and ((Invoke-Zorven $exePath @("service", "status") -Quiet) -eq 0)) {
            $upgrade = $true
            Write-Host "   Mevcut kurulum bulundu; yukseltiliyor." -ForegroundColor Cyan
            [void](Invoke-Zorven $exePath @("service", "stop") -Quiet)
            if ((Invoke-Zorven $exePath @("service", "uninstall")) -ne 0) { Fail "Mevcut servis kaldirilamadi; yukseltme durduruldu." }
        }
        $moved = $false
        for ($i = 0; $i -lt 8 -and -not $moved; $i++) {
            try { Move-Item -Force -LiteralPath $newExe -Destination $exePath; $moved = $true }
            catch { Start-Sleep -Seconds 1 }
        }
        if (-not $moved) { Fail "$exePath degistirilemedi (dosya kullanimda olabilir; Zorven uygulamalarini kapatip tekrar deneyin)." }
        Write-Host "   [OK] $exePath yazildi." -ForegroundColor Green
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $mfPath, $newExe
    }

    # 6. wintun.dll (Mod B): manifest sha256 + Authenticode. Basarisizlik kurulumu durdurmaz.
    $tmpDll = Join-Path $installDir "wintun.dll.tmp"
    try {
        $entry = $mf.files.PSObject.Properties["windows/$arch+wintun"]
        if ($entry) {
            $entry = $entry.Value
            if ($entry.name -match '^wintun-(amd64|arm64)\.dll$') {
                Get-ZorvenFile "$base/$($entry.name)" $tmpDll ([bool]$Insecure)
                $gotSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $tmpDll).Hash.ToLower()
                $sig = Get-AuthenticodeSignature $tmpDll
                if ($gotSha -ne $entry.sha256.ToLower()) { throw "sha256 uyusmuyor" }
                if ($sig.Status -ne "Valid") { throw "Authenticode imzasi gecersiz ($($sig.Status))" }
                Move-Item -Force -LiteralPath $tmpDll -Destination (Join-Path $installDir "wintun.dll")
                Write-Host "   [OK] wintun.dll (tam ag katmani) hazir." -ForegroundColor Green
            }
        }
    } catch {
        Write-Host "   [UYARI] wintun.dll indirilemedi/dogrulanamadi: $_ (Tam ag katmani icin 'zorven net up' yeniden dener)" -ForegroundColor Yellow
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $tmpDll
    }

    # 7. PATH (Machine): ';' ile bol, tam esitlik karsilastir
    Write-Host "`n2. Sistem PATH ortam degiskeni yapilandiriliyor..." -ForegroundColor Cyan
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $entries = @()
    if ($machinePath) { $entries = @($machinePath.Split(';') | Where-Object { $_ }) }
    $present = $entries | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') }
    if (-not $present) {
        [Environment]::SetEnvironmentVariable("Path", (($entries + $installDir) -join ';'), "Machine")
        Write-Host "   [OK] $installDir sistem PATH'ine eklendi (yeni terminallerde gecerli)." -ForegroundColor Green
    } else {
        Write-Host "   [OK] PATH zaten tanimli." -ForegroundColor Green
    }
    if (($env:Path.Split(';') | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') }) -eq $null) {
        $env:Path = "$env:Path;$installDir"
    }

    # 8. Servis: token komut satirina yazilmaz; --token-file ile verilir (CLI dosyayi siler)
    Write-Host "`n3. Windows Servisi (Zorven Service) kuruluyor ve baslatiliyor..." -ForegroundColor Cyan
    $tokFile = Join-Path $installDir (".tok-" + [guid]::NewGuid().ToString("N"))
    try {
        [IO.File]::WriteAllText($tokFile, $Token)
        $rc = Invoke-Zorven $exePath @("service", "install", "--server", $Server, "--token-file", $tokFile)
        if ($rc -ne 0) { Fail "Servis kurulamadi (cikis kodu $rc)." }
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $tokFile
        $script:Token = ""
    }
    Write-Host "   [OK] Servis kaydedildi." -ForegroundColor Green
    $rc = Invoke-Zorven $exePath @("service", "start")
    if ($rc -ne 0) { Fail "Servis baslatilamadi (cikis kodu $rc). 'zorven service status' ile kontrol edin." }
    Write-Host "   [OK] Servis baslatildi." -ForegroundColor Green

    Write-Host "`n==================================================================" -ForegroundColor Green
    if ($upgrade) {
        Write-Host "    ZORVEN WINDOWS SERVISI YUKSELTILDI VE BASLATILDI" -ForegroundColor Green
    } else {
        Write-Host "    TEBRIKLER! ZORVEN WINDOWS SERVISI BASARIYLA KURULDU VE BASLADI" -ForegroundColor Green
    }
    Write-Host "==================================================================" -ForegroundColor Green
    Write-Host "Servis Durumu : Aktif (Windows Service arka planda 7/24 calisiyor)" -ForegroundColor Green
    Write-Host "Yonetim Komutlari (yeni bir terminalden calistirin):"
    Write-Host "  - Durum:      zorven service status" -ForegroundColor White
    Write-Host "  - Yeniden:    zorven service restart" -ForegroundColor White
    Write-Host "  - Durdur:     zorven service stop" -ForegroundColor White
    Write-Host "  - Kaldir:     & ([scriptblock]::Create((irm https://$Server/install.ps1))) -Uninstall" -ForegroundColor White
    Write-Host "  - Canli Port: zorven 8080" -ForegroundColor White
    Write-Host "`nZorven Dashboard'unuzda bu makinenin canli baglanti durumunu ve CPU/RAM metriklerini gorebilirsiniz.`n"
}

$failed = $false
try {
    Install-Zorven
} catch {
    $failed = $true
    Write-Host "[HATA] $_" -ForegroundColor Red
}
if ($Elevated) {
    # Yukseltilmis pencere kapanmadan once sonucu gorulebilsin.
    Read-Host "Devam etmek icin Enter'a basin" | Out-Null
}
if ($failed -and $PSCommandPath) { exit 1 }
