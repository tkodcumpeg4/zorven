# Build stage
# NOT: Bu bir Go workspace monorepo'sudur (kökte go.mod YOK, go.work var).
# `server` modülü `replace .../shared => ../shared` içerdiği için, sunucuyu
# yalnızca `server/` + `shared/` ile (client/desktop bağımlılıkları olmadan)
# derleyebiliriz. Bu, imajı çok daha küçük ve hızlı yapar.
FROM golang:1.25-alpine AS builder

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

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/zorven-server ./server

# Final runtime stage
FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl bash openssl

COPY --from=builder /app/zorven-server /app/zorven-server

# Default ports
EXPOSE 8443 80

ENTRYPOINT ["/app/zorven-server"]
CMD ["serve"]
