# Zorven Network — Taşıma Katmanı Kararı (F17 ön koşulu)

> Roadmap F17'yi açıkça bir **ayrı teknik karar dokümanına** bağlıyor:
> *"transport tek seçeneğe kilitlenMEZ... bu karar sonraki tüm routing
> mimarisini belirler."* Bu doküman o kararı hazırlar.
>
> **Durum: KARAR VERİLDİ (2026-09-24) — Aşama 1 = C (SOCKS5) + B (WSS-mux).**
> TUN ve WireGuard sonraki aşamalar; varsayılan değil.
>
> Tarih: 2026-09-24

---

## 1. Problem

Bugün Zorven'de bir tünel **public**'tir: bir hostname'e gelen HTTP isteği
agent'a, oradan yerel servise gider. Private Network bunun tersini ister:

> Kaynak **hiç public'e açılmasın**; yetkili kullanıcı kendi makinesinden
> `db.internal`, `ssh.server`, `nas.internal` gibi adlara sanki aynı ağdaymış
> gibi erişsin.

Bu, "bir HTTP isteğini proxy'lemek"ten farklı bir problemdir: rastgele TCP/UDP
akışlarını, DNS çözümlemesini ve rota yönetimini gerektirir.

---

## 2. Bugün elimizde ne var (varsayım değil, kod)

| Yetenek | Durum | Nerede |
|---|---|---|
| Outbound-only WSS kontrol tüneli | Var | `server/tunnel` |
| Çerçeveli akış (istek/gövde/kapanış) | Var | `shared/protocol` |
| Ham TCP/UDP veri yolu | Var | `server/rawproxy` |
| Akış kontrolü (flow control) | Var | `FeatureFlowControl` |
| Cihaz kimliği + kalıcı envanter | Var (F14) | `clients` tablosu |
| Uzaktan ajan yapılandırması | Var (F15) | `device_config` |
| TUN arayüzü / L3 routing | **Yok** | — |
| Ağ istemcisi (`zorven connect`) | **Yok** | — |

Yani mux'lanabilir, kimliği doğrulanmış, firewall-dostu bir taşıma **zaten**
var. Eksik olan ziyaretçi tarafı.

---

## 3. Seçenekler

### A) WireGuard (kernel veya userspace)
- **Artı:** en yüksek performans; olgun, denetlenmiş kriptografi; L3 tam ağ.
- **Eksi:** UDP gerektirir — kurumsal/okul ağlarında sık engellenir. Kernel
  modülü veya `wireguard-go` bağımlılığı. Anahtar dağıtımı ve IP tahsisi ayrı
  bir kontrol düzlemi ister. Zorven'in **"tek binary, outbound-only"** vaadini
  zayıflatır.

### B) WSS/TCP üstünde mux
- **Artı:** mevcut kontrol tüneli üstünde çalışır; **yeni port, yeni protokol,
  yeni bağımlılık yok**; 443/TCP her yerde açık. Zorven'in vaadiyle birebir uyumlu.
- **Eksi:** TCP-over-TCP head-of-line blocking; WireGuard'a göre yavaş.
  UDP taşımak ek çerçeveleme ister.

### C) SOCKS5 proxy modu (TUN yok)
- **Artı:** **en hızlı teslim.** İşletim sistemi entegrasyonu, yönetici hakkı,
  sanal arayüz gerekmez. Tarayıcı/SSH/DB istemcileri SOCKS'u zaten konuşur.
  B ile aynı taşımayı kullanır; C → B geçişi altyapıyı değiştirmez.
- **Eksi:** SOCKS bilmeyen uygulamalar çalışmaz. Split-DNS kısıtlı. ICMP yok
  (yani `ping db.internal` çalışmaz — kullanıcı bunu bekler, beklenti yönetimi gerekir).

### D) TUN modu (tam L3)
- **Artı:** her uygulama çalışır; gerçek ağ deneyimi.
- **Eksi:** her platformda yönetici hakkı; Windows'ta sürücü (Wintun),
  macOS'ta ağ uzantısı yetkilendirmesi; route/DNS çakışmaları; en yüksek
  destek maliyeti. Tek başına bir ürün kadar iş.

