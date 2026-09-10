# Windows Kurulumu

rpshell masaüstü istemcisini Windows'a kurmanın iki yolu:

## 1. Hızlı kurulum (PowerShell — NSIS gerektirmez)

`rpshell-desktop.exe`'yi bu klasöre koy (ya da `desktop/build/bin/` altında dursun), sonra:

- **`install.bat`'a çift tıkla**

Yaptıkları (admin **gerekmez**, kullanıcı düzeyinde):
- `%LOCALAPPDATA%\Programs\rpshell\` altına kurar
- Başlat menüsü + masaüstü kısayolu oluşturur
- "Program Ekle/Kaldır" listesine ekler

**Kaldırma:** Ayarlar → Uygulamalar → "Reverse Proxy Shell" → Kaldır
(veya `%LOCALAPPDATA%\Programs\rpshell\uninstall.ps1`). Ayarların
(`%AppData%\rpshell\config.json`) korunur.

## 2. Profesyonel installer (.exe — NSIS)

Tek dosyalık gerçek bir kurulum sihirbazı. NSIS gerektirir:

```bash
choco install nsis -y          # ya da https://nsis.sourceforge.io
cd desktop && wails build -platform windows/amd64 -nsis
```

Çıktı: `desktop/build/bin/rpshell-desktop-amd64-installer.exe`

Bu installer CI'da da otomatik üretilir (`.github/workflows/desktop.yml`,
bir sürüm etiketi `v*` push edilince → artifact olarak iner).

---

Kurulumdan sonra uygulamayı aç, **Ayarlar**'dan tünel sunucusu adresini
(`host:port`) ve istemci token'ını (`rpsh_live_...`) gir, **Bağlan**.
Ayrıntı için ana `RUNNING.md`.

---

## Başka bir PC'ye hazır paket (sertifika + IP dahil)

Sunucunun çalıştığı makinede:

```bash
./scripts/package-client.sh
```

`dist/client-setup/` klasörü üretir — içinde exe, **sunucunun sertifikası**
(`server.crt`) ve **sunucu LAN adresi** (`server.txt`). Bu klasörü hedef PC'ye
kopyala → `install.bat`'a çift tıkla.

Hedef PC'de uygulama ilk açıldığında:
- **CA sertifikası** yolu → ONCEDEN DOLU (paketteki sertifika kuruldu)
- **Tünel sunucusu adresi** → ONCEDEN DOLU (ör. `192.168.1.180:8443`)
- **İstemci token'ı** → tek boş alan; dashboard'dan o PC için üret, yapıştır

> LAN IP yanlış algılanırsa elle ver:
> `ZORVEN_SERVER_IP=192.168.1.180 ./scripts/package-client.sh`

### Aynı ağdaki farklı PC için IP ne?

Sunucunun **LAN IP'si** (localhost DEĞİL). Sunucu makinesinde `ipconfig`/`hostname -I`
ile bak; örneğin `192.168.1.180`. İstemcide adres: **`192.168.1.180:8443`**.
`package-client.sh` bunu zaten `server.txt`'e koyup uygulamaya önceden doldurur.

Sertifikanın o IP'yi kapsadığından emin ol (start.sh otomatik ekler; elle:
`go run . cert --host "localhost,127.0.0.1,192.168.1.180,::1"`).

