# Zorven — Protokol Uyumluluk Matrisi

> FAZ 2 / F10. Bu belge bir **taahhüt listesidir**: burada "Destekleniyor"
> yazan her satırın karşılığında `server/ingress/protocol_compat_test.go`
> içinde otomatik bir test vardır.
>
> "HTTP tüneli çalışıyor" ile "gRPC streaming production'da destekli" aynı şey
> değildir. Bu matris o farkı açıkça yazar.
>
> Son doğrulama: **2026-09-24** · Protokol sürümü: `0.1.0`

---

## 1. Mimari — hangi bacak hangi protokolü konuşur

Bir istek üç bacaktan geçer, her biri ayrı protokol konuşabilir:

```
[ziyaretçi] --(A)--> [Zorven sunucu] --(B: WSS tünel)--> [agent] --(C)--> [yerel servis]
```

| Bacak | Protokol | Not |
|---|---|---|
| **A** ziyaretçi → sunucu | HTTP/1.1 · HTTP/2 (TLS ALPN `h2`) | ALPN listesi: `h2`, `http/1.1`, `acme-tls/1` |
| **B** sunucu → agent | WebSocket üzerinde Zorven çerçeve protokolü | Tek yönde akış; gövde 64 KB parçalar hâlinde |
| **C** agent → yerel servis | HTTP/1.1 | Agent'ın `http.Transport`'u HTTP/2 denemez |

**Sonuç:** uçtan uca HTTP/2 **yoktur**. Ziyaretçi h2 ile bağlanabilir, ama istek
tünelde Zorven çerçevelerine dönüşür ve yerel servise HTTP/1.1 olarak ulaşır.

---

## 2. Matris

| Senaryo | Durum | Dayanak |
|---|---|---|
| HTTP/1.1 istek/yanıt | **Destekleniyor** | Temel akış |
| HTTP/2 (ziyaretçi ↔ sunucu) | **Destekleniyor** | `NextProtos: h2` |
| HTTP/2 uçtan uca (yerel servise kadar) | **Desteklenmiyor** | C bacağı HTTP/1.1 |
| HTTP/3 (QUIC) | **Desteklenmiyor** | Kod tabanında QUIC dinleyici yok |
| WebSocket (ziyaretçi ↔ yerel servis) | **Destekleniyor** | `ingress/websocket.go`, çift yönlü köprü |
| SSE (`text/event-stream`) | **Destekleniyor** | `flushWriter` her yazmada flush eder |
| Uzun sorgu (long polling) | **Destekleniyor** | Aynı flush yolu |
| Chunked yanıt akışı | **Destekleniyor** | Yanıt gövdesi parça parça iletilir |
| Büyük yükleme (upload) | **Destekleniyor, 32 MB sınırla** | `MaxBodyBytes = 32 MB`; aşılırsa 413 |
| HTTP trailer başlıkları | **Desteklenmiyor** | `trailer` hop-by-hop listesinde, düşürülür |
| gRPC (unary veya streaming) | **Desteklenmiyor** | h2 uçtan uca + trailer gerektirir; ikisi de yok |
| TCP / UDP ham tünel | **Destekleniyor** | Ayrı yol: `rawproxy`, HTTP matrisinin dışında |

---

## 3. Sınırlar (sayılar koddan)

| Sınır | Değer | Sabit |
|---|---|---|
| Tek istek/yanıt gövdesi | 32 MB | `protocol.MaxBodyBytes` |
| Gövde parça boyutu | 64 KB | `protocol.BodyChunkSize` |
| Tek WebSocket mesajı | 1 MB | `protocol.WSReadLimit` |
| Upstream zaman aşımı | 30 sn | `ingress.UpstreamTimeout` |

> SSE ve WebSocket bu zaman aşımından etkilenmez; akan bağlantılar ayrı yoldan
> yürür. 30 sn, **tek bir istek/yanıt turu** için geçerlidir.

---

## 4. Düşürülen başlıklar (hop-by-hop)

RFC 7230 gereği tek bağlantıya özgü olan şu başlıklar iletilmez:
`connection · keep-alive · proxy-authenticate · proxy-authorization ·
proxy-connection · te · trailer · transfer-encoding · upgrade`

`upgrade` listede olmasına rağmen WebSocket çalışır: yükseltme istekleri normal
proxy yolundan **önce** ayrı bir köprüye ayrılır.

---

## 5. Bu matris nasıl korunur

`server/ingress/protocol_compat_test.go` her satırı test eder. Bir davranış
değişirse test kırmızıya döner ve bu belge ile kod birlikte güncellenir.
Yeni bir protokol desteği eklendiğinde önce test, sonra satır yazılır.