---

## 4. Değerlendirme

| Ölçüt | A WireGuard | B WSS-mux | C SOCKS | D TUN |
|---|---|---|---|---|
| Teslim süresi | Uzun | Orta | **Kısa** | En uzun |
| Firewall geçirgenliği | Zayıf (UDP) | **Güçlü** | **Güçlü** | Taşımaya bağlı |
| Performans | **En iyi** | Orta | Orta | Taşımaya bağlı |
| Kurulum sürtünmesi | Orta | Düşük | **En düşük** | Yüksek |
| "Tek binary" vaadiyle uyum | Zayıf | **Güçlü** | **Güçlü** | Zayıf |
| Uygulama kapsamı | Tam | Tam | **Kısmi** | Tam |

---

## 5. Öneri

**Aşama 1 — C (SOCKS5) + B (WSS-mux):** ağ istemcisi `zorven connect <network>`
yerel bir SOCKS5 proxy açar; akışlar mevcut WSS tüneli üzerinden mux'lanarak
hedef agent'a taşınır. Yeni port, yeni bağımlılık, yönetici hakkı yok.

**Aşama 2 — D (TUN) opsiyonel katman:** aynı mux taşıması üstünde, SOCKS
bilmeyen uygulamalar için. Taşıma değişmediği için Aşama 1'i bozmaz.

**Aşama 3 — A (WireGuard) yalnızca performans gerektiğinde:** ölçülen bir
ihtiyaç ortaya çıkarsa alternatif taşıma olarak; varsayılan olarak değil.

**Gerekçe:** B ve C, Zorven'in zaten doğru yaptığı şeyin (outbound-only, tek
binary, 443 üstünde) üstüne biner. A ile başlamak, ürünün en güçlü
farklılaştırıcısını — hiçbir şey kurmadan çalışması — feda ederdi.

---

## 6. Bu karar verilmeden yapılmayacaklar

- `tunnels.exposure` değerine `private` eklemek
- `networks` / `network_members` tablolarını oluşturmak
- `zorven connect` komutunu yazmak
- F19 (site-to-site), F20 (routing) — ikisi de F17'ye bağlı

## 7. Karar verilmeden önce cevaplanması gerekenler

1. Hedef kullanıcı SOCKS ile yaşayabilir mi, yoksa `ping` ve rastgele uygulama
   desteği ilk günden şart mı?
2. Private ağda beklenen eşzamanlı akış sayısı ve bant genişliği nedir?
   (TCP-over-TCP'nin kabul edilebilir olup olmadığını bu belirler.)
3. Kurumsal müşteride UDP/443 dışına çıkma izni var mı?

---

## 8. Uygulanan (2026-09-24)

**Aşama 1 (SOCKS5 + WSS) üstünde:**
- **F17** — tekil özel kaynaklar (`db.internal:5432`).
- **F20 alt ağ yönlendirmesi** — bir cihaz yerel ağından bir aralık yayınlar
  (`subnet:192.168.1.0/24`); SOCKS isteği o aralıktaki bir IP'ye gelirse en spesifik
  aralığı yayınlayan cihaza yönlendirilir. Yalnızca özel aralıklar; hedef IP literali
  olmalı; ajan aralığı **bağımsız** olarak da doğrular (ele geçirilmiş sunucu bile
  aralık dışına yönlendiremez).
- **F19 site-to-site (uygulama katmanı)** — `zorven connect --gateway --allow-from <LAN>`
  bir sitede LAN'a açık SOCKS ağ geçidi çalıştırır; karşı sitenin alt ağlarına erişir.
  Kimlik = kullanıcıya bağlı API token'ı.

**Hâlâ Aşama 2 (TUN) gerektirenler:** SOCKS bilmeyen uygulamalar, UDP, ICMP (ping),
işletim sistemi düzeyinde rota ve split-DNS; çift yönlü L3 site-to-site.
