# Zorven web (Nuxt) — Better Auth ucları için Node sunucu (Nitro node-server).
#
# Bu servis tunel sunucusunun (Go) `--auth-upstream` hedefidir: /api/auth/*
# isteklerini karşılar. Dashboard SPA'sını Go gömülü servis eder; bu container
# yalnızca Better Auth (kayıt/giriş/e-posta doğrulama) çalıştırır.

# ---- build stage ----
FROM node:22-alpine AS builder
WORKDIR /app

# Bağımlılıklar önce (katman cache): package + lock kopyala, kur.
# `npm ci`: imaj tam olarak lockfile'daki (denetlenmis) surumlerle kurulur;
# `npm install` lockfile'dan sapip denetlenmemis transitif surum cekebilirdi.
# Lockfile yerelde ve imajda ayni npm ana surumuyle (v10, node:22) uretilmeli;
# eskiden yerel npm v11 iken `npm ci` kucuk agac farklarinda kiriliyordu.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

# Kaynakları kopyala ve node-server preset'iyle derle (.output üretir).
COPY . .
RUN npm run build

# ---- runtime stage ----
FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
# Nitro node-server varsayılan olarak :3000 dinler.
ENV PORT=3000
ENV NITRO_PORT=3000

# Yalnızca derlenmiş çıktı gerekir (node_modules .output içine gömülüdür).
COPY --from=builder /app/.output ./.output

EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
