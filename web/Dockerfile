# Zorven web (Nuxt) — Better Auth ucları için Node sunucu (Nitro node-server).
#
# Bu servis tunel sunucusunun (Go) `--auth-upstream` hedefidir: /api/auth/*
# isteklerini karşılar. Dashboard SPA'sını Go gömülü servis eder; bu container
# yalnızca Better Auth (kayıt/giriş/e-posta doğrulama) çalıştırır.

# ---- build stage ----
FROM node:22-alpine AS builder
WORKDIR /app

# Bağımlılıklar önce (katman cache): package + lock kopyala, kur.
# NOT: `npm ci` yerine `npm install` — lockfile farklı bir npm sürümüyle
# (yerelde v11, container'da v10) üretildiğinde `npm ci` küçük ağaç
# farklarında (ör. transitif unplugin) hata verir; `npm install` container'ın
# kendi npm'iyle çözer ve derlemeyi kırmaz.
COPY package.json package-lock.json ./
RUN npm install --no-audit --no-fund

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
