# Build stage
# NOT: Bu bir Go workspace monorepo'sudur (kökte go.mod YOK, go.work var).
# `server` modülü `replace .../shared => ../shared` içerdiği için, sunucuyu
# yalnızca `server/` + `shared/` ile (client/desktop bağımlılıkları olmadan)
# derleyebiliriz. Bu, imajı çok daha küçük ve hızlı yapar.
# Surum go.mod/go.work'teki toolchain satiriyla AYNI olmali: asagidaki
# `go work init` uretilen go.work'e toolchain satiri yazmaz ve golang imaji
# GOTOOLCHAIN=local ile gelir; yani derlemeyi imajdaki Go yapar. Eski imaj
# eski stdlib (bilinen zafiyetler) ile ikili uretir.
FROM golang:1.27.4-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

# Yalnızca server+shared'ı kapsayan minimal bir Go workspace kuruyoruz.
# server/go.sum tek başına eksik (bazı transitif hash'ler kökteki go.work.sum'da
# tutuluyor), bu yüzden go.work.sum'ı da kopyalayıp iki modülü içeren bir go.work
# üretiyoruz. Böylece client/desktop'ın ağır bağımlılıkları olmadan derliyoruz.
# shared modülünün dış bağımlılığı yok, bu yüzden go.sum'ı da yok.
COPY go.work.sum ./
COPY shared/go.mod ./shared/
COPY server/go.mod server/go.sum ./server/
RUN go work init ./shared ./server

# Kaynakları kopyala (server webdist, install şablonları ve migration'lar
# go:embed ile server modülünün içinde gömülüdür).
COPY shared/ ./shared/
COPY server/ ./server/

# Derleme surumu (zorven_build_info, /api/v1/health, baslangic logu). Baglamda .git
# yok (.dockerignore); surumu cagiran verir, or. scripts/build-server.sh'in urettigi
# bicimde: --build-arg ZORVEN_BUILD_VERSION=v0.3.8-4-gabc1234+20261111
# Verilmezse "dev". scripts/build-server.sh ile ayni ldflags.
ARG ZORVEN_BUILD_VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.buildVersion=${ZORVEN_BUILD_VERSION}" -o /app/zorven-server ./server

# Final runtime stage
FROM alpine:3.21

WORKDIR /app

# curl: compose saglik kontrolu ve ops betikleri (docker exec ... curl /metrics).
# openssl: multinode e2e konteyner icinde sertifika seri no'sunu okur; libssl curl'un
# bagimliligi olarak zaten imajda, CLI ek saldiri yuzeyi getirmez. bash KALDIRILDI
# (hicbir betik konteyner icinde bash calistirmiyor; busybox sh yeterli).
RUN apk add --no-cache ca-certificates tzdata curl openssl

# Root olmayan kullanici (USER gecisi, docs/operasyon-gozlemlenebilirlik.md §9).
# uid/gid 65532: distroless "nonroot" ile ayni sayi; host'taki gercek bir kullaniciyla
# cakisma olasiligi dusuk. Yazilabilir tek dizin /app/state: yeni adlandirilmis birim
# ilk baglandiginda Docker imajdaki bu dizinin sahipligini birime kopyalar, yani
# chown gerekmez. Kalan her sey (ikili, /app) root'a ait ve salt okunur kalir.
RUN addgroup -S -g 65532 zorven \
 && adduser -S -D -H -u 65532 -G zorven -h /app/state -s /sbin/nologin zorven \
 && install -d -o 65532 -g 65532 -m 0750 /app/state \
 && install -d -o 0 -g 0 -m 0755 /app/server/bin /app/certs /app/data

COPY --from=builder /app/zorven-server /app/zorven-server

# Default ports
EXPOSE 8443 80

# Imaj varsayilani root DEGIL. Mevcut compose dosyalari gecis bitene kadar `user: "0:0"`
# ile eski davranisi korur (80/443'e baglanma, 0600 sertifika anahtari); nonroot
# override (deploy/docker-compose.nonroot.yml) bunu 65532 yapar.
USER 65532:65532

ENTRYPOINT ["/app/zorven-server"]
CMD ["serve"]
