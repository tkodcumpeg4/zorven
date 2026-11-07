# Zorven — Protokol Uyumluluk Matrisi

> FAZ 2 / F10. Bu belge bir **taahhüt listesidir**: burada "Destekleniyor"
> yazan her satırın karşılığında `server/ingress/protocol_compat_test.go`
> içinde otomatik bir test vardır.
>
> "HTTP tüneli çalışıyor" ile "gRPC streaming production'da destekli" aynı şey
> değildir. Bu matris o farkı açıkça yazar.
>
> Son güncelleme: **2026-11-05** (Akış G1: HTTP trailer + gRPC) · Protokol sürümü: `0.1.0`

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
| **C** agent → yerel servis | HTTP/1.1; **gRPC için HTTP/2** (h2c ya da TLS ALPN `h2`) | `--origin-protocol` ile seçilir (bkz. §6) |

**Sonuç:** istek tünelde Zorven çerçevelerine dönüşür; HTTP/2 tünelin kendisinden
geçmez ama **her bacakta gRPC'nin ihtiyacı olan şey** (h2 akışı + yanıt
trailer'ları) korunur: ziyaretçi h2 konuşur (A), trailer'lar `http_trailers`
mesajıyla taşınır (B), agent yerel gRPC sunucusuna HTTP/2 ile bağlanır (C).
Genel amaçlı "her HTTP/2 isteği h2 olarak yerel servise" davranışı varsayılan
değildir; `--origin-protocol h2` ile açılır.

---

## 2. Matris

| Senaryo | Durum | Dayanak |
|---|---|---|
| HTTP/1.1 istek/yanıt | **Destekleniyor** | Temel akış |
| HTTP/2 (ziyaretçi ↔ sunucu) | **Destekleniyor** | `NextProtos: h2` |
| HTTP/2 uçtan uca (yerel servise kadar) | **Kısmen: gRPC ve `--origin-protocol h2/h2c`** | C bacağı varsayılan HTTP/1.1; gRPC/h2 seçilince HTTP/2 |
| HTTP/3 (QUIC) | **Desteklenmiyor** | Kod tabanında QUIC dinleyici yok |
| WebSocket (ziyaretçi ↔ yerel servis) | **Destekleniyor** | `ingress/websocket.go`, çift yönlü köprü |
| SSE (`text/event-stream`) | **Destekleniyor** | `flushWriter` her yazmada flush eder |
| Uzun sorgu (long polling) | **Destekleniyor** | Aynı flush yolu |
| Chunked yanıt akışı | **Destekleniyor** | Yanıt gövdesi parça parça iletilir |
| Büyük yükleme (upload) | **Destekleniyor, 32 MB sınırla** | `MaxBodyBytes = 32 MB`; aşılırsa 413 |
| HTTP trailer başlıkları (yanıt) | **Destekleniyor, istemci 0.3.4+** | `http_trailers` mesajı; ziyaretçiye h2 trailer / HTTP/1.1 chunked trailer. İstek trailer'ı taşınmaz |
| gRPC unary | **Destekleniyor, istemci 0.3.4+** | h2 ziyaretçi + agent h2c/ALPN h2 + `grpc-status` trailer'ı |
| gRPC server-streaming | **Destekleniyor, istemci 0.3.4+** | Her yanıt parçası flush edilir |
| gRPC client-streaming ve bidi | **Destekleniyor, istemci 0.3.4+** | İstek gövdesi yanıt beklenirken eşzamanlı akar; 32 MB tavanı yok |
| grpc-web | **Destekleniyor (tüm istemciler)** | HTTP/1.1 uyumlu; trailer gövdede çerçevelenir, özel yol yok |
| gRPC, eski agent (`http_trailers` yok) | **Net hata** | gRPC `UNAVAILABLE` (trailers-only, HTTP 200); istek agent'a gönderilmez |
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

Bu, `Trailer`/`TE` **başlıklarının** başlık olarak iletilmediği anlamına gelir.
Trailer'ların kendisi ayrı bir yoldan taşınır (§6); `TE: trailers` bilgisi
`http_request.te_trailers` alanıyla agent'a bildirilir ve agent h2 yerel
bağlantıda `TE: trailers` başlığını kendisi yazar.

`upgrade` listede olmasına rağmen WebSocket çalışır: yükseltme istekleri normal
proxy yolundan **önce** ayrı bir köprüye ayrılır.

---

## 5. Bu matris nasıl korunur

`server/ingress/protocol_compat_test.go` her satırı test eder. Bir davranış
değişirse test kırmızıya döner ve bu belge ile kod birlikte güncellenir.
Yeni bir protokol desteği eklendiğinde önce test, sonra satır yazılır.

