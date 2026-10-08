-- 0058: tunel ziyaretci erisim olaylari (Basic Auth / OAuth giris istatistikleri).
--
-- Parola ASLA saklanmaz; identity yalnizca kullanici adi veya e-postadir.
-- Saklama suresi request_logs ile ayni (plan log_retention_days) - bkz. retention job'u.
CREATE TABLE IF NOT EXISTS tunnel_access_events (
    id          text PRIMARY KEY,
    tenant_id   text NOT NULL DEFAULT '',
    tunnel_id   text NOT NULL DEFAULT '',
    hostname    text NOT NULL DEFAULT '',
    method      text NOT NULL DEFAULT '',      -- basic | oauth
    provider    text NOT NULL DEFAULT '',      -- google | github | '' (basic)
    identity    text NOT NULL DEFAULT '',      -- kullanici adi veya e-posta
    success     boolean NOT NULL DEFAULT false,
    reason      text NOT NULL DEFAULT '',      -- ok, bad_credentials, email_not_allowed, rate_limited, provider_not_configured
    client_ip   text NOT NULL DEFAULT '',
    user_agent  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tae_tenant_created ON tunnel_access_events (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tae_tunnel_created ON tunnel_access_events (tunnel_id, created_at);
