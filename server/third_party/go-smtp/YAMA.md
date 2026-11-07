# Zorven yerel yamasi

Upstream: github.com/emersion/go-smtp v0.25.0 (MIT, bkz. LICENSE).

## Ne
RCPT TO komutunda, buyuk/kucuk harf duyarsiz tam "postmaster" yerel kismi alan adi
olmadan (`RCPT TO:<postmaster>`) kabul edilir. MAIL FROM ve AUTH parametresi dahil
diger tum yollar degismedi (alan adsiz adres hala 501 doner).

## Neden
RFC 5321 4.5.1: SMTP sunucusu alan adsiz postmaster adresini kabul etmelidir.
Upstream parseMailbox her adreste "@" istedigi icin satir oturuma (Session.Rcpt)
ulasmadan reddediliyordu.

## Degisen yerler
`// ZORVEN-YAMA:` ile isaretli: parse.go (parser.allowBarePostmaster, parseLocalPart, parseMailbox),
conn.go (handleRcpt), zorven_postmaster_test.go (TestParsePathBarePostmaster, yeni dosya).
cmd/ dizini ve CI dosyalari kopyalanmadi.

## Guncelleme yolu
1. Yeni surumu modul onbelleginden/indirerek bu dizine kopyala (cmd/ haric).
2. `grep -rn ZORVEN-YAMA .` ile isaretli degisiklikleri yeniden uygula.
3. server/go.mod'daki require surumunu guncelle; `go test .` bu dizinde, sonra server/mail'de.
4. Upstream yamayi kabul ederse replace satirini ve bu dizini sil.
