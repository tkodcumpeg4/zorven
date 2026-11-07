# Windows Kurulumu

Zorven masaüstü istemcisi **kendi kurulumunu yapar**; ayrı bir betik ya da NSIS
paketi gerekmez. İndirme sayfasındaki `Zorven-Setup-windows-amd64.exe`,
masaüstü ikilisinin (`desktop/build/bin/Zorven.exe`) aynı baytlarıdır.

## Kurulum

`Zorven-Setup-windows-amd64.exe`'ye çift tıkla (ya da `Zorven.exe --install`).
Kısa bir onay ekranından sonra Windows'un yönetici onayı (UAC) istenir ve:

1. Komut satırı istemcisi (`zorven.exe`) sunucudaki imzalı `manifest.json`
   (`windows/amd64+cli`) üzerinden indirilir; sha256 ve manifest imzası
   doğrulanır. Çevrimdışıysa açık hata verilir, hiçbir şey kurulmaz.
2. `%ProgramFiles%\Zorven\ZorvenDesktop.exe` (masaüstü) ve `zorven.exe` (CLI) kopyalanır.
   Masaüstü adı CLI'den bilerek farklıdır: Windows dosya adlarında büyük/küçük
   harf ayırmaz, `Zorven.exe` ile `zorven.exe` aynı dosya olurdu (0.3.4 hatası:
   CLI masaüstünün üstüne yazılıyordu). Kurulum adı çakışırsa açık hata verir.
3. Dizin Makine PATH'ine eklenir.
4. Başlat menüsü kısayolu `%ProgramData%\Microsoft\Windows\Start Menu\Programs\Zorven.lnk`
   (tüm kullanıcılar; aramada çıkar) ve istenirse ortak masaüstü kısayolu oluşur.
5. `HKLM\...\Uninstall\Zorven` kaydı yazılır (Ayarlar > Uygulamalar'da görünür).
6. Windows servisi tam izinli (servis) kipte kurulup başlatılır:
   `zorven service install --allow-no-token --server <sunucu>`. Kurulumda kimlik
   yoksa servis kimliksiz bekler; kullanıcı masaüstünde giriş yapınca kimlik
   servise aktarılır.
7. Kurulu kopya normal kullanıcı olarak açılır; kurulum süreci çıkar.

Eski kullanıcı düzeyi kurulum (`%LOCALAPPDATA%\Programs\Zorven`, kullanıcı
Başlat/masaüstü kısayolu, HKCU kaydı) varsa kurulumdan sonra temizlenir;
`%AppData%\zorven` ayarları korunur.

## Bozuk 0.3.4 kurulumundan kurtarma

0.3.4 Setup'ı CLI'yi masaüstünün üstüne yazdı: `Program Files\Zorven\` altında
yalnız CLI var, Başlat kısayolu ve Program Ekle/Kaldır kaydı CLI'ye işaret eder
(açınca "This is a command line tool" konsolu çıkar). Yeni Setup'ı çalıştırmak
yeterlidir: eski kısayol ve kayıt silinip yeniden yazılır, masaüstü
`ZorvenDesktop.exe` olarak kurulur, CLI güncellenir, servis korunur. Kaldırmak
için `Zorven-Setup-windows-amd64.exe --uninstall` de çalışır.

## Kip ayrımı (tek ikili)

| Durum | Davranış |
| --- | --- |
| Dosya adında `Setup` var ya da `--install` verildi, kurulu kopya değil | Kurulum akışı (onay + UAC) |
| `Program Files\Zorven\ZorvenDesktop.exe` | Normal uygulama; bağlantı hep servis üzerinden |
| Başka bir yerden `Zorven.exe` (taşınabilir) | Eski gömülü ajanla çalışır; arayüz "Kur" önerir |

## Kaldırma

Ayarlar > Uygulamalar > Zorven, ya da `ZorvenDesktop.exe --uninstall` (UAC; bozuk kurulumda Setup ile `Zorven-Setup-windows-amd64.exe --uninstall`): servis
durdurulup kaldırılır; kısayollar, PATH girdisi, kayıt ve Program Files dizini
silinir. Kullanıcı ayarları (`%AppData%\zorven`) ve servis yapılandırması
(`%ProgramData%\zorven`) **korunur** (CLI kaldırıcıyla aynı ilke).

## Güncelleme

Masaüstü güncellemesi tek UAC onayıyla masaüstü `ZorvenDesktop.exe` ve CLI `zorven.exe`
dosyalarını birlikte değiştirir, servisi durdurup yeniden başlatır. CLI manifest
sürümü masaüstü hedef sürümüyle aynı değilse güncelleme ertelenir (sürüm kilidi).

## Yayın

`scripts/build-clients.sh X.Y.Z` masaüstü ikilisi derlenmişse
(`cd desktop && wails build -platform windows/amd64 -ldflags "-X main.version=X.Y.Z" -clean`)
`dist/Zorven-Setup-windows-amd64.exe` olarak kopyalar. Masaüstü ve CLI aynı
sürümde yayınlanmalıdır. Windows tarafı henüz kod imzalı değildir (SmartScreen
uyarısı; bkz. `.planning/kod-imzalama-secenekleri.md`).

Elle doğrulama listesi: `docs/desktop-windows-kurulum-testi.md`.

---

Kurulumdan sonra uygulamayı aç, hesabınla giriş yap (tarayıcıdan ya da token ile)
ve **Bağlan**. Ayrıntı için ana `RUNNING.md`.

## Başka bir PC'ye hazır paket (sertifika + IP dahil)

Sunucunun çalıştığı makinede `./scripts/package-client.sh` çalıştır. `dist/client-setup/`
içinde `Zorven-Setup.exe`, sunucu sertifikası (`server.crt`) ve LAN adresi
(`server.txt`) üretilir. Kurulum bunları otomatik doldurmaz; hedef PC'de
Ayarlar > Gelişmiş'ten girilir. LAN IP yanlış algılanırsa:
`ZORVEN_SERVER_IP=192.168.1.180 ./scripts/package-client.sh`.
