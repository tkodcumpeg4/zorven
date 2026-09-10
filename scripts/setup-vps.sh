#!/usr/bin/env bash
# ==============================================================================
# Zorven Çok Düğümlü (Multi-Node) VPS/VDS Otomatik Kurulum Betiği
# Desteklenen Dağıtımlar: Ubuntu 20.04+, 22.04+, 24.04+, Debian 11, 12
# Kullanım:
#   Primary Node:  ./setup-vps.sh primary
#   Edge Node:     ./setup-vps.sh edge <PRIMARY_DB_DSN> <CLUSTER_SECRET>
# ==============================================================================
set -euo pipefail

ROLE="${1:-primary}"
INSTALL_DIR="/opt/zorven"

echo "=========================================================="
echo "   🚀 ZORVEN ÇOK DÜĞÜMLÜ VPS KURULUM SİHİRBAZI"
echo "   Rol: ${ROLE^^}"
echo "=========================================================="

# 1. Root / Sudo kontrolü
if [ "$EUID" -ne 0 ]; then
  echo "❌ Hata: Bu betiğin root veya sudo yetkileriyle çalıştırılması gerekir."
  exit 1
fi

# 2. Docker & Docker Compose Kurulumu (Eğer kurulu değilse)
if ! command -v docker &>/dev/null; then
  echo "📦 Docker bulunamadı, kuruluyor..."
  apt-get update -y
  apt-get install -y curl ca-certificates gnupg lsb-release
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker
  echo "✅ Docker başarıyla kuruldu."
else
  echo "✅ Docker zaten kurulu."
fi

if ! docker compose version &>/dev/null; then
  echo "📦 Docker Compose eklentisi kuruluyor..."
  apt-get update -y
  apt-get install -y docker-compose-plugin
fi

# 3. Dizin yapısını oluştur
echo "📁 Kurulum dizini oluşturuluyor: ${INSTALL_DIR}"
mkdir -p "${INSTALL_DIR}/certs"
mkdir -p "${INSTALL_DIR}/data"
mkdir -p "${INSTALL_DIR}/deploy"

# 4. Geçici veya kalıcı TLS Sertifikası üretimi
if [ ! -f "${INSTALL_DIR}/certs/server.crt" ]; then
  echo "🔐 TLS sertifikası bulunamadı, geçici self-signed sertifika oluşturuluyor..."
  openssl req -x509 -newkey rsa:4096 -nodes \
    -keyout "${INSTALL_DIR}/certs/server.key" \
    -out "${INSTALL_DIR}/certs/server.crt" \
    -days 365 -subj "/CN=zorven.app/O=Zorven Cluster"
  chmod 600 "${INSTALL_DIR}/certs/server.key"
  echo "✅ Sertifika oluşturuldu: ${INSTALL_DIR}/certs/"
fi

# 5. Ortam değişkenlerini (.env) yapılandır
ENV_FILE="${INSTALL_DIR}/.env"
HOSTNAME=$(hostname -s)
PUBLIC_IP=$(curl -s4 ifconfig.me || curl -s4 icanhazip.com || echo "127.0.0.1")

if [ ! -f "$ENV_FILE" ]; then
  echo "⚙️ Yapılandırma dosyası (.env) oluşturuluyor..."
  
  if [ "$ROLE" == "primary" ]; then
    CLUSTER_SECRET=$(openssl rand -hex 32)
    # Admin anahtarı formatı: zrv_admin_<id(16 hex)>_<secret(base64url)>
    # Server auth.Split() bu yapıyı bekler; id ile secret '_' ile ayrılır.
    ADMIN_KEY="zrv_admin_$(openssl rand -hex 8)_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
    DB_PASS=$(openssl rand -hex 16)
    
    cat <<EOF > "$ENV_FILE"
PLATFORM_DOMAIN=zorven.app
ADMIN_KEY=${ADMIN_KEY}
ACME_EMAIL=admin@zorven.app
CLUSTER_SECRET=${CLUSTER_SECRET}
NODE_ID=vps-${HOSTNAME}
PUBLIC_IP_OR_HOST=${PUBLIC_IP}
POSTGRES_USER=rpshell
POSTGRES_PASSWORD=${DB_PASS}
POSTGRES_DB=rpshell
POSTGRES_PORT=5432
EOF
    echo "=========================================================="
    echo "🔑 PRIMARY DÜĞÜM BİLGİLERİ (GÜVENLİ BİR YERE KAYDEDİN):"
    echo "   Admin Key:      ${ADMIN_KEY}"
    echo "   Cluster Secret: ${CLUSTER_SECRET}"
    echo "   DB DSN for Edge: postgres://rpshell:${DB_PASS}@${PUBLIC_IP}:5432/rpshell?sslmode=disable"
    echo "=========================================================="
  else
    # Edge Node
    PRIMARY_DB_DSN="${2:-}"
    CLUSTER_SEC="${3:-}"
    
    if [ -z "$PRIMARY_DB_DSN" ] || [ -z "$CLUSTER_SEC" ]; then
      read -p "Primary PostgreSQL DSN: " PRIMARY_DB_DSN
      read -p "Cluster Secret (Primary ile aynı): " CLUSTER_SEC
    fi
    
    cat <<EOF > "$ENV_FILE"
PLATFORM_DOMAIN=zorven.app
ADMIN_KEY=unused_on_edge
CLUSTER_SECRET=${CLUSTER_SEC}
NODE_ID=edge-${HOSTNAME}
PUBLIC_IP_OR_HOST=${PUBLIC_IP}
PRIMARY_DB_DSN=${PRIMARY_DB_DSN}
EOF
  fi
fi

# 6. Güvenlik Duvarı (UFW) Ayarları
if command -v ufw &>/dev/null && ufw status | grep -q "Status: active"; then
  echo "🛡️ UFW Güvenlik Duvarı portları açılıyor..."
  ufw allow 80/tcp comment 'Zorven HTTP Redirect'
  ufw allow 443/tcp comment 'Zorven HTTPS Ingress'
  ufw allow 8443/tcp comment 'Zorven Ingress & Tunnel'
  if [ "$ROLE" == "primary" ]; then
    ufw allow 5432/tcp comment 'Zorven Postgres for Edge Nodes'
  fi
fi

# 7. Servisleri Başlat
echo "🚢 Docker servisleri başlatılıyor..."
cd "$INSTALL_DIR"
if [ "$ROLE" == "primary" ]; then
  docker compose -f deploy/docker-compose.yml up -d
else
  docker compose -f deploy/docker-compose.edge.yml up -d
fi

echo "=========================================================="
echo "🎉 Zorven Node (${ROLE^^}) başarıyla kuruldu ve başlatıldı!"
echo "   Durum kontrolü: docker compose ps"
echo "   Loglar:         docker compose logs -f"
echo "=========================================================="