---

## 6. gRPC ve trailer — tasarım kararları (Akış G1)

**Protokol (geriye uyumlu, `shared/protocol/httptrailers.go`).**
Agent Hello'da `http_trailers` yeteneğini ilan eder, sunucu `hello_ack`'te
yankılar. Yeni içerik: `http_request.http_major` ve `te_trailers`
(`omitempty`; yalnız yetenek pazarlıksa dolar) ve ajandan sunucuya
`http_trailers {req_id, trailers}` kontrol mesajı. Agent bu mesajı yerel
yanıtın gövdesi bittiğinde, **EOF'lu son gövde çerçevesinden hemen önce** yollar;
kontrol mesajı ve ikili çerçeveler tek WebSocket'te sıralı aktığı için sunucu
EOF'u gördüğünde trailer'lar hazırdır. Sunucu bunları ziyaretçiye gövde
yazıldıktan sonra `http.TrailerPrefix` ile yazar (h2'de HEADERS, HTTP/1.1'de
chunked sonu). Her gövde parçası flush edilir; gRPC yanıt başlıkları da
hemen flush edilir.

**Agent → yerel servis protokolü (`--origin-protocol`).**

| Değer | Davranış |
|---|---|
| `auto` (varsayılan) | `Content-Type: application/grpc*` (grpc-web hariç) istekleri HTTP/2: `http://` hedefte h2c (ön bilgili), `https://` hedefte ALPN `h2`. Diğer istekler HTTP/1.1 |
| `http1` | Hiçbir istek HTTP/2 kullanmaz (eski davranış) |
| `h2` / `h2c` | Tüm istekler HTTP/2 (`http://` hedefte h2c, `https://` hedefte ALPN h2) |

CLI: `zorven http 50051 --h2c` veya `--origin-protocol h2c`. gRPC için geri
dönüş (HTTP/1.1'e düşme) yoktur: gRPC HTTP/1.1 ile çalışmaz; h2 bağlantısı
kurulamazsa istek `local_unreachable` ile net biçimde biter. Panelde tünel başına
ayar bu sürümde YOKTUR (ajan yerel ayarıdır); `auto` gRPC için sıfır ayar
gerektirir.

**gRPC'de bilinçli kararlar.**

- **İstek gövdesi:** akar ve 32 MB tavanı uygulanmaz (uzun akışlar için);
  kotayı bant genişliği ölçümü korur. Sunucu yanıt başlığı bekleme süresi 30 sn
  yerine 15 dk (client-streaming'de sunucu başlığı akış bitince yollayabilir).
- **Gövde yakalama (capture):** gRPC'de kapalı (ikili, uzun omürlü akış bellekte
  tutulmaz). **WAF** yalnız istek satırı/başlıkları inceler; gövde incelemesi
  yoktur. `verify_webhook` politikası gövdeyi tamamen okumayı gerektirdiğinden
  gRPC isteklerinde desteklenmez (415).
- **Sıkıştırma:** Zorven yanıt sıkıştırmaz; `grpc-encoding` uçtan uca kalır.
- **Yetenek yok (eski agent):** istek agent'a hiç gönderilmez, gRPC istemcisi
  `UNAVAILABLE (14)` + açıklayıcı `grpc-message` alır.
- **Küme atlaması:** dügümler arası aktarım trailer'ları ve gRPC akışını korur
  (TLS küme adreslerinde h2, düz adreslerde HTTP/1.1 chunked trailer +
  tam çift yönlü okuma).
- **Bayt sayımı:** uzunluğu bilinmeyen (chunked / gRPC) isteklerde `bytes_in`
  (istek logu, kiracı bant kaydı) ajana/hedef düğüme iletilen gerçek istek
  gövdesi baytlarıdır: gRPC mesajlarının 5 baytlık önekleri dahil, HTTP/2 DATA
  çerçeve başlıkları ve trailer'lar hariç. `Content-Length` bilinen isteklerde
  değer değişmedi (`Content-Length`). Küme atlamasında yalnız kenar düğüm yazar
  (çift sayım yok). Yanıt yönü (`bytes_out`) gövde baytlarıdır, trailer hariç.
  Bant kotası aşımı gRPC'de de aynı davranır: yanıt kısıtlanır (throttle),
  istek reddedilmez.
- **Sınırlar:** istek trailer'ı taşınmaz; H6 akış (ham TCP/UDP) tünelleri
  HTTP taşımaz ve bu mekanizmanın dışındadır.
